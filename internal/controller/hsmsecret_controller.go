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
	"slices"
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

	// StartupGracePeriod is the duration during which "no agents available" is logged at Info level
	StartupGracePeriod = 2 * time.Minute
)

// HSMSecretReconciler reconciles a HSMSecret object
type HSMSecretReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	AgentManager      *agent.Manager
	OperatorNamespace string
	OperatorName      string
	StartupTime       time.Time
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

	// Find available devices via AgentManager
	devices, err := r.AgentManager.GetAvailableDevices(ctx, r.OperatorNamespace)
	if err != nil {
		// During startup grace period, log at Info level to reduce noise
		if time.Since(r.StartupTime) < StartupGracePeriod {
			logger.Info("No HSM agents available yet (startup grace period)", "error", err.Error(), "elapsed", time.Since(r.StartupTime).Round(time.Second))
		} else {
			logger.Error(err, "Failed to get available devices")
		}
		return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
	}
	if len(devices) == 0 {
		// During startup grace period, log at Info level to reduce noise
		if time.Since(r.StartupTime) < StartupGracePeriod {
			logger.Info("No HSM devices available yet (startup grace period)", "elapsed", time.Since(r.StartupTime).Round(time.Second))
		} else {
			logger.Info("No HSM devices available")
		}
		return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
	}

	// Connect to first available device
	deviceName := devices[0]
	grpcClient, err := r.AgentManager.CreateGRPCClient(ctx, deviceName, hsmSecret.Namespace, logger)
	if err != nil {
		logger.Error(err, "Failed to create gRPC client", "device", deviceName)
		return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
	}
	defer func() {
		if err := grpcClient.Close(); err != nil {
			logger.Error(err, "Failed to close gRPC client")
		}
	}()

	// Check if secret exists
	secrets, err := grpcClient.ListSecrets(ctx, "")
	if err != nil {
		logger.Error(err, "Failed to list secrets")
		return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
	}

	if !slices.Contains(secrets, hsmSecret.Name) {
		logger.Info("Secret not found in HSM", "secret", hsmSecret.Name)
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	// Read secret data and metadata
	hsmData, err := grpcClient.ReadSecret(ctx, hsmSecret.Name)
	if err != nil {
		logger.Error(err, "Failed to read secret from HSM")
		return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
	}

	hsmMetadata, err := grpcClient.ReadMetadata(ctx, hsmSecret.Name)
	if err != nil {
		logger.V(1).Info("Failed to read metadata", "error", err)
		hsmMetadata = nil // Continue without metadata
	}

	// Reconcile the HSMSecret with the data from HSM
	result, err := r.reconcileSecret(ctx, &hsmSecret, hsmData, hsmMetadata)
	if err != nil {
		logger.Error(err, "Failed to reconcile HSMSecret")
		r.updateStatus(ctx, &hsmSecret, hsmv1alpha1.SyncStatusError, err.Error())
	}

	return result, err
}

// reconcileSecret handles HSM secret reconciliation with data from HSM
func (r *HSMSecretReconciler) reconcileSecret(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret, hsmData hsm.SecretData, hsmMetadata *hsm.SecretMetadata) (ctrl.Result, error) {
	// Set default values
	secretName := hsmSecret.Spec.SecretName
	if secretName == "" {
		secretName = hsmSecret.Name
	}

	syncInterval := hsmSecret.Spec.SyncInterval
	if syncInterval == 0 {
		syncInterval = DefaultSyncInterval
	}

	return r.updateKubernetesSecret(ctx, hsmSecret, secretName, hsmData, hsmMetadata, syncInterval)
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
