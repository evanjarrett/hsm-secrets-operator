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

package controller

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

const (
	// HSMSecretFinalizer is the finalizer used by the HSMSecret controller
	HSMSecretFinalizer = "hsmsecret.hsm.j5t.io/finalizer"

	// DefaultSyncInterval is the default sync interval in seconds
	DefaultSyncInterval = 30
)

// HSMSecretReconciler reconciles a HSMSecret object
type HSMSecretReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	AgentManager      *agent.Manager
	OperatorNamespace string
	OperatorName      string
}

// HSMDeviceClients holds multiple HSM devices and their clients
type HSMDeviceClients struct {
	Devices []*hsmv1alpha1.HSMDevice
	Clients []hsm.Client
}

// Close closes all clients
func (hdc *HSMDeviceClients) Close() error {
	var errs []error
	for _, hsmClient := range hdc.Clients {
		if hsmClient != nil {
			if err := hsmClient.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to close %d clients: %v", len(errs), errs)
	}
	return nil
}

// DeviceInfo holds device data and metadata for version-based conflict resolution
type DeviceInfo struct {
	Data      hsm.SecretData
	Metadata  *hsm.SecretMetadata
	Version   int64
	Checksum  string
	Timestamp time.Time
}

// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmsecrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmsecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmsecrets/finalizers,verbs=update
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmdevices,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles HSMSecret reconciliation
func (r *HSMSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the HSMSecret instance
	var hsmSecret hsmv1alpha1.HSMSecret
	if err := r.Get(ctx, req.NamespacedName, &hsmSecret); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("HSMSecret resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get HSMSecret")
		return ctrl.Result{}, err
	}

	// Check if this HSMSecret should be handled by this operator instance
	if !r.shouldHandleSecret(&hsmSecret) {
		logger.V(1).Info("HSMSecret not assigned to this operator instance, skipping",
			"secret", hsmSecret.Name,
			"namespace", hsmSecret.Namespace,
			"operatorName", r.OperatorName,
			"operatorNamespace", r.OperatorNamespace)
		return ctrl.Result{}, nil
	}

	// Find target HSM devices and ensure agents are running
	deviceClients, err := r.ensureHSMAgents(ctx, &hsmSecret)
	if err != nil {
		logger.Error(err, "Failed to ensure HSM agents")
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}
	defer func() {
		if err := deviceClients.Close(); err != nil {
			logger.Error(err, "Failed to close device clients")
		}
	}()

	// Check that we have at least one connected client
	if len(deviceClients.Clients) == 0 {
		logger.Error(fmt.Errorf("no HSM agents available"), "No HSM agents connected")
		return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
	}

	// Validate all clients are connected
	for i, hsmClient := range deviceClients.Clients {
		if hsmClient == nil || !hsmClient.IsConnected() {
			logger.Error(fmt.Errorf("HSM agent not available"), "HSM agent not connected", "device", deviceClients.Devices[i].Name)
			return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
		}
	}

	// Handle deletion
	if hsmSecret.DeletionTimestamp != nil {
		return ctrl.Result{}, r.reconcileDelete(ctx, &hsmSecret)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&hsmSecret, HSMSecretFinalizer) {
		controllerutil.AddFinalizer(&hsmSecret, HSMSecretFinalizer)
		if err := r.Update(ctx, &hsmSecret); err != nil {
			logger.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Reconcile the HSMSecret across all available devices
	result, err := r.reconcileSecret(ctx, &hsmSecret, deviceClients)
	if err != nil {
		logger.Error(err, "Failed to reconcile HSMSecret")
		r.updateStatus(ctx, &hsmSecret, hsmv1alpha1.SyncStatusError, err.Error())
	}

	return result, err
}

// ensureHSMAgents finds all HSM devices and ensures agents are running for each
func (r *HSMSecretReconciler) ensureHSMAgents(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret) (*HSMDeviceClients, error) {
	logger := log.FromContext(ctx)

	// Find all appropriate HSM devices
	hsmDevices, err := r.findAllHSMDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to find HSM devices for secret: %w", err)
	}

	if r.AgentManager == nil {
		return nil, fmt.Errorf("agent manager not configured")
	}

	deviceClients := &HSMDeviceClients{
		Devices: hsmDevices,
		Clients: make([]hsm.Client, 0, len(hsmDevices)),
	}

	// Ensure agent pods are running for all devices and create clients
	for _, hsmDevice := range hsmDevices {
		// EnsureAgent ensures agents for all devices in the pool
		err = r.AgentManager.EnsureAgent(ctx, hsmDevice, hsmSecret)
		if err != nil {
			// Clean up any successful connections before returning error
			if err := deviceClients.Close(); err != nil {
				logger.Error(err, "Failed to close device clients during cleanup")
			}
			return nil, fmt.Errorf("failed to ensure HSM agent for device %s: %w", hsmDevice.Name, err)
		}

		// Create gRPC client using agent manager's direct pod connections
		agentClient, err := r.AgentManager.CreateSingleGRPCClient(ctx, hsmDevice.Name, logger)
		if err != nil {
			// Clean up any successful connections before returning error
			if err := deviceClients.Close(); err != nil {
				logger.Error(err, "Failed to close device clients during cleanup")
			}
			return nil, fmt.Errorf("failed to create gRPC client for device %s: %w", hsmDevice.Name, err)
		}

		// Test connection
		if !agentClient.IsConnected() {
			logger.Info("Waiting for HSM agent to be ready", "device", hsmDevice.Name)
			time.Sleep(5 * time.Second)

			// Test again
			if !agentClient.IsConnected() {
				if err := agentClient.Close(); err != nil {
					logger.Error(err, "Failed to close gRPC client after failed connection test")
				}
				// Clean up any successful connections before returning error
				if err := deviceClients.Close(); err != nil {
					logger.Error(err, "Failed to close device clients during cleanup")
				}
				return nil, fmt.Errorf("HSM agent not ready after waiting for device %s", hsmDevice.Name)
			}
		}

		deviceClients.Clients = append(deviceClients.Clients, agentClient)
	}

	return deviceClients, nil
}

// reconcileSecret handles HSM secret reconciliation across all available devices
func (r *HSMSecretReconciler) reconcileSecret(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret, deviceClients *HSMDeviceClients) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Set default values
	secretName := hsmSecret.Spec.SecretName
	if secretName == "" {
		secretName = hsmSecret.Name
	}

	syncInterval := hsmSecret.Spec.SyncInterval
	if syncInterval == 0 {
		syncInterval = DefaultSyncInterval
	}

	// Read from all devices (handles both single and multi-device scenarios)
	if len(deviceClients.Devices) > 1 {
		logger.Info("Multi-device setup detected, checking for consistency", "deviceCount", len(deviceClients.Devices))
	} else {
		logger.V(1).Info("Single device setup", "deviceCount", len(deviceClients.Devices))
	}

	deviceInfos, primaryDevice, err := r.readFromAllDevices(ctx, hsmSecret, deviceClients)
	if err != nil {
		logger.Error(err, "Failed to read from devices")
		return ctrl.Result{RequeueAfter: time.Minute * 2}, err
	}

	// Check for inconsistencies and sync if needed (only matters for multi-device)
	if len(deviceClients.Devices) > 1 && r.detectInconsistencies(deviceInfos) {
		logger.Info("Inconsistency detected between devices, performing sync", "primaryDevice", primaryDevice)

		if err := r.syncAcrossDevices(ctx, hsmSecret, deviceClients, primaryDevice, deviceInfos[primaryDevice]); err != nil {
			logger.Error(err, "Failed to sync across devices")
			return ctrl.Result{RequeueAfter: time.Minute * 2}, err
		}
		logger.Info("Successfully synced secret across all devices")
	}

	// Use the primary device data for Kubernetes secret
	hsmData := deviceInfos[primaryDevice].Data
	hsmMetadata := deviceInfos[primaryDevice].Metadata

	return r.updateKubernetesSecret(ctx, hsmSecret, secretName, hsmData, hsmMetadata, syncInterval)
}

// readFromAllDevices reads the secret from all devices with version information
func (r *HSMSecretReconciler) readFromAllDevices(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret, deviceClients *HSMDeviceClients) (map[string]*DeviceInfo, string, error) {
	logger := log.FromContext(ctx)
	deviceInfos := make(map[string]*DeviceInfo)

	for i, hsmClient := range deviceClients.Clients {
		deviceName := deviceClients.Devices[i].Name

		data, err := hsmClient.ReadSecret(ctx, hsmSecret.Name)
		if err != nil {
			logger.V(1).Info("Failed to read from device", "device", deviceName, "error", err)
			// Continue with other devices - this might be a new device without the secret
			continue
		}

		// Read metadata to get version information
		metadata, err := hsmClient.ReadMetadata(ctx, hsmSecret.Name)
		if err != nil {
			logger.V(1).Info("Failed to read metadata from device", "device", deviceName, "error", err)
			metadata = nil
		}

		// Extract version from metadata
		var version int64
		if metadata != nil && metadata.Labels != nil {
			if versionStr, exists := metadata.Labels["sync.version"]; exists {
				if parsedVersion, parseErr := r.parseVersion(versionStr); parseErr == nil {
					version = parsedVersion
				}
			}
		}

		deviceInfos[deviceName] = &DeviceInfo{
			Data:      data,
			Metadata:  metadata,
			Version:   version,
			Checksum:  hsm.CalculateChecksum(data),
			Timestamp: time.Now(),
		}
	}

	if len(deviceInfos) == 0 {
		return nil, "", fmt.Errorf("no devices contain the secret %s", hsmSecret.Name)
	}

	// Select primary device based on version and HSMSecret status
	primaryDevice := r.selectPrimaryDevice(deviceInfos, hsmSecret)

	return deviceInfos, primaryDevice, nil
}

// parseVersion parses version string from metadata
func (r *HSMSecretReconciler) parseVersion(versionStr string) (int64, error) {
	var version int64
	_, err := fmt.Sscanf(versionStr, "%d", &version)
	return version, err
}

// selectPrimaryDevice chooses the primary device based on version and HSMSecret status
func (r *HSMSecretReconciler) selectPrimaryDevice(deviceInfos map[string]*DeviceInfo, hsmSecret *hsmv1alpha1.HSMSecret) string {
	// Check if there's already a designated primary in the status that's still available
	if hsmSecret.Status.PrimaryDevice != "" {
		if info, exists := deviceInfos[hsmSecret.Status.PrimaryDevice]; exists && info != nil {
			return hsmSecret.Status.PrimaryDevice
		}
	}

	// Find device with highest version number
	var bestDevice string
	var highestVersion int64 = -1
	var mostRecentTime time.Time

	for deviceName, info := range deviceInfos {
		// Prefer higher version numbers
		if info.Version > highestVersion {
			highestVersion = info.Version
			bestDevice = deviceName
			mostRecentTime = info.Timestamp
		} else if info.Version == highestVersion && info.Timestamp.After(mostRecentTime) {
			// If versions are equal, prefer more recent timestamp
			bestDevice = deviceName
			mostRecentTime = info.Timestamp
		}
	}

	return bestDevice
}

// detectInconsistencies checks if devices have different versions of the secret
func (r *HSMSecretReconciler) detectInconsistencies(deviceInfos map[string]*DeviceInfo) bool {
	if len(deviceInfos) <= 1 {
		return false
	}

	checksums := make(map[string]int)
	for _, info := range deviceInfos {
		checksums[info.Checksum]++
	}

	// Inconsistency if we have more than one unique checksum
	return len(checksums) > 1
}

// syncAcrossDevices copies the primary device's secret to all other devices with proper versioning
func (r *HSMSecretReconciler) syncAcrossDevices(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret, deviceClients *HSMDeviceClients, primaryDevice string, primaryInfo *DeviceInfo) error {
	logger := log.FromContext(ctx)

	for i, hsmClient := range deviceClients.Clients {
		deviceName := deviceClients.Devices[i].Name

		// Skip the primary device
		if deviceName == primaryDevice {
			continue
		}

		logger.Info("Syncing secret to device", "device", deviceName, "from", primaryDevice)

		// Create metadata with updated version and sync information
		newVersion := time.Now().Unix()
		metadata := &hsm.SecretMetadata{
			Labels: map[string]string{
				"sync.version":   fmt.Sprintf("%d", newVersion),
				"sync.primary":   primaryDevice,
				"sync.timestamp": time.Now().Format(time.RFC3339),
			},
		}

		// Copy over other metadata if it exists
		if primaryInfo.Metadata != nil {
			if metadata.Labels == nil {
				metadata.Labels = make(map[string]string)
			}
			if primaryInfo.Metadata.Description != "" {
				metadata.Description = primaryInfo.Metadata.Description
			}
			if primaryInfo.Metadata.Format != "" {
				metadata.Format = primaryInfo.Metadata.Format
			}
			if primaryInfo.Metadata.DataType != "" {
				metadata.DataType = primaryInfo.Metadata.DataType
			}
			if primaryInfo.Metadata.Source != "" {
				metadata.Source = primaryInfo.Metadata.Source
			}
			// Copy non-sync labels
			for key, value := range primaryInfo.Metadata.Labels {
				if !strings.HasPrefix(key, "sync.") {
					metadata.Labels[key] = value
				}
			}
		}

		// Write the primary device's data with metadata to this device
		if err := hsmClient.WriteSecretWithMetadata(ctx, hsmSecret.Name, primaryInfo.Data, metadata); err != nil {
			logger.Error(err, "Failed to sync secret to device", "device", deviceName)
			return fmt.Errorf("failed to sync to device %s: %w", deviceName, err)
		}

		logger.Info("Successfully synced secret to device", "device", deviceName, "version", newVersion)
	}

	return nil
}

// updateKubernetesSecret updates the Kubernetes Secret with the given data
func (r *HSMSecretReconciler) updateKubernetesSecret(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret, secretName string, hsmData hsm.SecretData, hsmMetadata *hsm.SecretMetadata, syncInterval int32) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Calculate HSM checksum
	hsmChecksum := hsm.CalculateChecksum(hsmData)

	// Get or create Kubernetes Secret
	var k8sSecret corev1.Secret
	secretKey := types.NamespacedName{
		Namespace: hsmSecret.Namespace,
		Name:      secretName,
	}

	err := r.Get(ctx, secretKey, &k8sSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new secret
			k8sSecret = r.buildSecret(hsmSecret, secretName, hsmData, hsmMetadata)
			if err := r.Create(ctx, &k8sSecret); err != nil {
				logger.Error(err, "Failed to create Secret")
				return ctrl.Result{}, err
			}
			logger.Info("Created new Secret", "secret", secretKey)
		} else {
			logger.Error(err, "Failed to get Secret")
			return ctrl.Result{}, err
		}
	} else {
		// Update existing secret if needed
		r.updateSecretWithMetadata(&k8sSecret, hsmSecret, hsmData, hsmMetadata)
		if err := r.Update(ctx, &k8sSecret); err != nil {
			logger.Error(err, "Failed to update Secret")
			return ctrl.Result{}, err
		}
		logger.V(1).Info("Updated existing Secret", "secret", secretKey)
	}

	// Calculate K8s Secret checksum
	secretChecksum := hsm.CalculateChecksum(r.convertSecretDataToHSMData(k8sSecret.Data))

	// Update status
	syncStatus := hsmv1alpha1.SyncStatusInSync
	if hsmChecksum != secretChecksum {
		syncStatus = hsmv1alpha1.SyncStatusOutOfSync
	}

	r.updateStatus(ctx, hsmSecret, syncStatus, "")
	hsmSecret.Status.HSMChecksum = hsmChecksum
	hsmSecret.Status.SecretChecksum = secretChecksum
	hsmSecret.Status.SecretRef = &corev1.ObjectReference{
		APIVersion: "v1",
		Kind:       "Secret",
		Name:       k8sSecret.Name,
		Namespace:  k8sSecret.Namespace,
		UID:        k8sSecret.UID,
	}

	if err := r.Status().Update(ctx, hsmSecret); err != nil {
		logger.Error(err, "Failed to update HSMSecret status")
		return ctrl.Result{}, err
	}

	// Schedule next sync if AutoSync is enabled
	if hsmSecret.Spec.AutoSync {
		return ctrl.Result{RequeueAfter: time.Second * time.Duration(syncInterval)}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileDelete handles HSMSecret deletion
func (r *HSMSecretReconciler) reconcileDelete(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret) error {
	logger := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(hsmSecret, HSMSecretFinalizer) {
		logger.Info("Cleaning up HSMSecret resources")

		// Optionally delete the Kubernetes Secret
		secretName := hsmSecret.Spec.SecretName
		if secretName == "" {
			secretName = hsmSecret.Name
		}

		secretKey := types.NamespacedName{
			Namespace: hsmSecret.Namespace,
			Name:      secretName,
		}

		var k8sSecret corev1.Secret
		if err := r.Get(ctx, secretKey, &k8sSecret); err == nil {
			if err := r.Delete(ctx, &k8sSecret); err != nil {
				logger.Error(err, "Failed to delete associated Secret")
				return err
			}
			logger.Info("Deleted associated Secret", "secret", secretKey)
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(hsmSecret, HSMSecretFinalizer)
		if err := r.Update(ctx, hsmSecret); err != nil {
			logger.Error(err, "Failed to remove finalizer")
			return err
		}
	}

	return nil
}

// buildSecret creates a new Kubernetes Secret from HSM data and metadata
func (r *HSMSecretReconciler) buildSecret(hsmSecret *hsmv1alpha1.HSMSecret, secretName string, hsmData hsm.SecretData, hsmMetadata *hsm.SecretMetadata) corev1.Secret {
	secretType := hsmSecret.Spec.SecretType
	if secretType == "" {
		secretType = corev1.SecretTypeOpaque
	}

	// Build labels starting with default operator labels
	labels := map[string]string{
		"managed-by": "hsm-secrets-operator",
		"hsm-path":   strings.ReplaceAll(hsmSecret.Name, "/", "_"),
	}

	// Build annotations starting with empty map
	annotations := make(map[string]string)

	// Add metadata labels and annotations if metadata exists
	r.applyMetadataToLabelsAndAnnotations(labels, annotations, hsmMetadata)

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        secretName,
			Namespace:   hsmSecret.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Type: secretType,
		Data: r.convertHSMDataToSecretData(hsmData),
	}

	// Set owner reference
	if err := ctrl.SetControllerReference(hsmSecret, &secret, r.Scheme); err != nil {
		ctrl.Log.Error(err, "Failed to set owner reference")
	}

	return secret
}

// updateSecretWithMetadata updates an existing Kubernetes Secret with HSM data and metadata
func (r *HSMSecretReconciler) updateSecretWithMetadata(secret *corev1.Secret, hsmSecret *hsmv1alpha1.HSMSecret, hsmData hsm.SecretData, hsmMetadata *hsm.SecretMetadata) {
	// Update data
	secret.Data = r.convertHSMDataToSecretData(hsmData)

	// Initialize labels if nil
	if secret.Labels == nil {
		secret.Labels = make(map[string]string)
	}

	// Initialize annotations if nil
	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}

	// Ensure essential operator labels are present
	secret.Labels["managed-by"] = "hsm-secrets-operator"
	secret.Labels["hsm-path"] = strings.ReplaceAll(hsmSecret.Name, "/", "_")

	// Apply metadata to labels and annotations
	r.applyMetadataToLabelsAndAnnotations(secret.Labels, secret.Annotations, hsmMetadata)
}

// applyMetadataToLabelsAndAnnotations applies HSM metadata to Kubernetes labels and annotations
func (r *HSMSecretReconciler) applyMetadataToLabelsAndAnnotations(labels map[string]string, annotations map[string]string, hsmMetadata *hsm.SecretMetadata) {
	if hsmMetadata == nil {
		return
	}

	// Apply metadata labels directly to Kubernetes labels
	if hsmMetadata.Labels != nil {
		for key, value := range hsmMetadata.Labels {
			// Validate Kubernetes label format
			if r.isValidKubernetesLabelKey(key) && r.isValidKubernetesLabelValue(value) {
				labels[key] = value
			}
		}
	}

	// Apply other metadata fields as annotations with hsm.j5t.io prefix
	if hsmMetadata.Description != "" {
		annotations["hsm.j5t.io/description"] = hsmMetadata.Description
	}
	if hsmMetadata.Format != "" {
		annotations["hsm.j5t.io/format"] = hsmMetadata.Format
	}
	if hsmMetadata.DataType != "" {
		annotations["hsm.j5t.io/data-type"] = hsmMetadata.DataType
	}
	if hsmMetadata.Source != "" {
		annotations["hsm.j5t.io/source"] = hsmMetadata.Source
	}
	if hsmMetadata.CreatedAt != "" {
		annotations["hsm.j5t.io/created-at"] = hsmMetadata.CreatedAt
	}
}

// isValidKubernetesLabelKey validates a Kubernetes label key
func (r *HSMSecretReconciler) isValidKubernetesLabelKey(key string) bool {
	// Basic validation - more comprehensive validation could be added
	if len(key) == 0 || len(key) > 63 {
		return false
	}
	// Should start and end with alphanumeric, can contain alphanumeric, dash, underscore, and dot
	for i, char := range key {
		isAlphaNumeric := (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
		isAllowedSymbol := char == '-' || char == '_' || char == '.'

		if !isAlphaNumeric && !isAllowedSymbol {
			return false
		}
		if (i == 0 || i == len(key)-1) && !isAlphaNumeric {
			return false
		}
	}
	return true
}

// isValidKubernetesLabelValue validates a Kubernetes label value
func (r *HSMSecretReconciler) isValidKubernetesLabelValue(value string) bool {
	// Basic validation
	if len(value) > 63 {
		return false
	}
	if len(value) == 0 {
		return true // Empty values are allowed
	}
	// Should start and end with alphanumeric, can contain alphanumeric, dash, underscore, and dot
	for i, char := range value {
		isAlphaNumeric := (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
		isAllowedSymbol := char == '-' || char == '_' || char == '.'

		if !isAlphaNumeric && !isAllowedSymbol {
			return false
		}
		if (i == 0 || i == len(value)-1) && !isAlphaNumeric {
			return false
		}
	}
	return true
}

// convertHSMDataToSecretData converts HSM data format to Kubernetes Secret data format
func (r *HSMSecretReconciler) convertHSMDataToSecretData(hsmData hsm.SecretData) map[string][]byte {
	result := make(map[string][]byte)
	maps.Copy(result, hsmData)
	return result
}

// convertSecretDataToHSMData converts Kubernetes Secret data format to HSM data format
func (r *HSMSecretReconciler) convertSecretDataToHSMData(secretData map[string][]byte) hsm.SecretData {
	result := make(hsm.SecretData)
	maps.Copy(result, secretData)
	return result
}

// updateStatus updates the HSMSecret status
func (r *HSMSecretReconciler) updateStatus(_ context.Context, hsmSecret *hsmv1alpha1.HSMSecret, status hsmv1alpha1.SyncStatus, errorMsg string) {
	now := metav1.Now()
	hsmSecret.Status.SyncStatus = status
	hsmSecret.Status.LastError = errorMsg

	if status == hsmv1alpha1.SyncStatusInSync {
		hsmSecret.Status.LastSyncTime = &now
	}

	// Update conditions
	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		LastTransitionTime: now,
		Reason:             string(status),
		Message:            errorMsg,
	}

	if status == hsmv1alpha1.SyncStatusError {
		condition.Status = metav1.ConditionFalse
	}

	// Update or add condition
	found := false
	for i, cond := range hsmSecret.Status.Conditions {
		if cond.Type == condition.Type {
			hsmSecret.Status.Conditions[i] = condition
			found = true
			break
		}
	}
	if !found {
		hsmSecret.Status.Conditions = append(hsmSecret.Status.Conditions, condition)
	}
}

// findAllHSMDevices finds all HSMDevices with ready HSMPools
// Note: HSMDevices are managed in the operator namespace, not the HSMSecret's namespace
func (r *HSMSecretReconciler) findAllHSMDevices(ctx context.Context) ([]*hsmv1alpha1.HSMDevice, error) {
	// List HSMDevices in this operator's namespace (where operator infrastructure is contained)
	var hsmDeviceList hsmv1alpha1.HSMDeviceList
	if err := r.List(ctx, &hsmDeviceList, client.InNamespace(r.OperatorNamespace)); err != nil {
		return nil, fmt.Errorf("failed to list HSM devices: %w", err)
	}

	var readyDevices []*hsmv1alpha1.HSMDevice

	// Look for devices with associated HSMPools that are ready with available devices
	for _, device := range hsmDeviceList.Items {
		// Check the HSMPool for this device
		poolName := device.Name + "-pool"
		pool := &hsmv1alpha1.HSMPool{}

		err := r.Get(ctx, client.ObjectKey{
			Name:      poolName,
			Namespace: device.Namespace,
		}, pool)

		if err == nil && pool.Status.Phase == hsmv1alpha1.HSMPoolPhaseReady &&
			len(pool.Status.AggregatedDevices) > 0 {

			// This is a suitable device for HSM operations
			deviceCopy := device // Create a copy to avoid issues with loop variable
			readyDevices = append(readyDevices, &deviceCopy)
		}
	}

	if len(readyDevices) == 0 {
		return nil, fmt.Errorf("no suitable HSM devices found in ready state")
	}

	return readyDevices, nil
}

// shouldHandleSecret determines if this operator instance should handle the given HSMSecret
func (r *HSMSecretReconciler) shouldHandleSecret(hsmSecret *hsmv1alpha1.HSMSecret) bool {
	// If no parentRef is present, ignore the secret (explicit association required)
	if hsmSecret.Spec.ParentRef == nil {
		return false
	}

	parentRef := hsmSecret.Spec.ParentRef

	// Check if the parent name matches this operator
	if parentRef.Name != r.OperatorName {
		return false
	}

	// Check namespace match - if parentRef.Namespace is nil, assume same namespace as HSMSecret
	expectedNamespace := r.OperatorNamespace
	if parentRef.Namespace != nil {
		expectedNamespace = *parentRef.Namespace
	}

	return expectedNamespace == r.OperatorNamespace
}

// SetupWithManager sets up the controller with the Manager.
func (r *HSMSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hsmv1alpha1.HSMSecret{}).
		Owns(&corev1.Secret{}).
		Named("hsmsecret").
		Complete(r)
}
