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
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
)

const (
	// deviceReportAnnotation is the annotation key used by discovery pods
	deviceReportAnnotation = "hsm.j5t.io/device-report"
	// DefaultGracePeriod is the default grace period for considering pod reports stale
	DefaultGracePeriod = 5 * time.Minute
	// DefaultAggregationInterval is the default interval for checking pod annotations
	DefaultAggregationInterval = 30 * time.Second
)

// PodDiscoveryReport represents the structure of discovery data in pod annotations
type PodDiscoveryReport struct {
	HSMDeviceName     string                         `json:"hsmDeviceName"`
	ReportingNode     string                         `json:"reportingNode"`
	DiscoveredDevices []hsmv1alpha1.DiscoveredDevice `json:"discoveredDevices"`
	LastReportTime    metav1.Time                    `json:"lastReportTime"`
	DiscoveryStatus   string                         `json:"discoveryStatus"` // "discovering", "completed", "error"
	Error             string                         `json:"error,omitempty"`
}

// HSMPoolReconciler reconciles a HSMPool object
type HSMPoolReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmpools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmpools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmpools/finalizers,verbs=update
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmdevices,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch

// Reconcile handles HSMPool reconciliation - aggregates device discovery from pod annotations
func (r *HSMPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the HSMPool instance
	var hsmPool hsmv1alpha1.HSMPool
	if err := r.Get(ctx, req.NamespacedName, &hsmPool); err != nil {
		logger.Error(err, "Unable to fetch HSMPool")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Validate that the referenced HSMDevice exists (from ownerReferences)
	if len(hsmPool.OwnerReferences) == 0 {
		return r.updatePoolStatus(ctx, &hsmPool, hsmv1alpha1.HSMPoolPhaseError, nil, nil, 0, "HSMPool has no owner references")
	}

	deviceRef := hsmPool.OwnerReferences[0].Name
	hsmDevices := make([]*hsmv1alpha1.HSMDevice, 0, 1)
	hsmDevice := &hsmv1alpha1.HSMDevice{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      deviceRef,
		Namespace: hsmPool.Namespace,
	}, hsmDevice); err != nil {
		logger.Error(err, "Unable to fetch referenced HSMDevice", "hsmDevice", deviceRef)
		return r.updatePoolStatus(ctx, &hsmPool, hsmv1alpha1.HSMPoolPhaseError, nil, nil, 0, fmt.Sprintf("HSMDevice %s not found", deviceRef))
	}
	hsmDevices = append(hsmDevices, hsmDevice)

	// Find discovery pods and their annotations
	podReports, aggregatedDevices, expectedPods, err := r.collectPodReports(ctx, hsmDevices)
	if err != nil {
		logger.Error(err, "Failed to collect pod reports")
		return r.updatePoolStatus(ctx, &hsmPool, hsmv1alpha1.HSMPoolPhaseError, nil, nil, expectedPods, err.Error())
	}

	// Aggregate devices from all pod reports
	phase := r.aggregateDevices(podReports, expectedPods)

	return r.updatePoolStatus(ctx, &hsmPool, phase, aggregatedDevices, podReports, expectedPods, "")
}

// collectPodReports finds discovery DaemonSet pods owned by HSMDevices and queries their status
func (r *HSMPoolReconciler) collectPodReports(ctx context.Context, hsmDevices []*hsmv1alpha1.HSMDevice) ([]hsmv1alpha1.PodReport, []hsmv1alpha1.DiscoveredDevice, int32, error) {
	logger := log.FromContext(ctx)

	podReports := make([]hsmv1alpha1.PodReport, 0)
	var allDevices []hsmv1alpha1.DiscoveredDevice
	totalExpectedPods := int32(0)

	// For each HSMDevice referenced by this pool, find its DaemonSet and pods
	for _, hsmDevice := range hsmDevices {
		daemonSetName := fmt.Sprintf("%s-discovery", hsmDevice.Name)

		// Get the DaemonSet owned by this HSMDevice
		daemonSet := &appsv1.DaemonSet{}
		err := r.Get(ctx, client.ObjectKey{
			Name:      daemonSetName,
			Namespace: hsmDevice.Namespace,
		}, daemonSet)

		if apierrors.IsNotFound(err) {
			logger.Info("Discovery DaemonSet not found", "device", hsmDevice.Name, "daemonset", daemonSetName)
			continue
		} else if err != nil {
			logger.Error(err, "Failed to get discovery DaemonSet", "device", hsmDevice.Name, "daemonset", daemonSetName)
			continue
		}

		// Add expected pods from this DaemonSet
		totalExpectedPods += daemonSet.Status.DesiredNumberScheduled

		// List pods owned by this DaemonSet
		pods := &corev1.PodList{}
		labelSelector := labels.SelectorFromSet(daemonSet.Spec.Selector.MatchLabels)

		listOpts := &client.ListOptions{
			LabelSelector: labelSelector,
			Namespace:     hsmDevice.Namespace,
		}

		if err := r.List(ctx, pods, listOpts); err != nil {
			return nil, nil, totalExpectedPods, fmt.Errorf("failed to list DaemonSet pods for device %s: %w", hsmDevice.Name, err)
		}

		// Create pod reports from pod annotations
		for _, pod := range pods.Items {
			podReport := hsmv1alpha1.PodReport{
				PodName:         pod.Name,
				NodeName:        pod.Spec.NodeName,
				LastReportTime:  metav1.Now(),
				DiscoveryStatus: r.getPodDiscoveryStatus(&pod),
				Fresh:           r.isPodFresh(&pod),
			}

			podReport.DevicesFound = 0
			// Parse device count from pod annotation if available
			if devicesFound, status, reportTime := r.parseDeviceReportAnnotation(&pod); devicesFound >= 0 {
				podReport.DevicesFound = devicesFound
				if status != "" {
					podReport.DiscoveryStatus = status
				}
				if !reportTime.IsZero() {
					podReport.LastReportTime = reportTime
				}

				// Also collect the actual discovered devices from annotation
				if pod.Annotations != nil {
					if reportJSON, exists := pod.Annotations[deviceReportAnnotation]; exists {
						var discoveryReport PodDiscoveryReport
						if err := json.Unmarshal([]byte(reportJSON), &discoveryReport); err == nil {
							allDevices = append(allDevices, discoveryReport.DiscoveredDevices...)
						}
					}
				}
			}

			podReports = append(podReports, podReport)
		}
	}

	return podReports, allDevices, totalExpectedPods, nil
}

// getPodDiscoveryStatus determines the discovery status based on pod phase and conditions
func (r *HSMPoolReconciler) getPodDiscoveryStatus(pod *corev1.Pod) string {
	switch pod.Status.Phase {
	case corev1.PodRunning:
		return "completed"
	case corev1.PodPending:
		return "pending"
	case corev1.PodFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// isPodFresh checks if the pod is recently updated (simple implementation)
func (r *HSMPoolReconciler) isPodFresh(pod *corev1.Pod) bool {
	// Consider pod fresh if it's been ready for less than grace period
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}

	// For now, consider all running pods as fresh
	// TODO: Could check pod start time or last transition time
	return true
}

// parseDeviceReportAnnotation parses the device discovery report from pod annotation
// Returns (devicesFound, discoveryStatus, lastReportTime) or (-1, "", time.Time{}) if not found/invalid
func (r *HSMPoolReconciler) parseDeviceReportAnnotation(pod *corev1.Pod) (int32, string, metav1.Time) {
	if pod.Annotations == nil {
		return -1, "", metav1.Time{}
	}

	reportJSON, exists := pod.Annotations[deviceReportAnnotation]
	if !exists {
		return -1, "", metav1.Time{}
	}

	var report PodDiscoveryReport
	if err := json.Unmarshal([]byte(reportJSON), &report); err != nil {
		// Log error but don't fail - return fallback values
		return -1, "", metav1.Time{}
	}

	return int32(len(report.DiscoveredDevices)), report.DiscoveryStatus, report.LastReportTime
}

// aggregateDevices determines the pool phase based on pod reports
func (r *HSMPoolReconciler) aggregateDevices(podReports []hsmv1alpha1.PodReport, expectedPods int32) hsmv1alpha1.HSMPoolPhase {
	freshReports := 0
	completedReports := 0

	// Count fresh and completed reports
	for _, report := range podReports {
		if report.Fresh {
			freshReports++
		}
		if report.DiscoveryStatus == "completed" && report.Fresh {
			completedReports++
		}
	}

	// Determine phase based on reporting status
	var phase hsmv1alpha1.HSMPoolPhase

	if len(podReports) == 0 {
		phase = hsmv1alpha1.HSMPoolPhasePending
	} else if int32(completedReports) >= expectedPods {
		// All expected pods have completed reporting
		phase = hsmv1alpha1.HSMPoolPhaseReady
	} else if int32(freshReports) < expectedPods {
		// Some pods are not reporting within grace period
		phase = hsmv1alpha1.HSMPoolPhasePartial
	} else {
		// Still collecting reports
		phase = hsmv1alpha1.HSMPoolPhaseAggregating
	}

	return phase
}

// updatePoolStatus updates the HSMPool status
func (r *HSMPoolReconciler) updatePoolStatus(ctx context.Context, hsmPool *hsmv1alpha1.HSMPool, phase hsmv1alpha1.HSMPoolPhase, devices []hsmv1alpha1.DiscoveredDevice, podReports []hsmv1alpha1.PodReport, expectedPods int32, errorMsg string) (ctrl.Result, error) {
	now := metav1.Now()

	// Update basic status fields
	hsmPool.Status.Phase = phase
	hsmPool.Status.AggregatedDevices = devices
	hsmPool.Status.TotalDevices = int32(len(devices))
	hsmPool.Status.ReportingPods = podReports
	hsmPool.Status.ExpectedPods = expectedPods
	hsmPool.Status.LastAggregationTime = &now

	// Count available devices
	availableCount := int32(0)
	for _, device := range devices {
		if device.Available {
			availableCount++
		}
	}
	hsmPool.Status.AvailableDevices = availableCount

	// Update conditions
	conditionType := "DeviceAggregation"
	conditionStatus := metav1.ConditionTrue
	reason := string(phase)
	message := fmt.Sprintf("Aggregated %d devices from %d pods", len(devices), expectedPods)

	if errorMsg != "" {
		conditionStatus = metav1.ConditionFalse
		message = errorMsg
		reason = "Error"
	}

	// Find or create condition
	found := false
	for i, cond := range hsmPool.Status.Conditions {
		if cond.Type == conditionType {
			lastTransitionTime := cond.LastTransitionTime
			if cond.Status != conditionStatus {
				lastTransitionTime = now
			}

			hsmPool.Status.Conditions[i] = metav1.Condition{
				Type:               conditionType,
				Status:             conditionStatus,
				LastTransitionTime: lastTransitionTime,
				Reason:             reason,
				Message:            message,
			}
			found = true
			break
		}
	}

	if !found {
		hsmPool.Status.Conditions = append(hsmPool.Status.Conditions, metav1.Condition{
			Type:               conditionType,
			Status:             conditionStatus,
			LastTransitionTime: now,
			Reason:             reason,
			Message:            message,
		})
	}

	// Update status
	if err := r.Status().Update(ctx, hsmPool); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: DefaultAggregationInterval}, nil
		}
		return ctrl.Result{}, err
	}

	// Requeue based on phase
	requeueInterval := DefaultAggregationInterval
	if phase == hsmv1alpha1.HSMPoolPhaseReady {
		requeueInterval = time.Minute // Less frequent when ready
	}

	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *HSMPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hsmv1alpha1.HSMPool{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Watch for pod annotation changes
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.findPoolsForPod),
			builder.WithPredicates(predicate.AnnotationChangedPredicate{}),
		).
		Named("hsmpool").
		Complete(r)
}

// findPoolsForPod finds HSMPools that should be updated when a pod's annotations change
func (r *HSMPoolReconciler) findPoolsForPod(ctx context.Context, obj client.Object) []ctrl.Request {
	pod := obj.(*corev1.Pod)

	// Only watch discovery pods
	if pod.Labels == nil {
		return nil
	}

	if pod.Labels["app.kubernetes.io/component"] != "discovery" {
		return nil
	}

	// Check if pod has device reports
	if pod.Annotations == nil || pod.Annotations[deviceReportAnnotation] == "" {
		return nil
	}

	// Parse the report to find which HSMDevice it's for
	var discoveryReport PodDiscoveryReport
	if err := json.Unmarshal([]byte(pod.Annotations[deviceReportAnnotation]), &discoveryReport); err != nil {
		return nil
	}

	// Find HSMPools that reference this HSMDevice
	pools := &hsmv1alpha1.HSMPoolList{}
	if err := r.List(ctx, pools, &client.ListOptions{Namespace: pod.Namespace}); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, pool := range pools.Items {
		// Check if this pool references the HSMDevice in the report (from ownerReferences)
		if len(pool.OwnerReferences) > 0 && pool.OwnerReferences[0].Name == discoveryReport.HSMDeviceName {
			requests = append(requests, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Name:      pool.Name,
					Namespace: pool.Namespace,
				},
			})
		}
	}

	return requests
}
