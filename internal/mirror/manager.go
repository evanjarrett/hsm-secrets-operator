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

package mirror

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// AgentManagerInterface defines the interface for HSM agent management used by mirror
type AgentManagerInterface interface {
	CreateGRPCClient(ctx context.Context, device hsmv1alpha1.DiscoveredDevice, logger logr.Logger) (hsm.Client, error)
	GetAvailableDevices(ctx context.Context, namespace string) ([]hsmv1alpha1.DiscoveredDevice, error)
}

// MirrorManager handles multi-device HSM mirroring and conflict resolution
type MirrorManager struct {
	client            client.Client
	agentManager      AgentManagerInterface
	logger            logr.Logger
	operatorNamespace string
}

// NewMirrorManager creates a new mirror manager
func NewMirrorManager(k8sClient client.Client, agentManager AgentManagerInterface, logger logr.Logger, operatorNamespace string) *MirrorManager {
	return &MirrorManager{
		client:            k8sClient,
		agentManager:      agentManager,
		logger:            logger.WithName("mirror-manager"),
		operatorNamespace: operatorNamespace,
	}
}

// MirrorResult represents the result of a per-secret mirroring operation
type MirrorResult struct {
	Success          bool
	SecretsProcessed int
	SecretsUpdated   int
	SecretsCreated   int
	MetadataRestored int
	SecretResults    map[string]SecretMirrorResult
	Errors           []string
}

// SecretMirrorResult represents the result of mirroring a specific secret
type SecretMirrorResult struct {
	SecretPath    string
	SourceDevice  string
	SourceVersion int64
	TargetDevices []string
	MirrorType    MirrorType
	Success       bool
	Error         error
}

// SecretInventory represents the state of a secret across all devices
type SecretInventory struct {
	SecretPath   string
	DeviceStates map[string]*SecretState // device -> metadata & presence
}

// SecretState represents the state of a secret on a specific device
type SecretState struct {
	Present     bool
	Version     int64
	Timestamp   time.Time
	Checksum    string
	HasMetadata bool
	Error       error
}

// SecretMirrorPlan represents the plan for mirroring a specific secret
type SecretMirrorPlan struct {
	SecretPath    string
	SourceDevice  string
	SourceVersion int64
	TargetDevices []string
	MirrorType    MirrorType
}

// MirrorType represents the type of mirror operation needed
type MirrorType int

const (
	MirrorTypeSkip            MirrorType = iota // Already in sync
	MirrorTypeUpdate                            // Update existing secret
	MirrorTypeCreate                            // Create missing secret
	MirrorTypeRestoreMetadata                   // Add metadata to existing secret
)

// buildSecretInventory builds a comprehensive inventory of secrets across all devices
//
//nolint:unparam // Error return preserved for future error handling scenarios
func (mm *MirrorManager) buildSecretInventory(ctx context.Context, secretPaths []string, devices []hsmv1alpha1.DiscoveredDevice, logger logr.Logger) (map[string]*SecretInventory, error) {
	inventory := make(map[string]*SecretInventory)

	// Initialize inventory entries for requested secrets
	for _, secretPath := range secretPaths {
		inventory[secretPath] = &SecretInventory{
			SecretPath:   secretPath,
			DeviceStates: make(map[string]*SecretState),
		}
	}

	// Check each device for the presence and state of each secret
	for _, device := range devices {
		deviceId := device.SerialNumber
		logger.Info("Checking device for secrets", "device", deviceId, "secretCount", len(secretPaths))

		// Create gRPC client for this device (agents are in operator namespace)
		grpcClient, err := mm.agentManager.CreateGRPCClient(ctx, device, logger)
		if err != nil {
			logger.Error(err, "Failed to create gRPC client", "device", deviceId)
			// Mark all secrets as having an error on this device
			for secretPath := range inventory {
				inventory[secretPath].DeviceStates[deviceId] = &SecretState{
					Present:     false,
					Version:     0,
					Timestamp:   time.Now(),
					Checksum:    "",
					HasMetadata: false,
					Error:       fmt.Errorf("gRPC client creation failed: %w", err),
				}
			}
			continue
		}

		// Check if device is connected
		if !grpcClient.IsConnected() {
			logger.V(1).Info("Device not connected", "device", deviceId)
			for secretPath := range inventory {
				inventory[secretPath].DeviceStates[deviceId] = &SecretState{
					Present:     false,
					Version:     0,
					Timestamp:   time.Now(),
					Checksum:    "",
					HasMetadata: false,
					Error:       fmt.Errorf("device not connected"),
				}
			}
			continue
		}

		// Check each secret on this device
		for _, secretPath := range secretPaths {
			state := &SecretState{
				Present:     false,
				Version:     0,
				Timestamp:   time.Now(),
				Checksum:    "",
				HasMetadata: false,
				Error:       nil,
			}

			// Try to read the secret to check if it exists
			data, err := grpcClient.ReadSecret(ctx, secretPath)
			if err != nil {
				// Secret doesn't exist on this device
				logger.V(1).Info("Secret not found on device", "device", deviceId, "secret", secretPath)
				state.Error = fmt.Errorf("secret not found: %w", err)
			} else {
				// Secret exists, calculate checksum
				state.Present = true
				state.Checksum = mm.calculateChecksum(data)
				logger.V(1).Info("Secret found on device", "device", deviceId, "secret", secretPath, "checksum", state.Checksum[:8])

				// Try to read metadata
				metadata, metaErr := grpcClient.ReadMetadata(ctx, secretPath)
				if metaErr == nil && metadata != nil && metadata.Labels != nil {
					// Extract version and timestamp from metadata
					if versionStr, exists := metadata.Labels["sync.version"]; exists {
						if version, parseErr := parseVersion(versionStr); parseErr == nil {
							state.Version = version
							state.HasMetadata = true
						}
					}
					if timestampStr, exists := metadata.Labels["sync.timestamp"]; exists {
						if timestamp, parseErr := time.Parse(time.RFC3339, timestampStr); parseErr == nil {
							state.Timestamp = timestamp
						}
					}
					logger.V(1).Info("Metadata found", "device", deviceId, "secret", secretPath,
						"version", state.Version, "timestamp", state.Timestamp.Format(time.RFC3339))
				} else {
					logger.V(1).Info("No metadata found", "device", deviceId, "secret", secretPath)
				}
			}

			inventory[secretPath].DeviceStates[deviceId] = state
		}
	}

	return inventory, nil
}

// createMirrorPlans analyzes secret inventory and creates sync plans for each secret
func (mm *MirrorManager) createMirrorPlans(inventory map[string]*SecretInventory, logger logr.Logger) []*SecretMirrorPlan {
	var plans []*SecretMirrorPlan

	for secretPath, secretInventory := range inventory {
		plan := mm.createMirrorPlanForSecret(secretPath, secretInventory, logger)
		if plan != nil {
			plans = append(plans, plan)
		}
	}

	return plans
}

// createMirrorPlanForSecret creates a sync plan for a specific secret across all devices
func (mm *MirrorManager) createMirrorPlanForSecret(secretPath string, inventory *SecretInventory, logger logr.Logger) *SecretMirrorPlan {
	// Find the authoritative source device (highest version, most recent timestamp)
	var sourceDevice string
	var sourceVersion int64 = -1
	var sourceTimestamp time.Time
	var devicesWithSecret []string
	var devicesNeedingSecret []string
	var devicesNeedingMetadata []string

	// Analyze all device states for this secret
	for deviceName, state := range inventory.DeviceStates {
		if state.Error != nil {
			logger.V(1).Info("Device has error, skipping", "device", deviceName, "secret", secretPath, "error", state.Error)
			continue
		}

		if state.Present {
			devicesWithSecret = append(devicesWithSecret, deviceName)

			// Check if this device has the most authoritative version
			if state.HasMetadata {
				if state.Version > sourceVersion ||
					(state.Version == sourceVersion && state.Timestamp.After(sourceTimestamp)) {
					sourceDevice = deviceName
					sourceVersion = state.Version
					sourceTimestamp = state.Timestamp
				}
			} else {
				// Secret exists but lacks metadata - needs restoration
				devicesNeedingMetadata = append(devicesNeedingMetadata, deviceName)

				// If no source found yet and this device has the secret, it could be the source
				// We'll use timestamp as fallback (creation time from our scan)
				if sourceDevice == "" {
					sourceDevice = deviceName
					sourceVersion = 0 // Will trigger metadata creation
					sourceTimestamp = state.Timestamp
				}
			}
		} else {
			// Device doesn't have this secret
			devicesNeedingSecret = append(devicesNeedingSecret, deviceName)
		}
	}

	// Determine sync operation type
	if len(devicesWithSecret) == 0 {
		// No devices have this secret - nothing to sync
		logger.V(1).Info("Secret not found on any device", "secret", secretPath)
		return nil
	}

	if len(devicesNeedingSecret) == 0 && len(devicesNeedingMetadata) == 0 {
		// All devices have the secret with metadata - check if they're in sync
		allInSync := true
		for _, state := range inventory.DeviceStates {
			if state.Error == nil && state.Present {
				if !state.HasMetadata || state.Version != sourceVersion {
					allInSync = false
					break
				}
			}
		}

		if allInSync {
			logger.V(1).Info("Secret already in sync across all devices", "secret", secretPath)
			return &SecretMirrorPlan{
				SecretPath:    secretPath,
				SourceDevice:  sourceDevice,
				SourceVersion: sourceVersion,
				TargetDevices: []string{}, // No targets needed
				MirrorType:    MirrorTypeSkip,
			}
		}
	}

	// Determine target devices that need updates
	var targetDevices []string
	var syncType MirrorType

	// Add devices that need the secret created
	targetDevices = append(targetDevices, devicesNeedingSecret...)
	if len(devicesNeedingSecret) > 0 {
		syncType = MirrorTypeCreate
	}

	// Add devices that need metadata restoration
	targetDevices = append(targetDevices, devicesNeedingMetadata...)
	if len(devicesNeedingMetadata) > 0 && syncType != MirrorTypeCreate {
		syncType = MirrorTypeRestoreMetadata
	}

	// Add devices that have outdated versions
	for deviceName, state := range inventory.DeviceStates {
		if state.Error == nil && state.Present && state.HasMetadata {
			if state.Version < sourceVersion {
				// This device has an older version
				targetDevices = append(targetDevices, deviceName)
				if syncType != MirrorTypeCreate && syncType != MirrorTypeRestoreMetadata {
					syncType = MirrorTypeUpdate
				}
			}
		}
	}

	// Remove duplicates from target devices
	targetDevices = removeDuplicates(targetDevices)

	// Remove source device from targets (don't sync to itself)
	targetDevices = removeDevice(targetDevices, sourceDevice)

	if len(targetDevices) == 0 {
		// No targets needed
		return &SecretMirrorPlan{
			SecretPath:    secretPath,
			SourceDevice:  sourceDevice,
			SourceVersion: sourceVersion,
			TargetDevices: []string{},
			MirrorType:    MirrorTypeSkip,
		}
	}

	logger.Info("Created sync plan", "secret", secretPath,
		"sourceDevice", sourceDevice, "sourceVersion", sourceVersion,
		"targetDevices", len(targetDevices), "syncType", syncType)

	return &SecretMirrorPlan{
		SecretPath:    secretPath,
		SourceDevice:  sourceDevice,
		SourceVersion: sourceVersion,
		TargetDevices: targetDevices,
		MirrorType:    syncType,
	}
}

// removeDuplicates removes duplicate strings from a slice
func removeDuplicates(slice []string) []string {
	keys := make(map[string]bool)
	var result []string

	for _, item := range slice {
		if !keys[item] {
			keys[item] = true
			result = append(result, item)
		}
	}

	return result
}

// removeDevice removes a specific device from a slice of devices
func removeDevice(devices []string, deviceToRemove string) []string {
	var result []string

	for _, device := range devices {
		if device != deviceToRemove {
			result = append(result, device)
		}
	}

	return result
}

// executeMirrorPlans executes sync operations for all planned secret synchronizations
func (mm *MirrorManager) executeMirrorPlans(ctx context.Context, plans []*SecretMirrorPlan, deviceLookup map[string]hsmv1alpha1.DiscoveredDevice, logger logr.Logger) *MirrorResult {
	result := &MirrorResult{
		Success:          true,
		SecretsProcessed: len(plans),
		SecretsUpdated:   0,
		SecretsCreated:   0,
		MetadataRestored: 0,
		SecretResults:    make(map[string]SecretMirrorResult),
		Errors:           []string{},
	}

	for _, plan := range plans {
		secretResult := mm.executeMirrorPlan(ctx, plan, deviceLookup, logger)
		result.SecretResults[plan.SecretPath] = secretResult

		if secretResult.Success {
			switch secretResult.MirrorType {
			case MirrorTypeCreate:
				result.SecretsCreated++
			case MirrorTypeUpdate:
				result.SecretsUpdated++
			case MirrorTypeRestoreMetadata:
				result.MetadataRestored++
			}
		} else {
			result.Success = false
			if secretResult.Error != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", plan.SecretPath, secretResult.Error))
			}
		}
	}

	return result
}

// executeMirrorPlan executes a single secret sync plan
func (mm *MirrorManager) executeMirrorPlan(ctx context.Context, plan *SecretMirrorPlan, deviceLookup map[string]hsmv1alpha1.DiscoveredDevice, logger logr.Logger) SecretMirrorResult {
	result := SecretMirrorResult{
		SecretPath:    plan.SecretPath,
		SourceDevice:  plan.SourceDevice,
		SourceVersion: plan.SourceVersion,
		TargetDevices: plan.TargetDevices,
		MirrorType:    plan.MirrorType,
		Success:       false,
		Error:         nil,
	}

	// Skip if no sync needed
	if plan.MirrorType == MirrorTypeSkip {
		result.Success = true
		logger.V(1).Info("Skipping sync - already in sync", "secret", plan.SecretPath)
		return result
	}

	// Get source data and metadata
	sourceDevice, ok := deviceLookup[plan.SourceDevice]
	if !ok {
		result.Error = fmt.Errorf("source device not found in lookup: %s", plan.SourceDevice)
		return result
	}
	sourceData, sourceMetadata, err := mm.readSecretWithMetadata(ctx, sourceDevice, plan.SecretPath, logger)
	if err != nil {
		result.Error = fmt.Errorf("failed to read source secret: %w", err)
		logger.Error(err, "Failed to read source secret", "device", plan.SourceDevice, "secret", plan.SecretPath)
		return result
	}

	// Prepare metadata for sync
	newVersion := time.Now().Unix()
	if sourceMetadata != nil && sourceMetadata.Labels != nil {
		if versionStr, exists := sourceMetadata.Labels["sync.version"]; exists {
			if existingVersion, parseErr := parseVersion(versionStr); parseErr == nil && existingVersion > 0 {
				newVersion = existingVersion
			}
		}
	}

	// Create or update metadata
	syncMetadata := &hsm.SecretMetadata{
		Labels: map[string]string{
			"sync.version":   fmt.Sprintf("%d", newVersion),
			"sync.timestamp": time.Now().Format(time.RFC3339),
			"sync.source":    plan.SourceDevice,
		},
	}

	// Handle metadata restoration on source device if needed
	if plan.MirrorType == MirrorTypeRestoreMetadata {
		if sourceMetadata == nil || sourceMetadata.Labels == nil || sourceMetadata.Labels["sync.version"] == "" {
			logger.Info("Restoring metadata on source device", "device", plan.SourceDevice, "secret", plan.SecretPath)
			if err := mm.writeSecretWithMetadata(ctx, sourceDevice, plan.SecretPath, sourceData, syncMetadata, logger); err != nil {
				result.Error = fmt.Errorf("failed to restore metadata on source: %w", err)
				return result
			}
		}
	}

	// Sync to target devices
	successfulTargets := 0
	for _, targetDeviceId := range plan.TargetDevices {
		targetDevice, ok := deviceLookup[targetDeviceId]
		if !ok {
			logger.Error(fmt.Errorf("target device not found in lookup: %s", targetDeviceId), "Failed to find target device", "target", targetDeviceId, "secret", plan.SecretPath)
			continue
		}

		logger.Info("Syncing secret to target device", "secret", plan.SecretPath, "source", plan.SourceDevice, "target", targetDeviceId, "version", newVersion)

		var syncErr error
		switch plan.MirrorType {
		case MirrorTypeCreate, MirrorTypeUpdate:
			syncErr = mm.writeSecretWithMetadata(ctx, targetDevice, plan.SecretPath, sourceData, syncMetadata, logger)
		case MirrorTypeRestoreMetadata:
			// For metadata restoration, we just update the metadata without changing the data
			syncErr = mm.writeMetadataOnly(ctx, targetDevice, plan.SecretPath, syncMetadata, logger)
		}

		if syncErr != nil {
			logger.Error(syncErr, "Failed to sync to target device", "target", targetDeviceId, "secret", plan.SecretPath)
			if result.Error == nil {
				result.Error = syncErr
			}
		} else {
			successfulTargets++
			logger.Info("Successfully synced to target device", "target", targetDevice, "secret", plan.SecretPath)
		}
	}

	// Consider sync successful if we synced to at least some targets (partial success)
	result.Success = successfulTargets > 0 || len(plan.TargetDevices) == 0

	if result.Success {
		logger.Info("Sync plan executed successfully", "secret", plan.SecretPath,
			"syncType", plan.MirrorType, "targetCount", successfulTargets)
	}

	return result
}

// readSecretWithMetadata reads both secret data and metadata from a device
func (mm *MirrorManager) readSecretWithMetadata(ctx context.Context, device hsmv1alpha1.DiscoveredDevice, secretPath string, logger logr.Logger) (hsm.SecretData, *hsm.SecretMetadata, error) {
	grpcClient, err := mm.agentManager.CreateGRPCClient(ctx, device, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create gRPC client: %w", err)
	}

	if !grpcClient.IsConnected() {
		return nil, nil, fmt.Errorf("device not connected")
	}

	// Read secret data
	data, err := grpcClient.ReadSecret(ctx, secretPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read secret: %w", err)
	}

	// Read metadata (may not exist)
	metadata, err := grpcClient.ReadMetadata(ctx, secretPath)
	if err != nil {
		logger.V(1).Info("No metadata found for secret", "secret", secretPath, "device", device.SerialNumber)
		metadata = nil // Not an error - metadata may not exist
	}

	return data, metadata, nil
}

// writeSecretWithMetadata writes both secret data and metadata to a device
func (mm *MirrorManager) writeSecretWithMetadata(ctx context.Context, device hsmv1alpha1.DiscoveredDevice, secretPath string, data hsm.SecretData, metadata *hsm.SecretMetadata, logger logr.Logger) error {
	grpcClient, err := mm.agentManager.CreateGRPCClient(ctx, device, logger)
	if err != nil {
		return fmt.Errorf("failed to create gRPC client: %w", err)
	}
	if !grpcClient.IsConnected() {
		return fmt.Errorf("device not connected")
	}

	// Write secret with metadata
	if err := grpcClient.WriteSecretWithMetadata(ctx, secretPath, data, metadata); err != nil {
		return fmt.Errorf("failed to write secret with metadata: %w", err)
	}

	return nil
}

// writeMetadataOnly updates only the metadata for an existing secret
func (mm *MirrorManager) writeMetadataOnly(ctx context.Context, device hsmv1alpha1.DiscoveredDevice, secretPath string, metadata *hsm.SecretMetadata, logger logr.Logger) error {
	grpcClient, err := mm.agentManager.CreateGRPCClient(ctx, device, logger)
	if err != nil {
		return fmt.Errorf("failed to create gRPC client: %w", err)
	}

	if !grpcClient.IsConnected() {
		return fmt.Errorf("device not connected")
	}

	// Read existing secret data first
	existingData, err := grpcClient.ReadSecret(ctx, secretPath)
	if err != nil {
		return fmt.Errorf("failed to read existing secret data: %w", err)
	}

	// Write secret with updated metadata
	if err := grpcClient.WriteSecretWithMetadata(ctx, secretPath, existingData, metadata); err != nil {
		return fmt.Errorf("failed to write secret with metadata: %w", err)
	}

	return nil
}

// MirrorAllSecrets performs device-scoped mirroring of ALL secrets across HSM devices
// This discovers all secrets on any device and ensures they exist on all other devices
func (mm *MirrorManager) MirrorAllSecrets(ctx context.Context) (*MirrorResult, error) {
	logger := mm.logger.WithValues("operation", "device-scoped-mirror")
	logger.Info("Starting device-scoped mirroring of all HSM secrets")

	// Get all available HSM devices
	devices, err := mm.agentManager.GetAvailableDevices(ctx, mm.operatorNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get available devices: %w", err)
	}

	if len(devices) <= 1 {
		logger.Info("Skipping mirroring - need at least 2 devices", "devices", len(devices))
		return &MirrorResult{Success: true, SecretsProcessed: 0}, nil
	}

	// Extract device identifiers for logging
	deviceIds := make([]string, len(devices))
	for i, device := range devices {
		deviceIds[i] = device.SerialNumber
	}
	logger.Info("Starting mirroring across devices", "devices", deviceIds, "deviceCount", len(devices))

	// Discover all secrets across all devices
	allSecretPaths := mm.discoverAllSecrets(ctx, devices, logger)

	if len(allSecretPaths) == 0 {
		logger.Info("No secrets found to mirror")
		return &MirrorResult{Success: true, SecretsProcessed: 0}, nil
	}

	logger.Info("Discovered secrets for mirroring", "secretCount", len(allSecretPaths), "secrets", allSecretPaths)

	// Create device lookup map for execution functions
	deviceLookup := make(map[string]hsmv1alpha1.DiscoveredDevice)
	for _, device := range devices {
		deviceLookup[device.SerialNumber] = device
	}

	// Build inventory for all discovered secrets
	inventory, err := mm.buildSecretInventory(ctx, allSecretPaths, devices, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to build secret inventory: %w", err)
	}

	// Create mirror plans
	plans := mm.createMirrorPlans(inventory, logger)
	logger.Info("Created mirror plans", "planCount", len(plans))

	// Execute mirror plans
	return mm.executeMirrorPlans(ctx, plans, deviceLookup, logger), nil
}

// discoverAllSecrets discovers all secrets present on any HSM device with retry logic
func (mm *MirrorManager) discoverAllSecrets(ctx context.Context, devices []hsmv1alpha1.DiscoveredDevice, logger logr.Logger) []string {
	secretPaths := make(map[string]bool)
	failedDevices := make([]hsmv1alpha1.DiscoveredDevice, 0)

	logger.Info("Starting secret discovery", "totalDevices", len(devices))

	for _, device := range devices {
		deviceId := device.SerialNumber
		deviceLogger := logger.WithValues("device", deviceId)

		secrets, err := mm.discoverDeviceSecretsWithRetry(ctx, device, deviceLogger, 3)
		if err != nil {
			deviceLogger.Error(err, "Failed to discover secrets on device after retries")
			failedDevices = append(failedDevices, device)
			continue
		}

		deviceLogger.Info("Successfully discovered secrets on device", "secretCount", len(secrets))
		for _, secretPath := range secrets {
			secretPaths[secretPath] = true
		}
	}

	// Log summary of discovery results
	successCount := len(devices) - len(failedDevices)
	logger.Info("Discovery completed",
		"successfulDevices", successCount,
		"failedDevices", len(failedDevices),
		"totalSecretsFound", len(secretPaths))

	if len(failedDevices) > 0 {
		for _, device := range failedDevices {
			logger.Info("Device failed discovery", "device", device.SerialNumber, "nodeName", device.NodeName, "devicePath", device.DevicePath)
		}
	}

	// Convert to sorted slice
	result := make([]string, 0, len(secretPaths))
	for secretPath := range secretPaths {
		result = append(result, secretPath)
	}
	sort.Strings(result)

	return result
}

// discoverDeviceSecretsWithRetry attempts to discover secrets from a single device with retry logic
func (mm *MirrorManager) discoverDeviceSecretsWithRetry(ctx context.Context, device hsmv1alpha1.DiscoveredDevice, logger logr.Logger, maxRetries int) ([]string, error) {
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		attemptLogger := logger.WithValues("attempt", attempt, "maxRetries", maxRetries)

		// Create client with connection pool retry logic
		hsmClient, err := mm.agentManager.CreateGRPCClient(ctx, device, attemptLogger)
		if err != nil {
			lastErr = fmt.Errorf("failed to create gRPC client: %w", err)
			attemptLogger.Info("Failed to connect to device", "error", err)

			if attempt < maxRetries {
				backoffDuration := time.Duration(attempt) * time.Second
				attemptLogger.Info("Retrying device connection after backoff", "backoff", backoffDuration.String())
				time.Sleep(backoffDuration)
				continue
			}
			break
		}

		// Ensure client is closed after use
		defer func() {
			if closeErr := hsmClient.Close(); closeErr != nil {
				logger.V(1).Info("Error closing HSM client", "error", closeErr)
			}
		}()

		// Add timeout for list secrets operation
		listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		secrets, err := hsmClient.ListSecrets(listCtx, "")
		cancel()

		if err != nil {
			lastErr = fmt.Errorf("failed to list secrets: %w", err)
			attemptLogger.Info("Failed to list secrets on device", "error", err)

			// Check for specific connection-related errors that might benefit from retry
			if isConnectionError(err) && attempt < maxRetries {
				backoffDuration := time.Duration(attempt) * time.Second
				attemptLogger.Info("Connection error detected, retrying after backoff",
					"backoff", backoffDuration.String(), "error", err)
				time.Sleep(backoffDuration)
				continue
			}

			if attempt < maxRetries {
				backoffDuration := time.Duration(attempt) * time.Second
				attemptLogger.Info("Retrying list secrets after backoff", "backoff", backoffDuration.String())
				time.Sleep(backoffDuration)
				continue
			}
			break
		}

		// Success case
		attemptLogger.Info("Successfully listed secrets", "secretCount", len(secrets))
		return secrets, nil
	}

	return nil, fmt.Errorf("failed to discover secrets after %d attempts: %w", maxRetries, lastErr)
}

// isConnectionError checks if an error is related to connection issues that might benefit from retry
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	connectionErrors := []string{
		"grpc: the client connection is closing",
		"connection refused",
		"connection reset",
		"connection timeout",
		"context deadline exceeded",
		"rpc error: code = Canceled",
		"rpc error: code = Unavailable",
	}

	for _, connErr := range connectionErrors {
		if strings.Contains(errStr, connErr) {
			return true
		}
	}

	return false
}

// calculateChecksum calculates SHA256 checksum of secret data
func (mm *MirrorManager) calculateChecksum(data hsm.SecretData) string {
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

// WaitForAgentsReady waits for at least one HSM device to have ready agents
// Returns true when agents are ready, false on timeout
func (mm *MirrorManager) WaitForAgentsReady(ctx context.Context, timeout time.Duration) (bool, error) {
	logger := mm.logger.WithValues("operation", "wait-for-agents")
	logger.Info("Waiting for HSM agents to be ready", "timeout", timeout)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Timeout waiting for agents to be ready")
			return false, ctx.Err()

		case <-ticker.C:
			devices, err := mm.agentManager.GetAvailableDevices(ctx, mm.operatorNamespace)
			if err != nil {
				logger.V(1).Info("Failed to check available devices", "error", err)
				continue
			}

			if len(devices) > 0 {
				// Try to connect to at least one device to verify agents are actually ready
				for _, device := range devices {
					grpcClient, err := mm.agentManager.CreateGRPCClient(ctx, device, logger)
					if err != nil {
						logger.V(1).Info("Agent not ready yet", "device", device.SerialNumber, "error", err)
						continue
					}

					// Test connection
					if grpcClient.IsConnected() {
						if closeErr := grpcClient.Close(); closeErr != nil {
							logger.V(1).Info("Failed to close gRPC client", "error", closeErr)
						}
						logger.Info("HSM agents are ready", "readyDevices", len(devices))
						return true, nil
					}
				}
			}

			logger.V(1).Info("Still waiting for agents", "availableDevices", len(devices))
		}
	}
}

// Helper function to parse version string
func parseVersion(versionStr string) (int64, error) {
	var version int64
	_, err := fmt.Sscanf(versionStr, "%d", &version)
	return version, err
}
