//go:build cgo
// +build cgo

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
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/jochenvg/go-udev"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
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

// USBDiscoverer handles USB device discovery and monitoring
type USBDiscoverer struct {
	logger logr.Logger
	udev   *udev.Udev

	// Event monitoring
	monitor      *udev.Monitor
	eventChannel chan USBEvent
	activeSpecs  map[string]*hsmv1alpha1.USBDeviceSpec
}

// NewUSBDiscoverer creates a new USB device discoverer
func NewUSBDiscoverer() *USBDiscoverer {
	logger := ctrl.Log.WithName("usb-discoverer")

	udev := &udev.Udev{}

	return &USBDiscoverer{
		logger:       logger,
		udev:         udev,
		eventChannel: make(chan USBEvent, 100),
		activeSpecs:  make(map[string]*hsmv1alpha1.USBDeviceSpec),
	}
}

// NewUSBDiscovererWithMethod creates a new USB device discoverer (method parameter is ignored, kept for compatibility)
func NewUSBDiscovererWithMethod(method string) *USBDiscoverer {
	return NewUSBDiscoverer()
}

// DiscoverDevices finds USB devices matching the given specification
func (u *USBDiscoverer) DiscoverDevices(ctx context.Context, spec *hsmv1alpha1.USBDeviceSpec) ([]USBDevice, error) {
	u.logger.V(1).Info("Starting USB device discovery",
		"vendorId", spec.VendorID,
		"productId", spec.ProductID)

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
			if u.matchesSpec(*usbDev, spec) {
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

// convertUdevDevice converts a go-udev Device to our USBDevice format
func (u *USBDiscoverer) convertUdevDevice(device *udev.Device) *USBDevice {
	// Only process actual USB devices, not interfaces
	if device.PropertyValue("DEVTYPE") != "usb_device" {
		return nil
	}

	vendorID := device.PropertyValue("ID_VENDOR_ID")
	productID := device.PropertyValue("ID_MODEL_ID")

	// Skip devices without vendor/product IDs
	if vendorID == "" || productID == "" {
		return nil
	}

	return &USBDevice{
		VendorID:     vendorID,
		ProductID:    productID,
		SerialNumber: device.PropertyValue("ID_SERIAL_SHORT"),
		DevicePath:   device.Devnode(),
		Manufacturer: device.PropertyValue("ID_VENDOR"),
		Product:      device.PropertyValue("ID_MODEL"),
		DeviceInfo:   device.Properties(),
	}
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

// GetWellKnownHSMSpecs returns USB specifications for well-known HSM devices
func GetWellKnownHSMSpecs() map[hsmv1alpha1.HSMDeviceType]*hsmv1alpha1.USBDeviceSpec {
	return map[hsmv1alpha1.HSMDeviceType]*hsmv1alpha1.USBDeviceSpec{
		hsmv1alpha1.HSMDeviceTypePicoHSM: {
			VendorID:  "20a0", // Pico HSM vendor ID
			ProductID: "4230", // Pico HSM product ID
		},
		hsmv1alpha1.HSMDeviceTypeSmartCardHSM: {
			VendorID:  "04e6", // Example SmartCard-HSM vendor ID
			ProductID: "5816", // Example SmartCard-HSM product ID
		},
	}
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

// handleDeviceEvent processes a single USB device event
func (u *USBDiscoverer) handleDeviceEvent(device *udev.Device) {
	action := device.Action()

	// Only process add/remove events
	if action != "add" && action != "remove" {
		return
	}

	// Convert to USBDevice
	usbDev := u.convertUdevDevice(device)
	if usbDev == nil {
		return
	}

	u.logger.V(2).Info("Received USB event",
		"action", action,
		"vendor", usbDev.VendorID,
		"product", usbDev.ProductID,
		"serial", usbDev.SerialNumber)

	// Check which active specs match this device
	for hsmDeviceName, spec := range u.activeSpecs {
		if u.matchesSpec(*usbDev, spec) {
			event := USBEvent{
				Action:        action,
				Device:        *usbDev,
				Timestamp:     time.Now(),
				HSMDeviceName: hsmDeviceName,
			}

			select {
			case u.eventChannel <- event:
				u.logger.V(1).Info("Sent USB event",
					"action", action,
					"device", hsmDeviceName,
					"vendor", usbDev.VendorID,
					"product", usbDev.ProductID,
					"serial", usbDev.SerialNumber)
			default:
				u.logger.Error(nil, "USB event channel full, dropping event", "action", action)
			}
		}
	}
}

// AddSpecForMonitoring registers an HSMDevice spec for event monitoring
func (u *USBDiscoverer) AddSpecForMonitoring(hsmDeviceName string, spec *hsmv1alpha1.USBDeviceSpec) {
	u.activeSpecs[hsmDeviceName] = spec
	u.logger.V(2).Info("Added USB spec for monitoring",
		"device", hsmDeviceName,
		"vendor", spec.VendorID,
		"product", spec.ProductID)
}

// RemoveSpecFromMonitoring unregisters an HSMDevice spec from event monitoring
func (u *USBDiscoverer) RemoveSpecFromMonitoring(hsmDeviceName string) {
	delete(u.activeSpecs, hsmDeviceName)
	u.logger.V(2).Info("Removed USB spec from monitoring", "device", hsmDeviceName)
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
	for k, v := range usbDevice.DeviceInfo {
		device.DeviceInfo[k] = v
	}

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
