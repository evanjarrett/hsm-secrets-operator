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
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
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
	// Create a new flag set for discovery-specific flags
	fs := flag.NewFlagSet("discovery", flag.ContinueOnError)

	var nodeName string
	var podName string
	var podNamespace string
	var syncInterval time.Duration
	var detectionMethod string

	fs.StringVar(&nodeName, "node-name", "", "The name of the node this discovery agent is running on")
	fs.StringVar(&podName, "pod-name", "", "The name of this discovery pod")
	fs.StringVar(&podNamespace, "pod-namespace", "", "The namespace of this discovery pod")
	fs.DurationVar(&syncInterval, "sync-interval", 30*time.Second, "Interval for device discovery sync")
	fs.StringVar(&detectionMethod, "detection-method", "auto",
		"USB detection method: 'sysfs' (native), 'legacy' (privileged), or 'auto'")

	// Parse discovery-specific flags from the remaining unparsed arguments
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Get node name
	if nodeName == "" {
		if name := os.Getenv("NODE_NAME"); name != "" {
			nodeName = name
		} else if hostname, err := os.Hostname(); err == nil {
			nodeName = hostname
		} else {
			return fmt.Errorf("node name must be provided via --node-name flag or NODE_NAME environment variable")
		}
	}

	// Get pod name
	if podName == "" {
		if name := os.Getenv("POD_NAME"); name != "" {
			podName = name
		} else {
			podName = nodeName + "-discovery"
		}
	}

	// Get pod namespace
	if podNamespace == "" {
		if namespace := os.Getenv("POD_NAMESPACE"); namespace != "" {
			podNamespace = namespace
		} else {
			podNamespace = "default"
		}
	}

	setupLog.Info("Starting HSM device discovery agent",
		"node", nodeName,
		"pod", podName,
		"namespace", podNamespace,
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
		nodeName:      nodeName,
		podName:       podName,
		podNamespace:  podNamespace,
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
}

// Run starts the discovery loop
func (d *DiscoveryAgent) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.syncInterval)
	defer ticker.Stop()

	// Run initial discovery
	if err := d.performDiscovery(ctx); err != nil {
		d.logger.Error(err, "Initial discovery failed")
	}

	// Start periodic discovery
	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Discovery agent shutting down")
			return nil
		case <-ticker.C:
			if err := d.performDiscovery(ctx); err != nil {
				d.logger.Error(err, "Discovery iteration failed")
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
	// Perform discovery based on specification
	if hsmDevice.Spec.Discovery != nil && hsmDevice.Spec.Discovery.USB != nil {
		return d.discoverUSBDevices(ctx, hsmDevice)
	} else if hsmDevice.Spec.Discovery != nil && hsmDevice.Spec.Discovery.DevicePath != nil {
		return d.discoverPathDevices(ctx, hsmDevice)
	} else if hsmDevice.Spec.Discovery != nil && hsmDevice.Spec.Discovery.AutoDiscovery {
		return d.autoDiscoverDevices(ctx, hsmDevice)
	} else {
		// Default to auto-discovery
		return d.autoDiscoverDevices(ctx, hsmDevice)
	}
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
		for k, v := range usbDev.DeviceInfo {
			device.DeviceInfo[k] = v
		}

		devices = append(devices, device)
	}

	d.logger.V(1).Info("USB device discovery completed", "devicesFound", len(devices))
	return devices, nil
}

// discoverPathDevices discovers devices using path-based specifications
func (d *DiscoveryAgent) discoverPathDevices(
	ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice,
) ([]hsmv1alpha1.DiscoveredDevice, error) {
	usbDevices, err := d.usbDiscoverer.DiscoverByPath(ctx, hsmDevice.Spec.Discovery.DevicePath)
	if err != nil {
		return nil, fmt.Errorf("path discovery failed: %w", err)
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
				"discovery-type": "path",
				"path-pattern":   hsmDevice.Spec.Discovery.DevicePath.Path,
			},
		}

		// Add additional device info
		for k, v := range usbDev.DeviceInfo {
			device.DeviceInfo[k] = v
		}

		devices = append(devices, device)
	}

	d.logger.V(1).Info("Path device discovery completed", "devicesFound", len(devices))
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
