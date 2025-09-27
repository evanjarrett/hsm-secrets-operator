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

package discovery

import (
	"maps"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	discoveryconfig "github.com/evanjarrett/hsm-secrets-operator/internal/config"
	"github.com/evanjarrett/hsm-secrets-operator/internal/discovery"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("discovery")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hsmv1alpha1.AddToScheme(scheme))
}

// PodDiscoveryReport represents the structure of discovery data in pod annotations
type PodDiscoveryReport struct {
	HSMDeviceName     string                         `json:"hsmDeviceName"`
	ReportingNode     string                         `json:"reportingNode"`
	DiscoveredDevices []hsmv1alpha1.DiscoveredDevice `json:"discoveredDevices"`
	LastReportTime    metav1.Time                    `json:"lastReportTime"`
	DiscoveryStatus   string                         `json:"discoveryStatus"` // "discovering", "completed", "error"
	Error             string                         `json:"error,omitempty"`
}

const (
	// DeviceReportAnnotation is the annotation key used by discovery pods
	DeviceReportAnnotation = "hsm.j5t.io/device-report"
)

// Run starts the discovery mode
func Run(args []string) error {
	// Create configuration from environment variables (downward API only)
	discoveryConfig, err := discoveryconfig.NewDiscoveryConfigFromEnv()
	if err != nil {
		return fmt.Errorf("failed to create discovery config: %w", err)
	}

	// Create a new flag set for discovery-specific flags
	fs := flag.NewFlagSet("discovery", flag.ContinueOnError)

	var syncInterval time.Duration
	var detectionMethod string

	fs.DurationVar(&syncInterval, "sync-interval", 30*time.Second, "Interval for device discovery sync")
	fs.StringVar(&detectionMethod, "detection-method", "auto",
		"USB detection method: 'sysfs' (native), 'legacy' (privileged), or 'auto'")

	// Parse discovery-specific flags from the remaining unparsed arguments
	if err := fs.Parse(args); err != nil {
		return err
	}

	setupLog.Info("Starting HSM device discovery agent",
		"node", discoveryConfig.NodeName,
		"pod", discoveryConfig.PodName,
		"namespace", discoveryConfig.PodNamespace,
		"sync-interval", syncInterval,
		"detection-method", detectionMethod)

	// Create Kubernetes client
	config := ctrl.GetConfigOrDie()
	k8sClient, err := client.New(config, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		setupLog.Error(err, "unable to create Kubernetes client")
		return err
	}

	// Initialize USB discoverer
	usbDiscoverer := discovery.NewUSBDiscovererWithMethod(detectionMethod)

	// Start discovery loop
	ctx := ctrl.SetupSignalHandler()
	discoveryAgent := &DiscoveryAgent{
		client:        k8sClient,
		logger:        setupLog,
		nodeName:      discoveryConfig.NodeName,
		podName:       discoveryConfig.PodName,
		podNamespace:  discoveryConfig.PodNamespace,
		usbDiscoverer: usbDiscoverer,
		syncInterval:  syncInterval,
	}

	if err := discoveryAgent.Run(ctx); err != nil {
		setupLog.Error(err, "discovery agent failed")
		return err
	}

	return nil
}

// DiscoveryAgent handles device discovery and pod annotation updates
type DiscoveryAgent struct {
	client        client.Client
	logger        logr.Logger
	nodeName      string
	podName       string
	podNamespace  string
	usbDiscoverer *discovery.USBDiscoverer
	syncInterval  time.Duration

	// Event-driven discovery support
	eventMonitoringActive bool
	deviceCache           map[string][]hsmv1alpha1.DiscoveredDevice // Cache current device state per HSMDevice
	deviceLookup          map[string]map[string]int                 // Fast lookup: HSMDevice -> (serial/path -> index)
}

// Run starts the discovery loop with event-driven monitoring
func (d *DiscoveryAgent) Run(ctx context.Context) error {
	// Initialize device cache
	d.deviceCache = make(map[string][]hsmv1alpha1.DiscoveredDevice)
	d.deviceLookup = make(map[string]map[string]int)

	// Step 1: Mandatory initial discovery to detect already-connected devices
	d.logger.Info("Performing initial device discovery scan")
	if err := d.performDiscovery(ctx); err != nil {
		d.logger.Error(err, "Initial discovery failed - continuing with event monitoring")
	} else {
		d.logger.Info("Initial device discovery completed successfully")
	}

	// Step 2: Start event monitoring for real-time changes
	if err := d.startEventMonitoring(ctx); err != nil {
		d.logger.Error(err, "Failed to start USB event monitoring - falling back to polling only")
		d.eventMonitoringActive = false
	} else {
		d.logger.Info("USB event monitoring started successfully")
		d.eventMonitoringActive = true
	}

	// Step 3: Set up periodic reconciliation (reduced frequency - acts as safety net)
	reconcileTicker := time.NewTicker(d.syncInterval)
	defer reconcileTicker.Stop()

	// Step 4: Main event loop
	eventChan := d.usbDiscoverer.GetEventChannel()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Discovery agent shutting down")
			d.usbDiscoverer.StopEventMonitoring()
			return nil

		case event, ok := <-eventChan:
			if !ok {
				d.logger.Info("USB event channel closed, restarting event monitoring")
				if err := d.restartEventMonitoring(ctx); err != nil {
					d.logger.Error(err, "Failed to restart event monitoring")
					d.eventMonitoringActive = false
				}
				continue
			}
			d.handleUSBEvent(ctx, event)

		case <-reconcileTicker.C:
			// Periodic reconciliation - safety net and health check
			d.logger.V(1).Info("Performing periodic reconciliation")
			if err := d.reconcileDevices(ctx); err != nil {
				d.logger.Error(err, "Reconciliation failed")
			}
		}
	}
}

// performDiscovery discovers devices for all HSMDevice specs and updates pod annotations
func (d *DiscoveryAgent) performDiscovery(ctx context.Context) error {
	d.logger.V(1).Info("Starting discovery iteration")

	// List all HSMDevice resources in the cluster
	var hsmDeviceList hsmv1alpha1.HSMDeviceList
	if err := d.client.List(ctx, &hsmDeviceList); err != nil {
		return fmt.Errorf("failed to list HSMDevice resources: %w", err)
	}

	// Process each HSMDevice
	for _, hsmDevice := range hsmDeviceList.Items {
		if err := d.processHSMDevice(ctx, &hsmDevice); err != nil {
			d.logger.Error(err, "Failed to process HSMDevice", "device", hsmDevice.Name)
		}

		// Register USB specs for event monitoring
		d.registerSpecForMonitoring(&hsmDevice)
	}

	return nil
}

// processHSMDevice performs discovery for a single HSMDevice and updates annotations
func (d *DiscoveryAgent) processHSMDevice(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
	// Check if this device should be discovered on this node
	if !d.shouldDiscoverOnNode(hsmDevice) {
		d.logger.V(2).Info("Skipping device - not targeted for this node", "device", hsmDevice.Name)
		return nil
	}

	d.logger.V(1).Info("Discovering devices", "hsmDevice", hsmDevice.Name, "node", d.nodeName)

	// Perform local discovery
	discoveredDevices, err := d.discoverDevicesForSpec(ctx, hsmDevice)
	if err != nil {
		d.logger.Error(err, "Discovery failed for device", "device", hsmDevice.Name, "node", d.nodeName)
		return err
	}

	d.logger.Info("Discovery completed",
		"device", hsmDevice.Name,
		"node", d.nodeName,
		"devicesFound", len(discoveredDevices))

	// Update device cache
	d.updateDeviceCache(hsmDevice.Name, discoveredDevices)

	// Update pod annotation with discovery results
	if err := d.updatePodAnnotation(ctx, hsmDevice.Name, discoveredDevices); err != nil {
		d.logger.Error(err, "Failed to update pod annotation", "device", hsmDevice.Name)
		return err
	}

	return nil
}

// discoverDevicesForSpec performs actual device discovery based on HSMDevice spec
func (d *DiscoveryAgent) discoverDevicesForSpec(
	ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice,
) ([]hsmv1alpha1.DiscoveredDevice, error) {
	var devices []hsmv1alpha1.DiscoveredDevice
	var err error

	// Perform discovery based on specification
	if hsmDevice.Spec.Discovery != nil && hsmDevice.Spec.Discovery.USB != nil {
		devices, err = d.discoverUSBDevices(ctx, hsmDevice)
	} else if hsmDevice.Spec.Discovery != nil && hsmDevice.Spec.Discovery.AutoDiscovery {
		devices, err = d.autoDiscoverDevices(ctx, hsmDevice)
	} else {
		// Default to auto-discovery
		devices, err = d.autoDiscoverDevices(ctx, hsmDevice)
	}

	if err != nil {
		return nil, err
	}

	// Apply maxDevices limit
	if hsmDevice.Spec.MaxDevices > 0 && int32(len(devices)) > hsmDevice.Spec.MaxDevices {
		originalCount := len(devices)
		devices = devices[:hsmDevice.Spec.MaxDevices]
		d.logger.Info("Limited discovered devices due to maxDevices setting",
			"device", hsmDevice.Name,
			"maxDevices", hsmDevice.Spec.MaxDevices,
			"originalCount", originalCount,
			"limitedCount", len(devices))
	}

	return devices, nil
}

// discoverUSBDevices discovers devices using USB specifications
func (d *DiscoveryAgent) discoverUSBDevices(
	ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice,
) ([]hsmv1alpha1.DiscoveredDevice, error) {
	usbDevices, err := d.usbDiscoverer.DiscoverDevices(ctx, hsmDevice.Spec.Discovery.USB)
	if err != nil {
		return nil, fmt.Errorf("USB discovery failed: %w", err)
	}

	devices := make([]hsmv1alpha1.DiscoveredDevice, 0, len(usbDevices))
	for _, usbDev := range usbDevices {
		device := hsmv1alpha1.DiscoveredDevice{
			DevicePath:   usbDev.DevicePath,
			SerialNumber: usbDev.SerialNumber,
			NodeName:     d.nodeName,
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
		maps.Copy(device.DeviceInfo, usbDev.DeviceInfo)

		devices = append(devices, device)
	}

	d.logger.V(1).Info("USB device discovery completed", "devicesFound", len(devices))
	return devices, nil
}

// autoDiscoverDevices performs auto-discovery based on device type
func (d *DiscoveryAgent) autoDiscoverDevices(
	ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice,
) ([]hsmv1alpha1.DiscoveredDevice, error) {
	// Get well-known USB specs for device type
	wellKnownSpecs := discovery.GetWellKnownHSMSpecs()
	spec, exists := wellKnownSpecs[hsmDevice.Spec.DeviceType]

	if !exists {
		return nil, fmt.Errorf("no well-known specification for device type %s", hsmDevice.Spec.DeviceType)
	}

	d.logger.V(1).Info("Using well-known USB specification",
		"deviceType", hsmDevice.Spec.DeviceType,
		"vendorId", spec.VendorID,
		"productId", spec.ProductID)

	// Use the well-known spec for discovery
	tempDevice := *hsmDevice
	if tempDevice.Spec.Discovery == nil {
		tempDevice.Spec.Discovery = &hsmv1alpha1.DiscoverySpec{}
	}
	tempDevice.Spec.Discovery.USB = spec

	return d.discoverUSBDevices(ctx, &tempDevice)
}

// shouldDiscoverOnNode determines if device discovery should run on this node
func (d *DiscoveryAgent) shouldDiscoverOnNode(hsmDevice *hsmv1alpha1.HSMDevice) bool {
	// If no node selector is specified, discover on all nodes
	if len(hsmDevice.Spec.NodeSelector) == 0 {
		return true
	}

	// Check if this node matches the node selector
	// This is a simplified check - in production, you'd want to fetch
	// the actual node labels and compare them
	for key, value := range hsmDevice.Spec.NodeSelector {
		if key == "kubernetes.io/hostname" && value == d.nodeName {
			return true
		}
	}

	return false
}

// updatePodAnnotation updates the pod's annotation with discovery results
func (d *DiscoveryAgent) updatePodAnnotation(
	ctx context.Context, hsmDeviceName string, discoveredDevices []hsmv1alpha1.DiscoveredDevice,
) error {
	// Create discovery report
	report := PodDiscoveryReport{
		HSMDeviceName:     hsmDeviceName,
		ReportingNode:     d.nodeName,
		DiscoveredDevices: discoveredDevices,
		LastReportTime:    metav1.Now(),
		DiscoveryStatus:   "completed",
	}

	// Marshal report to JSON
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal discovery report: %w", err)
	}

	// Get the current pod
	pod := &corev1.Pod{}
	podKey := types.NamespacedName{
		Name:      d.podName,
		Namespace: d.podNamespace,
	}

	if err := d.client.Get(ctx, podKey, pod); err != nil {
		return fmt.Errorf("failed to get pod %s/%s: %w", d.podNamespace, d.podName, err)
	}

	// Create a copy for patching
	podCopy := pod.DeepCopy()

	// Initialize annotations if nil
	if podCopy.Annotations == nil {
		podCopy.Annotations = make(map[string]string)
	}

	// Update the annotation
	podCopy.Annotations[DeviceReportAnnotation] = string(reportJSON)

	// Patch the pod
	if err := d.client.Patch(ctx, podCopy, client.MergeFrom(pod)); err != nil {
		return fmt.Errorf("failed to update pod annotation: %w", err)
	}

	d.logger.V(1).Info("Updated pod annotation with discovery report",
		"device", hsmDeviceName,
		"devicesFound", len(discoveredDevices),
		"pod", d.podName)

	return nil
}

// startEventMonitoring initializes and starts USB event monitoring
func (d *DiscoveryAgent) startEventMonitoring(ctx context.Context) error {
	// Start the USB event monitor
	if err := d.usbDiscoverer.StartEventMonitoring(ctx); err != nil {
		return fmt.Errorf("failed to start USB event monitoring: %w", err)
	}

	d.logger.Info("USB event monitoring initialized successfully")
	return nil
}

// restartEventMonitoring attempts to restart USB event monitoring
func (d *DiscoveryAgent) restartEventMonitoring(ctx context.Context) error {
	d.logger.Info("Restarting USB event monitoring")

	// Stop current monitoring
	d.usbDiscoverer.StopEventMonitoring()

	// Re-register all specs
	if err := d.reregisterAllSpecs(); err != nil {
		return fmt.Errorf("failed to re-register specs: %w", err)
	}

	// Start monitoring again
	if err := d.usbDiscoverer.StartEventMonitoring(ctx); err != nil {
		return fmt.Errorf("failed to restart USB event monitoring: %w", err)
	}

	d.eventMonitoringActive = true
	d.logger.Info("USB event monitoring restarted successfully")
	return nil
}

// handleUSBEvent processes a USB device event (add/remove)
func (d *DiscoveryAgent) handleUSBEvent(ctx context.Context, event discovery.USBEvent) {
	d.logger.V(1).Info("Processing USB event",
		"action", event.Action,
		"device", event.HSMDeviceName,
		"vendor", event.Device.VendorID,
		"product", event.Device.ProductID,
		"serial", event.Device.SerialNumber)

	switch event.Action {
	case "add":
		d.handleDeviceAdd(ctx, event)
	case "remove":
		d.handleDeviceRemove(ctx, event)
	default:
		d.logger.V(2).Info("Ignoring USB event with unknown action", "action", event.Action)
	}
}

// handleDeviceAdd handles USB device addition events
func (d *DiscoveryAgent) handleDeviceAdd(ctx context.Context, event discovery.USBEvent) {
	// Convert USBDevice to DiscoveredDevice
	device := discovery.ConvertToDiscoveredDevice(event.Device, d.nodeName)

	// Update device cache
	currentDevices, exists := d.deviceCache[event.HSMDeviceName]
	if !exists {
		currentDevices = []hsmv1alpha1.DiscoveredDevice{}
	}

	// Check if device already exists using fast lookup (by serial number)
	if existingIdx := d.findDeviceIndex(event.HSMDeviceName, device); existingIdx != -1 {
		existingDevice := currentDevices[existingIdx]

		// Check if this is a migration (same serial, different node)
		if !existingDevice.Available && existingDevice.NodeName != d.nodeName {
			d.logger.Info("Device migrated from another node",
				"device", event.HSMDeviceName,
				"serial", device.SerialNumber,
				"fromNode", existingDevice.NodeName,
				"toNode", d.nodeName,
				"fromPath", existingDevice.DevicePath,
				"toPath", device.DevicePath)
		} else if existingDevice.Available && existingDevice.NodeName == d.nodeName {
			d.logger.V(2).Info("Device already available on this node, updating last seen",
				"device", event.HSMDeviceName,
				"serial", device.SerialNumber)
		} else if !existingDevice.Available && existingDevice.NodeName == d.nodeName {
			d.logger.Info("Device reconnected on same node",
				"device", event.HSMDeviceName,
				"serial", device.SerialNumber)
		}

		// Update the existing device entry with new information
		currentDevices[existingIdx] = device
		currentDevices[existingIdx].Available = true // Mark as available

		d.updateDeviceCache(event.HSMDeviceName, currentDevices)
		if err := d.updatePodAnnotation(ctx, event.HSMDeviceName, currentDevices); err != nil {
			d.logger.Error(err, "Failed to update pod annotation after device reconnection")
		}
		return
	}

	// Add new device
	currentDevices = append(currentDevices, device)
	d.updateDeviceCache(event.HSMDeviceName, currentDevices)

	d.logger.Info("Added device to cache",
		"device", event.HSMDeviceName,
		"serial", device.SerialNumber,
		"path", device.DevicePath,
		"totalDevices", len(currentDevices))

	// Update pod annotation
	if err := d.updatePodAnnotation(ctx, event.HSMDeviceName, currentDevices); err != nil {
		d.logger.Error(err, "Failed to update pod annotation after device add")
	}
}

// handleDeviceRemove handles USB device removal events
func (d *DiscoveryAgent) handleDeviceRemove(ctx context.Context, event discovery.USBEvent) {
	currentDevices, exists := d.deviceCache[event.HSMDeviceName]
	if !exists {
		d.logger.V(2).Info("No devices in cache for device removal", "device", event.HSMDeviceName)
		return
	}

	device := discovery.ConvertToDiscoveredDevice(event.Device, d.nodeName)

	// Find device using fast lookup
	deviceIdx := d.findDeviceIndex(event.HSMDeviceName, device)
	if deviceIdx == -1 {
		d.logger.V(2).Info("Device not found in cache for removal",
			"device", event.HSMDeviceName,
			"serial", device.SerialNumber)
		return
	}

	d.logger.Info("Marking device as lost in cache",
		"device", event.HSMDeviceName,
		"serial", device.SerialNumber,
		"path", device.DevicePath)

	// Mark device as lost instead of removing it from cache
	currentDevices[deviceIdx].Available = false
	currentDevices[deviceIdx].LastSeen = metav1.Now() // Use LastSeen as "lost at" timestamp

	d.updateDeviceCache(event.HSMDeviceName, currentDevices)

	d.logger.Info("Updated device cache after marking as lost",
		"device", event.HSMDeviceName,
		"serial", device.SerialNumber,
		"totalDevices", len(currentDevices))

	// Update pod annotation
	if err := d.updatePodAnnotation(ctx, event.HSMDeviceName, currentDevices); err != nil {
		d.logger.Error(err, "Failed to update pod annotation after device remove")
	}
}

// reconcileDevices performs periodic reconciliation as a safety net
func (d *DiscoveryAgent) reconcileDevices(ctx context.Context) error {
	if !d.eventMonitoringActive {
		// If event monitoring is not active, fall back to regular discovery
		d.logger.V(1).Info("Event monitoring inactive, performing full discovery")
		return d.performDiscovery(ctx)
	}

	// Light reconciliation - just verify our cached state matches reality
	d.logger.V(2).Info("Performing light reconciliation check")

	// List all HSMDevice resources
	var hsmDeviceList hsmv1alpha1.HSMDeviceList
	if err := d.client.List(ctx, &hsmDeviceList); err != nil {
		return fmt.Errorf("failed to list HSMDevice resources during reconciliation: %w", err)
	}

	// Re-register specs in case any were missed
	for _, hsmDevice := range hsmDeviceList.Items {
		d.registerSpecForMonitoring(&hsmDevice)
	}

	// Clean up devices that have been lost for too long (5 minutes)
	cleanupThreshold := 5 * time.Minute
	for hsmDeviceName, devices := range d.deviceCache {
		var activeDevices []hsmv1alpha1.DiscoveredDevice
		cleanedCount := 0

		for _, device := range devices {
			if !device.Available && time.Since(device.LastSeen.Time) > cleanupThreshold {
				// Skip this device, it's been gone too long
				cleanedCount++
				d.logger.V(1).Info("Cleaning up stale lost device from cache",
					"device", hsmDeviceName,
					"serial", device.SerialNumber,
					"lastSeen", device.LastSeen.Time,
					"timeSinceLost", time.Since(device.LastSeen.Time))
				continue
			}
			activeDevices = append(activeDevices, device)
		}

		if cleanedCount > 0 {
			d.logger.Info("Cleaned up stale lost devices",
				"device", hsmDeviceName,
				"cleanedCount", cleanedCount,
				"remainingDevices", len(activeDevices))

			d.updateDeviceCache(hsmDeviceName, activeDevices)
			if err := d.updatePodAnnotation(ctx, hsmDeviceName, activeDevices); err != nil {
				d.logger.Error(err, "Failed to update pod annotation after cleanup", "device", hsmDeviceName)
			}
		}
	}

	return nil
}

// registerSpecForMonitoring registers an HSMDevice specification for event monitoring
func (d *DiscoveryAgent) registerSpecForMonitoring(hsmDevice *hsmv1alpha1.HSMDevice) {
	// Skip if device should not be discovered on this node
	if !d.shouldDiscoverOnNode(hsmDevice) {
		return
	}

	var spec *hsmv1alpha1.USBDeviceSpec

	// Determine the USB spec to monitor
	if hsmDevice.Spec.Discovery != nil && hsmDevice.Spec.Discovery.USB != nil {
		spec = hsmDevice.Spec.Discovery.USB
	} else if hsmDevice.Spec.Discovery != nil && hsmDevice.Spec.Discovery.AutoDiscovery {
		// Use well-known specs for auto-discovery
		wellKnownSpecs := discovery.GetWellKnownHSMSpecs()
		if wellKnownSpec, exists := wellKnownSpecs[hsmDevice.Spec.DeviceType]; exists {
			spec = wellKnownSpec
		}
	} else {
		// Default to auto-discovery with well-known specs
		wellKnownSpecs := discovery.GetWellKnownHSMSpecs()
		if wellKnownSpec, exists := wellKnownSpecs[hsmDevice.Spec.DeviceType]; exists {
			spec = wellKnownSpec
		}
	}

	// Register the spec if we have one
	if spec != nil {
		d.usbDiscoverer.AddSpecForMonitoring(hsmDevice.Name, spec)
		d.logger.V(2).Info("Registered USB spec for event monitoring",
			"device", hsmDevice.Name,
			"vendor", spec.VendorID,
			"product", spec.ProductID)
	}
}

// reregisterAllSpecs re-registers all current HSMDevice specs for monitoring
func (d *DiscoveryAgent) reregisterAllSpecs() error {
	ctx := context.Background()

	var hsmDeviceList hsmv1alpha1.HSMDeviceList
	if err := d.client.List(ctx, &hsmDeviceList); err != nil {
		return fmt.Errorf("failed to list HSMDevice resources: %w", err)
	}

	for _, hsmDevice := range hsmDeviceList.Items {
		d.registerSpecForMonitoring(&hsmDevice)
	}

	return nil
}

// updateDeviceCache updates both the device cache and lookup index
func (d *DiscoveryAgent) updateDeviceCache(hsmDeviceName string, devices []hsmv1alpha1.DiscoveredDevice) {
	d.deviceCache[hsmDeviceName] = devices

	// Rebuild lookup index for this HSM device
	if d.deviceLookup[hsmDeviceName] == nil {
		d.deviceLookup[hsmDeviceName] = make(map[string]int)
	} else {
		// Clear existing entries
		for k := range d.deviceLookup[hsmDeviceName] {
			delete(d.deviceLookup[hsmDeviceName], k)
		}
	}

	// Build lookup index
	for i, device := range devices {
		// Index by serial number if available
		if device.SerialNumber != "" {
			d.deviceLookup[hsmDeviceName]["serial:"+device.SerialNumber] = i
		}
		// Index by device path if available
		if device.DevicePath != "" {
			d.deviceLookup[hsmDeviceName]["path:"+device.DevicePath] = i
		}
	}
}

// findDeviceIndex returns the index of a device in the cache, or -1 if not found
func (d *DiscoveryAgent) findDeviceIndex(hsmDeviceName string, device hsmv1alpha1.DiscoveredDevice) int {
	lookupMap, exists := d.deviceLookup[hsmDeviceName]
	if !exists {
		return -1
	}

	// Try serial number lookup first
	if device.SerialNumber != "" {
		if idx, found := lookupMap["serial:"+device.SerialNumber]; found {
			return idx
		}
	}

	// Fall back to device path lookup
	if device.DevicePath != "" {
		if idx, found := lookupMap["path:"+device.DevicePath]; found {
			return idx
		}
	}

	return -1
}
