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

package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// ManagerInterface defines the interface for HSM agent management
// This allows for easier testing with mocks
type ManagerInterface interface {
	EnsureAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, hsmSecret *hsmv1alpha1.HSMSecret) (string, error)
	CleanupAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error
	GetAgentEndpoint(hsmDevice *hsmv1alpha1.HSMDevice) string
}

// AgentStatus represents the current status of an agent
type AgentStatus string

const (
	AgentStatusCreating AgentStatus = "Creating"
	AgentStatusReady    AgentStatus = "Ready"
	AgentStatusFailed   AgentStatus = "Failed"
)

// AgentInfo tracks agent state and connections
type AgentInfo struct {
	DeviceName      string
	PodIPs          []string
	CreatedAt       time.Time
	LastHealthCheck time.Time
	Status          AgentStatus
	AgentName       string
	Namespace       string
}

const (
	// AgentNamePrefix is the prefix for HSM agent deployment names
	AgentNamePrefix = "hsm-agent"

	// AgentPort is the port the HSM agent serves on (now gRPC)
	AgentPort = 9090

	// AgentHealthPort is the port for health checks (HTTP for simplicity)
	AgentHealthPort = 8093
)

// Manager handles HSM agent pod lifecycle
type Manager struct {
	client.Client
	AgentImage     string
	AgentNamespace string
	ImageResolver  ImageResolver

	// Internal tracking
	activeAgents map[string]*AgentInfo // deviceName -> AgentInfo
	mu           sync.RWMutex

	// Test configuration
	TestMode         bool          // Enable test mode for faster operations
	WaitTimeout      time.Duration // Timeout for waiting operations (default: 60s)
	WaitPollInterval time.Duration // Polling interval for waiting operations (default: 2s)
}

// ImageResolver interface for dependency injection
type ImageResolver interface {
	GetImage(ctx context.Context, defaultImage string) string
}

// NewManager creates a new agent manager
func NewManager(k8sClient client.Client, namespace string, imageResolver ImageResolver) *Manager {

	m := &Manager{
		Client:         k8sClient,
		AgentNamespace: namespace,
		ImageResolver:  imageResolver,
		activeAgents:   make(map[string]*AgentInfo),
		// Default production timeouts
		WaitTimeout:      60 * time.Second,
		WaitPollInterval: 2 * time.Second,
	}

	// If no namespace provided, agents will be deployed in the same namespace as their HSMDevice
	// AgentNamespace is only used as a fallback now

	return m
}

// NewTestManager creates a new agent manager optimized for testing
func NewTestManager(k8sClient client.Client, namespace string, imageResolver ImageResolver) *Manager {
	m := &Manager{
		Client:         k8sClient,
		AgentNamespace: namespace,
		ImageResolver:  imageResolver,
		activeAgents:   make(map[string]*AgentInfo),
		// Fast test timeouts
		TestMode:         true,
		WaitTimeout:      5 * time.Second,
		WaitPollInterval: 100 * time.Millisecond,
	}
	return m
}

// EnsureAgent ensures an HSM agent pod exists for the given HSM device
func (m *Manager) EnsureAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, hsmSecret *hsmv1alpha1.HSMSecret) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	deviceName := hsmDevice.Name
	agentName := m.generateAgentName(hsmDevice)
	agentNamespace := hsmDevice.Namespace

	// Check if we already have this agent tracked
	if agentInfo, exists := m.activeAgents[deviceName]; exists {
		// Agent exists in tracking, verify it's still healthy
		if m.isAgentHealthy(ctx, agentInfo) {
			// Return the first pod IP as endpoint for backward compatibility
			if len(agentInfo.PodIPs) > 0 {
				return fmt.Sprintf("http://%s:%d", agentInfo.PodIPs[0], AgentPort), nil
			}
		}
		// Agent unhealthy, remove from tracking and recreate
		m.removeAgentFromTracking(deviceName)
	}

	// Check if deployment exists in Kubernetes
	var deployment appsv1.Deployment
	err := m.Get(ctx, types.NamespacedName{
		Name:      agentName,
		Namespace: agentNamespace,
	}, &deployment)

	if err == nil {
		// Agent exists, but check if volume mounts need updating due to device path changes
		needsUpdate, err := m.agentNeedsUpdate(ctx, &deployment, hsmDevice)
		if err != nil {
			return "", fmt.Errorf("failed to check if agent needs update: %w", err)
		}

		if needsUpdate {
			// Delete existing deployment to trigger recreation with new volume mounts
			if err := m.Delete(ctx, &deployment); err != nil {
				return "", fmt.Errorf("failed to delete outdated agent deployment: %w", err)
			}
			// Continue to create new deployment below
		} else {
			// Agent exists in K8s but not tracked - wait for it and track it
			podIPs, err := m.waitForAgentReady(ctx, agentName, agentNamespace)
			if err != nil {
				return "", fmt.Errorf("failed waiting for existing agent pods: %w", err)
			}

			// Track the existing agent
			agentInfo := &AgentInfo{
				DeviceName:      deviceName,
				PodIPs:          podIPs,
				CreatedAt:       time.Now(),
				LastHealthCheck: time.Now(),
				Status:          AgentStatusReady,
				AgentName:       agentName,
				Namespace:       agentNamespace,
			}

			m.activeAgents[deviceName] = agentInfo
			return fmt.Sprintf("http://%s:%d", podIPs[0], AgentPort), nil
		}
	}

	if !errors.IsNotFound(err) {
		return "", fmt.Errorf("failed to check agent deployment: %w", err)
	}

	// Create agent deployment
	if err := m.createAgentDeployment(ctx, hsmDevice, hsmSecret, agentNamespace); err != nil {
		return "", fmt.Errorf("failed to create agent deployment: %w", err)
	}

	// Wait for agent pods to be ready and get their IPs
	podIPs, err := m.waitForAgentReady(ctx, agentName, agentNamespace)
	if err != nil {
		return "", fmt.Errorf("failed waiting for agent pods: %w", err)
	}

	// Track the new agent
	agentInfo := &AgentInfo{
		DeviceName:      deviceName,
		PodIPs:          podIPs,
		CreatedAt:       time.Now(),
		LastHealthCheck: time.Now(),
		Status:          AgentStatusReady,
		AgentName:       agentName,
		Namespace:       agentNamespace,
	}

	m.activeAgents[deviceName] = agentInfo

	// For backward compatibility, still return HTTP endpoint (will change to gRPC later)
	return fmt.Sprintf("http://%s:%d", podIPs[0], AgentPort), nil
}

// CleanupAgent removes the HSM agent for the given device when no longer needed
func (m *Manager) CleanupAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agentName := m.generateAgentName(hsmDevice)

	// Check if any HSMSecrets still reference this device
	var hsmSecretList hsmv1alpha1.HSMSecretList
	if err := m.List(ctx, &hsmSecretList); err != nil {
		return fmt.Errorf("failed to list HSMSecrets: %w", err)
	}

	// Count references to this device
	references := 0
	for _, secret := range hsmSecretList.Items {
		if m.secretReferencesDevice(&secret, hsmDevice) {
			references++
		}
	}

	// If there are still references, don't cleanup
	if references > 0 {
		return nil
	}

	// Remove from internal tracking
	m.removeAgentFromTracking(hsmDevice.Name)

	// Delete deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentName,
			Namespace: hsmDevice.Namespace,
		},
	}
	if err := m.Delete(ctx, deployment); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete agent deployment: %w", err)
	}

	return nil
}

// generateAgentName creates a consistent agent name for an HSM device
func (m *Manager) generateAgentName(hsmDevice *hsmv1alpha1.HSMDevice) string {
	return fmt.Sprintf("%s-%s", AgentNamePrefix, hsmDevice.Name)
}

// getAgentEndpoint returns the HTTP endpoint for the agent
// TODO: This will be removed when we switch to direct pod gRPC connections
func (m *Manager) getAgentEndpoint(agentName, namespace string) string {
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", agentName, namespace, AgentPort)
}

// GetAgentEndpoint returns the HTTP endpoint for the agent for a given HSM device
// This implements the ManagerInterface
func (m *Manager) GetAgentEndpoint(hsmDevice *hsmv1alpha1.HSMDevice) string {
	agentName := m.generateAgentName(hsmDevice)
	namespace := hsmDevice.Namespace
	if namespace == "" {
		namespace = m.AgentNamespace
	}
	return m.getAgentEndpoint(agentName, namespace)
}

// createAgentDeployment creates the HSM agent deployment
func (m *Manager) createAgentDeployment(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, hsmSecret *hsmv1alpha1.HSMSecret, namespace string) error {
	agentName := m.generateAgentName(hsmDevice)

	// Find the node where the HSM device is located
	targetNode := m.findTargetNode(hsmDevice)
	if targetNode == "" {
		return fmt.Errorf("no target node found for HSM device %s", hsmDevice.Name)
	}

	// Get discovery image from environment, manager image, or use default
	agentImage := m.ImageResolver.GetImage(ctx, "AGENT_IMAGE")

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":                         agentName,
				"app.kubernetes.io/component": "hsm-agent",
				"app.kubernetes.io/instance":  agentName,
				"app.kubernetes.io/name":      "hsm-agent",
				"app.kubernetes.io/part-of":   "hsm-secrets-operator",
				"hsm.j5t.io/device":           hsmDevice.Name,
				"hsm.j5t.io/device-type":      string(hsmDevice.Spec.DeviceType),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": agentName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                         agentName,
						"app.kubernetes.io/component": "hsm-agent",
						"app.kubernetes.io/instance":  agentName,
						"app.kubernetes.io/name":      "hsm-agent",
						"app.kubernetes.io/part-of":   "hsm-secrets-operator",
						"hsm.j5t.io/device":           hsmDevice.Name,
						"hsm.j5t.io/device-type":      string(hsmDevice.Spec.DeviceType),
					},
				},
				Spec: corev1.PodSpec{
					// Pin to the specific node with the HSM device
					NodeSelector: map[string]string{
						"kubernetes.io/hostname": targetNode,
					},
					// Affinity for better scheduling
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "kubernetes.io/hostname",
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
						RunAsUser:    int64Ptr(0),
						RunAsGroup:   int64Ptr(0),
						RunAsNonRoot: boolPtr(false),
					},
					ServiceAccountName: "hsm-secrets-operator",
					Containers: []corev1.Container{
						{
							Name:  "agent",
							Image: agentImage,
							Command: []string{
								"/entrypoint.sh",
								"agent",
							},
							Args: []string{
								"--device-name=" + hsmDevice.Name,
								"--port=" + fmt.Sprintf("%d", AgentPort),
								"--health-port=" + fmt.Sprintf("%d", AgentHealthPort),
							},
							Env: m.buildAgentEnv(hsmDevice),
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
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(AgentHealthPort),
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       20,
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
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resourceQuantity("100m"),
									corev1.ResourceMemory: resourceQuantity("128Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resourceQuantity("500m"),
									corev1.ResourceMemory: resourceQuantity("256Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged:               boolPtr(true),
								AllowPrivilegeEscalation: boolPtr(true),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{},
									Add: []corev1.Capability{
										"SYS_ADMIN",
									},
								},
								ReadOnlyRootFilesystem: boolPtr(false),
								RunAsNonRoot:           boolPtr(false),
								RunAsUser:              int64Ptr(0),
							},
							VolumeMounts: m.buildAgentVolumeMounts(hsmDevice),
						},
					},
					Volumes: m.buildAgentVolumes(hsmDevice),
				},
			},
		},
	}

	// Set HSMSecret as owner if provided (for cleanup)
	if hsmSecret != nil {
		if err := ctrl.SetControllerReference(hsmSecret, deployment, m.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}
	}

	return m.Create(ctx, deployment)
}

// findTargetNode finds the node where the HSM device is located by checking the HSMPool
func (m *Manager) findTargetNode(hsmDevice *hsmv1alpha1.HSMDevice) string {
	// Find the HSMPool for this device
	poolName := hsmDevice.Name + "-pool"
	pool := &hsmv1alpha1.HSMPool{}

	ctx := context.Background()
	err := m.Get(ctx, types.NamespacedName{
		Name:      poolName,
		Namespace: hsmDevice.Namespace,
	}, pool)

	if err != nil {
		// Fallback: if no pool found, use node selector if present
		if hsmDevice.Spec.NodeSelector != nil {
			// This would need more sophisticated logic to map selectors to actual nodes
			// For now, return empty to indicate no target found
			return ""
		}
		return ""
	}

	// Look for discovered devices in the pool status
	for _, device := range pool.Status.AggregatedDevices {
		if device.Available && device.NodeName != "" {
			return device.NodeName
		}
	}

	// Fallback: if no specific node found, use node selector if present
	if hsmDevice.Spec.NodeSelector != nil {
		// This would need more sophisticated logic to map selectors to actual nodes
		// For now, return empty to indicate no target found
		return ""
	}

	return ""
}

// secretReferencesDevice checks if an HSMSecret references the given device
func (m *Manager) secretReferencesDevice(hsmSecret *hsmv1alpha1.HSMSecret, hsmDevice *hsmv1alpha1.HSMDevice) bool {
	// This is a simplified check - in practice, you might want more sophisticated logic
	// to determine which device an HSMSecret should use based on path, device type, etc.
	_ = hsmSecret // TODO: Use for device preference checks
	_ = hsmDevice // TODO: Use for device type compatibility

	// For now, assume any HSMSecret could use any available device of the right type
	// A more sophisticated implementation might check:
	// - HSMSecret annotations for device preferences
	// - Path-based device mapping
	// - Device type compatibility

	return true // Simplified for initial implementation
}

// buildAgentEnv builds environment variables for the HSM agent
func (m *Manager) buildAgentEnv(hsmDevice *hsmv1alpha1.HSMDevice) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{
			Name:  "HSM_DEVICE_NAME",
			Value: hsmDevice.Name,
		},
		{
			Name:  "HSM_DEVICE_TYPE",
			Value: string(hsmDevice.Spec.DeviceType),
		},
	}

	// Add PKCS#11 configuration if available
	if hsmDevice.Spec.PKCS11 != nil {
		env = append(env, []corev1.EnvVar{
			{
				Name:  "PKCS11_LIBRARY_PATH",
				Value: hsmDevice.Spec.PKCS11.LibraryPath,
			},
			{
				Name:  "PKCS11_SLOT_ID",
				Value: fmt.Sprintf("%d", hsmDevice.Spec.PKCS11.SlotId),
			},
			{
				Name:  "PKCS11_TOKEN_LABEL",
				Value: hsmDevice.Spec.PKCS11.TokenLabel,
			},
		}...)

		// Add PIN from secret if configured
		if hsmDevice.Spec.PKCS11.PinSecret != nil {
			env = append(env, corev1.EnvVar{
				Name: "PKCS11_PIN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: hsmDevice.Spec.PKCS11.PinSecret.Name,
						},
						Key: hsmDevice.Spec.PKCS11.PinSecret.Key,
					},
				},
			})
		}
	}

	return env
}

// buildAgentVolumeMounts builds volume mounts for the HSM agent
func (m *Manager) buildAgentVolumeMounts(hsmDevice *hsmv1alpha1.HSMDevice) []corev1.VolumeMount {
	mounts := []corev1.VolumeMount{
		{
			Name:      "tmp",
			MountPath: "/tmp",
		},
	}

	// Add device mounts if needed - get from HSMPool
	poolName := hsmDevice.Name + "-pool"
	pool := &hsmv1alpha1.HSMPool{}

	ctx := context.Background()
	err := m.Get(ctx, types.NamespacedName{
		Name:      poolName,
		Namespace: hsmDevice.Namespace,
	}, pool)

	if err == nil {
		for _, device := range pool.Status.AggregatedDevices {
			if device.DevicePath != "" {
				mounts = append(mounts, corev1.VolumeMount{
					Name:      "hsm-device",
					MountPath: "/dev/hsm",
				})
				break // Only need one mount point
			}
		}
	}

	return mounts
}

// buildAgentVolumes builds volumes for the HSM agent
func (m *Manager) buildAgentVolumes(hsmDevice *hsmv1alpha1.HSMDevice) []corev1.Volume {
	volumes := []corev1.Volume{
		{
			Name: "tmp",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	// Add device volumes if needed - get from HSMPool
	poolName := hsmDevice.Name + "-pool"
	pool := &hsmv1alpha1.HSMPool{}

	ctx := context.Background()
	err := m.Get(ctx, types.NamespacedName{
		Name:      poolName,
		Namespace: hsmDevice.Namespace,
	}, pool)

	if err == nil {
		for _, device := range pool.Status.AggregatedDevices {
			if device.DevicePath != "" {
				volumes = append(volumes, corev1.Volume{
					Name: "hsm-device",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: device.DevicePath,
							Type: hostPathTypePtr(corev1.HostPathCharDev),
						},
					},
				})
				break // Only need one volume
			}
		}
	}

	return volumes
}

// agentNeedsUpdate checks if the agent deployment needs to be updated due to device path changes
func (m *Manager) agentNeedsUpdate(ctx context.Context, deployment *appsv1.Deployment, hsmDevice *hsmv1alpha1.HSMDevice) (bool, error) {
	// Get current HSMPool to check for updated device paths
	poolName := hsmDevice.Name + "-pool"
	pool := &hsmv1alpha1.HSMPool{}

	if err := m.Get(ctx, types.NamespacedName{
		Name:      poolName,
		Namespace: hsmDevice.Namespace,
	}, pool); err != nil {
		// If pool doesn't exist, no devices are available, so agent doesn't need update
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get HSMPool %s: %w", poolName, err)
	}

	// Extract current volume mounts from deployment
	if len(deployment.Spec.Template.Spec.Containers) == 0 {
		return false, fmt.Errorf("deployment has no containers")
	}

	container := deployment.Spec.Template.Spec.Containers[0]
	currentDeviceMounts := make(map[string]string) // mount name -> device path

	for _, mount := range container.VolumeMounts {
		if mount.Name == "hsm-device" {
			// Find corresponding volume
			for _, vol := range deployment.Spec.Template.Spec.Volumes {
				if vol.Name == mount.Name && vol.HostPath != nil {
					currentDeviceMounts[mount.Name] = vol.HostPath.Path
					break
				}
			}
		}
	}

	// Check if any device paths in the pool differ from current mounts
	for _, device := range pool.Status.AggregatedDevices {
		if device.DevicePath != "" && device.Available {
			// Check if this device path is already mounted
			found := false
			for _, path := range currentDeviceMounts {
				if path == device.DevicePath {
					found = true
					break
				}
			}
			if !found {
				// New device path found that's not in current deployment
				return true, nil
			}
		}
	}

	// Check for stale device paths (mounted paths that are no longer in aggregated devices)
	for _, currentPath := range currentDeviceMounts {
		found := false
		for _, device := range pool.Status.AggregatedDevices {
			if device.DevicePath == currentPath && device.Available {
				found = true
				break
			}
		}
		if !found {
			// Current mount points to a device path that's no longer available
			return true, nil
		}
	}

	return false, nil
}

// Helper functions
// waitForAgentReady waits for agent pods to be ready and returns their IPs
func (m *Manager) waitForAgentReady(ctx context.Context, agentName, namespace string) ([]string, error) {
	// In test mode, simulate immediate readiness for faster tests
	if m.TestMode {
		return []string{"127.0.0.1"}, nil
	}

	timeout := time.After(m.WaitTimeout)
	ticker := time.NewTicker(m.WaitPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for agent pods to be ready after %v", m.WaitTimeout)
		case <-ticker.C:
			pods := &corev1.PodList{}
			err := m.List(ctx, pods,
				client.InNamespace(namespace),
				client.MatchingLabels{"app": agentName},
			)
			if err != nil {
				continue
			}

			var readyPodIPs []string
			for _, pod := range pods.Items {
				if pod.Status.Phase == corev1.PodRunning &&
					len(pod.Status.PodIP) > 0 {
					// Check if all containers are ready
					allReady := true
					for _, condition := range pod.Status.Conditions {
						if condition.Type == corev1.PodReady {
							allReady = condition.Status == corev1.ConditionTrue
							break
						}
					}
					if allReady {
						readyPodIPs = append(readyPodIPs, pod.Status.PodIP)
					}
				}
			}

			if len(readyPodIPs) > 0 {
				return readyPodIPs, nil
			}
		}
	}
}

// GetAgentInfo returns the AgentInfo for a device
func (m *Manager) GetAgentInfo(deviceName string) (*AgentInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agentInfo, exists := m.activeAgents[deviceName]
	return agentInfo, exists
}

// removeAgentFromTracking removes an agent from internal tracking
func (m *Manager) removeAgentFromTracking(deviceName string) {
	delete(m.activeAgents, deviceName)
}

// RemoveAgentFromTracking removes an agent from tracking (public method for testing)
func (m *Manager) RemoveAgentFromTracking(deviceName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeAgentFromTracking(deviceName)
}

// SetAgentInfo sets agent information for testing
func (m *Manager) SetAgentInfo(deviceName string, info *AgentInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.activeAgents[deviceName] = info
}

// isAgentHealthy checks if an agent is healthy by verifying pod IPs
func (m *Manager) isAgentHealthy(ctx context.Context, agentInfo *AgentInfo) bool {
	// Simple health check: ensure pod IPs are still valid
	// In the future, we can add gRPC health checks here
	if len(agentInfo.PodIPs) == 0 {
		return false
	}

	// Check if pods still exist and are running
	pods := &corev1.PodList{}
	err := m.List(ctx, pods,
		client.InNamespace(agentInfo.Namespace),
		client.MatchingLabels{"app": agentInfo.AgentName},
	)
	if err != nil {
		return false
	}

	runningPods := 0
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			runningPods++
		}
	}

	return runningPods > 0
}

// GetAgentPodIPs returns the pod IPs for a device (for direct gRPC connections)
func (m *Manager) GetAgentPodIPs(deviceName string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agentInfo, exists := m.activeAgents[deviceName]
	if !exists {
		return nil, fmt.Errorf("no active agents found for device %s", deviceName)
	}

	if len(agentInfo.PodIPs) == 0 {
		return nil, fmt.Errorf("no pod IPs available for device %s", deviceName)
	}

	return agentInfo.PodIPs, nil
}

// GetGRPCEndpoints returns gRPC endpoints for all agent pods of a device
func (m *Manager) GetGRPCEndpoints(deviceName string) ([]string, error) {
	podIPs, err := m.GetAgentPodIPs(deviceName)
	if err != nil {
		return nil, err
	}

	endpoints := make([]string, 0, len(podIPs))
	for _, ip := range podIPs {
		endpoints = append(endpoints, fmt.Sprintf("%s:%d", ip, AgentPort))
	}

	return endpoints, nil
}

// CreateGRPCClients creates gRPC clients for all agent pods of a device
func (m *Manager) CreateGRPCClients(ctx context.Context, deviceName string, logger logr.Logger) ([]hsm.Client, error) {
	endpoints, err := m.GetGRPCEndpoints(deviceName)
	if err != nil {
		return nil, err
	}

	clients := make([]hsm.Client, 0, len(endpoints))
	for _, endpoint := range endpoints {
		grpcClient, err := NewGRPCClient(endpoint, deviceName, logger)
		if err != nil {
			// Clean up any successful connections
			for _, c := range clients {
				if err := c.Close(); err != nil {
					logger.Error(err, "Failed to close gRPC connection during cleanup")
				}
			}
			return nil, fmt.Errorf("failed to create gRPC client for %s: %w", endpoint, err)
		}

		// Test the connection
		if err := grpcClient.Initialize(ctx, hsm.Config{}); err != nil {
			// Clean up any successful connections
			for _, c := range clients {
				if err := c.Close(); err != nil {
					logger.Error(err, "Failed to close gRPC connection during cleanup")
				}
			}
			if err := grpcClient.Close(); err != nil {
				logger.Error(err, "Failed to close gRPC client during cleanup")
			}
			return nil, fmt.Errorf("failed to initialize gRPC client for %s: %w", endpoint, err)
		}

		clients = append(clients, grpcClient)
	}

	return clients, nil
}

// CreateSingleGRPCClient creates a gRPC client for the first available agent pod of a device
func (m *Manager) CreateSingleGRPCClient(ctx context.Context, deviceName string, logger logr.Logger) (hsm.Client, error) {
	endpoints, err := m.GetGRPCEndpoints(deviceName)
	if err != nil {
		return nil, err
	}

	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no agent endpoints available for device %s", deviceName)
	}

	// Use the first endpoint for single client
	grpcClient, err := NewGRPCClient(endpoints[0], deviceName, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client for %s: %w", endpoints[0], err)
	}

	// Test the connection
	if err := grpcClient.Initialize(ctx, hsm.Config{}); err != nil {
		if err := grpcClient.Close(); err != nil {
			logger.Error(err, "Failed to close gRPC client after failed initialization")
		}
		return nil, fmt.Errorf("failed to initialize gRPC client for %s: %w", endpoints[0], err)
	}

	return grpcClient, nil
}

func int32Ptr(i int32) *int32 {
	return &i
}

func int64Ptr(i int64) *int64 {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}

func hostPathTypePtr(t corev1.HostPathType) *corev1.HostPathType {
	return &t
}

func resourceQuantity(s string) resource.Quantity {
	q, _ := resource.ParseQuantity(s)
	return q
}
