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
)

const (
	// AgentNamePrefix is the prefix for HSM agent deployment names
	AgentNamePrefix = "hsm-agent"

	// AgentImage is the container image for HSM agents
	AgentImage = "hsm-secrets-operator:latest"

	// AgentPort is the port the HSM agent serves on
	AgentPort = 8092

	// AgentHealthPort is the port for health checks
	AgentHealthPort = 8093
)

// Manager handles HSM agent pod lifecycle
type Manager struct {
	client.Client
	AgentImage     string
	AgentNamespace string
}

// NewManager creates a new agent manager
func NewManager(k8sClient client.Client, agentImage, namespace string) *Manager {
	if agentImage == "" {
		agentImage = AgentImage
	}

	m := &Manager{
		Client:         k8sClient,
		AgentImage:     agentImage,
		AgentNamespace: namespace,
	}

	// If no namespace provided, agents will be deployed in the same namespace as their HSMDevice
	// AgentNamespace is only used as a fallback now

	return m
}

// EnsureAgent ensures an HSM agent pod exists for the given HSM device
func (m *Manager) EnsureAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, hsmSecret *hsmv1alpha1.HSMSecret) (string, error) {
	agentName := m.generateAgentName(hsmDevice)

	// Deploy agent in the same namespace as the HSMDevice
	agentNamespace := hsmDevice.Namespace

	// Check if deployment exists
	var deployment appsv1.Deployment
	err := m.Get(ctx, types.NamespacedName{
		Name:      agentName,
		Namespace: agentNamespace,
	}, &deployment)

	if err == nil {
		// Agent exists, ensure it's running and return endpoint
		return m.getAgentEndpoint(agentName, agentNamespace), nil
	}

	if !errors.IsNotFound(err) {
		return "", fmt.Errorf("failed to check agent deployment: %w", err)
	}

	// Create agent deployment
	if err := m.createAgentDeployment(ctx, hsmDevice, hsmSecret, agentNamespace); err != nil {
		return "", fmt.Errorf("failed to create agent deployment: %w", err)
	}

	// Create agent service
	if err := m.createAgentService(ctx, hsmDevice, agentNamespace); err != nil {
		return "", fmt.Errorf("failed to create agent service: %w", err)
	}

	return m.getAgentEndpoint(agentName, agentNamespace), nil
}

// CleanupAgent removes the HSM agent for the given device when no longer needed
func (m *Manager) CleanupAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
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

	// Delete service
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentName,
			Namespace: hsmDevice.Namespace,
		},
	}
	if err := m.Delete(ctx, service); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete agent service: %w", err)
	}

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
func (m *Manager) getAgentEndpoint(agentName, namespace string) string {
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", agentName, namespace, AgentPort)
}

// createAgentDeployment creates the HSM agent deployment
func (m *Manager) createAgentDeployment(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, hsmSecret *hsmv1alpha1.HSMSecret, namespace string) error {
	agentName := m.generateAgentName(hsmDevice)

	// Find the node where the HSM device is located
	targetNode := m.findTargetNode(hsmDevice)
	if targetNode == "" {
		return fmt.Errorf("no target node found for HSM device %s", hsmDevice.Name)
	}

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
							Image: m.AgentImage,
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
									Name:          "http",
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

// createAgentService creates the service for the HSM agent
func (m *Manager) createAgentService(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, namespace string) error {
	agentName := m.generateAgentName(hsmDevice)

	service := &corev1.Service{
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
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": agentName,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       AgentPort,
					TargetPort: intstr.FromInt(AgentPort),
					Protocol:   corev1.ProtocolTCP,
				},
				{
					Name:       "health",
					Port:       AgentHealthPort,
					TargetPort: intstr.FromInt(AgentHealthPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	return m.Create(ctx, service)
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

// Helper functions
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
