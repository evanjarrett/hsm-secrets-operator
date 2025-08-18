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
	"os"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/discovery"
)

const (
	// DefaultDiscoveryInterval is the default interval for device discovery when devices are found
	DefaultDiscoveryInterval = 5 * time.Minute
	// RetryDiscoveryInterval is the interval when no devices are found (slower to avoid spam)
	RetryDiscoveryInterval = 30 * time.Second
)

// HSMDeviceReconciler reconciles a HSMDevice object
type HSMDeviceReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	NodeName         string
	USBDiscoverer    *discovery.USBDiscoverer
	MirroringManager *discovery.MirroringManager
	DeviceManager    *discovery.HSMDeviceManager
	pluginStarted    bool
}

// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmdevices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmdevices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hsm.j5t.io,resources=hsmdevices/finalizers,verbs=update

// Reconcile handles HSMDevice reconciliation - discovers USB HSM devices on nodes
func (r *HSMDeviceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the HSMDevice instance
	var hsmDevice hsmv1alpha1.HSMDevice
	if err := r.Get(ctx, req.NamespacedName, &hsmDevice); err != nil {
		logger.Error(err, "Unable to fetch HSMDevice")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if this device should be discovered on this node
	if !r.shouldDiscoverOnNode(&hsmDevice) {
		logger.V(1).Info("Device discovery not required on this node")
		return ctrl.Result{RequeueAfter: DefaultDiscoveryInterval}, nil
	}

	// Set initial phase if not set
	if hsmDevice.Status.Phase == "" {
		hsmDevice.Status.Phase = hsmv1alpha1.HSMDevicePhasePending
		if err := r.Status().Update(ctx, &hsmDevice); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Start device discovery
	return r.reconcileDeviceDiscovery(ctx, &hsmDevice)
}

// reconcileDeviceDiscovery performs the actual device discovery
func (r *HSMDeviceReconciler) reconcileDeviceDiscovery(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("deviceType", hsmDevice.Spec.DeviceType)

	// Update phase to discovering
	if hsmDevice.Status.Phase != hsmv1alpha1.HSMDevicePhaseDiscovering {
		hsmDevice.Status.Phase = hsmv1alpha1.HSMDevicePhaseDiscovering
		if err := r.Status().Update(ctx, hsmDevice); err != nil {
			return ctrl.Result{}, err
		}
	}

	var discoveredDevices []hsmv1alpha1.DiscoveredDevice
	var err error

	// Perform discovery based on specification
	if hsmDevice.Spec.USB != nil {
		discoveredDevices, err = r.discoverUSBDevices(ctx, hsmDevice)
	} else if hsmDevice.Spec.DevicePath != nil {
		discoveredDevices, err = r.discoverPathDevices(ctx, hsmDevice)
	} else {
		// Auto-discovery based on device type
		discoveredDevices, err = r.autoDiscoverDevices(ctx, hsmDevice)
	}

	if err != nil {
		logger.Error(err, "Device discovery failed")
		return r.updateStatus(ctx, hsmDevice, hsmv1alpha1.HSMDevicePhaseError,
			discoveredDevices, err.Error())
	}

	logger.Info("Device discovery completed", "foundDevices", len(discoveredDevices))

	// Update status with discovered devices
	phase := hsmv1alpha1.HSMDevicePhaseReady
	if len(discoveredDevices) == 0 {
		phase = hsmv1alpha1.HSMDevicePhasePending
	}

	result, err := r.updateStatus(ctx, hsmDevice, phase, discoveredDevices, "")
	if err != nil {
		return result, err
	}

	// Handle device mirroring if configured
	if r.MirroringManager != nil && hsmDevice.Spec.Mirroring != nil &&
		hsmDevice.Spec.Mirroring.Policy != hsmv1alpha1.MirroringPolicyNone {

		logger.Info("Starting device mirroring", "policy", hsmDevice.Spec.Mirroring.Policy)

		if err := r.MirroringManager.SyncDevices(ctx, hsmDevice); err != nil {
			logger.Error(err, "Device mirroring failed")
			// Don't fail the reconciliation, just log the error
			// The mirroring will be retried on the next reconcile cycle
		}
	}

	return result, err
}

// discoverUSBDevices discovers devices using USB specifications
func (r *HSMDeviceReconciler) discoverUSBDevices(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) ([]hsmv1alpha1.DiscoveredDevice, error) {
	logger := log.FromContext(ctx)

	if r.USBDiscoverer == nil {
		return nil, fmt.Errorf("USB discoverer not available")
	}

	usbDevices, err := r.USBDiscoverer.DiscoverDevices(ctx, hsmDevice.Spec.USB)
	if err != nil {
		return nil, fmt.Errorf("USB discovery failed: %w", err)
	}

	devices := make([]hsmv1alpha1.DiscoveredDevice, 0)
	for _, usbDev := range usbDevices {
		device := hsmv1alpha1.DiscoveredDevice{
			DevicePath:   usbDev.DevicePath,
			SerialNumber: usbDev.SerialNumber,
			NodeName:     r.NodeName,
			LastSeen:     metav1.Now(),
			Available:    true,
			DeviceInfo: map[string]string{
				"vendor-id":      usbDev.VendorID,
				"product-id":     usbDev.ProductID,
				"manufacturer":   usbDev.Manufacturer,
				"product":        usbDev.Product,
				"discovery-type": "usb",
			},
		}

		// Add additional device info
		for k, v := range usbDev.DeviceInfo {
			device.DeviceInfo[k] = v
		}

		devices = append(devices, device)
	}

	logger.V(1).Info("USB device discovery completed", "devicesFound", len(devices))
	return devices, nil
}

// discoverPathDevices discovers devices using path-based specifications
func (r *HSMDeviceReconciler) discoverPathDevices(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) ([]hsmv1alpha1.DiscoveredDevice, error) {
	logger := log.FromContext(ctx)

	if r.USBDiscoverer == nil {
		return nil, fmt.Errorf("USB discoverer not available")
	}

	usbDevices, err := r.USBDiscoverer.DiscoverByPath(ctx, hsmDevice.Spec.DevicePath)
	if err != nil {
		return nil, fmt.Errorf("path discovery failed: %w", err)
	}

	devices := make([]hsmv1alpha1.DiscoveredDevice, 0)
	for _, usbDev := range usbDevices {
		device := hsmv1alpha1.DiscoveredDevice{
			DevicePath:   usbDev.DevicePath,
			SerialNumber: usbDev.SerialNumber,
			NodeName:     r.NodeName,
			LastSeen:     metav1.Now(),
			Available:    true,
			DeviceInfo: map[string]string{
				"discovery-type": "path",
				"path-pattern":   hsmDevice.Spec.DevicePath.Path,
			},
		}

		// Add additional device info
		for k, v := range usbDev.DeviceInfo {
			device.DeviceInfo[k] = v
		}

		devices = append(devices, device)
	}

	logger.V(1).Info("Path device discovery completed", "devicesFound", len(devices))
	return devices, nil
}

// autoDiscoverDevices performs auto-discovery based on device type
func (r *HSMDeviceReconciler) autoDiscoverDevices(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) ([]hsmv1alpha1.DiscoveredDevice, error) {
	logger := log.FromContext(ctx)

	// Get well-known USB specs for device type
	wellKnownSpecs := discovery.GetWellKnownHSMSpecs()
	spec, exists := wellKnownSpecs[hsmDevice.Spec.DeviceType]

	if !exists {
		return nil, fmt.Errorf("no well-known specification for device type %s", hsmDevice.Spec.DeviceType)
	}

	logger.V(1).Info("Using well-known USB specification",
		"deviceType", hsmDevice.Spec.DeviceType,
		"vendorId", spec.VendorID,
		"productId", spec.ProductID)

	// Use the well-known spec for discovery
	tempDevice := *hsmDevice
	tempDevice.Spec.USB = spec

	return r.discoverUSBDevices(ctx, &tempDevice)
}

// shouldDiscoverOnNode determines if device discovery should run on this node
func (r *HSMDeviceReconciler) shouldDiscoverOnNode(hsmDevice *hsmv1alpha1.HSMDevice) bool {
	// If no node selector is specified, discover on all nodes
	if len(hsmDevice.Spec.NodeSelector) == 0 {
		return true
	}

	// Check if this node matches the node selector
	// This is a simplified check - in production, you'd want to fetch
	// the actual node labels and compare them
	nodeName := r.getNodeName()
	for key, value := range hsmDevice.Spec.NodeSelector {
		if key == "kubernetes.io/hostname" && value == nodeName {
			return true
		}
	}

	return false
}

// getNodeName returns the current node name
func (r *HSMDeviceReconciler) getNodeName() string {
	if r.NodeName != "" {
		return r.NodeName
	}

	// Try to get from environment
	if nodeName := os.Getenv("NODE_NAME"); nodeName != "" {
		r.NodeName = nodeName
		return nodeName
	}

	// Fallback to hostname
	if hostname, err := os.Hostname(); err == nil {
		r.NodeName = hostname
		return hostname
	}

	return "unknown"
}

// updateStatus updates the HSMDevice status
func (r *HSMDeviceReconciler) updateStatus(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, phase hsmv1alpha1.HSMDevicePhase, devices []hsmv1alpha1.DiscoveredDevice, errorMsg string) (ctrl.Result, error) {
	now := metav1.Now()

	// Check if status actually needs updating to avoid reconciliation loops
	needsUpdate := false

	logger := log.FromContext(ctx)
	logger.V(2).Info("Checking if status update needed",
		"currentPhase", hsmDevice.Status.Phase, "newPhase", phase,
		"currentDevices", hsmDevice.Status.TotalDevices, "newDevices", len(devices))

	// Check if phase changed
	if hsmDevice.Status.Phase != phase {
		needsUpdate = true
		hsmDevice.Status.Phase = phase
	}

	// Check if device count changed
	newDeviceCount := int32(len(devices))
	if hsmDevice.Status.TotalDevices != newDeviceCount {
		needsUpdate = true
		hsmDevice.Status.TotalDevices = newDeviceCount
		hsmDevice.Status.DiscoveredDevices = devices
	}

	// Only update LastDiscoveryTime if there are significant changes or it's been a while
	shouldUpdateTime := false

	// Update time if this is first time (nil), there are other changes, or it's been 5+ minutes
	if hsmDevice.Status.LastDiscoveryTime == nil {
		shouldUpdateTime = true
	} else if needsUpdate {
		// Only update time if there are other significant changes
		shouldUpdateTime = true
	} else {
		// Update time only if it's been more than 5 minutes since last update
		timeSinceLastUpdate := now.Sub(hsmDevice.Status.LastDiscoveryTime.Time)
		shouldUpdateTime = timeSinceLastUpdate > (5 * time.Minute)
	}

	if shouldUpdateTime {
		needsUpdate = true
		hsmDevice.Status.LastDiscoveryTime = &now
	}

	// Count available devices
	availableCount := int32(0)
	for _, device := range devices {
		if device.Available {
			availableCount++
		}
	}

	// Check if available device count changed
	if hsmDevice.Status.AvailableDevices != availableCount {
		needsUpdate = true
		hsmDevice.Status.AvailableDevices = availableCount
	}

	// Update conditions only if needed
	conditionType := "DeviceDiscovery"
	conditionStatus := metav1.ConditionTrue
	reason := string(phase)
	message := fmt.Sprintf("Discovered %d devices", len(devices))

	if phase == hsmv1alpha1.HSMDevicePhaseError {
		conditionStatus = metav1.ConditionFalse
		message = errorMsg
	}

	// Check if condition needs updating
	shouldUpdateCondition := false
	existingConditionIndex := -1
	for i, cond := range hsmDevice.Status.Conditions {
		if cond.Type == conditionType {
			existingConditionIndex = i
			if cond.Status != conditionStatus || cond.Reason != reason || cond.Message != message {
				shouldUpdateCondition = true
			}
			break
		}
	}

	// Add condition if it doesn't exist
	if existingConditionIndex == -1 {
		shouldUpdateCondition = true
	}

	if shouldUpdateCondition {
		needsUpdate = true

		if existingConditionIndex >= 0 {
			// Update existing condition but preserve LastTransitionTime if status hasn't changed
			existingCondition := hsmDevice.Status.Conditions[existingConditionIndex]
			lastTransitionTime := existingCondition.LastTransitionTime

			// Only update LastTransitionTime if the status actually changed
			if existingCondition.Status != conditionStatus {
				lastTransitionTime = now
			}

			hsmDevice.Status.Conditions[existingConditionIndex] = metav1.Condition{
				Type:               conditionType,
				Status:             conditionStatus,
				LastTransitionTime: lastTransitionTime,
				Reason:             reason,
				Message:            message,
			}
		} else {
			// New condition
			condition := metav1.Condition{
				Type:               conditionType,
				Status:             conditionStatus,
				LastTransitionTime: now,
				Reason:             reason,
				Message:            message,
			}
			hsmDevice.Status.Conditions = append(hsmDevice.Status.Conditions, condition)
		}
	}

	// Only update status if there are actual changes
	if needsUpdate {
		logger.V(1).Info("Updating HSMDevice status", "reason", "status changed")
		if err := r.Status().Update(ctx, hsmDevice); err != nil {
			if apierrors.IsConflict(err) {
				logger.V(1).Info("Status update conflict, will retry", "error", err)
				// Use the same requeue logic as the main reconciliation
				requeueInterval := DefaultDiscoveryInterval
				if phase == hsmv1alpha1.HSMDevicePhasePending || phase == hsmv1alpha1.HSMDevicePhaseError {
					requeueInterval = RetryDiscoveryInterval
				}
				return ctrl.Result{RequeueAfter: requeueInterval}, nil
			}
			return ctrl.Result{}, err
		}
	} else {
		logger.V(2).Info("Skipping status update", "reason", "no changes detected")
	}

	// Update device manager with discovered devices for Kubernetes resource management
	if r.DeviceManager != nil {
		logger.V(1).Info("Updating device manager with discovered devices", "deviceCount", len(devices))
		r.DeviceManager.UpdateDevices(devices)

		// Start device plugin if we have devices and it's not already running
		if len(devices) > 0 {
			// Try to start the device plugin (idempotent - won't start if already running)
			if err := r.startDevicePluginIfNeeded(); err != nil {
				logger.Error(err, "Failed to start device plugin")
			}
		}

		// Log device resource information
		resourceName := r.DeviceManager.GetResourceName()
		availableDevices := r.DeviceManager.GetAvailableDevices()
		logger.Info("Device resources updated",
			"resourceName", resourceName,
			"availableDevices", len(availableDevices))
	}

	// Requeue based on discovery result
	requeueInterval := DefaultDiscoveryInterval
	if phase == hsmv1alpha1.HSMDevicePhasePending || phase == hsmv1alpha1.HSMDevicePhaseError {
		// Use shorter retry interval when no devices found or error occurred
		requeueInterval = RetryDiscoveryInterval
	}

	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// startDevicePluginIfNeeded starts the device plugin if not already running
func (r *HSMDeviceReconciler) startDevicePluginIfNeeded() error {
	if r.pluginStarted {
		return nil // Already running
	}

	if r.DeviceManager == nil {
		return fmt.Errorf("device manager not initialized")
	}

	if err := r.DeviceManager.Start(); err != nil {
		return fmt.Errorf("failed to start device plugin: %w", err)
	}

	r.pluginStarted = true
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HSMDeviceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hsmv1alpha1.HSMDevice{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("hsmdevice").
		Complete(r)
}
