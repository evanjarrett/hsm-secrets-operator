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
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
)

var _ = Describe("HSMPoolAgentReconciler", func() {
	Context("When reconciling an HSMPool for agent deployment", func() {
		var (
			hsmDeviceName    string
			hsmPoolName      string
			hsmPoolNamespace = "default"
		)

		ctx := context.Background()
		var hsmDevice *hsmv1alpha1.HSMDevice
		var hsmPool *hsmv1alpha1.HSMPool
		var agentManager *agent.Manager

		BeforeEach(func() {
			// Generate unique resource names for each test
			hsmDeviceName = fmt.Sprintf("test-agent-device-%d", GinkgoRandomSeed())
			// Agent manager expects HSMPool to be named {deviceName}-pool
			hsmPoolName = fmt.Sprintf("%s-pool", hsmDeviceName)

			// Create HSMDevice
			hsmDevice = &hsmv1alpha1.HSMDevice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hsmDeviceName,
					Namespace: hsmPoolNamespace,
				},
				Spec: hsmv1alpha1.HSMDeviceSpec{
					DeviceType: "PicoHSM",
					Discovery: &hsmv1alpha1.DiscoverySpec{
						USB: &hsmv1alpha1.USBDeviceSpec{
							VendorID:  "20a0",
							ProductID: "4230",
						},
					},
					PKCS11: &hsmv1alpha1.PKCS11Config{
						LibraryPath: "/usr/lib/libsc-hsm-pkcs11.so",
						SlotId:      0,
						PinSecret: &hsmv1alpha1.SecretKeySelector{
							Name: "test-pin",
							Key:  "pin",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, hsmDevice)).To(Succeed())

			// Create HSMPool with ready status and aggregated devices
			hsmPool = &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
				Spec: hsmv1alpha1.HSMPoolSpec{
					HSMDeviceRefs: []string{hsmDeviceName},
				},
				Status: hsmv1alpha1.HSMPoolStatus{
					Phase:            hsmv1alpha1.HSMPoolPhaseReady,
					AvailableDevices: 1,
					TotalDevices:     1,
					ExpectedPods:     1,
					AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
						{
							DevicePath:   "/dev/bus/usb/001/015",
							SerialNumber: "DC6A33145E23A42A",
							NodeName:     "worker-1",
							LastSeen:     metav1.Now(),
							Available:    true,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, hsmPool)).To(Succeed())

			// Update HSMPool status separately
			hsmPool.Status = hsmv1alpha1.HSMPoolStatus{
				Phase:            hsmv1alpha1.HSMPoolPhaseReady,
				AvailableDevices: 1,
				TotalDevices:     1,
				ExpectedPods:     1,
				AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
					{
						DevicePath:   "/dev/bus/usb/001/015",
						SerialNumber: "DC6A33145E23A42A",
						NodeName:     "worker-1",
						LastSeen:     metav1.Now(),
						Available:    true,
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, hsmPool)).To(Succeed())

			// Create agent manager
			imageResolver := NewImageResolver(k8sClient)
			agentManager = agent.NewManager(k8sClient, hsmPoolNamespace, imageResolver)
		})

		AfterEach(func() {
			// Clean up resources
			if hsmPool != nil {
				_ = k8sClient.Delete(ctx, hsmPool)
			}
			if hsmDevice != nil {
				_ = k8sClient.Delete(ctx, hsmDevice)
			}

			// Clean up any agent deployments that might have been created
			agentName := fmt.Sprintf("hsm-agent-%s", hsmDeviceName)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      agentName,
					Namespace: hsmPoolNamespace,
				},
			}
			_ = k8sClient.Delete(ctx, deployment)

			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      agentName,
					Namespace: hsmPoolNamespace,
				},
			}
			_ = k8sClient.Delete(ctx, service)
		})

		It("Should deploy agent when HSMPool becomes ready with discovered devices", func() {
			By("Reconciling the HSMPool")
			reconciler := &HSMPoolAgentReconciler{
				Client:       k8sClient,
				Scheme:       k8sClient.Scheme(),
				AgentManager: agentManager,
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that agent deployment was created")
			agentName := fmt.Sprintf("hsm-agent-%s", hsmDeviceName)
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      agentName,
					Namespace: hsmPoolNamespace,
				}, deployment)
			}).Should(Succeed())

			// Verify deployment configuration
			Expect(deployment.Name).To(Equal(agentName))
			Expect(deployment.Namespace).To(Equal(hsmPoolNamespace))
			Expect(deployment.Labels).To(HaveKeyWithValue("hsm.j5t.io/device", hsmDeviceName))
			Expect(deployment.Labels).To(HaveKeyWithValue("hsm.j5t.io/device-type", "PicoHSM"))

			// Check pod template
			podSpec := deployment.Spec.Template.Spec
			Expect(podSpec.NodeSelector).To(HaveKeyWithValue("kubernetes.io/hostname", "worker-1"))
			Expect(podSpec.Containers).To(HaveLen(1))

			container := podSpec.Containers[0]
			Expect(container.Name).To(Equal("agent"))
			Expect(container.Image).To(Equal("test-agent:latest"))
			Expect(container.Command).To(Equal([]string{"/entrypoint.sh", "agent"}))
			Expect(container.Args).To(ContainElement("--device-name=" + hsmDeviceName))

			By("Checking that agent service was created")
			service := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      agentName,
					Namespace: hsmPoolNamespace,
				}, service)
			}).Should(Succeed())

			// Verify service configuration
			Expect(service.Name).To(Equal(agentName))
			Expect(service.Namespace).To(Equal(hsmPoolNamespace))
			Expect(service.Labels).To(HaveKeyWithValue("hsm.j5t.io/device", hsmDeviceName))
			Expect(service.Spec.Ports).To(HaveLen(2))
			Expect(service.Spec.Ports[0].Port).To(Equal(int32(8092))) // AgentPort
			Expect(service.Spec.Ports[1].Port).To(Equal(int32(8093))) // AgentHealthPort
		})

		It("Should not deploy agent when HSMPool is not ready", func() {
			By("Updating HSMPool to pending phase")
			Eventually(func() error {
				pool := &hsmv1alpha1.HSMPool{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				}, pool); err != nil {
					return err
				}
				pool.Status.Phase = hsmv1alpha1.HSMPoolPhasePending
				return k8sClient.Status().Update(ctx, pool)
			}).Should(Succeed())

			By("Reconciling the HSMPool")
			reconciler := &HSMPoolAgentReconciler{
				Client:       k8sClient,
				Scheme:       k8sClient.Scheme(),
				AgentManager: agentManager,
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that no agent deployment was created")
			agentName := fmt.Sprintf("hsm-agent-%s", hsmDeviceName)
			deployment := &appsv1.Deployment{}
			Consistently(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      agentName,
					Namespace: hsmPoolNamespace,
				}, deployment)
			}).Should(MatchError(ContainSubstring("not found")))
		})

		It("Should not deploy agent when HSMPool has no aggregated devices", func() {
			By("Updating HSMPool to remove aggregated devices")
			Eventually(func() error {
				pool := &hsmv1alpha1.HSMPool{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				}, pool); err != nil {
					return err
				}
				pool.Status.AggregatedDevices = []hsmv1alpha1.DiscoveredDevice{}
				return k8sClient.Status().Update(ctx, pool)
			}).Should(Succeed())

			By("Reconciling the HSMPool")
			reconciler := &HSMPoolAgentReconciler{
				Client:       k8sClient,
				Scheme:       k8sClient.Scheme(),
				AgentManager: agentManager,
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that no agent deployment was created")
			agentName := fmt.Sprintf("hsm-agent-%s", hsmDeviceName)
			deployment := &appsv1.Deployment{}
			Consistently(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      agentName,
					Namespace: hsmPoolNamespace,
				}, deployment)
			}).Should(MatchError(ContainSubstring("not found")))
		})

		It("Should handle missing HSMDevice gracefully", func() {
			By("Creating HSMPool referencing non-existent HSMDevice")
			missingDevicePoolName := fmt.Sprintf("missing-device-pool-%d", GinkgoRandomSeed())
			missingDevicePool := &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      missingDevicePoolName,
					Namespace: hsmPoolNamespace,
				},
				Spec: hsmv1alpha1.HSMPoolSpec{
					HSMDeviceRefs: []string{"non-existent-device"},
				},
				Status: hsmv1alpha1.HSMPoolStatus{
					Phase: hsmv1alpha1.HSMPoolPhaseReady,
					AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
						{
							DevicePath: "/dev/bus/usb/001/016",
							NodeName:   "worker-2",
							Available:  true,
							LastSeen:   metav1.Now(),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, missingDevicePool)).To(Succeed())

			// Update status separately
			missingDevicePool.Status = hsmv1alpha1.HSMPoolStatus{
				Phase: hsmv1alpha1.HSMPoolPhaseReady,
				AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
					{
						DevicePath: "/dev/bus/usb/001/016",
						NodeName:   "worker-2",
						Available:  true,
						LastSeen:   metav1.Now(),
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, missingDevicePool)).To(Succeed())

			By("Reconciling the HSMPool with missing device")
			reconciler := &HSMPoolAgentReconciler{
				Client:       k8sClient,
				Scheme:       k8sClient.Scheme(),
				AgentManager: agentManager,
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      missingDevicePoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying no agent was deployed for missing device")
			deployment := &appsv1.Deployment{}
			Consistently(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      "hsm-agent-non-existent-device",
					Namespace: hsmPoolNamespace,
				}, deployment)
			}).Should(MatchError(ContainSubstring("not found")))

			// Clean up
			_ = k8sClient.Delete(ctx, missingDevicePool)
		})

		It("Should handle agent manager errors gracefully", func() {
			By("Creating reconciler with nil agent manager")
			reconciler := &HSMPoolAgentReconciler{
				Client:       k8sClient,
				Scheme:       k8sClient.Scheme(),
				AgentManager: nil, // This will cause an error
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			// Should not return error (errors are logged but don't fail reconciliation)
			Expect(err).NotTo(HaveOccurred())

			By("Checking that no agent deployment was created")
			agentName := fmt.Sprintf("hsm-agent-%s", hsmDeviceName)
			deployment := &appsv1.Deployment{}
			Consistently(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      agentName,
					Namespace: hsmPoolNamespace,
				}, deployment)
			}).Should(MatchError(ContainSubstring("not found")))
		})

		It("Should idempotently handle existing agent deployments", func() {
			By("First reconciliation to create agent")
			reconciler := &HSMPoolAgentReconciler{
				Client:       k8sClient,
				Scheme:       k8sClient.Scheme(),
				AgentManager: agentManager,
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying agent deployment exists")
			agentName := fmt.Sprintf("hsm-agent-%s", hsmDeviceName)
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      agentName,
					Namespace: hsmPoolNamespace,
				}, deployment)
			}).Should(Succeed())

			originalUID := deployment.UID

			By("Second reconciliation (should be idempotent)")
			_, err = reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying deployment was not recreated")
			updatedDeployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      agentName,
				Namespace: hsmPoolNamespace,
			}, updatedDeployment)).To(Succeed())

			// Same UID means it wasn't recreated
			Expect(updatedDeployment.UID).To(Equal(originalUID))
		})
	})
})

// MockAgentManager implements the agent.ManagerInterface for testing cleanup functionality
type MockAgentManager struct {
	CleanupCalls []string // Track which devices were cleaned up
}

func (m *MockAgentManager) EnsureAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, hsmSecret *hsmv1alpha1.HSMSecret) (string, error) {
	return "mock-endpoint", nil
}

func (m *MockAgentManager) CleanupAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
	m.CleanupCalls = append(m.CleanupCalls, hsmDevice.Name)
	return nil
}

func (m *MockAgentManager) GetAgentEndpoint(hsmDevice *hsmv1alpha1.HSMDevice) string {
	return "mock-endpoint"
}

func TestCleanupStaleAgents(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))

	now := time.Now()
	tenMinutesAgo := now.Add(-10 * time.Minute)
	fiveMinutesAgo := now.Add(-5 * time.Minute)

	tests := []struct {
		name             string
		hsmPool          *hsmv1alpha1.HSMPool
		hsmDevices       []*hsmv1alpha1.HSMDevice
		absenceTimeout   time.Duration
		expectedCleanups []string
		description      string
	}{
		{
			name: "cleanup device absent for too long",
			hsmPool: &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: hsmv1alpha1.HSMPoolSpec{
					HSMDeviceRefs: []string{"absent-device"},
					GracePeriod:   &metav1.Duration{Duration: 5 * time.Minute},
				},
				Status: hsmv1alpha1.HSMPoolStatus{
					AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
						{
							DevicePath: "/dev/bus/usb/001/015",
							LastSeen:   metav1.NewTime(tenMinutesAgo), // 10 minutes ago
							Available:  false,                         // Device is unavailable
						},
					},
				},
			},
			hsmDevices: []*hsmv1alpha1.HSMDevice{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "absent-device",
						Namespace: "default",
					},
				},
			},
			absenceTimeout:   8 * time.Minute, // Cleanup after 8 minutes
			expectedCleanups: []string{"absent-device"},
			description:      "Device last seen 10 minutes ago, should be cleaned up (timeout: 8 min)",
		},
		{
			name: "no cleanup for recently seen device",
			hsmPool: &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: hsmv1alpha1.HSMPoolSpec{
					HSMDeviceRefs: []string{"recent-device"},
					GracePeriod:   &metav1.Duration{Duration: 5 * time.Minute},
				},
				Status: hsmv1alpha1.HSMPoolStatus{
					AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
						{
							DevicePath: "/dev/bus/usb/001/015",
							LastSeen:   metav1.NewTime(fiveMinutesAgo), // 5 minutes ago
							Available:  false,                          // Device is unavailable
						},
					},
				},
			},
			hsmDevices: []*hsmv1alpha1.HSMDevice{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "recent-device",
						Namespace: "default",
					},
				},
			},
			absenceTimeout:   8 * time.Minute, // Cleanup after 8 minutes
			expectedCleanups: []string{},      // No cleanup - within timeout
			description:      "Device last seen 5 minutes ago, should not be cleaned up (timeout: 8 min)",
		},
		{
			name: "no cleanup for available device",
			hsmPool: &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: hsmv1alpha1.HSMPoolSpec{
					HSMDeviceRefs: []string{"available-device"},
					GracePeriod:   &metav1.Duration{Duration: 5 * time.Minute},
				},
				Status: hsmv1alpha1.HSMPoolStatus{
					AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
						{
							DevicePath: "/dev/bus/usb/001/015",
							LastSeen:   metav1.NewTime(tenMinutesAgo), // 10 minutes ago
							Available:  true,                          // Device is available
						},
					},
				},
			},
			hsmDevices: []*hsmv1alpha1.HSMDevice{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "available-device",
						Namespace: "default",
					},
				},
			},
			absenceTimeout:   8 * time.Minute, // Cleanup after 8 minutes
			expectedCleanups: []string{},      // No cleanup - device is available
			description:      "Device is available, should not be cleaned up regardless of LastSeen",
		},
		{
			name: "cleanup device never seen after pool timeout",
			hsmPool: &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-pool",
					Namespace:         "default",
					CreationTimestamp: metav1.NewTime(tenMinutesAgo), // Pool created 10 minutes ago
				},
				Spec: hsmv1alpha1.HSMPoolSpec{
					HSMDeviceRefs: []string{"never-seen-device"},
					GracePeriod:   &metav1.Duration{Duration: 5 * time.Minute},
				},
				Status: hsmv1alpha1.HSMPoolStatus{
					AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{}, // No devices ever discovered
				},
			},
			hsmDevices: []*hsmv1alpha1.HSMDevice{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "never-seen-device",
						Namespace: "default",
					},
				},
			},
			absenceTimeout:   8 * time.Minute,               // Cleanup after 8 minutes
			expectedCleanups: []string{"never-seen-device"}, // Should cleanup - pool older than timeout
			description:      "Device never discovered, pool older than timeout, should be cleaned up",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create fake client with objects
			objs := []runtime.Object{tt.hsmPool}
			for _, device := range tt.hsmDevices {
				objs = append(objs, device)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objs...).
				Build()

			// Create mock agent manager
			mockAgentManager := &MockAgentManager{}

			// Create reconciler
			reconciler := &HSMPoolAgentReconciler{
				Client:               fakeClient,
				Scheme:               scheme,
				AgentManager:         mockAgentManager,
				DeviceAbsenceTimeout: tt.absenceTimeout,
			}

			// Run cleanup
			err := reconciler.cleanupStaleAgents(ctx, tt.hsmPool)
			require.NoError(t, err, tt.description)

			// Verify expected cleanups
			if len(tt.expectedCleanups) == 0 {
				assert.Empty(t, mockAgentManager.CleanupCalls,
					"Expected no cleanups but got some. %s", tt.description)
			} else {
				assert.Equal(t, tt.expectedCleanups, mockAgentManager.CleanupCalls,
					"Expected cleanups didn't match actual cleanups. %s", tt.description)
			}
		})
	}
}

func TestDefaultAbsenceTimeout(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))

	ctx := context.Background()
	now := time.Now()
	elevenMinutesAgo := now.Add(-11 * time.Minute)

	// Pool with custom grace period but no explicit absence timeout
	hsmPool := &hsmv1alpha1.HSMPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pool",
			Namespace: "default",
		},
		Spec: hsmv1alpha1.HSMPoolSpec{
			HSMDeviceRefs: []string{"test-device"},
			GracePeriod:   &metav1.Duration{Duration: 3 * time.Minute}, // 3 minute grace period
		},
		Status: hsmv1alpha1.HSMPoolStatus{
			AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
				{
					DevicePath: "/dev/bus/usb/001/015",
					LastSeen:   metav1.NewTime(elevenMinutesAgo), // 11 minutes ago
					Available:  false,
				},
			},
		},
	}

	hsmDevice := &hsmv1alpha1.HSMDevice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-device",
			Namespace: "default",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(hsmPool, hsmDevice).
		Build()

	mockAgentManager := &MockAgentManager{}

	reconciler := &HSMPoolAgentReconciler{
		Client:       fakeClient,
		Scheme:       scheme,
		AgentManager: mockAgentManager,
		// DeviceAbsenceTimeout not set - should default to 2x grace period (6 minutes)
	}

	err := reconciler.cleanupStaleAgents(ctx, hsmPool)
	require.NoError(t, err)

	// Should cleanup because device was last seen 11 minutes ago, and default timeout is 2x3=6 minutes
	assert.Equal(t, []string{"test-device"}, mockAgentManager.CleanupCalls,
		"Should cleanup device when using default timeout (2x grace period)")
}
