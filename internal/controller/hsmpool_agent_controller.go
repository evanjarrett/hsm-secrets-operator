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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
)

// HSMPoolAgentReconciler watches HSMPools and ensures agents are deployed when pools become ready
type HSMPoolAgentReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	AgentManager *agent.Manager
}

// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmpools,verbs=get;list;watch
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmpools/status,verbs=get
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmdevices,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

// Reconcile ensures HSM agents are deployed for ready pools
func (r *HSMPoolAgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the HSMPool instance
	var hsmPool hsmv1alpha1.HSMPool
	if err := r.Get(ctx, req.NamespacedName, &hsmPool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("Reconciling HSM agent deployment for pool", "pool", hsmPool.Name, "phase", hsmPool.Status.Phase)

	// Only deploy agents for ready pools with discovered hardware
	if hsmPool.Status.Phase == hsmv1alpha1.HSMPoolPhaseReady && len(hsmPool.Status.AggregatedDevices) > 0 {
		// For each HSMDevice referenced by this pool, ensure an agent exists
		for _, deviceRef := range hsmPool.Spec.HSMDeviceRefs {
			// Get the HSMDevice to pass to agent manager
			var hsmDevice hsmv1alpha1.HSMDevice
			if err := r.Get(ctx, client.ObjectKey{
				Name:      deviceRef,
				Namespace: hsmPool.Namespace,
			}, &hsmDevice); err != nil {
				logger.Error(err, "Failed to get referenced HSMDevice", "device", deviceRef)
				continue
			}

			if err := r.ensureHSMAgent(ctx, &hsmDevice); err != nil {
				logger.Error(err, "Failed to ensure HSM agent", "device", deviceRef)
			}
		}
	} else {
		logger.V(1).Info("HSMPool not ready for agent deployment",
			"phase", hsmPool.Status.Phase,
			"devices", len(hsmPool.Status.AggregatedDevices))
	}

	return ctrl.Result{}, nil
}

// ensureHSMAgent ensures an HSM agent pod is running for the given device
func (r *HSMPoolAgentReconciler) ensureHSMAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
	logger := log.FromContext(ctx)

	if r.AgentManager == nil {
		return fmt.Errorf("agent manager not configured")
	}

	// Ensure agent pod is running for this device
	agentEndpoint, err := r.AgentManager.EnsureAgent(ctx, hsmDevice, nil)
	if err != nil {
		return fmt.Errorf("failed to ensure HSM agent: %w", err)
	}

	logger.Info("HSM agent ensured", "device", hsmDevice.Name, "endpoint", agentEndpoint)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HSMPoolAgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hsmv1alpha1.HSMPool{}).
		Named("hsmpool-agent").
		Complete(r)
}
