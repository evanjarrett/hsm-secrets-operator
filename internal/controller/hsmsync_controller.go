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
	"github.com/evanjarrett/hsm-secrets-operator/internal/sync"
)

// HSMSyncReconciler handles multi-device HSM synchronization and conflict resolution
type HSMSyncReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	SyncManager *sync.SyncManager

	// SyncInterval controls how often to perform sync checks (default: 30 seconds)
	SyncInterval time.Duration
}

// NewHSMSyncReconciler creates a new HSM sync reconciler
func NewHSMSyncReconciler(k8sClient client.Client, scheme *runtime.Scheme, agentManager *agent.Manager) *HSMSyncReconciler {
	logger := ctrl.Log.WithName("hsm-sync-controller")
	syncManager := sync.NewSyncManager(k8sClient, agentManager, logger)

	return &HSMSyncReconciler{
		Client:       k8sClient,
		Scheme:       scheme,
		SyncManager:  syncManager,
		SyncInterval: 30 * time.Second, // Default sync interval
	}
}

// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmsecrets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmsecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmpools,verbs=get;list;watch
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmdevices,verbs=get;list;watch

// Reconcile performs HSM device synchronization and conflict resolution
func (r *HSMSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	logger.Info("Starting multi-device HSM sync", "secret", hsmSecret.Name)

	// Perform the sync operation
	result, err := r.SyncManager.SyncSecret(ctx, &hsmSecret)
	if err != nil {
		logger.Error(err, "Failed to perform HSM sync", "secret", hsmSecret.Name)

		// Update status with error
		hsmSecret.Status.SyncStatus = hsmv1alpha1.SyncStatusError
		hsmSecret.Status.LastError = err.Error()
		if updateErr := r.Status().Update(ctx, &hsmSecret); updateErr != nil {
			logger.Error(updateErr, "Failed to update HSMSecret status")
		}

		// Retry sooner on error
		return ctrl.Result{RequeueAfter: r.SyncInterval / 2}, nil
	}

	// Update HSMSecret status with sync results
	if err := r.SyncManager.UpdateHSMSecretStatus(ctx, &hsmSecret, result); err != nil {
		logger.Error(err, "Failed to update HSMSecret status", "secret", hsmSecret.Name)
		return ctrl.Result{RequeueAfter: r.SyncInterval / 2}, err
	}

	// Log sync results
	if result.ConflictDetected {
		logger.Info("Conflict detected and resolved",
			"secret", hsmSecret.Name,
			"primaryDevice", result.PrimaryDevice,
			"devices", len(result.DeviceResults))
	} else {
		logger.V(1).Info("HSM sync completed successfully",
			"secret", hsmSecret.Name,
			"devices", len(result.DeviceResults))
	}

	// Calculate next sync interval based on HSMSecret spec
	syncInterval := r.SyncInterval
	if hsmSecret.Spec.SyncInterval > 0 {
		syncInterval = time.Duration(hsmSecret.Spec.SyncInterval) * time.Second
	}

	return ctrl.Result{RequeueAfter: syncInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HSMSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hsmv1alpha1.HSMSecret{}).
		Named("hsmsync").
		Complete(r)
}
