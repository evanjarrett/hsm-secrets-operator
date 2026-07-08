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
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hsmv1alpha1 "tangled.org/evan.jarrett.net/hsm-secrets-operator/api/v1alpha1"
	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/agent"
	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/config"
)

const (
	// AgentNamePrefix is the prefix for HSM agent deployment names
	AgentNamePrefix = "hsm-agent"

	// AgentPort is the port the HSM agent serves on (now gRPC)
	AgentPort = 9090

	// AgentHealthPort is the port for health checks (HTTP for simplicity)
	AgentHealthPort = 8093

	// agentTLSVolumeName is the volume name for the mounted mTLS server cert.
	agentTLSVolumeName = "agent-mtls"
	// agentTLSMountPath is where the mTLS server cert Secret is mounted in agents.
	agentTLSMountPath = "/etc/hsm/tls"
)

// HSMPoolAgentReconciler watches HSMPools and ensures agents are deployed when pools become ready
type HSMPoolAgentReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	AgentManager       agent.ManagerInterface
	ImageResolver      *config.ImageResolver
	AgentImage         string
	ServiceAccountName string

	// AgentTLSEnabled mounts the shared server certificate into agent pods and
	// passes the --tls-* flags so the agent gRPC server requires mutual TLS.
	AgentTLSEnabled bool
	// AgentTLSSecretName is the Secret (ca.crt/tls.crt/tls.key) mounted into agents.
	AgentTLSSecretName string

	// DeviceAbsenceTimeout is the duration after which agents are cleaned up when devices are unavailable
	// Defaults to 2x grace period (10 minutes) if not set
	DeviceAbsenceTimeout time.Duration
}

// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmpools,verbs=get;list;watch
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmpools/status,verbs=get
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmdevices,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile ensures HSM agents are deployed for ready pools
func (r *HSMPoolAgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the HSMPool instance
	var hsmPool hsmv1alpha1.HSMPool
	if err := r.Get(ctx, req.NamespacedName, &hsmPool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("Reconciling HSM agent deployment", "phase", hsmPool.Status.Phase)

	// Only deploy agents for ready pools with discovered hardware
	if hsmPool.Status.Phase == hsmv1alpha1.HSMPoolPhaseReady && len(hsmPool.Status.AggregatedDevices) > 0 {
		// Ensure owner reference exists and get the HSMDevice
		if len(hsmPool.OwnerReferences) == 0 {
			logger.Error(fmt.Errorf("no owner references"), "HSMPool has no owner references", componentPool, hsmPool.Name)
			return ctrl.Result{}, nil
		}

		deviceRef := hsmPool.OwnerReferences[0].Name
		// Get the HSMDevice to pass to agent manager
		var hsmDevice hsmv1alpha1.HSMDevice
		if err := r.Get(ctx, client.ObjectKey{
			Name:      deviceRef,
			Namespace: hsmPool.Namespace,
		}, &hsmDevice); err != nil {
			logger.Error(err, "Failed to get referenced HSMDevice", "device", deviceRef)
			// Don't return error - this allows graceful handling of missing devices
			return ctrl.Result{}, nil
		}

		// Ensure agent deployments for all available devices in the pool
		if err := r.ensureAgentDeployments(ctx, &hsmPool); err != nil {
			logger.Error(err, "Failed to ensure HSM agent deployments for pool", "device", deviceRef)
			return ctrl.Result{}, err
		}

		// Notify agent manager to track the agents
		if r.AgentManager != nil {
			if err := r.AgentManager.EnsureAgent(ctx, &hsmPool); err != nil {
				logger.Error(err, "Failed to track HSM agents for pool", "device", deviceRef)
				// Don't return error - deployment succeeded, tracking is secondary
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

	// Check if the HSMDevice referenced by this pool should be cleaned up (from ownerReferences)
	if len(hsmPool.OwnerReferences) == 0 {
		logger.V(1).Info("HSMPool has no owner references, skipping cleanup")
		return nil
	}

	deviceRef := hsmPool.OwnerReferences[0].Name
	// Get the HSMDevice
	var hsmDevice hsmv1alpha1.HSMDevice
	if err := r.Get(ctx, client.ObjectKey{
		Name:      deviceRef,
		Namespace: hsmPool.Namespace,
	}, &hsmDevice); err != nil {
		logger.V(1).Info("HSMDevice not found, skipping cleanup check", "device", deviceRef)
		return nil
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

				if err := r.cleanupAgentDeployments(ctx, &hsmDevice); err != nil {
					logger.Error(err, "Failed to cleanup agent for device with no instances", "device", deviceRef)
				}
			}
		} else if timeSinceLastSeen > absenceTimeout {
			logger.Info("Cleaning up agent for device absent too long",
				"device", deviceRef,
				"timeSinceLastSeen", timeSinceLastSeen,
				"absenceTimeout", absenceTimeout,
				"lastSeen", lastSeenTime)

			if err := r.cleanupAgentDeployments(ctx, &hsmDevice); err != nil {
				logger.Error(err, "Failed to cleanup agent for absent device", "device", deviceRef)
			}
		} else {
			logger.V(1).Info("Device unavailable but within tolerance",
				"device", deviceRef,
				"timeSinceLastSeen", timeSinceLastSeen,
				"absenceTimeout", absenceTimeout)
		}
	}

	return nil
}

// cleanupAgentDeployments removes all agent deployments for a device, matching by the
// labelHSMDevice label so cleanup is independent of how agents are named or ordered.
func (r *HSMPoolAgentReconciler) cleanupAgentDeployments(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
	logger := log.FromContext(ctx)

	// List all deployments in the namespace and match by device label
	var deploymentList appsv1.DeploymentList
	if err := r.List(ctx, &deploymentList, client.InNamespace(hsmDevice.Namespace)); err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	// Find and delete deployments that match this device
	for _, deployment := range deploymentList.Items {
		// Check if this is an agent deployment for this device
		if deviceName, exists := deployment.Labels[labelHSMDevice]; exists && deviceName == hsmDevice.Name {
			if err := r.Delete(ctx, &deployment); err != nil && !errors.IsNotFound(err) {
				logger.Error(err, "Failed to delete agent deployment", "deployment", deployment.Name)
			} else {
				logger.Info("Deleted agent deployment", "deployment", deployment.Name)
			}
		}
	}

	// Also clean up tracking in agent manager
	if r.AgentManager != nil {
		if err := r.AgentManager.CleanupAgent(ctx, hsmDevice); err != nil {
			logger.Error(err, "Failed to cleanup agent tracking", "device", hsmDevice.Name)
		}
	}

	return nil
}

// Deployment creation and management functions

// ensureAgentDeployments ensures agent deployments exist for all available devices in the pool
// Handles device migrations by grouping devices by serial number and detecting state changes
func (r *HSMPoolAgentReconciler) ensureAgentDeployments(ctx context.Context, hsmPool *hsmv1alpha1.HSMPool) error {
	logger := log.FromContext(ctx)

	// Group devices by serial number to detect migrations
	devicesBySerial := make(map[string][]hsmv1alpha1.DiscoveredDevice)
	for _, device := range hsmPool.Status.AggregatedDevices {
		devicesBySerial[device.SerialNumber] = append(devicesBySerial[device.SerialNumber], device)
	}

	deviceName := hsmPool.OwnerReferences[0].Name
	var deploymentErrors []error

	for serial, devices := range devicesBySerial {
		agentName, ok := agent.AgentInstanceName(deviceName, serial)
		if !ok {
			// Device reported no serial; it has no stable identity to route to,
			// so skip deploying an agent for it.
			logger.Info("Skipping device with no serial number; cannot assign a stable agent name",
				componentPool, hsmPool.Name)
			continue
		}

		// Find the active device (if any) and lost device (if any)
		var activeDevice *hsmv1alpha1.DiscoveredDevice
		var lostDevice *hsmv1alpha1.DiscoveredDevice

		for j := range devices {
			dev := &devices[j]
			if dev.Available {
				activeDevice = dev
			} else {
				lostDevice = dev
			}
		}

		// Decision logic based on device state
		if activeDevice != nil && lostDevice != nil {
			// Migration scenario - device moved nodes
			timeSinceLost := time.Since(lostDevice.LastSeen.Time)
			gracePeriod := 5 * time.Minute
			if hsmPool.Spec.GracePeriod != nil {
				gracePeriod = hsmPool.Spec.GracePeriod.Duration
			}

			if timeSinceLost < gracePeriod && activeDevice.NodeName != lostDevice.NodeName {
				logger.Info("Device migration detected",
					"serial", serial,
					"from", lostDevice.NodeName,
					"to", activeDevice.NodeName,
					"timeSinceLost", timeSinceLost)

				// Ensure agent is on the new node (will handle deletion/creation)
				if err := r.ensureAgentOnNode(ctx, hsmPool, activeDevice, agentName); err != nil {
					logger.Error(err, "Failed to ensure agent on new node after migration", "serial", serial)
					deploymentErrors = append(deploymentErrors, fmt.Errorf("migration failed for %s: %w", serial, err))
					continue
				}
			} else if timeSinceLost < gracePeriod {
				// Device came back on same node within grace period
				logger.Info("Device reconnected on same node",
					"serial", serial,
					"node", activeDevice.NodeName,
					"timeSinceLost", timeSinceLost)

				if err := r.ensureAgentOnNode(ctx, hsmPool, activeDevice, agentName); err != nil {
					logger.Error(err, "Failed to ensure agent after reconnection", "serial", serial)
					deploymentErrors = append(deploymentErrors, fmt.Errorf("reconnection failed for %s: %w", serial, err))
					continue
				}
			}
		} else if activeDevice != nil {
			// Normal case - device is available
			if err := r.ensureAgentOnNode(ctx, hsmPool, activeDevice, agentName); err != nil {
				logger.Error(err, "Failed to ensure agent for available device", "serial", serial)
				deploymentErrors = append(deploymentErrors, fmt.Errorf("agent creation failed for %s: %w", serial, err))
				continue
			}
		} else if lostDevice != nil {
			// Device is lost - check if we should clean up
			timeSinceLost := time.Since(lostDevice.LastSeen.Time)
			gracePeriod := 5 * time.Minute
			if hsmPool.Spec.GracePeriod != nil {
				gracePeriod = hsmPool.Spec.GracePeriod.Duration
			}

			if timeSinceLost > gracePeriod {
				logger.Info("Cleaning up agent for lost device",
					"serial", serial,
					"lastNode", lostDevice.NodeName,
					"timeSinceLost", timeSinceLost)

				if err := r.deleteAgent(ctx, agentName, hsmPool.Namespace); err != nil {
					logger.Error(err, "Failed to delete agent for lost device", "serial", serial)
					deploymentErrors = append(deploymentErrors, fmt.Errorf("cleanup failed for %s: %w", serial, err))
				}
			} else {
				logger.V(1).Info("Device lost but within grace period",
					"serial", serial,
					"timeSinceLost", timeSinceLost,
					"gracePeriod", gracePeriod)
			}
		}
	}

	// Return aggregated errors if any occurred
	if len(deploymentErrors) > 0 {
		return fmt.Errorf("deployment errors occurred: %v", deploymentErrors)
	}

	return nil
}

// ensureAgentOnNode ensures an agent deployment exists on the correct node for the given device
func (r *HSMPoolAgentReconciler) ensureAgentOnNode(ctx context.Context, hsmPool *hsmv1alpha1.HSMPool, device *hsmv1alpha1.DiscoveredDevice, agentName string) error {
	logger := log.FromContext(ctx)

	// Check if deployment exists
	var deployment appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{
		Name:      agentName,
		Namespace: hsmPool.Namespace,
	}, &deployment)

	if err == nil {
		// Deployment exists - check if it's on the right node
		if !r.isDeploymentOnNode(&deployment, device.NodeName) {
			logger.Info("Agent on wrong node, recreating",
				componentAgent, agentName,
				"currentNode", r.getDeploymentNode(&deployment),
				"targetNode", device.NodeName,
				"serial", device.SerialNumber)

			// Delete and recreate
			if err := r.Delete(ctx, &deployment); err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("failed to delete outdated agent: %w", err)
			}
			// Fall through to create
		} else {
			// Agent is on correct node - check if other details need updating
			needsUpdate, err := r.agentNeedsUpdate(ctx, &deployment, hsmPool)
			if err != nil {
				return fmt.Errorf("failed to check if agent needs update: %w", err)
			}

			if !needsUpdate {
				needsUpdate = r.deploymentNeedsUpdateForDevice(&deployment, device)
			}

			if needsUpdate {
				logger.Info("Agent needs updating, recreating",
					componentAgent, agentName,
					"node", device.NodeName,
					"serial", device.SerialNumber)

				if err := r.Delete(ctx, &deployment); err != nil && !errors.IsNotFound(err) {
					return fmt.Errorf("failed to delete outdated agent: %w", err)
				}
				// Fall through to create
			} else {
				// Agent is up to date
				logger.V(1).Info("Agent deployment is up to date",
					componentAgent, agentName,
					"node", device.NodeName,
					"serial", device.SerialNumber)
				return nil
			}
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check agent deployment: %w", err)
	}

	// Create agent deployment
	logger.Info("Creating agent deployment",
		componentAgent, agentName,
		"node", device.NodeName,
		"serial", device.SerialNumber)

	return r.createAgentDeployment(ctx, hsmPool, device, agentName)
}

// isDeploymentOnNode checks if a deployment is pinned to the specified node
func (r *HSMPoolAgentReconciler) isDeploymentOnNode(deployment *appsv1.Deployment, nodeName string) bool {
	if deployment.Spec.Template.Spec.NodeSelector != nil {
		return deployment.Spec.Template.Spec.NodeSelector[labelHostname] == nodeName
	}
	return false
}

// getDeploymentNode returns the node name that a deployment is pinned to
func (r *HSMPoolAgentReconciler) getDeploymentNode(deployment *appsv1.Deployment) string {
	if deployment.Spec.Template.Spec.NodeSelector != nil {
		return deployment.Spec.Template.Spec.NodeSelector[labelHostname]
	}
	return ""
}

// deleteAgent deletes an agent deployment by name
func (r *HSMPoolAgentReconciler) deleteAgent(ctx context.Context, name, namespace string) error {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if err := r.Delete(ctx, deployment); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete agent deployment %s: %w", name, err)
	}
	return nil
}

// createAgentDeployment creates the HSM agent deployment for a specific device
func (r *HSMPoolAgentReconciler) createAgentDeployment(ctx context.Context, hsmPool *hsmv1alpha1.HSMPool, specificDevice *hsmv1alpha1.DiscoveredDevice, agentName string) error {
	logger := log.FromContext(ctx)

	if specificDevice == nil {
		return fmt.Errorf("specificDevice is required")
	}
	if agentName == "" {
		return fmt.Errorf("agentName is required")
	}

	targetNode := specificDevice.NodeName
	deviceName := hsmPool.OwnerReferences[0].Name

	// Fetch the HSMDevice to get the device type for extended resource requests
	var hsmDevice hsmv1alpha1.HSMDevice
	if err := r.Get(ctx, types.NamespacedName{Name: deviceName, Namespace: hsmPool.Namespace}, &hsmDevice); err != nil {
		logger.Error(err, "Failed to get HSMDevice for agent deployment", "device", deviceName)
		return fmt.Errorf("failed to get HSMDevice %s: %w", deviceName, err)
	}

	// Build extended resource name from device type (e.g., "hsm.j5t.io/picohsm")
	extendedResourceName := corev1.ResourceName(
		fmt.Sprintf("hsm.j5t.io/%s", strings.ToLower(string(hsmDevice.Spec.DeviceType))),
	)
	logger.V(1).Info("Using extended resource for agent",
		componentAgent, agentName,
		"resource", extendedResourceName)

	// Get agent image from config or fallback to auto-detection
	var agentImage string
	if r.AgentImage != "" {
		agentImage = r.AgentImage
	} else if r.ImageResolver != nil {
		// Fallback to ImageResolver for backward compatibility or auto-detection
		agentImage = r.ImageResolver.GetImage(ctx, "")
	}

	var replicas int32 = 1
	var rootUserId int64 = 0
	// Fallback to root for USB device access - compensated by distroless
	falsePtr := new(bool)
	*falsePtr = false
	truePtr := new(bool)
	*truePtr = true

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentName,
			Namespace: hsmPool.Namespace,
			Labels: map[string]string{
				labelApp:                     agentName,
				labelAppComponent:            AgentNamePrefix,
				"app.kubernetes.io/instance": agentName,
				labelAppName:                 AgentNamePrefix,
				"app.kubernetes.io/part-of":  appNameHSMOperator,
				labelHSMDevice:               deviceName,
				"hsm.j5t.io/serial-number":   specificDevice.SerialNumber,
				"hsm.j5t.io/device-path":     sanitizeLabelValue(specificDevice.DevicePath),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			// Recreate (not the default RollingUpdate): each agent claims exactly one
			// device-plugin resource (hsm.j5t.io/... capacity 1). A rolling update would
			// surge a new pod before terminating the old one, but the single device is
			// still allocated to the old pod, so the surge pod fails admission with
			// "no healthy devices" and the ReplicaSet storms UnexpectedAdmissionError
			// pods. Recreate terminates the old pod (freeing the device) before creating
			// the new one, at the cost of a brief per-agent gap during upgrades.
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RecreateDeploymentStrategyType,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					labelApp: agentName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						labelApp:                     agentName,
						labelAppComponent:            AgentNamePrefix,
						"app.kubernetes.io/instance": agentName,
						labelAppName:                 AgentNamePrefix,
						"app.kubernetes.io/part-of":  appNameHSMOperator,
						labelHSMDevice:               deviceName,
						"hsm.j5t.io/serial-number":   specificDevice.SerialNumber,
						"hsm.j5t.io/device-path":     sanitizeLabelValue(specificDevice.DevicePath),
					},
				},
				Spec: corev1.PodSpec{
					// Pin to the specific node with the HSM device
					NodeSelector: map[string]string{
						labelHostname: targetNode,
					},
					// Affinity for better scheduling
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      labelHostname,
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{targetNode},
											},
										},
									},
								},
							},
						},
					},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    &rootUserId,
						RunAsGroup:   &rootUserId,
						RunAsNonRoot: falsePtr, // Root required for USB access
					},
					ServiceAccountName: r.ServiceAccountName,
					Containers: []corev1.Container{
						{
							Name:  componentAgent,
							Image: agentImage,
							Args:  r.buildAgentArgs(ctx, hsmPool, deviceName),
							Env:   []corev1.EnvVar{},
							Ports: []corev1.ContainerPort{
								{
									Name:          "grpc",
									ContainerPort: AgentPort,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									Name:          "health",
									ContainerPort: AgentHealthPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							// /healthz and /readyz actively probe the HSM token (a live
							// C_GetTokenInfo), so a device that fell off the USB bus fails the
							// probes instead of leaving a "zombie" agent Ready. TimeoutSeconds is
							// raised from the 1s default to allow for a synchronous PKCS#11 call.
							// Liveness is deliberately more tolerant than readiness: the pod goes
							// NotReady quickly (~20s) to stop receiving work, but only restarts on
							// a sustained loss (~60s), so a brief blip or write-lock contention
							// doesn't churn the pod.
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(AgentHealthPort),
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       20,
								TimeoutSeconds:      3,
								FailureThreshold:    3,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/readyz",
										Port: intstr.FromInt(AgentHealthPort),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
								TimeoutSeconds:      3,
								FailureThreshold:    2,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
									extendedResourceName:  resource.MustParse("1"), // Request 1 HSM device
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("256Mi"),
									extendedResourceName:  resource.MustParse("1"), // Limit to 1 HSM device
								},
							},
							// Non-privileged: the device plugin's Allocate response injects the
							// pod's single USB device node WITH a device-cgroup allowance, so
							// pcscd needs no privileges beyond root (host USB nodes are
							// root-owned 0664, owner rw bits suffice). Verified against real
							// Pico HSM hardware with this exact profile. pcscd's writable paths
							// (/run/pcscd, /var/lock/pcsc, /tmp) are all emptyDir mounts.
							SecurityContext: &corev1.SecurityContext{
								Privileged:               falsePtr,
								AllowPrivilegeEscalation: falsePtr,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{capabilityAll},
								},
								ReadOnlyRootFilesystem: truePtr,
								RunAsNonRoot:           falsePtr, // Root required for USB device access
								RunAsUser:              &rootUserId,
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "tmp",
									MountPath: "/tmp",
									ReadOnly:  false, // Required for pcscd runtime with readonly filesystem
								},
								// NOTE: no broad /dev/bus/usb hostPath mount. The device plugin
								// (internal/discovery/deviceplugin.go Allocate) injects ONLY this
								// pod's assigned device node, and because the container is
								// non-privileged that scoping is actually enforced: this agent's
								// pcscd cannot see any other USB device on the node, so
								// multi-device LIBUSB_ERROR_BUSY contention and slot-0
								// mis-binding are structurally impossible.
								{
									Name:      "pcscd-run",
									MountPath: "/run/pcscd",
									ReadOnly:  false, // Required for pcscd socket
								},
								{
									Name:      "pcscd-lock",
									MountPath: "/var/lock/pcsc",
									ReadOnly:  false, // Required for pcscd locking
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "tmp",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "pcscd-run",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "pcscd-lock",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	// Mount the shared mTLS server cert into the agent when enabled.
	r.injectAgentTLS(deployment)

	return r.Create(ctx, deployment)
}

// injectAgentTLS mounts the shared mTLS server-cert Secret read-only into the
// agent container. No-op when mTLS is disabled. Rotation needs no pod-template
// change: kubelet refreshes the mounted Secret and certwatcher reloads it.
func (r *HSMPoolAgentReconciler) injectAgentTLS(deployment *appsv1.Deployment) {
	if !r.AgentTLSEnabled || r.AgentTLSSecretName == "" {
		return
	}

	podSpec := &deployment.Spec.Template.Spec
	secretMode := int32(0o400)
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: agentTLSVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName:  r.AgentTLSSecretName,
				DefaultMode: &secretMode,
			},
		},
	})
	for i := range podSpec.Containers {
		podSpec.Containers[i].VolumeMounts = append(podSpec.Containers[i].VolumeMounts, corev1.VolumeMount{
			Name:      agentTLSVolumeName,
			MountPath: agentTLSMountPath,
			ReadOnly:  true,
		})
	}
}

// agentNeedsUpdate checks if the agent deployment needs to be updated due to device path or image changes
func (r *HSMPoolAgentReconciler) agentNeedsUpdate(ctx context.Context, deployment *appsv1.Deployment, hsmPool *hsmv1alpha1.HSMPool) (bool, error) {
	if hsmPool == nil {
		return false, nil // No pool available, no update needed
	}
	// Check if container image needs updating
	if len(deployment.Spec.Template.Spec.Containers) == 0 {
		return false, fmt.Errorf("deployment has no containers")
	}

	container := deployment.Spec.Template.Spec.Containers[0]
	currentImage := container.Image

	// Check if image has changed
	var expectedImage string
	if r.AgentImage != "" {
		expectedImage = r.AgentImage
	} else if r.ImageResolver != nil {
		// Fallback to auto-detection
		expectedImage = r.ImageResolver.GetImage(ctx, "")
	}

	if expectedImage != "" && currentImage != expectedImage {
		// Image has changed, need to update
		return true, nil
	}

	// Detect a mismatch between the desired mTLS state and what the running
	// deployment mounts, so toggling agent-tls (without an image change) still
	// triggers a recreate that adds or removes the cert volume.
	hasTLSVolume := false
	for _, vol := range deployment.Spec.Template.Spec.Volumes {
		if vol.Name == agentTLSVolumeName {
			hasTLSVolume = true
			break
		}
	}
	if wantTLS := r.AgentTLSEnabled && r.AgentTLSSecretName != ""; wantTLS != hasTLSVolume {
		return true, nil
	}

	// Detect SecurityContext drift so hardening changes (e.g. dropping
	// privileged) roll out to existing agents even without an image change.
	if securityContextNeedsUpdate(container.SecurityContext) {
		return true, nil
	}

	// Device-specific path validation is handled by deploymentNeedsUpdateForDevice
	// This function only checks image changes and other deployment-wide properties

	return false, nil
}

// securityContextNeedsUpdate reports whether a running agent container's
// SecurityContext differs from the current non-privileged template on the
// fields that matter for hardening. Nil pointers are treated as their
// Kubernetes defaults (privileged=false, allowPrivilegeEscalation=true,
// readOnlyRootFilesystem=false).
func securityContextNeedsUpdate(sc *corev1.SecurityContext) bool {
	if sc == nil {
		return true
	}
	if sc.Privileged != nil && *sc.Privileged {
		return true
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		return true
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		return true
	}
	return sc.Capabilities == nil || !slices.Contains(sc.Capabilities.Drop, capabilityAll)
}

// deploymentNeedsUpdateForDevice checks if a deployment needs to be updated for a specific device
// This is a simplified check that only validates device-specific configuration
func (r *HSMPoolAgentReconciler) deploymentNeedsUpdateForDevice(deployment *appsv1.Deployment, aggregatedDevice *hsmv1alpha1.DiscoveredDevice) bool {
	// Check node affinity - ensure agent is pinned to the correct node
	if deployment.Spec.Template.Spec.Affinity == nil ||
		deployment.Spec.Template.Spec.Affinity.NodeAffinity == nil ||
		deployment.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return true // Missing required node affinity
	}

	// Check if the node name matches the aggregated device's node
	nodeSelector := deployment.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if len(nodeSelector.NodeSelectorTerms) == 0 {
		return true
	}

	// Check if hostname requirement matches the device's node
	nodeMatches := false
	for _, term := range nodeSelector.NodeSelectorTerms {
		for _, expr := range term.MatchExpressions {
			if expr.Key == labelHostname && expr.Operator == corev1.NodeSelectorOpIn {
				if slices.Contains(expr.Values, aggregatedDevice.NodeName) {
					nodeMatches = true
				}
			}
		}
	}

	if !nodeMatches {
		return true // Node doesn't match
	}

	// Check device path in volume mounts
	for _, vol := range deployment.Spec.Template.Spec.Volumes {
		if vol.Name == volumeNameHSMDevice && vol.HostPath != nil {
			if vol.HostPath.Path != aggregatedDevice.DevicePath {
				return true // Device path changed
			}
		}
	}

	return false
}

// buildAgentArgs builds CLI arguments for the HSM agent
func (r *HSMPoolAgentReconciler) buildAgentArgs(ctx context.Context, hsmPool *hsmv1alpha1.HSMPool, deviceName string) []string {
	args := []string{
		"--mode=agent",
		"--device-name=" + deviceName,
		"--port=" + fmt.Sprintf("%d", AgentPort),
		"--health-port=" + fmt.Sprintf("%d", AgentHealthPort),
	}

	// Point the agent gRPC server at its mounted mTLS material when enabled.
	if r.AgentTLSEnabled && r.AgentTLSSecretName != "" {
		args = append(args,
			"--tls-cert-file="+agentTLSMountPath+"/"+corev1.TLSCertKey,
			"--tls-key-file="+agentTLSMountPath+"/"+corev1.TLSPrivateKeyKey,
			"--tls-client-ca-file="+agentTLSMountPath+"/"+corev1.ServiceAccountRootCAKey,
		)
	}

	// Get HSMDevice from owner reference
	var hsmDevice hsmv1alpha1.HSMDevice
	if err := r.Get(ctx, types.NamespacedName{
		Name:      deviceName,
		Namespace: hsmPool.Namespace,
	}, &hsmDevice); err != nil {
		// If we can't get the device, return basic args
		return args
	}

	// Add PKCS#11 configuration if available
	if hsmDevice.Spec.PKCS11 != nil {
		if hsmDevice.Spec.PKCS11.TokenLabel != "" {
			args = append(args, "--token-label="+hsmDevice.Spec.PKCS11.TokenLabel)
		}

		if hsmDevice.Spec.PKCS11.SlotId >= 0 {
			args = append(args, "--slot-id="+fmt.Sprintf("%d", hsmDevice.Spec.PKCS11.SlotId))
		}

		if hsmDevice.Spec.PKCS11.LibraryPath != "" {
			args = append(args, "--pkcs11-library="+hsmDevice.Spec.PKCS11.LibraryPath)
		}
	}

	return args
}

// sanitizeLabelValue sanitizes a string to be a valid Kubernetes label value
// Kubernetes labels must be alphanumeric, '-', '_', or '.' and start/end with alphanumeric
func sanitizeLabelValue(value string) string {
	if len(value) == 0 {
		return value
	}

	// Replace invalid characters with dashes
	sanitized := strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, value)

	// Ensure starts and ends with alphanumeric
	sanitized = strings.TrimFunc(sanitized, func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})

	// Kubernetes label values have a 63 character limit
	if len(sanitized) > 63 {
		sanitized = sanitized[:63]
		// Re-trim end if we cut off at a non-alphanumeric
		sanitized = strings.TrimFunc(sanitized, func(r rune) bool {
			return (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9')
		})
	}

	return sanitized
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
	deviceName, exists := deployment.Labels[labelHSMDevice]
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
