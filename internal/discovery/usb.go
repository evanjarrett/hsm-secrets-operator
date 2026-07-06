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
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	hsmv1alpha1 "tangled.org/evan.jarrett.net/hsm-secrets-operator/api/v1alpha1"
)

// USBDevice represents a discovered USB device
type USBDevice struct {
	VendorID     string
	ProductID    string
	SerialNumber string
	DevicePath   string
	Manufacturer string
	Product      string
	DeviceInfo   map[string]string
}

// USBEvent represents a USB device event
type USBEvent struct {
	Action        string    // "add" or "remove"
	Device        USBDevice // The device that changed
	Timestamp     time.Time
	HSMDeviceName string // Which HSMDevice spec this event relates to
}

// MatchCriteria is the per-HSMDevice rule set evaluated against each USB device.
// A device matches when autoDiscovery is enabled and the device is in the CCID
// allowlist, OR when it satisfies any explicit spec. The two are unioned, and
// explicit specs match regardless of the allowlist (so they can bridge IDs not
// yet known to libccid, or pin a single device by serial number).
type MatchCriteria struct {
	AutoDiscovery bool
	Specs         []hsmv1alpha1.USBDeviceSpec
}

// USBDiscoverer handles USB device discovery and monitoring
type USBDiscoverer struct {
	logger    logr.Logger
	udev      *Udev
	allowlist *CCIDAllowlist

	// Event monitoring
	monitor      *Monitor
	eventChannel chan USBEvent
	activeMu     sync.RWMutex
	activeSpecs  map[string]MatchCriteria
}

// NewUSBDiscoverer creates a new USB device discoverer
func NewUSBDiscoverer() *USBDiscoverer {
	logger := ctrl.Log.WithName("usb-discoverer")

	udev := &Udev{}

	return &USBDiscoverer{
		logger:       logger,
		udev:         udev,
		allowlist:    LoadCCIDAllowlist(logger),
		eventChannel: make(chan USBEvent, 100),
		activeSpecs:  make(map[string]MatchCriteria),
	}
}

// DiscoverDevices finds USB devices matching the given criteria
func (u *USBDiscoverer) DiscoverDevices(ctx context.Context, criteria MatchCriteria) ([]USBDevice, error) {
	u.logger.V(1).Info("Starting USB device discovery",
		"autoDiscovery", criteria.AutoDiscovery,
		"explicitSpecs", len(criteria.Specs))

	// Create enumerate object
	enumerate := u.udev.NewEnumerate()

	// Filter for USB devices only
	if err := enumerate.AddMatchSubsystem("usb"); err != nil {
		return nil, fmt.Errorf("failed to add subsystem filter: %w", err)
	}

	if err := enumerate.AddMatchProperty("DEVTYPE", "usb_device"); err != nil {
		return nil, fmt.Errorf("failed to add device type filter: %w", err)
	}

	// Get all USB devices
	devices, err := enumerate.Devices()
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate devices: %w", err)
	}

	u.logger.V(1).Info("Found USB devices", "totalDevices", len(devices))

	// Convert and filter devices
	var matchingDevices []USBDevice
	for _, device := range devices {
		if usbDev := u.convertUdevDevice(device); usbDev != nil {
			if u.matchesCriteria(*usbDev, criteria) {
				u.logger.V(1).Info("Found matching USB device",
					"vendorId", usbDev.VendorID,
					"productId", usbDev.ProductID,
					"serial", usbDev.SerialNumber,
					"path", usbDev.DevicePath)
				matchingDevices = append(matchingDevices, *usbDev)
			}
		}
	}

	u.logger.Info("USB device discovery completed",
		"matchedDevices", len(matchingDevices))

	return matchingDevices, nil
}

// matchesSpec checks if a USB device matches the given specification
func (u *USBDiscoverer) matchesSpec(device USBDevice, spec *hsmv1alpha1.USBDeviceSpec) bool {
	// Check vendor ID
	if spec.VendorID != "" && !strings.EqualFold(device.VendorID, spec.VendorID) {
		return false
	}

	// Check product ID
	if spec.ProductID != "" && !strings.EqualFold(device.ProductID, spec.ProductID) {
		return false
	}

	// Check serial number if specified
	if spec.SerialNumber != "" && device.SerialNumber != spec.SerialNumber {
		return false
	}

	return true
}

// matchesCriteria reports whether a device satisfies an HSMDevice's criteria:
// the CCID allowlist (when autoDiscovery is on) unioned with any explicit specs.
func (u *USBDiscoverer) matchesCriteria(device USBDevice, criteria MatchCriteria) bool {
	if criteria.AutoDiscovery && u.allowlist.Contains(device.VendorID, device.ProductID) {
		return true
	}
	for i := range criteria.Specs {
		if u.matchesSpec(device, &criteria.Specs[i]) {
			return true
		}
	}
	return false
}

// StartEventMonitoring begins real-time USB device event monitoring
func (u *USBDiscoverer) StartEventMonitoring(ctx context.Context) error {
	u.logger.Info("Starting USB event monitoring")

	// Create monitor
	u.monitor = u.udev.NewMonitorFromNetlink("udev")

	// Add filters for USB devices
	if err := u.monitor.FilterAddMatchSubsystem("usb"); err != nil {
		return fmt.Errorf("failed to add subsystem filter to monitor: %w", err)
	}

	// Start monitoring goroutine
	go u.monitorLoop(ctx)

	u.logger.Info("USB event monitoring started successfully")
	return nil
}

// monitorLoop handles udev events in a separate goroutine
func (u *USBDiscoverer) monitorLoop(ctx context.Context) {
	defer close(u.eventChannel)

	deviceChan, errorChan, err := u.monitor.DeviceChan(ctx)
	if err != nil {
		u.logger.Error(err, "Failed to create device channel")
		return
	}

	for {
		select {
		case <-ctx.Done():
			u.logger.Info("USB event monitoring stopped")
			return

		case device, ok := <-deviceChan:
			if !ok {
				u.logger.Info("USB device channel closed")
				return
			}
			u.handleDeviceEvent(device)

		case err, ok := <-errorChan:
			if !ok {
				u.logger.Info("USB error channel closed")
				return
			}
			u.logger.Error(err, "USB monitoring error")
		}
	}
}

// AddSpecForMonitoring registers an HSMDevice's match criteria for event monitoring
func (u *USBDiscoverer) AddSpecForMonitoring(hsmDeviceName string, criteria MatchCriteria) {
	u.activeMu.Lock()
	u.activeSpecs[hsmDeviceName] = criteria
	u.activeMu.Unlock()
	u.logger.V(2).Info("Added USB criteria for monitoring",
		"device", hsmDeviceName,
		"autoDiscovery", criteria.AutoDiscovery,
		"explicitSpecs", len(criteria.Specs))
}

// RemoveSpecFromMonitoring unregisters an HSMDevice from event monitoring
func (u *USBDiscoverer) RemoveSpecFromMonitoring(hsmDeviceName string) {
	u.activeMu.Lock()
	delete(u.activeSpecs, hsmDeviceName)
	u.activeMu.Unlock()
	u.logger.V(2).Info("Removed USB criteria from monitoring", "device", hsmDeviceName)
}

// MonitoredDeviceNames returns the HSMDevice names currently registered for
// event monitoring. Used to prune criteria for HSMDevices that no longer exist.
func (u *USBDiscoverer) MonitoredDeviceNames() []string {
	u.activeMu.RLock()
	defer u.activeMu.RUnlock()
	names := make([]string, 0, len(u.activeSpecs))
	for name := range u.activeSpecs {
		names = append(names, name)
	}
	return names
}

// GetEventChannel returns the channel for receiving USB device events
func (u *USBDiscoverer) GetEventChannel() <-chan USBEvent {
	return u.eventChannel
}

// StopEventMonitoring stops the USB event monitor
func (u *USBDiscoverer) StopEventMonitoring() {
	u.logger.Info("Stopping USB event monitor")
	if u.monitor != nil {
		u.monitor = nil
	}
}

// IsEventMonitoringActive returns whether event monitoring is currently active
func (u *USBDiscoverer) IsEventMonitoringActive() bool {
	return u.monitor != nil
}

// ConvertToDiscoveredDevice converts a USBDevice to a DiscoveredDevice
func ConvertToDiscoveredDevice(usbDevice USBDevice, nodeName string) hsmv1alpha1.DiscoveredDevice {
	device := hsmv1alpha1.DiscoveredDevice{
		DevicePath:   usbDevice.DevicePath,
		SerialNumber: usbDevice.SerialNumber,
		NodeName:     nodeName,
		LastSeen:     metav1.Now(),
		Available:    true,
		DeviceInfo: map[string]string{
			"vendor-id":    usbDevice.VendorID,
			"product-id":   usbDevice.ProductID,
			"manufacturer": usbDevice.Manufacturer,
			"product":      usbDevice.Product,
		},
	}

	// Add additional device info
	maps.Copy(device.DeviceInfo, usbDevice.DeviceInfo)

	return device
}

// IsSameDevice checks if two devices are the same (by serial number or device path)
func IsSameDevice(device1, device2 hsmv1alpha1.DiscoveredDevice) bool {
	// Compare by serial number if both have one
	if device1.SerialNumber != "" && device2.SerialNumber != "" {
		return device1.SerialNumber == device2.SerialNumber
	}

	// Fall back to device path comparison
	if device1.DevicePath != "" && device2.DevicePath != "" {
		return device1.DevicePath == device2.DevicePath
	}

	// If we can't identify devices uniquely, assume they're different
	return false
}
