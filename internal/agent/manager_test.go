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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
)

func TestAgentTracking(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	t.Run("GetAgentInfo - agent exists", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		manager := NewManager(fakeClient, "test-namespace", "test-agent:latest", nil)

		// Add agent to tracking
		agentInfo := &AgentInfo{
			PodIPs:    []string{"10.1.1.5", "10.1.1.6"},
			Status:    AgentStatusReady,
			AgentName: "hsm-agent-test-device",
			Namespace: "default",
		}
		manager.activeAgents["test-device"] = agentInfo

		// Test retrieval
		retrieved, exists := manager.GetAgentInfo("test-device")
		assert.True(t, exists)
		assert.Equal(t, agentInfo, retrieved)
	})

	t.Run("GetAgentInfo - agent does not exist", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		manager := NewManager(fakeClient, "test-namespace", "test-agent:latest", nil)

		retrieved, exists := manager.GetAgentInfo("nonexistent-device")
		assert.False(t, exists)
		assert.Nil(t, retrieved)
	})

	// GetAgentPodIPs tests removed - function now uses HSMPool-based lookup

	// GetGRPCEndpoints tests removed - function now uses HSMPool-based lookup

	t.Run("removeAgentFromTracking", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		manager := NewManager(fakeClient, "test-namespace", "test-agent:latest", nil)

		// Add agent to tracking
		agentInfo := &AgentInfo{
			PodIPs: []string{"10.1.1.5"},
			Status: AgentStatusReady,
		}
		manager.activeAgents["test-device"] = agentInfo

		// Verify it exists
		_, exists := manager.GetAgentInfo("test-device")
		assert.True(t, exists)

		// Remove it
		manager.removeAgentFromTracking("test-device")

		// Verify it's gone
		_, exists = manager.GetAgentInfo("test-device")
		assert.False(t, exists)
	})
}

func TestGetAvailableDevices(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	tests := []struct {
		name            string
		hsmPools        []*hsmv1alpha1.HSMPool
		agentPods       []*corev1.Pod
		expectedDevices []hsmv1alpha1.DiscoveredDevice
		expectError     bool
	}{
		{
			name: "ready pool returns device name regardless of agents",
			hsmPools: []*hsmv1alpha1.HSMPool{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pico-hsm-pool",
						Namespace: "test-namespace",
					},
					Status: hsmv1alpha1.HSMPoolStatus{
						Phase: hsmv1alpha1.HSMPoolPhaseReady,
						AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
							{
								DevicePath:   "/dev/bus/usb/001/015",
								Available:    true,
								SerialNumber: "ABC123",
							},
						},
					},
				},
			},
			agentPods: []*corev1.Pod{},
			expectedDevices: []hsmv1alpha1.DiscoveredDevice{
				{
					DevicePath:   "/dev/bus/usb/001/015",
					Available:    true,
					SerialNumber: "ABC123",
				},
			},
			expectError: false,
		},
		{
			name: "multiple ready pools return multiple device names",
			hsmPools: []*hsmv1alpha1.HSMPool{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pico-hsm-pool",
						Namespace: "test-namespace",
					},
					Status: hsmv1alpha1.HSMPoolStatus{
						Phase: hsmv1alpha1.HSMPoolPhaseReady,
						AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
							{
								DevicePath:   "/dev/bus/usb/001/016",
								Available:    true,
								SerialNumber: "DEF456",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "yubikey-pool",
						Namespace: "test-namespace",
					},
					Status: hsmv1alpha1.HSMPoolStatus{
						Phase: hsmv1alpha1.HSMPoolPhaseReady,
						AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
							{
								DevicePath:   "/dev/bus/usb/001/017",
								Available:    true,
								SerialNumber: "GHI789",
							},
						},
					},
				},
			},
			agentPods: []*corev1.Pod{},
			expectedDevices: []hsmv1alpha1.DiscoveredDevice{
				{
					DevicePath:   "/dev/bus/usb/001/016",
					Available:    true,
					SerialNumber: "DEF456",
				},
				{
					DevicePath:   "/dev/bus/usb/001/017",
					Available:    true,
					SerialNumber: "GHI789",
				},
			},
			expectError: false,
		},
		{
			name: "pool not ready - should be excluded",
			hsmPools: []*hsmv1alpha1.HSMPool{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pico-hsm-pool",
						Namespace: "test-namespace",
					},
					Status: hsmv1alpha1.HSMPoolStatus{
						Phase: hsmv1alpha1.HSMPoolPhasePending,
					},
				},
			},
			agentPods:       []*corev1.Pod{},
			expectedDevices: []hsmv1alpha1.DiscoveredDevice{},
			expectError:     true,
		},
		{
			name:            "no pools",
			hsmPools:        []*hsmv1alpha1.HSMPool{},
			agentPods:       []*corev1.Pod{},
			expectedDevices: []hsmv1alpha1.DiscoveredDevice{},
			expectError:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create fake client with objects
			var objs []runtime.Object
			for _, pool := range tt.hsmPools {
				objs = append(objs, pool)
			}
			for _, pod := range tt.agentPods {
				objs = append(objs, pod)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objs...).
				Build()

			manager := NewManager(fakeClient, "test-namespace", "test-agent:latest", nil)

			devices, err := manager.GetAvailableDevices(ctx, "test-namespace")

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, tt.expectedDevices, devices)
			}
		})
	}
}

func TestIsAgentHealthy(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	t.Run("healthy agent with running pods", func(t *testing.T) {
		ctx := context.Background()

		// Create running pods
		pod1 := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-pod-1",
				Namespace: "default",
				Labels:    map[string]string{"app": "hsm-agent-test-device"},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(pod1).
			Build()

		manager := NewManager(fakeClient, "test-namespace", "test-agent:latest", nil)

		agentInfo := &AgentInfo{
			PodIPs:    []string{"10.1.1.5"},
			Status:    AgentStatusReady,
			AgentName: "hsm-agent-test-device",
			Namespace: "default",
		}

		healthy := manager.isAgentHealthy(ctx, agentInfo)
		assert.True(t, healthy)
	})

	t.Run("unhealthy agent with no pod IPs", func(t *testing.T) {
		ctx := context.Background()
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		manager := NewManager(fakeClient, "test-namespace", "test-agent:latest", nil)

		agentInfo := &AgentInfo{
			PodIPs:    []string{}, // No pod IPs
			Status:    AgentStatusReady,
			AgentName: "hsm-agent-test-device",
			Namespace: "default",
		}

		healthy := manager.isAgentHealthy(ctx, agentInfo)
		assert.False(t, healthy)
	})

	t.Run("unhealthy agent with no running pods", func(t *testing.T) {
		ctx := context.Background()

		// Create non-running pod
		pod1 := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-pod-1",
				Namespace: "default",
				Labels:    map[string]string{"app": "hsm-agent-test-device"},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodFailed,
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(pod1).
			Build()

		manager := NewManager(fakeClient, "test-namespace", "test-agent:latest", nil)

		agentInfo := &AgentInfo{
			PodIPs:    []string{"10.1.1.5"},
			Status:    AgentStatusReady,
			AgentName: "hsm-agent-test-device",
			Namespace: "default",
		}

		healthy := manager.isAgentHealthy(ctx, agentInfo)
		assert.False(t, healthy)
	})
}
