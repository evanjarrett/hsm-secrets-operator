//go:build cgo

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
	"time"

	"github.com/jochenvg/go-udev"
)

type Udev = udev.Udev
type Monitor = udev.Monitor
type UDevDevice = udev.Device
type Enumerate = udev.Enumerate

// convertUdevDevice converts a go-udev Device to our USBDevice format
func (u *USBDiscoverer) convertUdevDevice(device *UDevDevice) *USBDevice {
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

// handleDeviceEvent processes a single USB device event
func (u *USBDiscoverer) handleDeviceEvent(device *UDevDevice) {
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
