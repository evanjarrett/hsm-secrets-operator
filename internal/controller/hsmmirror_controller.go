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
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/mirror"
)

// HSMMirrorReconciler handles multi-device HSM mirroring and conflict resolution
type HSMMirrorReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	MirrorManager *mirror.MirrorManager

	// MirrorInterval controls how often to perform mirror checks (default: 30 seconds)
	MirrorInterval time.Duration
}

// NewHSMMirrorReconciler creates a new HSM mirror reconciler
func NewHSMMirrorReconciler(k8sClient client.Client, scheme *runtime.Scheme, agentManager *agent.Manager, operatorNamespace string) *HSMMirrorReconciler {
	logger := ctrl.Log.WithName("hsm-mirror-controller")
	mirrorManager := mirror.NewMirrorManager(k8sClient, agentManager, logger, operatorNamespace)

	return &HSMMirrorReconciler{
		Client:         k8sClient,
		Scheme:         scheme,
		MirrorManager:  mirrorManager,
		MirrorInterval: 30 * time.Second, // Default mirror interval
	}
}

// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmsecrets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmsecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmpools,verbs=get;list;watch
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmdevices,verbs=get;list;watch

// Reconcile performs HSM device mirroring and conflict resolution
func (r *HSMMirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the HSMSecret instance
	var hsmSecret hsmv1alpha1.HSMSecret
	if err := r.Get(ctx, req.NamespacedName, &hsmSecret); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Skip sync if AutoSync is disabled
	if !hsmSecret.Spec.AutoSync {
		logger.V(1).Info("AutoSync disabled, skipping sync", "secret", hsmSecret.Name)
		return ctrl.Result{}, nil
	}

	logger.Info("Starting multi-device HSM mirror", "secret", hsmSecret.Name)

	// Perform the mirror operation
	result, err := r.MirrorManager.MirrorSecret(ctx, &hsmSecret)
	if err != nil {
		logger.Error(err, "Failed to perform HSM mirror", "secret", hsmSecret.Name)

		// Update status with error
		hsmSecret.Status.SyncStatus = hsmv1alpha1.SyncStatusError
		hsmSecret.Status.LastError = err.Error()
		if updateErr := r.Status().Update(ctx, &hsmSecret); updateErr != nil {
			logger.Error(updateErr, "Failed to update HSMSecret status")
		}

		// Retry sooner on error
		return ctrl.Result{RequeueAfter: r.MirrorInterval / 2}, nil
	}

	// Update HSMSecret status with mirror results
	if err := r.MirrorManager.UpdateHSMSecretStatus(ctx, &hsmSecret, result); err != nil {
		logger.Error(err, "Failed to update HSMSecret status", "secret", hsmSecret.Name)
		return ctrl.Result{RequeueAfter: r.MirrorInterval / 2}, err
	}

	// Log mirror results
	logger.Info("Per-secret HSM mirror completed",
		"secret", hsmSecret.Name,
		"success", result.Success,
		"secretsProcessed", result.SecretsProcessed,
		"secretsUpdated", result.SecretsUpdated,
		"secretsCreated", result.SecretsCreated,
		"metadataRestored", result.MetadataRestored,
		"errors", len(result.Errors))

	// Calculate next mirror interval based on HSMSecret spec
	mirrorInterval := r.MirrorInterval
	if hsmSecret.Spec.SyncInterval > 0 {
		mirrorInterval = time.Duration(hsmSecret.Spec.SyncInterval) * time.Second
	}

	return ctrl.Result{RequeueAfter: mirrorInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HSMMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hsmv1alpha1.HSMSecret{}).
		Named("hsmmirror").
		Complete(r)
}
