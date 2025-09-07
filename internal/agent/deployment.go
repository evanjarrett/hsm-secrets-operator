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
	"slices"
	"strings"
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
	EnsureAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, hsmSecret *hsmv1alpha1.HSMSecret) error
	CleanupAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error
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

// deviceWork represents work to be done for a specific device
type deviceWork struct {
	device    hsmv1alpha1.DiscoveredDevice
	agentName string
	agentKey  string
	index     int
}

// EnsureAgent ensures HSM agents are deployed for all available devices in the pool
func (m *Manager) EnsureAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, hsmSecret *hsmv1alpha1.HSMSecret) error {
	// Get the HSMPool for this device to find all aggregated devices
	poolName := hsmDevice.Name + "-pool"
	var hsmPool hsmv1alpha1.HSMPool
	if err := m.Get(ctx, types.NamespacedName{
		Name:      poolName,
		Namespace: hsmDevice.Namespace,
	}, &hsmPool); err != nil {
		return fmt.Errorf("failed to get HSMPool %s: %w", poolName, err)
	}

	// Pre-collect available devices to process (no mutex needed)
	workItems := make([]deviceWork, 0, len(hsmPool.Status.AggregatedDevices))
	for i, aggregatedDevice := range hsmPool.Status.AggregatedDevices {
		if !aggregatedDevice.Available {
			continue
		}
		workItems = append(workItems, deviceWork{
			device:    aggregatedDevice,
			agentName: fmt.Sprintf("%s-%s-%d", AgentNamePrefix, hsmDevice.Name, i),
			agentKey:  fmt.Sprintf("%s-%s", hsmDevice.Name, aggregatedDevice.SerialNumber),
			index:     i,
		})
	}

	if len(workItems) == 0 {
		return nil // No available devices to process
	}

	// Process devices in parallel
	var wg sync.WaitGroup
	errChan := make(chan error, len(workItems))

	for _, work := range workItems {
		wg.Add(1)
		go func(w deviceWork) {
			defer wg.Done()

			// Mutex-protected check and update of activeAgents
			m.mu.Lock()
			needsDeployment := false
			if agentInfo, exists := m.activeAgents[w.agentKey]; exists {
				if !m.isAgentHealthy(ctx, agentInfo) {
					m.removeAgentFromTracking(w.agentKey)
					needsDeployment = true
				}
			} else {
				needsDeployment = true
			}
			m.mu.Unlock()

			// Skip if agent is healthy and tracked
			if !needsDeployment {
				return
			}

			// Deploy agent for this device (Kubernetes API calls - no mutex needed)
			if err := m.deployAgentForDevice(ctx, w, hsmDevice); err != nil {
				errChan <- fmt.Errorf("failed to deploy agent %s: %w", w.agentName, err)
			}
		}(work)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Collect any errors
	deploymentErrors := make([]error, 0, len(workItems))
	for err := range errChan {
		deploymentErrors = append(deploymentErrors, err)
	}

	if len(deploymentErrors) > 0 {
		return fmt.Errorf("agent deployment errors: %v", deploymentErrors)
	}

	return nil
}

// deployAgentForDevice handles the deployment logic for a single device
func (m *Manager) deployAgentForDevice(ctx context.Context, work deviceWork, hsmDevice *hsmv1alpha1.HSMDevice) error {
	// Check if deployment exists in Kubernetes
	var deployment appsv1.Deployment
	err := m.Get(ctx, types.NamespacedName{
		Name:      work.agentName,
		Namespace: hsmDevice.Namespace,
	}, &deployment)

	if err == nil {
		// Agent exists, but check if it needs updating (image version, device/node configuration)
		needsUpdate, err := m.agentNeedsUpdate(ctx, &deployment, hsmDevice)
		if err != nil {
			return fmt.Errorf("failed to check if agent deployment %s needs update: %w", work.agentName, err)
		}

		// Also check device-specific configuration
		if !needsUpdate {
			needsUpdate = m.deploymentNeedsUpdateForDevice(&deployment, &work.device)
		}

		if needsUpdate {
			// Delete existing deployment to trigger recreation
			if err := m.Delete(ctx, &deployment); err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("failed to delete outdated agent deployment %s: %w", work.agentName, err)
			}
		} else {
			// Agent exists and is correct - wait for it and track it
			podIPs, err := m.waitForAgentReady(ctx, work.agentName, hsmDevice.Namespace)
			if err != nil {
				return fmt.Errorf("failed waiting for existing agent pods %s: %w", work.agentName, err)
			}

			// Track the existing agent (mutex-protected)
			m.mu.Lock()
			agentInfo := &AgentInfo{
				DeviceName:      work.agentKey,
				PodIPs:          podIPs,
				CreatedAt:       time.Now(),
				LastHealthCheck: time.Now(),
				Status:          AgentStatusReady,
				AgentName:       work.agentName,
				Namespace:       hsmDevice.Namespace,
			}
			m.activeAgents[work.agentKey] = agentInfo
			m.mu.Unlock()
			return nil
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check agent deployment %s: %w", work.agentName, err)
	}

	// Create agent deployment for this specific device
	if err := m.createAgentDeployment(ctx, hsmDevice, nil, hsmDevice.Namespace, &work.device, work.agentName); err != nil {
		return fmt.Errorf("failed to create agent deployment %s: %w", work.agentName, err)
	}

	// Wait for agent pods to be ready and get their IPs
	podIPs, err := m.waitForAgentReady(ctx, work.agentName, hsmDevice.Namespace)
	if err != nil {
		return fmt.Errorf("failed waiting for agent pods %s: %w", work.agentName, err)
	}

	// Track the new agent (mutex-protected)
	m.mu.Lock()
	agentInfo := &AgentInfo{
		DeviceName:      work.agentKey,
		PodIPs:          podIPs,
		CreatedAt:       time.Now(),
		LastHealthCheck: time.Now(),
		Status:          AgentStatusReady,
		AgentName:       work.agentName,
		Namespace:       hsmDevice.Namespace,
	}
	m.activeAgents[work.agentKey] = agentInfo
	m.mu.Unlock()

	return nil
}

// CleanupAgent removes all HSM agents for the given device when no longer needed
func (m *Manager) CleanupAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if any HSMSecrets still reference this device
	var hsmSecretList hsmv1alpha1.HSMSecretList
	if err := m.List(ctx, &hsmSecretList); err != nil {
		return fmt.Errorf("failed to list HSMSecrets: %w", err)
	}

	// In the HSMPool architecture, cleanup should be based on device availability in pool
	// rather than individual secret references, since all secrets can use any available device
	// Check if there are any active HSMSecrets - if so, keep the agents running
	if len(hsmSecretList.Items) > 0 {
		return nil
	}

	// Get the HSMPool to find all agent deployments to clean up
	poolName := hsmDevice.Name + "-pool"
	var hsmPool hsmv1alpha1.HSMPool
	if err := m.Get(ctx, types.NamespacedName{
		Name:      poolName,
		Namespace: hsmDevice.Namespace,
	}, &hsmPool); err != nil {
		// If pool doesn't exist, try to clean up any remaining tracked agents
		return m.cleanupTrackedAgents(ctx, hsmDevice)
	}

	// Clean up all agent deployments (one per aggregated device)
	for i, aggregatedDevice := range hsmPool.Status.AggregatedDevices {
		agentName := fmt.Sprintf("%s-%s-%d", AgentNamePrefix, hsmDevice.Name, i)
		agentKey := fmt.Sprintf("%s-%s", hsmDevice.Name, aggregatedDevice.SerialNumber)

		// Remove from internal tracking
		m.removeAgentFromTracking(agentKey)

		// Delete deployment
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      agentName,
				Namespace: hsmDevice.Namespace,
			},
		}
		if err := m.Delete(ctx, deployment); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete agent deployment %s: %w", agentName, err)
		}
	}

	return nil
}

// cleanupTrackedAgents cleans up any remaining tracked agents when HSMPool is not available
func (m *Manager) cleanupTrackedAgents(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
	// Find all tracked agents for this device
	var agentsToCleanup []string
	devicePrefix := hsmDevice.Name + "-"

	for agentKey := range m.activeAgents {
		if strings.HasPrefix(agentKey, devicePrefix) {
			agentsToCleanup = append(agentsToCleanup, agentKey)
		}
	}

	// Clean up each tracked agent
	for _, agentKey := range agentsToCleanup {
		agentInfo := m.activeAgents[agentKey]

		// Remove from tracking
		m.removeAgentFromTracking(agentKey)

		// Delete deployment
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      agentInfo.AgentName,
				Namespace: agentInfo.Namespace,
			},
		}
		if err := m.Delete(ctx, deployment); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete agent deployment %s: %w", agentInfo.AgentName, err)
		}
	}

	return nil
}

// generateAgentName creates a consistent agent name for an HSM device
func (m *Manager) generateAgentName(hsmDevice *hsmv1alpha1.HSMDevice) string {
	return fmt.Sprintf("%s-%s", AgentNamePrefix, hsmDevice.Name)
}

// createAgentDeployment creates the HSM agent deployment for a specific device
func (m *Manager) createAgentDeployment(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, hsmSecret *hsmv1alpha1.HSMSecret, namespace string, specificDevice *hsmv1alpha1.DiscoveredDevice, customAgentName string) error {
	if specificDevice == nil {
		return fmt.Errorf("specificDevice is required")
	}

	var agentName string
	if customAgentName != "" {
		agentName = customAgentName
	} else {
		agentName = m.generateAgentName(hsmDevice)
	}

	targetNode := specificDevice.NodeName
	devicePath := specificDevice.DevicePath

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
							VolumeMounts: m.buildAgentVolumeMounts(),
						},
					},
					Volumes: m.buildAgentVolumes(devicePath),
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
func (m *Manager) buildAgentVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      "tmp",
			MountPath: "/tmp",
		},
		{
			Name:      "hsm-device",
			MountPath: "/dev/hsm",
		},
	}
}

// buildAgentVolumes builds volumes for the HSM agent
func (m *Manager) buildAgentVolumes(devicePath string) []corev1.Volume {
	return []corev1.Volume{
		{
			Name: "tmp",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: "hsm-device",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: devicePath,
					Type: hostPathTypePtr(corev1.HostPathCharDev),
				},
			},
		},
	}
}

// agentNeedsUpdate checks if the agent deployment needs to be updated due to device path or image changes
func (m *Manager) agentNeedsUpdate(ctx context.Context, deployment *appsv1.Deployment, hsmDevice *hsmv1alpha1.HSMDevice) (bool, error) {
	// Check if container image needs updating
	if len(deployment.Spec.Template.Spec.Containers) == 0 {
		return false, fmt.Errorf("deployment has no containers")
	}

	container := deployment.Spec.Template.Spec.Containers[0]
	currentImage := container.Image

	// Check if image has changed (only if ImageResolver is available)
	if m.ImageResolver != nil {
		expectedImage := m.ImageResolver.GetImage(ctx, "AGENT_IMAGE")
		if currentImage != expectedImage {
			// Image has changed, need to update
			return true, nil
		}
	}

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

// deploymentNeedsUpdateForDevice checks if a deployment needs to be updated for a specific device
// This is a simplified check that only validates device-specific configuration
func (m *Manager) deploymentNeedsUpdateForDevice(deployment *appsv1.Deployment, aggregatedDevice *hsmv1alpha1.DiscoveredDevice) bool {
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
			if expr.Key == "kubernetes.io/hostname" && expr.Operator == corev1.NodeSelectorOpIn {
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
		if vol.Name == "hsm-device" && vol.HostPath != nil {
			if vol.HostPath.Path != aggregatedDevice.DevicePath {
				return true // Device path changed
			}
		}
	}

	return false
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

// GetAgentPodIPs returns all agent pod IPs for a device type from HSMPool
func (m *Manager) GetAgentPodIPs(ctx context.Context, deviceName, namespace string) ([]string, error) {
	// Get HSMPool for this device
	poolName := deviceName + "-pool"
	var hsmPool hsmv1alpha1.HSMPool
	if err := m.Get(ctx, types.NamespacedName{
		Name:      poolName,
		Namespace: namespace,
	}, &hsmPool); err != nil {
		return nil, fmt.Errorf("failed to get HSMPool %s: %w", poolName, err)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var allPodIPs []string

	// Collect pod IPs from all agent instances for this device
	for _, aggregatedDevice := range hsmPool.Status.AggregatedDevices {
		agentKey := fmt.Sprintf("%s-%s", deviceName, aggregatedDevice.SerialNumber)
		if agentInfo, exists := m.activeAgents[agentKey]; exists && len(agentInfo.PodIPs) > 0 {
			allPodIPs = append(allPodIPs, agentInfo.PodIPs...)
		}
	}

	if len(allPodIPs) == 0 {
		return nil, fmt.Errorf("no active agents found for device %s in pool %s", deviceName, poolName)
	}

	return allPodIPs, nil
}

// GetGRPCEndpoints returns gRPC endpoints for all agent pods of a device
func (m *Manager) GetGRPCEndpoints(ctx context.Context, deviceName, namespace string) ([]string, error) {
	podIPs, err := m.GetAgentPodIPs(ctx, deviceName, namespace)
	if err != nil {
		return nil, err
	}

	endpoints := make([]string, 0, len(podIPs))
	for _, ip := range podIPs {
		endpoints = append(endpoints, fmt.Sprintf("%s:%d", ip, AgentPort))
	}

	return endpoints, nil
}

// CreateGRPCClient creates a gRPC client for the first available agent pod of a device
func (m *Manager) CreateGRPCClient(ctx context.Context, deviceName, namespace string, logger logr.Logger) (hsm.Client, error) {
	endpoints, err := m.GetGRPCEndpoints(ctx, deviceName, namespace)
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
