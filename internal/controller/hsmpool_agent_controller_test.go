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
	"github.com/evanjarrett/hsm-secrets-operator/internal/config"
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
			Eventually(func() error {
				return k8sClient.Create(ctx, hsmDevice)
			}).WithTimeout(2 * time.Second).Should(Succeed())

			// Create HSMPool with ready status and aggregated devices
			hsmPool = &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "hsm.j5t.io/v1alpha1",
							Kind:       "HSMDevice",
							Name:       hsmDevice.Name,
							UID:        hsmDevice.UID,
						},
					},
				},
				Spec: hsmv1alpha1.HSMPoolSpec{},
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
			Eventually(func() error {
				return k8sClient.Create(ctx, hsmPool)
			}).WithTimeout(2 * time.Second).Should(Succeed())

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
			Eventually(func() error {
				return k8sClient.Status().Update(ctx, hsmPool)
			}).WithTimeout(2 * time.Second).Should(Succeed())

			// Create agent manager optimized for testing
			imageResolver := config.NewImageResolver(k8sClient)
			agentManager = agent.NewTestManager(k8sClient, hsmPoolNamespace, "test-agent:latest", imageResolver)
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
			agentName := fmt.Sprintf("hsm-agent-%s-0", hsmDeviceName)
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
				AgentImage:   "test-agent:latest",
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that agent deployment was created")
			agentName := fmt.Sprintf("hsm-agent-%s-0", hsmDeviceName)
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      agentName,
					Namespace: hsmPoolNamespace,
				}, deployment)
			}).WithTimeout(2 * time.Second).WithPolling(50 * time.Millisecond).Should(Succeed())

			// Verify deployment configuration
			Expect(deployment.Name).To(Equal(agentName))
			Expect(deployment.Namespace).To(Equal(hsmPoolNamespace))
			Expect(deployment.Labels).To(HaveKeyWithValue("hsm.j5t.io/device", hsmDeviceName))
			Expect(deployment.Labels).To(HaveKey("hsm.j5t.io/serial-number"))
			Expect(deployment.Labels).To(HaveKey("hsm.j5t.io/device-path"))

			// Check pod template
			podSpec := deployment.Spec.Template.Spec
			Expect(podSpec.NodeSelector).To(HaveKeyWithValue("kubernetes.io/hostname", "worker-1"))
			Expect(podSpec.Containers).To(HaveLen(1))

			container := podSpec.Containers[0]
			Expect(container.Name).To(Equal("agent"))
			Expect(container.Image).To(Equal("test-agent:latest"))
			Expect(container.Args).To(ContainElement("--mode=agent"))
			Expect(container.Args).To(ContainElement("--device-name=" + hsmDeviceName))

			// Note: Services are no longer created for gRPC agents - direct pod-to-pod communication is used
			// Verify ports are configured correctly in the deployment
			foundGRPCPort := false
			foundHealthPort := false
			for _, port := range container.Ports {
				if port.ContainerPort == 9090 {
					foundGRPCPort = true
				}
				if port.ContainerPort == 8093 {
					foundHealthPort = true
				}
			}
			Expect(foundGRPCPort).To(BeTrue(), "gRPC port 9090 should be exposed")
			Expect(foundHealthPort).To(BeTrue(), "Health port 8093 should be exposed")
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
			}).WithTimeout(2 * time.Second).WithPolling(50 * time.Millisecond).Should(Succeed())

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
			agentName := fmt.Sprintf("hsm-agent-%s-0", hsmDeviceName)
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
			}).WithTimeout(2 * time.Second).WithPolling(50 * time.Millisecond).Should(Succeed())

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
			agentName := fmt.Sprintf("hsm-agent-%s-0", hsmDeviceName)
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
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "hsm.j5t.io/v1alpha1",
							Kind:       "HSMDevice",
							Name:       "non-existent-device",
							UID:        "fake-uid-123",
						},
					},
				},
				Spec: hsmv1alpha1.HSMPoolSpec{},
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
				AgentManager: nil, // This will not prevent deployment creation
				AgentImage:   "test-agent:latest",
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			// Should not return error (errors are logged but don't fail reconciliation)
			Expect(err).NotTo(HaveOccurred())

			By("Checking that agent deployment was created despite nil agent manager")
			agentName := fmt.Sprintf("hsm-agent-%s-0", hsmDeviceName)
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      agentName,
					Namespace: hsmPoolNamespace,
				}, deployment)
			}).Should(Succeed())
		})

		It("Should idempotently handle existing agent deployments", func() {
			By("First reconciliation to create agent")
			reconciler := &HSMPoolAgentReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				ImageResolver: config.NewImageResolver(k8sClient),
				AgentManager:  agentManager,
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying agent deployment exists")
			agentName := fmt.Sprintf("hsm-agent-%s-0", hsmDeviceName)
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      agentName,
					Namespace: hsmPoolNamespace,
				}, deployment)
			}).WithTimeout(2 * time.Second).WithPolling(50 * time.Millisecond).Should(Succeed())

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

func (m *MockAgentManager) EnsureAgent(ctx context.Context, hsmPool *hsmv1alpha1.HSMPool) error {
	return nil
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
					Name:      "absent-device-pool",
					Namespace: "default",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "hsm.j5t.io/v1alpha1",
							Kind:       "HSMDevice",
							Name:       "absent-device",
							UID:        "test-uid-1",
						},
					},
				},
				Spec: hsmv1alpha1.HSMPoolSpec{
					GracePeriod: &metav1.Duration{Duration: 5 * time.Minute},
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
					Name:      "recent-device-pool",
					Namespace: "default",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "hsm.j5t.io/v1alpha1",
							Kind:       "HSMDevice",
							Name:       "recent-device",
							UID:        "test-uid-2",
						},
					},
				},
				Spec: hsmv1alpha1.HSMPoolSpec{
					GracePeriod: &metav1.Duration{Duration: 5 * time.Minute},
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
					Name:      "available-device-pool",
					Namespace: "default",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "hsm.j5t.io/v1alpha1",
							Kind:       "HSMDevice",
							Name:       "available-device",
							UID:        "test-uid-3",
						},
					},
				},
				Spec: hsmv1alpha1.HSMPoolSpec{
					GracePeriod: &metav1.Duration{Duration: 5 * time.Minute},
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
					Name:              "never-seen-device-pool",
					Namespace:         "default",
					CreationTimestamp: metav1.NewTime(tenMinutesAgo), // Pool created 10 minutes ago
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "hsm.j5t.io/v1alpha1",
							Kind:       "HSMDevice",
							Name:       "never-seen-device",
							UID:        "test-uid-4",
						},
					},
				},
				Spec: hsmv1alpha1.HSMPoolSpec{
					GracePeriod: &metav1.Duration{Duration: 5 * time.Minute},
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
			Name:      "test-device-pool",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "hsm.j5t.io/v1alpha1",
					Kind:       "HSMDevice",
					Name:       "test-device",
					UID:        "test-uid-5",
				},
			},
		},
		Spec: hsmv1alpha1.HSMPoolSpec{
			GracePeriod: &metav1.Duration{Duration: 3 * time.Minute}, // 3 minute grace period
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

func TestAgentNeedsUpdate(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	tests := []struct {
		name           string
		deployment     *appsv1.Deployment
		hsmDevice      *hsmv1alpha1.HSMDevice
		hsmPool        *hsmv1alpha1.HSMPool
		expectedUpdate bool
		expectError    bool
	}{
		{
			name: "no update needed - same device path",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-agent",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "agent",
									Image: "test-image", // Add image to match reconciler's AgentImage
									VolumeMounts: []corev1.VolumeMount{
										{
											Name:      "hsm-device",
											MountPath: "/dev/hsm",
										},
									},
								},
							},
							Volumes: []corev1.Volume{
								{
									Name: "hsm-device",
									VolumeSource: corev1.VolumeSource{
										HostPath: &corev1.HostPathVolumeSource{
											Path: "/dev/bus/usb/001/015",
										},
									},
								},
							},
						},
					},
				},
			},
			hsmDevice: &hsmv1alpha1.HSMDevice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device",
					Namespace: "default",
				},
			},
			hsmPool: &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device-pool",
					Namespace: "default",
				},
				Status: hsmv1alpha1.HSMPoolStatus{
					AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
						{
							DevicePath: "/dev/bus/usb/001/015",
							Available:  true,
						},
					},
				},
			},
			expectedUpdate: false,
			expectError:    false,
		},
		{
			name: "no update needed - device path changes handled by deploymentNeedsUpdateForDevice",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-agent",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "agent",
									Image: "test-image", // Add image to match reconciler's AgentImage
									VolumeMounts: []corev1.VolumeMount{
										{
											Name:      "hsm-device",
											MountPath: "/dev/hsm",
										},
									},
								},
							},
							Volumes: []corev1.Volume{
								{
									Name: "hsm-device",
									VolumeSource: corev1.VolumeSource{
										HostPath: &corev1.HostPathVolumeSource{
											Path: "/dev/bus/usb/001/015", // Old path
										},
									},
								},
							},
						},
					},
				},
			},
			hsmDevice: &hsmv1alpha1.HSMDevice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device",
					Namespace: "default",
				},
			},
			hsmPool: &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device-pool",
					Namespace: "default",
				},
				Status: hsmv1alpha1.HSMPoolStatus{
					AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
						{
							DevicePath: "/dev/bus/usb/001/016", // New path
							Available:  true,
						},
					},
				},
			},
			expectedUpdate: false,
			expectError:    false,
		},
		{
			name: "no update needed - pool not found",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-agent",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "agent",
									Image: "test-image", // Add image to match reconciler's AgentImage
								},
							},
						},
					},
				},
			},
			hsmDevice: &hsmv1alpha1.HSMDevice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device",
					Namespace: "default",
				},
			},
			// No HSMPool object created (testing nil pool case)
			hsmPool:        nil,
			expectedUpdate: false,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create fake client with objects
			objs := []runtime.Object{tt.hsmDevice}
			if tt.hsmPool != nil {
				objs = append(objs, tt.hsmPool)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objs...).
				Build()

			reconciler := &HSMPoolAgentReconciler{
				Client:        fakeClient,
				Scheme:        scheme,
				ImageResolver: &config.ImageResolver{},
				AgentImage:    "test-image",
			}

			needsUpdate, err := reconciler.agentNeedsUpdate(ctx, tt.deployment, tt.hsmPool)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedUpdate, needsUpdate)
			}
		})
	}
}
