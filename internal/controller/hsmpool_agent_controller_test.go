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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

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
			agentManager = agent.NewManager(k8sClient, "test-agent:latest", hsmPoolNamespace)
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
