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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/config"
)

// DiscoveryDaemonSetReconciler manages discovery DaemonSets for HSMDevice resources
type DiscoveryDaemonSetReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	ImageResolver      *config.ImageResolver
	DiscoveryImage     string
	ServiceAccountName string
}

const (
	trueValue = "true"
)

// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmpools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmpools/status,verbs=get;update;patch

func (r *DiscoveryDaemonSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Get the HSMDevice
	var hsmDevice hsmv1alpha1.HSMDevice
	if err := r.Get(ctx, req.NamespacedName, &hsmDevice); err != nil {
		if errors.IsNotFound(err) {
			// HSMDevice deleted, clean up discovery DaemonSet
			return r.cleanupDiscoveryDaemonSet(ctx, req.NamespacedName)
		}
		logger.Error(err, "Failed to get HSMDevice")
		return ctrl.Result{}, err
	}

	// Ensure HSMPool exists for this HSMDevice
	if err := r.ensureHSMPool(ctx, &hsmDevice); err != nil {
		logger.Error(err, "Failed to ensure HSMPool for HSMDevice")
		return ctrl.Result{}, err
	}

	// Create or update discovery DaemonSet for this HSMDevice
	return r.ensureDiscoveryDaemonSet(ctx, &hsmDevice)
}

// ensureHSMPool creates or updates the HSMPool for an HSMDevice
func (r *DiscoveryDaemonSetReconciler) ensureHSMPool(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
	logger := log.FromContext(ctx)

	poolName := fmt.Sprintf("%s-pool", hsmDevice.Name)

	// Define the desired HSMPool
	desired := &hsmv1alpha1.HSMPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      poolName,
			Namespace: hsmDevice.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "hsm-secrets-operator",
				"app.kubernetes.io/component": "pool",
				"hsm.j5t.io/device":           hsmDevice.Name,
			},
		},
		Spec: hsmv1alpha1.HSMPoolSpec{
			GracePeriod: &metav1.Duration{Duration: 5 * time.Minute}, // Default grace period
		},
	}

	// Set the HSMDevice as the owner of the HSMPool
	if err := controllerutil.SetControllerReference(hsmDevice, desired, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference on HSMPool: %w", err)
	}

	// Check if HSMPool already exists
	existing := &hsmv1alpha1.HSMPool{}
	err := r.Get(ctx, types.NamespacedName{Name: poolName, Namespace: hsmDevice.Namespace}, existing)

	if errors.IsNotFound(err) {
		// Create new HSMPool
		logger.Info("Creating HSMPool for HSMDevice", "device", hsmDevice.Name, "pool", poolName)
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create HSMPool: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get HSMPool: %w", err)
	}

	// Update existing HSMPool if needed
	needsUpdate := false

	// Update grace period if it's nil
	if existing.Spec.GracePeriod == nil {
		existing.Spec.GracePeriod = &metav1.Duration{Duration: 5 * time.Minute}
		needsUpdate = true
	}

	// Update labels if needed
	if existing.Labels == nil {
		existing.Labels = make(map[string]string)
	}
	for k, v := range desired.Labels {
		if existing.Labels[k] != v {
			existing.Labels[k] = v
			needsUpdate = true
		}
	}

	if needsUpdate {
		logger.Info("Updating HSMPool for HSMDevice", "device", hsmDevice.Name, "pool", poolName)
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update HSMPool: %w", err)
		}
	}

	return nil
}

// ensureDiscoveryDaemonSet creates or updates the discovery DaemonSet for an HSMDevice
func (r *DiscoveryDaemonSetReconciler) ensureDiscoveryDaemonSet(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	daemonSetName := fmt.Sprintf("%s-discovery", hsmDevice.Name)

	// Get discovery image from environment, manager image, or use default
	var discoveryImage string
	if r.DiscoveryImage != "" {
		discoveryImage = r.DiscoveryImage
	} else {
		// Fallback to ImageResolver for backward compatibility or auto-detection
		discoveryImage = r.ImageResolver.GetImage(ctx, "")
	}

	// Determine if we're in a test environment (check HSMDevice annotation)
	isTestEnvironment := r.isTestEnvironment(ctx, hsmDevice)

	// Define the desired DaemonSet
	desired := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      daemonSetName,
			Namespace: hsmDevice.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "hsm-secrets-operator",
				"app.kubernetes.io/component": "discovery",
				"hsm.j5t.io/device":           hsmDevice.Name,
			},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name":      "hsm-secrets-operator",
					"app.kubernetes.io/component": "discovery",
					"hsm.j5t.io/device":           hsmDevice.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name":      "hsm-secrets-operator",
						"app.kubernetes.io/component": "discovery",
						"hsm.j5t.io/device":           hsmDevice.Name,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: r.ServiceAccountName,
					Containers: []corev1.Container{
						{
							Name:    "discovery",
							Image:   discoveryImage,
							Command: []string{"/entrypoint.sh", "discovery"},
							Env: []corev1.EnvVar{
								{
									Name: "NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "spec.nodeName",
										},
									},
								},
								{
									Name: "POD_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
								{
									Name: "POD_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.name",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "dev",
									MountPath: "/dev",
									ReadOnly:  true,
								},
								{
									Name:      "sys",
									MountPath: "/sys",
									ReadOnly:  true,
								},
								{
									Name:      "run-udev",
									MountPath: "/run/udev",
									ReadOnly:  true,
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
							SecurityContext: r.getSecurityContext(isTestEnvironment),
						},
					},
					Volumes: r.getVolumes(isTestEnvironment),
					// Apply node selector from HSMDevice spec if specified
					NodeSelector: hsmDevice.Spec.NodeSelector,
					// Apply tolerations if needed for HSM nodes
					Tolerations: []corev1.Toleration{
						{
							Key:      "hsm-node",
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{true}[0],
						RunAsUser:    &[]int64{65534}[0], // nobody user
						RunAsGroup:   &[]int64{65534}[0], // nobody group
						FSGroup:      &[]int64{65534}[0],
					},
				},
			},
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
				Type: appsv1.RollingUpdateDaemonSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDaemonSet{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "50%"},
				},
			},
		},
	}

	// Set the HSMDevice as the owner of the DaemonSet
	if err := controllerutil.SetControllerReference(hsmDevice, desired, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set controller reference: %w", err)
	}

	// Check if DaemonSet already exists
	existing := &appsv1.DaemonSet{}
	err := r.Get(ctx, types.NamespacedName{Name: daemonSetName, Namespace: hsmDevice.Namespace}, existing)

	if errors.IsNotFound(err) {
		// Create new DaemonSet
		logger.Info("Creating discovery DaemonSet", "device", hsmDevice.Name, "daemonset", daemonSetName)
		if err := r.Create(ctx, desired); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create discovery DaemonSet: %w", err)
		}
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get discovery DaemonSet: %w", err)
	}

	// Check if DaemonSet needs updating using Kubernetes-aware equality
	specChanged := !equality.Semantic.DeepEqual(existing.Spec, desired.Spec)
	labelsChanged := !equality.Semantic.DeepEqual(existing.Labels, desired.Labels)

	if !specChanged && !labelsChanged {
		logger.V(1).Info("Discovery DaemonSet spec unchanged, skipping update",
			"device", hsmDevice.Name,
			"daemonset", daemonSetName)
		return ctrl.Result{}, nil
	}

	// Update existing DaemonSet
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels

	logger.Info("Updating discovery DaemonSet",
		"device", hsmDevice.Name,
		"daemonset", daemonSetName,
		"specChanged", specChanged,
		"labelsChanged", labelsChanged)

	if err := r.Update(ctx, existing); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update discovery DaemonSet: %w", err)
	}

	return ctrl.Result{}, nil
}

// cleanupDiscoveryDaemonSet removes the discovery DaemonSet when HSMDevice is deleted
func (r *DiscoveryDaemonSetReconciler) cleanupDiscoveryDaemonSet(ctx context.Context, deviceKey types.NamespacedName) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	daemonSetName := fmt.Sprintf("%s-discovery", deviceKey.Name)
	daemonSet := &appsv1.DaemonSet{}

	err := r.Get(ctx, types.NamespacedName{Name: daemonSetName, Namespace: deviceKey.Namespace}, daemonSet)
	if errors.IsNotFound(err) {
		// Already deleted
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get discovery DaemonSet for cleanup: %w", err)
	}

	logger.Info("Cleaning up discovery DaemonSet", "device", deviceKey.Name, "daemonset", daemonSetName)
	if err := r.Delete(ctx, daemonSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to delete discovery DaemonSet: %w", err)
	}

	return ctrl.Result{}, nil
}

// findDevicesForDaemonSet maps discovery DaemonSets back to HSMDevices for reconciliation
func (r *DiscoveryDaemonSetReconciler) findDevicesForDaemonSet(ctx context.Context, obj client.Object) []reconcile.Request {
	daemonSet, ok := obj.(*appsv1.DaemonSet)
	if !ok {
		return nil
	}

	// Check if this is a discovery DaemonSet
	deviceName, exists := daemonSet.Labels["hsm.j5t.io/device"]
	if !exists {
		return nil
	}

	// Check if this has the discovery component label
	component, exists := daemonSet.Labels["app.kubernetes.io/component"]
	if !exists || component != "discovery" {
		return nil
	}

	// Return reconcile request for the corresponding HSMDevice
	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      deviceName,
				Namespace: daemonSet.Namespace,
			},
		},
	}
}

// findDevicesForHSMPool maps HSMPools back to HSMDevices for reconciliation
func (r *DiscoveryDaemonSetReconciler) findDevicesForHSMPool(ctx context.Context, obj client.Object) []reconcile.Request {
	hsmPool, ok := obj.(*hsmv1alpha1.HSMPool)
	if !ok {
		return nil
	}

	// Check if this is a pool managed by this controller
	deviceName, exists := hsmPool.Labels["hsm.j5t.io/device"]
	if !exists {
		return nil
	}

	// Check if this has the pool component label
	component, exists := hsmPool.Labels["app.kubernetes.io/component"]
	if !exists || component != "pool" {
		return nil
	}

	// Return reconcile request for the corresponding HSMDevice
	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      deviceName,
				Namespace: hsmPool.Namespace,
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *DiscoveryDaemonSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hsmv1alpha1.HSMDevice{}).
		Watches(
			&appsv1.DaemonSet{},
			handler.EnqueueRequestsFromMapFunc(r.findDevicesForDaemonSet),
		).
		Watches(
			&hsmv1alpha1.HSMPool{},
			handler.EnqueueRequestsFromMapFunc(r.findDevicesForHSMPool),
		).
		Named("discovery-daemonset").
		Complete(r)
}

// isTestEnvironment determines if we're running in a test environment
// by checking the HSMDevice annotation
func (r *DiscoveryDaemonSetReconciler) isTestEnvironment(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) bool {
	logger := log.FromContext(ctx)

	// Check for test mode annotation on HSMDevice
	if hsmDevice.Annotations != nil {
		if testMode := hsmDevice.Annotations["hsm.j5t.io/test-mode"]; testMode == trueValue {
			logger.V(1).Info("Detected test environment via HSMDevice annotation", "device", hsmDevice.Name)
			return true
		}
	}
	return false
}

// getVolumes returns the appropriate volumes based on environment
func (r *DiscoveryDaemonSetReconciler) getVolumes(isTestEnvironment bool) []corev1.Volume {
	volumes := []corev1.Volume{}

	if isTestEnvironment {
		// In test environment, use emptyDir volumes for testing
		volumes = append(volumes,
			corev1.Volume{
				Name: "dev",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			corev1.Volume{
				Name: "sys",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			corev1.Volume{
				Name: "run-udev",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		)
	} else {
		// In production, add hostPath volumes for device discovery
		volumes = append(volumes,
			corev1.Volume{
				Name: "dev",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/dev",
						Type: &[]corev1.HostPathType{corev1.HostPathDirectory}[0],
					},
				},
			},
			corev1.Volume{
				Name: "sys",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/sys",
						Type: &[]corev1.HostPathType{corev1.HostPathDirectory}[0],
					},
				},
			},
			corev1.Volume{
				Name: "run-udev",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/run/udev",
						Type: &[]corev1.HostPathType{corev1.HostPathDirectory}[0],
					},
				},
			},
		)
	}

	return volumes
}

// getSecurityContext returns the appropriate security context based on environment
func (r *DiscoveryDaemonSetReconciler) getSecurityContext(isTestEnvironment bool) *corev1.SecurityContext {
	securityContext := &corev1.SecurityContext{
		RunAsNonRoot:             &[]bool{true}[0],
		AllowPrivilegeEscalation: &[]bool{false}[0],
		ReadOnlyRootFilesystem:   &[]bool{false}[0], // Need write access for termination log
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}

	// Add seccomp profile for test environments to pass restricted pod security policy
	if isTestEnvironment {
		securityContext.SeccompProfile = &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		}
	}

	return securityContext
}
