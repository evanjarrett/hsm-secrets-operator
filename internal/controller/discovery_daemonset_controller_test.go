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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/config"
)

var _ = Describe("DiscoveryDaemonSetReconciler", func() {
	Context("When reconciling an HSMDevice", func() {
		var (
			hsmDeviceName      string
			hsmDeviceNamespace = "default"
			discoveryImage     = "test-discovery:latest"
		)

		ctx := context.Background()
		var hsmDevice *hsmv1alpha1.HSMDevice

		BeforeEach(func() {
			// Generate unique resource name for each test
			hsmDeviceName = fmt.Sprintf("test-device-%d", GinkgoRandomSeed())
			hsmDevice = &hsmv1alpha1.HSMDevice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hsmDeviceName,
					Namespace: hsmDeviceNamespace,
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
					NodeSelector: map[string]string{
						"hsm-type": "pico",
					},
				},
			}
		})

		AfterEach(func() {
			// Clean up HSMDevice if it exists
			if hsmDevice != nil {
				_ = k8sClient.Delete(ctx, hsmDevice)
			}
		})

		It("Should create a DaemonSet when HSMDevice is created", func() {
			By("Creating the HSMDevice")
			Expect(k8sClient.Create(ctx, hsmDevice)).To(Succeed())

			By("Reconciling the HSMDevice")
			reconciler := &DiscoveryDaemonSetReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				ImageResolver:      config.NewImageResolver(k8sClient),
				DiscoveryImage:     discoveryImage,
				ServiceAccountName: "hsm-secrets-operator",
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmDeviceName,
					Namespace: hsmDeviceNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that the DaemonSet was created")
			daemonSetName := fmt.Sprintf("%s-discovery", hsmDeviceName)
			daemonSet := &appsv1.DaemonSet{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      daemonSetName,
					Namespace: hsmDeviceNamespace,
				}, daemonSet)
			}).Should(Succeed())

			By("Verifying DaemonSet configuration")
			Expect(daemonSet.Name).To(Equal(daemonSetName))
			Expect(daemonSet.Namespace).To(Equal(hsmDeviceNamespace))

			// Check labels
			Expect(daemonSet.Labels).To(HaveKeyWithValue("app.kubernetes.io/name", "hsm-secrets-operator"))
			Expect(daemonSet.Labels).To(HaveKeyWithValue("app.kubernetes.io/component", "discovery"))
			Expect(daemonSet.Labels).To(HaveKeyWithValue("hsm.j5t.io/device", hsmDeviceName))

			// Check owner reference
			Expect(daemonSet.OwnerReferences).To(HaveLen(1))
			Expect(daemonSet.OwnerReferences[0].Name).To(Equal(hsmDeviceName))
			Expect(daemonSet.OwnerReferences[0].Kind).To(Equal("HSMDevice"))

			// Check pod template
			podSpec := daemonSet.Spec.Template.Spec
			Expect(podSpec.ServiceAccountName).To(Equal(reconciler.ServiceAccountName))
			Expect(podSpec.Containers).To(HaveLen(1))

			container := podSpec.Containers[0]
			Expect(container.Name).To(Equal("discovery"))
			Expect(container.Image).To(Equal(discoveryImage))
			Expect(container.Args).To(ContainElement("--mode=discovery"))

			// Check environment variables
			envVars := container.Env
			var nodeNameEnv, podNamespaceEnv, podNameEnv *corev1.EnvVar
			for i := range envVars {
				switch envVars[i].Name {
				case "NODE_NAME":
					nodeNameEnv = &envVars[i]
				case "POD_NAMESPACE":
					podNamespaceEnv = &envVars[i]
				case "POD_NAME":
					podNameEnv = &envVars[i]
				}
			}

			Expect(nodeNameEnv).NotTo(BeNil())
			Expect(nodeNameEnv.ValueFrom.FieldRef.FieldPath).To(Equal("spec.nodeName"))
			Expect(podNamespaceEnv).NotTo(BeNil())
			Expect(podNamespaceEnv.ValueFrom.FieldRef.FieldPath).To(Equal("metadata.namespace"))
			Expect(podNameEnv).NotTo(BeNil())
			Expect(podNameEnv.ValueFrom.FieldRef.FieldPath).To(Equal("metadata.name"))

			// Check volumes - now includes /run/udev for event-driven USB discovery
			Expect(podSpec.Volumes).To(HaveLen(3))
			var devVolume, sysVolume, udevVolume *corev1.Volume
			for i := range podSpec.Volumes {
				switch podSpec.Volumes[i].Name {
				case "dev":
					devVolume = &podSpec.Volumes[i]
				case "sys":
					sysVolume = &podSpec.Volumes[i]
				case "run-udev":
					udevVolume = &podSpec.Volumes[i]
				}
			}

			Expect(devVolume).NotTo(BeNil())
			Expect(sysVolume).NotTo(BeNil())
			Expect(udevVolume).NotTo(BeNil())

			// In CI environments, volumes use EmptyDir; in production they use HostPath
			if devVolume.HostPath != nil {
				// Production environment - expect HostPath volumes
				Expect(devVolume.HostPath.Path).To(Equal("/dev"))
				Expect(sysVolume.HostPath.Path).To(Equal("/sys"))
				Expect(udevVolume.HostPath.Path).To(Equal("/run/udev"))
			} else {
				// CI/test environment - expect EmptyDir volumes
				Expect(devVolume.EmptyDir).NotTo(BeNil())
				Expect(sysVolume.EmptyDir).NotTo(BeNil())
				Expect(udevVolume.EmptyDir).NotTo(BeNil())
			}

			// Check node selector from HSMDevice
			Expect(podSpec.NodeSelector).To(HaveKeyWithValue("hsm-type", "pico"))

			// Check update strategy
			Expect(daemonSet.Spec.UpdateStrategy.Type).To(Equal(appsv1.RollingUpdateDaemonSetStrategyType))
			Expect(daemonSet.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable).To(Equal(&intstr.IntOrString{Type: intstr.String, StrVal: "50%"}))
		})

		It("Should update DaemonSet when HSMDevice is updated", func() {
			By("Creating the HSMDevice")
			Expect(k8sClient.Create(ctx, hsmDevice)).To(Succeed())

			By("Reconciling to create initial DaemonSet")
			reconciler := &DiscoveryDaemonSetReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				ImageResolver: config.NewImageResolver(k8sClient),
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmDeviceName,
					Namespace: hsmDeviceNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Updating the HSMDevice node selector")
			Eventually(func() error {
				device := &hsmv1alpha1.HSMDevice{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      hsmDeviceName,
					Namespace: hsmDeviceNamespace,
				}, device); err != nil {
					return err
				}

				device.Spec.NodeSelector = map[string]string{
					"hsm-type": "updated",
					"zone":     "us-west-1",
				}
				return k8sClient.Update(ctx, device)
			}).Should(Succeed())

			By("Reconciling after update")
			_, err = reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmDeviceName,
					Namespace: hsmDeviceNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that DaemonSet was updated")
			daemonSetName := fmt.Sprintf("%s-discovery", hsmDeviceName)
			daemonSet := &appsv1.DaemonSet{}
			Eventually(func() map[string]string {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      daemonSetName,
					Namespace: hsmDeviceNamespace,
				}, daemonSet)
				return daemonSet.Spec.Template.Spec.NodeSelector
			}).Should(And(
				HaveKeyWithValue("hsm-type", "updated"),
				HaveKeyWithValue("zone", "us-west-1"),
			))
		})

		It("Should clean up DaemonSet when HSMDevice is deleted", func() {
			By("Creating the HSMDevice")
			Expect(k8sClient.Create(ctx, hsmDevice)).To(Succeed())

			By("Reconciling to create DaemonSet")
			reconciler := &DiscoveryDaemonSetReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				ImageResolver: config.NewImageResolver(k8sClient),
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmDeviceName,
					Namespace: hsmDeviceNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying DaemonSet exists")
			daemonSetName := fmt.Sprintf("%s-discovery", hsmDeviceName)
			daemonSet := &appsv1.DaemonSet{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      daemonSetName,
					Namespace: hsmDeviceNamespace,
				}, daemonSet)
			}).Should(Succeed())

			By("Deleting the HSMDevice")
			Expect(k8sClient.Delete(ctx, hsmDevice)).To(Succeed())

			By("Reconciling after deletion")
			_, err = reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmDeviceName,
					Namespace: hsmDeviceNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying DaemonSet is deleted by garbage collection")
			// Note: The DaemonSet should be deleted by Kubernetes garbage collection
			// due to owner references, but in test environment this might need time
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      daemonSetName,
					Namespace: hsmDeviceNamespace,
				}, daemonSet)
				return errors.IsNotFound(err)
			}).Should(BeTrue())
		})

		It("Should fall back to auto-detection when no discovery image is specified", func() {
			By("Creating the HSMDevice")
			Expect(k8sClient.Create(ctx, hsmDevice)).To(Succeed())

			By("Reconciling the HSMDevice without DiscoveryImage set")
			reconciler := &DiscoveryDaemonSetReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				ImageResolver: config.NewImageResolver(k8sClient),
				// DiscoveryImage intentionally not set to test fallback
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmDeviceName,
					Namespace: hsmDeviceNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that DaemonSet uses default image from auto-detection")
			daemonSetName := fmt.Sprintf("%s-discovery", hsmDeviceName)
			daemonSet := &appsv1.DaemonSet{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      daemonSetName,
					Namespace: hsmDeviceNamespace,
				}, daemonSet)
				if len(daemonSet.Spec.Template.Spec.Containers) > 0 {
					return daemonSet.Spec.Template.Spec.Containers[0].Image
				}
				return ""
			}).Should(Equal("ghcr.io/evanjarrett/hsm-secrets-operator:latest"))
		})
	})

	Describe("findDevicesForDaemonSet", func() {
		var reconciler *DiscoveryDaemonSetReconciler

		BeforeEach(func() {
			reconciler = &DiscoveryDaemonSetReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
		})

		It("Should return reconcile request for discovery DaemonSet", func() {
			daemonSet := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device-discovery",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"app.kubernetes.io/component": "discovery",
						"hsm.j5t.io/device":           "test-device",
					},
				},
			}

			ctx := context.Background()
			requests := reconciler.findDevicesForDaemonSet(ctx, daemonSet)

			Expect(requests).To(HaveLen(1))
			Expect(requests[0].Name).To(Equal("test-device"))
			Expect(requests[0].Namespace).To(Equal("test-namespace"))
		})

		It("Should return no requests for non-discovery DaemonSet", func() {
			daemonSet := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-other-daemonset",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"app.kubernetes.io/component": "other",
						"hsm.j5t.io/device":           "test-device",
					},
				},
			}

			ctx := context.Background()
			requests := reconciler.findDevicesForDaemonSet(ctx, daemonSet)

			Expect(requests).To(BeEmpty())
		})

		It("Should return no requests for DaemonSet without device label", func() {
			daemonSet := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device-discovery",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"app.kubernetes.io/component": "discovery",
					},
				},
			}

			ctx := context.Background()
			requests := reconciler.findDevicesForDaemonSet(ctx, daemonSet)

			Expect(requests).To(BeEmpty())
		})

		It("Should return no requests for DaemonSet without component label", func() {
			daemonSet := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device-discovery",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"hsm.j5t.io/device": "test-device",
					},
				},
			}

			ctx := context.Background()
			requests := reconciler.findDevicesForDaemonSet(ctx, daemonSet)

			Expect(requests).To(BeEmpty())
		})

		It("Should return no requests for non-DaemonSet object", func() {
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-deployment",
					Namespace: "test-namespace",
				},
			}

			ctx := context.Background()
			requests := reconciler.findDevicesForDaemonSet(ctx, deployment)

			Expect(requests).To(BeEmpty())
		})
	})

	Describe("findDevicesForHSMPool", func() {
		var reconciler *DiscoveryDaemonSetReconciler

		BeforeEach(func() {
			reconciler = &DiscoveryDaemonSetReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
		})

		It("Should return reconcile request for pool HSMPool", func() {
			hsmPool := &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device-pool",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"app.kubernetes.io/component": "pool",
						"hsm.j5t.io/device":           "test-device",
					},
				},
			}

			ctx := context.Background()
			requests := reconciler.findDevicesForHSMPool(ctx, hsmPool)

			Expect(requests).To(HaveLen(1))
			Expect(requests[0].Name).To(Equal("test-device"))
			Expect(requests[0].Namespace).To(Equal("test-namespace"))
		})

		It("Should return no requests for non-pool HSMPool", func() {
			hsmPool := &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-other-pool",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"app.kubernetes.io/component": "other",
						"hsm.j5t.io/device":           "test-device",
					},
				},
			}

			ctx := context.Background()
			requests := reconciler.findDevicesForHSMPool(ctx, hsmPool)

			Expect(requests).To(BeEmpty())
		})

		It("Should return no requests for HSMPool without device label", func() {
			hsmPool := &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device-pool",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"app.kubernetes.io/component": "pool",
					},
				},
			}

			ctx := context.Background()
			requests := reconciler.findDevicesForHSMPool(ctx, hsmPool)

			Expect(requests).To(BeEmpty())
		})

		It("Should return no requests for HSMPool without component label", func() {
			hsmPool := &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device-pool",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"hsm.j5t.io/device": "test-device",
					},
				},
			}

			ctx := context.Background()
			requests := reconciler.findDevicesForHSMPool(ctx, hsmPool)

			Expect(requests).To(BeEmpty())
		})

		It("Should return no requests for non-HSMPool object", func() {
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-deployment",
					Namespace: "test-namespace",
				},
			}

			ctx := context.Background()
			requests := reconciler.findDevicesForHSMPool(ctx, deployment)

			Expect(requests).To(BeEmpty())
		})
	})
})
