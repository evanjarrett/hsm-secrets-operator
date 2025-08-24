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
	"crypto/sha256"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// AgentManagerInterface defines the interface for HSM agent management used by sync
type AgentManagerInterface interface {
	CreateSingleGRPCClient(ctx context.Context, deviceName string, logger logr.Logger) (hsm.Client, error)
}

// SyncManager handles multi-device HSM synchronization and conflict resolution
type SyncManager struct {
	client       client.Client
	agentManager AgentManagerInterface
	logger       logr.Logger
}

// NewSyncManager creates a new sync manager
func NewSyncManager(k8sClient client.Client, agentManager AgentManagerInterface, logger logr.Logger) *SyncManager {
	return &SyncManager{
		client:       k8sClient,
		agentManager: agentManager,
		logger:       logger.WithName("sync-manager"),
	}
}

// SyncResult represents the result of a sync operation
type SyncResult struct {
	Success          bool
	ConflictDetected bool
	PrimaryDevice    string
	DeviceResults    map[string]DeviceResult
	ResolvedData     hsm.SecretData
}

// DeviceResult represents the sync result for a specific device
type DeviceResult struct {
	Online    bool
	Checksum  string
	Version   int64
	Error     error
	Timestamp time.Time
}

// SyncSecret performs multi-device synchronization for an HSMSecret
func (sm *SyncManager) SyncSecret(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret) (*SyncResult, error) {
	logger := sm.logger.WithValues("secret", hsmSecret.Name, "namespace", hsmSecret.Namespace)
	secretPath := hsmSecret.Name

	// Get all available HSM devices from HSMPools
	devices, err := sm.getAvailableDevices(ctx, hsmSecret.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get available devices: %w", err)
	}

	if len(devices) == 0 {
		return &SyncResult{
			Success:       false,
			DeviceResults: make(map[string]DeviceResult),
		}, fmt.Errorf("no HSM devices available")
	}

	logger.Info("Starting multi-device sync", "devices", len(devices))

	// Read data from all online devices
	deviceResults := make(map[string]DeviceResult)
	validDeviceData := make(map[string]hsm.SecretData)

	for _, deviceName := range devices {
		result := sm.readFromDevice(ctx, deviceName, secretPath, logger)
		deviceResults[deviceName] = result

		if result.Online && result.Error == nil {
			// Calculate checksum for the data (we'll get the actual data in practice)
			// For now, simulating based on checksum
			validDeviceData[deviceName] = hsm.SecretData{
				"checksum": []byte(result.Checksum),
			}
		}
	}

	// Detect conflicts and resolve
	conflictDetected := sm.detectConflicts(deviceResults)
	primaryDevice := sm.selectPrimaryDevice(deviceResults, hsmSecret)

	var resolvedData hsm.SecretData
	if primaryDevice != "" && validDeviceData[primaryDevice] != nil {
		resolvedData = validDeviceData[primaryDevice]
		logger.Info("Using primary device data", "primaryDevice", primaryDevice)
	} else if len(validDeviceData) > 0 {
		// Use most recent data if no clear primary
		resolvedData = sm.selectMostRecentData(deviceResults, validDeviceData)
		logger.Info("Using most recent data for resolution")
	}

	// If conflict detected and we have a resolution, sync to all other devices
	if conflictDetected && primaryDevice != "" {
		sm.syncToSecondaryDevices(ctx, devices, primaryDevice, secretPath, resolvedData, logger)
	}

	return &SyncResult{
		Success:          len(validDeviceData) > 0,
		ConflictDetected: conflictDetected,
		PrimaryDevice:    primaryDevice,
		DeviceResults:    deviceResults,
		ResolvedData:     resolvedData,
	}, nil
}

// readFromDevice reads secret data from a specific HSM device
func (sm *SyncManager) readFromDevice(ctx context.Context, deviceName, secretPath string, logger logr.Logger) DeviceResult {
	result := DeviceResult{
		Timestamp: time.Now(),
	}

	// Get gRPC client for this device
	grpcClient, err := sm.agentManager.CreateSingleGRPCClient(ctx, deviceName, logger)
	if err != nil {
		result.Error = fmt.Errorf("failed to create gRPC client: %w", err)
		return result
	}
	defer func() {
		if closeErr := grpcClient.Close(); closeErr != nil {
			logger.V(1).Info("Failed to close gRPC client", "error", closeErr)
		}
	}()

	result.Online = grpcClient.IsConnected()
	if !result.Online {
		result.Error = fmt.Errorf("device not connected")
		return result
	}

	// Try to read the secret
	data, err := grpcClient.ReadSecret(ctx, secretPath)
	if err != nil {
		result.Error = fmt.Errorf("failed to read secret: %w", err)
		return result
	}

	// Calculate checksum
	result.Checksum = sm.calculateChecksum(data)

	// Get metadata to extract version (if available)
	metadata, err := grpcClient.ReadMetadata(ctx, secretPath)
	if err == nil && metadata != nil {
		if versionStr, exists := metadata.Tags["sync.version"]; exists {
			if version, parseErr := parseVersion(versionStr); parseErr == nil {
				result.Version = version
			}
		}
	}

	return result
}

// detectConflicts checks if there are conflicting checksums across devices
func (sm *SyncManager) detectConflicts(deviceResults map[string]DeviceResult) bool {
	checksums := make(map[string]int)
	onlineDevices := 0

	for _, result := range deviceResults {
		if result.Online && result.Error == nil && result.Checksum != "" {
			checksums[result.Checksum]++
			onlineDevices++
		}
	}

	// Conflict if we have more than one unique checksum across online devices
	return len(checksums) > 1 && onlineDevices > 1
}

// selectPrimaryDevice chooses the primary device for conflict resolution
func (sm *SyncManager) selectPrimaryDevice(deviceResults map[string]DeviceResult, hsmSecret *hsmv1alpha1.HSMSecret) string {
	// Check if there's already a designated primary in the status
	if hsmSecret.Status.PrimaryDevice != "" {
		if result, exists := deviceResults[hsmSecret.Status.PrimaryDevice]; exists && result.Online && result.Error == nil {
			return hsmSecret.Status.PrimaryDevice
		}
	}

	// Find device with highest version number among online devices
	var bestDevice string
	var highestVersion int64 = -1
	var mostRecentTime time.Time

	for deviceName, result := range deviceResults {
		if result.Online && result.Error == nil {
			// Prefer higher version numbers
			if result.Version > highestVersion {
				highestVersion = result.Version
				bestDevice = deviceName
				mostRecentTime = result.Timestamp
			} else if result.Version == highestVersion && result.Timestamp.After(mostRecentTime) {
				// If versions are equal, prefer more recent timestamp
				bestDevice = deviceName
				mostRecentTime = result.Timestamp
			}
		}
	}

	return bestDevice
}

// selectMostRecentData selects the most recently modified data
func (sm *SyncManager) selectMostRecentData(deviceResults map[string]DeviceResult, validDeviceData map[string]hsm.SecretData) hsm.SecretData {
	var mostRecentDevice string
	var mostRecentTime time.Time

	for deviceName, result := range deviceResults {
		if result.Online && result.Error == nil && result.Timestamp.After(mostRecentTime) {
			mostRecentTime = result.Timestamp
			mostRecentDevice = deviceName
		}
	}

	if mostRecentDevice != "" && validDeviceData[mostRecentDevice] != nil {
		return validDeviceData[mostRecentDevice]
	}

	// Return first available data if no clear winner
	for _, data := range validDeviceData {
		return data
	}

	return nil
}

// syncToSecondaryDevices syncs resolved data to all secondary devices
func (sm *SyncManager) syncToSecondaryDevices(ctx context.Context, devices []string, primaryDevice, secretPath string, data hsm.SecretData, logger logr.Logger) {
	for _, deviceName := range devices {
		if deviceName == primaryDevice {
			continue // Skip primary device
		}

		logger.Info("Syncing to secondary device", "device", deviceName)

		grpcClient, err := sm.agentManager.CreateSingleGRPCClient(ctx, deviceName, logger)
		if err != nil {
			logger.Error(err, "Failed to create gRPC client for sync", "device", deviceName)
			continue
		}

		if !grpcClient.IsConnected() {
			logger.V(1).Info("Device offline, skipping sync", "device", deviceName)
			if closeErr := grpcClient.Close(); closeErr != nil {
				logger.V(1).Info("Failed to close gRPC client", "error", closeErr)
			}
			continue
		}

		// Write data with updated version metadata
		metadata := &hsm.SecretMetadata{
			Tags: map[string]string{
				"sync.version":   fmt.Sprintf("%d", time.Now().Unix()),
				"sync.primary":   primaryDevice,
				"sync.timestamp": time.Now().Format(time.RFC3339),
			},
		}

		if err := grpcClient.WriteSecretWithMetadata(ctx, secretPath, data, metadata); err != nil {
			logger.Error(err, "Failed to sync to secondary device", "device", deviceName)
		} else {
			logger.Info("Successfully synced to secondary device", "device", deviceName)
		}

		if closeErr := grpcClient.Close(); closeErr != nil {
			logger.V(1).Info("Failed to close gRPC client", "error", closeErr)
		}
	}
}

// getAvailableDevices gets list of available HSM devices from HSMPools
func (sm *SyncManager) getAvailableDevices(ctx context.Context, namespace string) ([]string, error) {
	var hsmPoolList hsmv1alpha1.HSMPoolList
	if err := sm.client.List(ctx, &hsmPoolList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list HSM pools: %w", err)
	}

	deviceNames := make(map[string]bool)

	for _, pool := range hsmPoolList.Items {
		if pool.Status.Phase == hsmv1alpha1.HSMPoolPhaseReady {
			for _, deviceRef := range pool.Spec.HSMDeviceRefs {
				deviceNames[deviceRef] = true
			}
		}
	}

	devices := make([]string, 0, len(deviceNames))
	for deviceName := range deviceNames {
		devices = append(devices, deviceName)
	}

	sort.Strings(devices) // Ensure consistent ordering
	return devices, nil
}

// UpdateHSMSecretStatus updates the HSMSecret status with sync results
func (sm *SyncManager) UpdateHSMSecretStatus(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret, result *SyncResult) error {
	now := metav1.NewTime(time.Now())

	// Update overall status
	if result.Success {
		hsmSecret.Status.SyncStatus = hsmv1alpha1.SyncStatusInSync
		hsmSecret.Status.LastSyncTime = &now
		hsmSecret.Status.LastError = ""
	} else {
		hsmSecret.Status.SyncStatus = hsmv1alpha1.SyncStatusError
		hsmSecret.Status.LastError = "Failed to sync with any HSM device"
	}

	hsmSecret.Status.SyncConflict = result.ConflictDetected
	hsmSecret.Status.PrimaryDevice = result.PrimaryDevice

	// Update device-specific sync status
	hsmSecret.Status.DeviceSyncStatus = make([]hsmv1alpha1.HSMDeviceSync, 0, len(result.DeviceResults))

	for deviceName, deviceResult := range result.DeviceResults {
		syncTime := metav1.NewTime(deviceResult.Timestamp)
		deviceSync := hsmv1alpha1.HSMDeviceSync{
			DeviceName:   deviceName,
			LastSyncTime: &syncTime,
			Checksum:     deviceResult.Checksum,
			Online:       deviceResult.Online,
			Version:      deviceResult.Version,
		}

		if deviceResult.Error != nil {
			deviceSync.Status = hsmv1alpha1.SyncStatusError
			deviceSync.LastError = deviceResult.Error.Error()
		} else if deviceResult.Online {
			deviceSync.Status = hsmv1alpha1.SyncStatusInSync
		} else {
			deviceSync.Status = hsmv1alpha1.SyncStatusOutOfSync
		}

		hsmSecret.Status.DeviceSyncStatus = append(hsmSecret.Status.DeviceSyncStatus, deviceSync)
	}

	// Update Kubernetes Secret checksum if we have resolved data
	if result.ResolvedData != nil {
		hsmSecret.Status.SecretChecksum = sm.calculateChecksum(result.ResolvedData)
	}

	return sm.client.Status().Update(ctx, hsmSecret)
}

// calculateChecksum calculates SHA256 checksum of secret data
func (sm *SyncManager) calculateChecksum(data hsm.SecretData) string {
	if data == nil {
		return ""
	}

	h := sha256.New()

	// Sort keys for consistent checksum calculation
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		h.Write([]byte(k))
		h.Write(data[k])
	}

	return fmt.Sprintf("%x", h.Sum(nil))
}

// Helper function to parse version string
func parseVersion(versionStr string) (int64, error) {
	var version int64
	_, err := fmt.Sscanf(versionStr, "%d", &version)
	return version, err
}
