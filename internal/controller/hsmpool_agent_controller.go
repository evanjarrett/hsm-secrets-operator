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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
)

// HSMPoolAgentReconciler watches HSMPools and ensures agents are deployed when pools become ready
type HSMPoolAgentReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	AgentManager agent.ManagerInterface

	// DeviceAbsenceTimeout is the duration after which agents are cleaned up when devices are unavailable
	// Defaults to 2x grace period (10 minutes) if not set
	DeviceAbsenceTimeout time.Duration
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

	// Check for agents that need cleanup due to prolonged device absence
	if err := r.cleanupStaleAgents(ctx, &hsmPool); err != nil {
		logger.Error(err, "Failed to cleanup stale agents")
		// Don't return error - continue with normal reconciliation
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

// cleanupStaleAgents removes agent deployments for devices that have been unavailable for too long
// Returns nil to ensure reconciliation continues even if cleanup fails for individual devices
func (r *HSMPoolAgentReconciler) cleanupStaleAgents(ctx context.Context, hsmPool *hsmv1alpha1.HSMPool) error { //nolint:unparam
	logger := log.FromContext(ctx)

	// Get the device absence timeout (default to 2x grace period)
	absenceTimeout := r.DeviceAbsenceTimeout
	if absenceTimeout == 0 {
		gracePeriod := 5 * time.Minute // Default grace period
		if hsmPool.Spec.GracePeriod != nil {
			gracePeriod = hsmPool.Spec.GracePeriod.Duration
		}
		absenceTimeout = 2 * gracePeriod // Default to 2x grace period
	}

	// For each HSMDevice referenced by this pool, check if it should be cleaned up
	for _, deviceRef := range hsmPool.Spec.HSMDeviceRefs {
		// Get the HSMDevice
		var hsmDevice hsmv1alpha1.HSMDevice
		if err := r.Get(ctx, client.ObjectKey{
			Name:      deviceRef,
			Namespace: hsmPool.Namespace,
		}, &hsmDevice); err != nil {
			logger.V(1).Info("HSMDevice not found, skipping cleanup check", "device", deviceRef)
			continue
		}

		// Check if this device has available aggregated devices in the pool
		deviceAvailable := false
		var lastSeenTime time.Time

		for _, aggregatedDevice := range hsmPool.Status.AggregatedDevices {
			if aggregatedDevice.Available {
				deviceAvailable = true
				break
			}
			// Track the most recent LastSeen time for unavailable devices
			if aggregatedDevice.LastSeen.After(lastSeenTime) {
				lastSeenTime = aggregatedDevice.LastSeen.Time
			}
		}

		// If device is not available and hasn't been seen for longer than absence timeout
		if !deviceAvailable {
			timeSinceLastSeen := time.Since(lastSeenTime)

			if lastSeenTime.IsZero() {
				// No devices have ever been seen - check if pool has been around long enough
				poolAge := time.Since(hsmPool.CreationTimestamp.Time)
				if poolAge > absenceTimeout {
					logger.Info("Cleaning up agent for device with no discovered instances",
						"device", deviceRef,
						"poolAge", poolAge,
						"absenceTimeout", absenceTimeout)

					if err := r.cleanupAgentForDevice(ctx, &hsmDevice); err != nil {
						logger.Error(err, "Failed to cleanup agent for device with no instances", "device", deviceRef)
					}
				}
			} else if timeSinceLastSeen > absenceTimeout {
				logger.Info("Cleaning up agent for device absent too long",
					"device", deviceRef,
					"timeSinceLastSeen", timeSinceLastSeen,
					"absenceTimeout", absenceTimeout,
					"lastSeen", lastSeenTime)

				if err := r.cleanupAgentForDevice(ctx, &hsmDevice); err != nil {
					logger.Error(err, "Failed to cleanup agent for absent device", "device", deviceRef)
				}
			} else {
				logger.V(1).Info("Device unavailable but within tolerance",
					"device", deviceRef,
					"timeSinceLastSeen", timeSinceLastSeen,
					"absenceTimeout", absenceTimeout)
			}
		}
	}

	return nil
}

// cleanupAgentForDevice removes the agent deployment for a specific device
func (r *HSMPoolAgentReconciler) cleanupAgentForDevice(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
	if r.AgentManager == nil {
		return fmt.Errorf("agent manager not configured")
	}

	return r.AgentManager.CleanupAgent(ctx, hsmDevice)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HSMPoolAgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hsmv1alpha1.HSMPool{}).
		Watches(
			&appsv1.Deployment{},
			handler.EnqueueRequestsFromMapFunc(r.findPoolsForDeployment),
		).
		Named("hsmpool-agent").
		Complete(r)
}

// findPoolsForDeployment maps agent deployments back to HSMPools for reconciliation
func (r *HSMPoolAgentReconciler) findPoolsForDeployment(ctx context.Context, obj client.Object) []reconcile.Request {
	deployment, ok := obj.(*appsv1.Deployment)
	if !ok {
		return nil
	}

	// Check if this is an HSM agent deployment
	deviceName, exists := deployment.Labels["hsm.j5t.io/device"]
	if !exists {
		return nil
	}

	// Find the corresponding HSMPool (agent deployments are created for devices referenced in pools)
	poolName := deviceName + "-pool"

	return []reconcile.Request{
		{
			NamespacedName: client.ObjectKey{
				Name:      poolName,
				Namespace: deployment.Namespace,
			},
		},
	}
}
