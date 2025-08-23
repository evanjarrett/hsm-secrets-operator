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
	"github.com/evanjarrett/hsm-secrets-operator/internal/discovery"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

const (
	// HSMSecretFinalizer is the finalizer used by the HSMSecret controller
	HSMSecretFinalizer = "hsmsecret.hsm.j5t.io/finalizer"

	// DefaultSyncInterval is the default sync interval in seconds
	DefaultSyncInterval = 300
)

// HSMSecretReconciler reconciles a HSMSecret object
type HSMSecretReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	MirroringManager *discovery.MirroringManager
	AgentManager     *agent.Manager
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

	// Find target HSM device and ensure agent is running
	hsmDevice, agentClient, err := r.ensureHSMAgent(ctx, &hsmSecret)
	if err != nil {
		logger.Error(err, "Failed to ensure HSM agent")
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	// Use agent client instead of direct HSM client
	if agentClient == nil || !agentClient.IsConnected() {
		logger.Error(fmt.Errorf("HSM agent not available"), "HSM agent not connected", "device", hsmDevice.Name)
		return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
	}

	// Handle deletion
	if hsmSecret.DeletionTimestamp != nil {
		return r.reconcileDelete(ctx, &hsmSecret)
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

	// Reconcile the HSMSecret using the agent client
	result, err := r.reconcileNormal(ctx, &hsmSecret, agentClient)
	if err != nil {
		logger.Error(err, "Failed to reconcile HSMSecret")
		r.updateStatus(ctx, &hsmSecret, hsmv1alpha1.SyncStatusError, err.Error())
	}

	return result, err
}

// ensureHSMAgent finds an HSM device for the secret and ensures an agent is running
func (r *HSMSecretReconciler) ensureHSMAgent(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret) (*hsmv1alpha1.HSMDevice, hsm.Client, error) {
	logger := log.FromContext(ctx)

	// Find the appropriate HSM device
	hsmDevice, err := r.findHSMDeviceForSecret(ctx, hsmSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find HSM device for secret: %w", err)
	}

	// Ensure agent pod is running for this device
	if r.AgentManager == nil {
		return nil, nil, fmt.Errorf("agent manager not configured")
	}

	// EnsureAgent now returns HTTP endpoint for backward compatibility, but we'll use gRPC
	_, err = r.AgentManager.EnsureAgent(ctx, hsmDevice, hsmSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to ensure HSM agent: %w", err)
	}

	// Create gRPC client using agent manager's direct pod connections
	agentClient, err := r.AgentManager.CreateSingleGRPCClient(ctx, hsmDevice.Name, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create gRPC client: %w", err)
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
			return nil, nil, fmt.Errorf("HSM agent not ready after waiting")
		}
	}

	return hsmDevice, agentClient, nil
}

// reconcileNormal handles normal reconciliation logic
func (r *HSMSecretReconciler) reconcileNormal(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret, hsmClient hsm.Client) (ctrl.Result, error) {
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

	// Read secret from HSM with readonly fallback support
	hsmData, err := r.readSecretWithFallback(ctx, hsmSecret, hsmClient)
	if err != nil {
		logger.Error(err, "Failed to read secret from HSM and mirrors", "path", hsmSecret.Name)
		return ctrl.Result{RequeueAfter: time.Minute * 2}, err
	}

	// Calculate HSM checksum
	hsmChecksum := hsm.CalculateChecksum(hsmData)

	// Get or create Kubernetes Secret
	var k8sSecret corev1.Secret
	secretKey := types.NamespacedName{
		Namespace: hsmSecret.Namespace,
		Name:      secretName,
	}

	err = r.Get(ctx, secretKey, &k8sSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new secret
			k8sSecret = r.buildSecret(hsmSecret, secretName, hsmData)
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
		k8sSecret.Data = r.convertHSMDataToSecretData(hsmData)
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
func (r *HSMSecretReconciler) reconcileDelete(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret) (ctrl.Result, error) {
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
				return ctrl.Result{}, err
			}
			logger.Info("Deleted associated Secret", "secret", secretKey)
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(hsmSecret, HSMSecretFinalizer)
		if err := r.Update(ctx, hsmSecret); err != nil {
			logger.Error(err, "Failed to remove finalizer")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// buildSecret creates a new Kubernetes Secret from HSM data
func (r *HSMSecretReconciler) buildSecret(hsmSecret *hsmv1alpha1.HSMSecret, secretName string, hsmData hsm.SecretData) corev1.Secret {
	secretType := hsmSecret.Spec.SecretType
	if secretType == "" {
		secretType = corev1.SecretTypeOpaque
	}

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: hsmSecret.Namespace,
			Labels: map[string]string{
				"managed-by": "hsm-secrets-operator",
				"hsm-path":   strings.ReplaceAll(hsmSecret.Name, "/", "_"),
			},
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

// readSecretWithFallback attempts to read a secret from primary HSM, falling back to mirrors if needed
func (r *HSMSecretReconciler) readSecretWithFallback(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret, hsmClient hsm.Client) (hsm.SecretData, error) {
	logger := log.FromContext(ctx)

	// Try to read from primary HSM first (via agent)
	if hsmClient != nil && hsmClient.IsConnected() {
		data, err := hsmClient.ReadSecret(ctx, hsmSecret.Name)
		if err == nil {
			logger.V(1).Info("Successfully read secret from primary HSM", "path", hsmSecret.Name)
			return data, nil
		}
		logger.V(1).Info("Failed to read from primary HSM, attempting fallback", "error", err)
	}

	// If primary failed and we have a mirroring manager, try readonly access from mirrors
	if r.MirroringManager != nil {
		// Find relevant HSMDevice for this secret path
		hsmDevice, err := r.findHSMDeviceForSecret(ctx, hsmSecret)
		if err != nil {
			logger.Error(err, "Failed to find HSM device for readonly fallback")
		} else if hsmDevice != nil {
			data, err := r.MirroringManager.GetReadOnlyAccess(ctx, hsmSecret.Name, hsmDevice)
			if err == nil {
				logger.Info("Successfully read secret from readonly mirror", "path", hsmSecret.Name)
				return data, nil
			}
			logger.V(1).Info("Failed to read from mirrors", "error", err)
		}
	}

	return nil, fmt.Errorf("secret not accessible from primary HSM or mirrors")
}

// findHSMDeviceForSecret finds the HSMDevice that should contain the secret
func (r *HSMSecretReconciler) findHSMDeviceForSecret(ctx context.Context, hsmSecret *hsmv1alpha1.HSMSecret) (*hsmv1alpha1.HSMDevice, error) {
	// List all HSMDevices in the same namespace
	var hsmDeviceList hsmv1alpha1.HSMDeviceList
	if err := r.List(ctx, &hsmDeviceList, client.InNamespace(hsmSecret.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list HSM devices: %w", err)
	}

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
			return &device, nil
		}
	}

	return nil, fmt.Errorf("no suitable HSM device found in ready state")
}

// SetupWithManager sets up the controller with the Manager.
func (r *HSMSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hsmv1alpha1.HSMSecret{}).
		Owns(&corev1.Secret{}).
		Named("hsmsecret").
		Complete(r)
}
