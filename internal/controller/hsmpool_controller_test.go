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
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
)

var _ = Describe("HSMPoolReconciler with Manager-Owned DaemonSets", func() {
	Context("When reconciling an HSMPool with DaemonSet pods", func() {
		var (
			hsmDeviceName    string
			hsmPoolName      string
			hsmPoolNamespace = "default"
		)

		ctx := context.Background()

		var hsmDevice *hsmv1alpha1.HSMDevice
		var hsmPool *hsmv1alpha1.HSMPool
		var daemonSet *appsv1.DaemonSet

		BeforeEach(func() {
			// Generate unique resource names for each test
			hsmDeviceName = fmt.Sprintf("test-pool-device-%d", GinkgoRandomSeed())
			hsmPoolName = fmt.Sprintf("test-pool-%d", GinkgoRandomSeed())

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
				},
			}
			Expect(k8sClient.Create(ctx, hsmDevice)).To(Succeed())

			// Create DaemonSet owned by HSMDevice (simulating DiscoveryDaemonSetReconciler)
			daemonSet = &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-discovery", hsmDeviceName),
					Namespace: hsmPoolNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/name":      "hsm-secrets-operator",
						"app.kubernetes.io/component": "discovery",
						"hsm.j5t.io/device":           hsmDeviceName,
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "hsm.j5t.io/v1alpha1",
							Kind:       "HSMDevice",
							Name:       hsmDeviceName,
							UID:        hsmDevice.UID,
							Controller: &[]bool{true}[0],
						},
					},
				},
				Spec: appsv1.DaemonSetSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app.kubernetes.io/name":      "hsm-secrets-operator",
							"app.kubernetes.io/component": "discovery",
							"hsm.j5t.io/device":           hsmDeviceName,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app.kubernetes.io/name":      "hsm-secrets-operator",
								"app.kubernetes.io/component": "discovery",
								"hsm.j5t.io/device":           hsmDeviceName,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "discovery",
									Image: "test-discovery:latest",
								},
							},
						},
					},
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 2,
					NumberReady:            1,
				},
			}
			Expect(k8sClient.Create(ctx, daemonSet)).To(Succeed())

			// Update DaemonSet status separately (status is not created with the resource)
			daemonSet.Status = appsv1.DaemonSetStatus{
				DesiredNumberScheduled: 2,
				NumberReady:            1,
			}
			Expect(k8sClient.Status().Update(ctx, daemonSet)).To(Succeed())

			// Create HSMPool referencing the HSMDevice
			hsmPool = &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
				Spec: hsmv1alpha1.HSMPoolSpec{
					HSMDeviceRefs: []string{hsmDeviceName},
				},
			}
			Expect(k8sClient.Create(ctx, hsmPool)).To(Succeed())
		})

		AfterEach(func() {
			// Clean up resources
			if hsmPool != nil {
				_ = k8sClient.Delete(ctx, hsmPool)
			}
			if daemonSet != nil {
				_ = k8sClient.Delete(ctx, daemonSet)
			}
			if hsmDevice != nil {
				_ = k8sClient.Delete(ctx, hsmDevice)
			}
		})

		It("Should collect pod reports from DaemonSet pods directly", func() {
			By("Creating some discovery pods for the DaemonSet")
			runningPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-pod-1", hsmDeviceName),
					Namespace: hsmPoolNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/name":      "hsm-secrets-operator",
						"app.kubernetes.io/component": "discovery",
						"hsm.j5t.io/device":           hsmDeviceName,
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "node-1",
					Containers: []corev1.Container{
						{
							Name:  "discovery",
							Image: "test-discovery:latest",
						},
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			Expect(k8sClient.Create(ctx, runningPod)).To(Succeed())

			// Update pod status separately (status is not created with the resource)
			runningPod.Status = corev1.PodStatus{
				Phase: corev1.PodRunning,
			}
			Expect(k8sClient.Status().Update(ctx, runningPod)).To(Succeed())

			pendingPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-pod-2", hsmDeviceName),
					Namespace: hsmPoolNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/name":      "hsm-secrets-operator",
						"app.kubernetes.io/component": "discovery",
						"hsm.j5t.io/device":           hsmDeviceName,
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "node-2",
					Containers: []corev1.Container{
						{
							Name:  "discovery",
							Image: "test-discovery:latest",
						},
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			}
			Expect(k8sClient.Create(ctx, pendingPod)).To(Succeed())

			// Update pod status separately (status is not created with the resource)
			pendingPod.Status = corev1.PodStatus{
				Phase: corev1.PodPending,
			}
			Expect(k8sClient.Status().Update(ctx, pendingPod)).To(Succeed())

			By("Reconciling the HSMPool")
			reconciler := &HSMPoolReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that HSMPool status was updated with pod information")
			pool := &hsmv1alpha1.HSMPool{}
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				}, pool)
				return len(pool.Status.ReportingPods) > 0
			}).Should(BeTrue())

			// Verify reporting pods were collected from DaemonSet
			Expect(pool.Status.ReportingPods).To(HaveLen(2))

			var runningReport, pendingReport *hsmv1alpha1.PodReport
			for i := range pool.Status.ReportingPods {
				report := &pool.Status.ReportingPods[i]
				switch report.PodName {
				case runningPod.Name:
					runningReport = report
				case pendingPod.Name:
					pendingReport = report
				}
			}

			Expect(runningReport).NotTo(BeNil())
			Expect(runningReport.NodeName).To(Equal("node-1"))
			Expect(runningReport.DiscoveryStatus).To(Equal("completed"))
			Expect(runningReport.DevicesFound).To(Equal(int32(1))) // Running pod reports 1 device
			Expect(runningReport.Fresh).To(BeTrue())

			Expect(pendingReport).NotTo(BeNil())
			Expect(pendingReport.NodeName).To(Equal("node-2"))
			Expect(pendingReport.DiscoveryStatus).To(Equal("pending"))
			Expect(pendingReport.DevicesFound).To(Equal(int32(0))) // Pending pod reports 0 devices
			Expect(pendingReport.Fresh).To(BeFalse())              // Pending pods are not considered fresh

			// Verify expected pods count from DaemonSet status
			Expect(pool.Status.ExpectedPods).To(Equal(int32(2)))
		})

		It("Should handle missing DaemonSet gracefully", func() {
			By("Creating HSMPool referencing non-existent HSMDevice")
			missingPoolName := fmt.Sprintf("missing-pool-%d", GinkgoRandomSeed())
			missingPool := &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      missingPoolName,
					Namespace: hsmPoolNamespace,
				},
				Spec: hsmv1alpha1.HSMPoolSpec{
					HSMDeviceRefs: []string{"non-existent-device"},
				},
			}
			Expect(k8sClient.Create(ctx, missingPool)).To(Succeed())

			By("Reconciling the HSMPool with missing HSMDevice")
			reconciler := &HSMPoolReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      missingPoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that HSMPool status indicates error")
			pool := &hsmv1alpha1.HSMPool{}
			Eventually(func() hsmv1alpha1.HSMPoolPhase {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      missingPoolName,
					Namespace: hsmPoolNamespace,
				}, pool)
				return pool.Status.Phase
			}).Should(Equal(hsmv1alpha1.HSMPoolPhaseError))
		})

		It("Should aggregate devices from multiple HSMDevices", func() {
			By("Creating a second HSMDevice")
			secondDeviceName := fmt.Sprintf("second-device-%d", GinkgoRandomSeed())
			secondDevice := &hsmv1alpha1.HSMDevice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secondDeviceName,
					Namespace: hsmPoolNamespace,
				},
				Spec: hsmv1alpha1.HSMDeviceSpec{
					DeviceType: "SmartCard-HSM",
				},
			}
			Expect(k8sClient.Create(ctx, secondDevice)).To(Succeed())

			By("Creating a second DaemonSet")
			secondDaemonSet := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-discovery", secondDeviceName),
					Namespace: hsmPoolNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/name":      "hsm-secrets-operator",
						"app.kubernetes.io/component": "discovery",
						"hsm.j5t.io/device":           secondDeviceName,
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "hsm.j5t.io/v1alpha1",
							Kind:       "HSMDevice",
							Name:       secondDeviceName,
							UID:        secondDevice.UID,
							Controller: &[]bool{true}[0],
						},
					},
				},
				Spec: appsv1.DaemonSetSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app.kubernetes.io/name":      "hsm-secrets-operator",
							"app.kubernetes.io/component": "discovery",
							"hsm.j5t.io/device":           secondDeviceName,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app.kubernetes.io/name":      "hsm-secrets-operator",
								"app.kubernetes.io/component": "discovery",
								"hsm.j5t.io/device":           secondDeviceName,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "discovery",
									Image: "test-discovery:latest",
								},
							},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 1,
					NumberReady:            1,
				},
			}
			Expect(k8sClient.Create(ctx, secondDaemonSet)).To(Succeed())

			// Update DaemonSet status separately (status is not created with the resource)
			secondDaemonSet.Status = appsv1.DaemonSetStatus{
				DesiredNumberScheduled: 1,
				NumberReady:            1,
			}
			Expect(k8sClient.Status().Update(ctx, secondDaemonSet)).To(Succeed())

			By("Updating HSMPool to reference both devices")
			Eventually(func() error {
				pool := &hsmv1alpha1.HSMPool{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				}, pool); err != nil {
					return err
				}
				pool.Spec.HSMDeviceRefs = []string{hsmDeviceName, secondDeviceName}
				return k8sClient.Update(ctx, pool)
			}).Should(Succeed())

			By("Creating pods for the second DaemonSet")
			secondPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-pod-1", secondDeviceName),
					Namespace: hsmPoolNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/name":      "hsm-secrets-operator",
						"app.kubernetes.io/component": "discovery",
						"hsm.j5t.io/device":           secondDeviceName,
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "node-3",
					Containers: []corev1.Container{
						{
							Name:  "discovery",
							Image: "test-discovery:latest",
						},
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			Expect(k8sClient.Create(ctx, secondPod)).To(Succeed())

			// Update pod status separately (status is not created with the resource)
			secondPod.Status = corev1.PodStatus{
				Phase: corev1.PodRunning,
			}
			Expect(k8sClient.Status().Update(ctx, secondPod)).To(Succeed())

			By("Reconciling the updated HSMPool")
			reconciler := &HSMPoolReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that HSMPool aggregates pods from both DaemonSets")
			pool := &hsmv1alpha1.HSMPool{}
			Eventually(func() int32 {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      hsmPoolName,
					Namespace: hsmPoolNamespace,
				}, pool)
				return pool.Status.ExpectedPods
			}).Should(Equal(int32(3))) // 2 from first DaemonSet + 1 from second DaemonSet
		})
	})
})
