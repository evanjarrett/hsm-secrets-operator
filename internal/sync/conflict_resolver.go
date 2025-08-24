/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// ConflictResolutionStrategy defines how conflicts should be resolved
type ConflictResolutionStrategy string

const (
	// StrategyLatestVersion resolves conflicts by choosing the device with the highest version
	StrategyLatestVersion ConflictResolutionStrategy = "latest-version"

	// StrategyLatestTimestamp resolves conflicts by choosing the most recently modified data
	StrategyLatestTimestamp ConflictResolutionStrategy = "latest-timestamp"

	// StrategyManualResolution requires manual intervention for conflict resolution
	StrategyManualResolution ConflictResolutionStrategy = "manual"

	// StrategyPrimaryDevice always uses the designated primary device as source of truth
	StrategyPrimaryDevice ConflictResolutionStrategy = "primary-device"
)

// ConflictResolver handles HSM device synchronization conflicts
type ConflictResolver struct {
	logger   logr.Logger
	strategy ConflictResolutionStrategy
}

// NewConflictResolver creates a new conflict resolver
func NewConflictResolver(logger logr.Logger, strategy ConflictResolutionStrategy) *ConflictResolver {
	return &ConflictResolver{
		logger:   logger.WithName("conflict-resolver"),
		strategy: strategy,
	}
}

// ConflictInfo represents detected conflict information
type ConflictInfo struct {
	SecretPath    string
	Devices       []DeviceConflictData
	DetectedAt    time.Time
	ResolutionRef string // Reference to resolution method used
}

// DeviceConflictData represents conflict data from a specific device
type DeviceConflictData struct {
	DeviceName string
	Checksum   string
	Version    int64
	Timestamp  time.Time
	Data       hsm.SecretData
	Online     bool
	Error      error
}

// ResolveConflict resolves a detected conflict using the configured strategy
func (cr *ConflictResolver) ResolveConflict(ctx context.Context, conflict *ConflictInfo, hsmSecret *hsmv1alpha1.HSMSecret) (*ResolutionResult, error) {
	logger := cr.logger.WithValues("secret", hsmSecret.Name, "strategy", cr.strategy)
	logger.Info("Resolving HSM sync conflict", "devices", len(conflict.Devices))

	switch cr.strategy {
	case StrategyLatestVersion:
		return cr.resolveByLatestVersion(conflict, logger)
	case StrategyLatestTimestamp:
		return cr.resolveByLatestTimestamp(conflict, logger)
	case StrategyPrimaryDevice:
		return cr.resolveByPrimaryDevice(conflict, hsmSecret, logger)
	case StrategyManualResolution:
		return cr.requireManualResolution(conflict, hsmSecret, logger)
	default:
		return nil, fmt.Errorf("unknown conflict resolution strategy: %s", cr.strategy)
	}
}

// ResolutionResult represents the result of conflict resolution
type ResolutionResult struct {
	Winner                     *DeviceConflictData
	Resolution                 ConflictResolutionStrategy
	RequiresManualIntervention bool
	SyncTargets                []string // Devices that need to be updated
	ResolvedData               hsm.SecretData
}

// resolveByLatestVersion chooses the device with the highest version number
func (cr *ConflictResolver) resolveByLatestVersion(conflict *ConflictInfo, logger logr.Logger) (*ResolutionResult, error) {
	var winner *DeviceConflictData
	highestVersion := int64(-1)

	// Find device with highest version among online devices
	for i := range conflict.Devices {
		device := &conflict.Devices[i]
		if device.Online && device.Error == nil && device.Version > highestVersion {
			highestVersion = device.Version
			winner = device
		}
	}

	if winner == nil {
		return nil, fmt.Errorf("no online devices with valid version found")
	}

	// Determine which devices need updating
	var syncTargets []string
	for _, device := range conflict.Devices {
		if device.DeviceName != winner.DeviceName && device.Online && device.Error == nil {
			syncTargets = append(syncTargets, device.DeviceName)
		}
	}

	logger.Info("Resolved conflict by latest version",
		"winner", winner.DeviceName,
		"version", winner.Version,
		"targets", syncTargets)

	return &ResolutionResult{
		Winner:       winner,
		Resolution:   StrategyLatestVersion,
		SyncTargets:  syncTargets,
		ResolvedData: winner.Data,
	}, nil
}

// resolveByLatestTimestamp chooses the device with the most recent timestamp
func (cr *ConflictResolver) resolveByLatestTimestamp(conflict *ConflictInfo, logger logr.Logger) (*ResolutionResult, error) {
	var winner *DeviceConflictData
	var latestTime time.Time

	// Find device with most recent timestamp among online devices
	for i := range conflict.Devices {
		device := &conflict.Devices[i]
		if device.Online && device.Error == nil && device.Timestamp.After(latestTime) {
			latestTime = device.Timestamp
			winner = device
		}
	}

	if winner == nil {
		return nil, fmt.Errorf("no online devices with valid timestamp found")
	}

	// Determine which devices need updating
	var syncTargets []string
	for _, device := range conflict.Devices {
		if device.DeviceName != winner.DeviceName && device.Online && device.Error == nil {
			syncTargets = append(syncTargets, device.DeviceName)
		}
	}

	logger.Info("Resolved conflict by latest timestamp",
		"winner", winner.DeviceName,
		"timestamp", winner.Timestamp,
		"targets", syncTargets)

	return &ResolutionResult{
		Winner:       winner,
		Resolution:   StrategyLatestTimestamp,
		SyncTargets:  syncTargets,
		ResolvedData: winner.Data,
	}, nil
}

// resolveByPrimaryDevice uses the designated primary device as the source of truth
func (cr *ConflictResolver) resolveByPrimaryDevice(conflict *ConflictInfo, hsmSecret *hsmv1alpha1.HSMSecret, logger logr.Logger) (*ResolutionResult, error) {
	primaryDevice := hsmSecret.Status.PrimaryDevice
	if primaryDevice == "" {
		// No primary device set, fall back to latest version strategy
		logger.Info("No primary device set, falling back to latest version strategy")
		return cr.resolveByLatestVersion(conflict, logger)
	}

	// Find the primary device in the conflict data
	var winner *DeviceConflictData
	for i := range conflict.Devices {
		device := &conflict.Devices[i]
		if device.DeviceName == primaryDevice {
			if device.Online && device.Error == nil {
				winner = device
				break
			} else {
				logger.Info("Primary device is offline or has errors, falling back to latest version",
					"primaryDevice", primaryDevice,
					"online", device.Online,
					"error", device.Error)
				return cr.resolveByLatestVersion(conflict, logger)
			}
		}
	}

	if winner == nil {
		logger.Info("Primary device not found in conflict, falling back to latest version", "primaryDevice", primaryDevice)
		return cr.resolveByLatestVersion(conflict, logger)
	}

	// Determine which devices need updating
	var syncTargets []string
	for _, device := range conflict.Devices {
		if device.DeviceName != winner.DeviceName && device.Online && device.Error == nil {
			syncTargets = append(syncTargets, device.DeviceName)
		}
	}

	logger.Info("Resolved conflict by primary device",
		"winner", winner.DeviceName,
		"targets", syncTargets)

	return &ResolutionResult{
		Winner:       winner,
		Resolution:   StrategyPrimaryDevice,
		SyncTargets:  syncTargets,
		ResolvedData: winner.Data,
	}, nil
}

// requireManualResolution marks the conflict as requiring manual intervention
func (cr *ConflictResolver) requireManualResolution(conflict *ConflictInfo, hsmSecret *hsmv1alpha1.HSMSecret, logger logr.Logger) (*ResolutionResult, error) {
	logger.Info("Conflict marked for manual resolution",
		"devices", len(conflict.Devices),
		"secret", hsmSecret.Name)

	// Add condition to HSMSecret indicating manual resolution is required
	now := metav1.NewTime(time.Now())
	condition := metav1.Condition{
		Type:               "ConflictResolutionRequired",
		Status:             metav1.ConditionTrue,
		Reason:             "ManualResolutionRequired",
		Message:            fmt.Sprintf("Conflict detected between %d devices requires manual resolution", len(conflict.Devices)),
		LastTransitionTime: now,
	}

	// Update conditions (this would be done by the caller)
	_ = condition

	return &ResolutionResult{
		RequiresManualIntervention: true,
		Resolution:                 StrategyManualResolution,
		SyncTargets:                []string{}, // No automatic sync
	}, nil
}

// DetectConflicts analyzes device sync results to identify conflicts
func (cr *ConflictResolver) DetectConflicts(deviceResults map[string]DeviceResult, secretPath string) (*ConflictInfo, bool) {
	// Group devices by checksum
	checksumGroups := make(map[string][]string)
	conflictDevices := make([]DeviceConflictData, 0)

	for deviceName, result := range deviceResults {
		if result.Online && result.Error == nil && result.Checksum != "" {
			if _, exists := checksumGroups[result.Checksum]; !exists {
				checksumGroups[result.Checksum] = make([]string, 0)
			}
			checksumGroups[result.Checksum] = append(checksumGroups[result.Checksum], deviceName)

			// Add to conflict devices list
			conflictDevices = append(conflictDevices, DeviceConflictData{
				DeviceName: deviceName,
				Checksum:   result.Checksum,
				Version:    result.Version,
				Timestamp:  result.Timestamp,
				Online:     result.Online,
				Error:      result.Error,
			})
		}
	}

	// Conflict exists if we have more than one checksum group
	hasConflict := len(checksumGroups) > 1 && len(conflictDevices) > 1

	if !hasConflict {
		return nil, false
	}

	conflict := &ConflictInfo{
		SecretPath: secretPath,
		Devices:    conflictDevices,
		DetectedAt: time.Now(),
	}

	return conflict, true
}
