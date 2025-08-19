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
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
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

	// Validate that all referenced HSMDevices exist
	hsmDevices := make([]*hsmv1alpha1.HSMDevice, 0, len(hsmPool.Spec.HSMDeviceRefs))
	for _, deviceRef := range hsmPool.Spec.HSMDeviceRefs {
		hsmDevice := &hsmv1alpha1.HSMDevice{}
		if err := r.Get(ctx, client.ObjectKey{
			Name:      deviceRef,
			Namespace: hsmPool.Namespace,
		}, hsmDevice); err != nil {
			logger.Error(err, "Unable to fetch referenced HSMDevice", "hsmDevice", deviceRef)
			return r.updatePoolStatus(ctx, &hsmPool, hsmv1alpha1.HSMPoolPhaseError, nil, 0, fmt.Sprintf("HSMDevice %s not found", deviceRef))
		}
		hsmDevices = append(hsmDevices, hsmDevice)
	}

	// Find discovery pods and their annotations
	podReports, expectedPods, err := r.collectPodReports(ctx, &hsmPool, hsmDevices)
	if err != nil {
		logger.Error(err, "Failed to collect pod reports")
		return r.updatePoolStatus(ctx, &hsmPool, hsmv1alpha1.HSMPoolPhaseError, nil, expectedPods, err.Error())
	}

	// Aggregate devices from all pod reports
	phase := r.aggregateDevices(podReports, expectedPods)

	// Update pool status (TODO: implement device aggregation when needed)
	var aggregatedDevices []hsmv1alpha1.DiscoveredDevice
	return r.updatePoolStatus(ctx, &hsmPool, phase, aggregatedDevices, expectedPods, "")
}

// collectPodReports finds discovery pods and extracts their device reports from annotations
func (r *HSMPoolReconciler) collectPodReports(ctx context.Context, hsmPool *hsmv1alpha1.HSMPool, hsmDevices []*hsmv1alpha1.HSMDevice) ([]hsmv1alpha1.PodReport, int32, error) {
	logger := log.FromContext(ctx)

	// Find DaemonSet to determine expected pod count
	expectedPods, err := r.getExpectedPodCount(ctx, hsmPool.Namespace)
	if err != nil {
		logger.Error(err, "Failed to determine expected pod count")
		expectedPods = 1 // Fallback to single pod
	}

	// Find discovery pods
	pods := &corev1.PodList{}
	labelSelector := labels.SelectorFromSet(map[string]string{
		"app.kubernetes.io/name":      "hsm-secrets-operator",
		"app.kubernetes.io/component": "discovery",
	})

	listOpts := &client.ListOptions{
		LabelSelector: labelSelector,
		Namespace:     hsmPool.Namespace,
	}

	if err := r.List(ctx, pods, listOpts); err != nil {
		return nil, expectedPods, fmt.Errorf("failed to list discovery pods: %w", err)
	}

	// Extract reports from pod annotations
	podReports := make([]hsmv1alpha1.PodReport, 0, len(pods.Items))
	gracePeriod := r.getGracePeriod(hsmPool)

	for _, pod := range pods.Items {
		if pod.Annotations == nil {
			continue
		}

		reportData, exists := pod.Annotations[deviceReportAnnotation]
		if !exists {
			// Pod hasn't reported yet
			continue
		}

		var discoveryReport PodDiscoveryReport
		if err := json.Unmarshal([]byte(reportData), &discoveryReport); err != nil {
			logger.Error(err, "Failed to parse discovery report from pod", "pod", pod.Name)
			continue
		}

		// Only include reports for HSMDevices referenced by this pool
		validDevice := false
		for _, device := range hsmDevices {
			if discoveryReport.HSMDeviceName == device.Name {
				validDevice = true
				break
			}
		}
		if !validDevice {
			continue
		}

		// Check if report is fresh (within grace period)
		reportAge := time.Since(discoveryReport.LastReportTime.Time)
		fresh := reportAge <= gracePeriod

		podReport := hsmv1alpha1.PodReport{
			PodName:         pod.Name,
			NodeName:        discoveryReport.ReportingNode,
			DevicesFound:    int32(len(discoveryReport.DiscoveredDevices)),
			LastReportTime:  discoveryReport.LastReportTime,
			DiscoveryStatus: discoveryReport.DiscoveryStatus,
			Error:           discoveryReport.Error,
			Fresh:           fresh,
		}

		podReports = append(podReports, podReport)
	}

	logger.V(1).Info("Collected pod reports",
		"totalPods", len(pods.Items),
		"reportingPods", len(podReports),
		"expectedPods", expectedPods)

	return podReports, expectedPods, nil
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

// getExpectedPodCount determines how many pods should be reporting based on DaemonSet
func (r *HSMPoolReconciler) getExpectedPodCount(ctx context.Context, namespace string) (int32, error) {
	daemonSets := &appsv1.DaemonSetList{}
	labelSelector := labels.SelectorFromSet(map[string]string{
		"app.kubernetes.io/name":      "hsm-secrets-operator",
		"app.kubernetes.io/component": "discovery",
	})

	listOpts := &client.ListOptions{
		LabelSelector: labelSelector,
		Namespace:     namespace,
	}

	if err := r.List(ctx, daemonSets, listOpts); err != nil {
		return 1, err
	}

	if len(daemonSets.Items) == 0 {
		return 1, nil // Default to single pod if no DaemonSet found
	}

	return daemonSets.Items[0].Status.DesiredNumberScheduled, nil
}

// getGracePeriod returns the grace period for this pool
func (r *HSMPoolReconciler) getGracePeriod(hsmPool *hsmv1alpha1.HSMPool) time.Duration {
	if hsmPool.Spec.GracePeriod != nil {
		return hsmPool.Spec.GracePeriod.Duration
	}
	return DefaultGracePeriod
}

// updatePoolStatus updates the HSMPool status
func (r *HSMPoolReconciler) updatePoolStatus(ctx context.Context, hsmPool *hsmv1alpha1.HSMPool, phase hsmv1alpha1.HSMPoolPhase, devices []hsmv1alpha1.DiscoveredDevice, expectedPods int32, errorMsg string) (ctrl.Result, error) {
	now := metav1.Now()

	// Update basic status fields
	hsmPool.Status.Phase = phase
	hsmPool.Status.AggregatedDevices = devices
	hsmPool.Status.TotalDevices = int32(len(devices))
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
		// Check if this pool references the HSMDevice in the report
		for _, deviceRef := range pool.Spec.HSMDeviceRefs {
			if deviceRef == discoveryReport.HSMDeviceName {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKey{
						Name:      pool.Name,
						Namespace: pool.Namespace,
					},
				})
				break // Don't add the same pool multiple times
			}
		}
	}

	return requests
}
