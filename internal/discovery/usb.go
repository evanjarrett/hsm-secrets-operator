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
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-logr/logr"
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

// USBDiscoverer handles USB device discovery
type USBDiscoverer struct {
	logger logr.Logger
	// mutex  sync.RWMutex // unused

	// Known USB paths to scan (support both container and host-mounted paths)
	usbSysPaths []string
	devicePaths []string
}

// NewUSBDiscoverer creates a new USB device discoverer
func NewUSBDiscoverer() *USBDiscoverer {
	return &USBDiscoverer{
		logger: ctrl.Log.WithName("usb-discoverer"),
		usbSysPaths: []string{
			"/host/sys/bus/usb/devices", // Host-mounted path (for DaemonSet)
			"/sys/bus/usb/devices",      // Container path (for regular deployment)
			"/host/sys/class/usbmisc",   // Alternative host path
			"/sys/class/usbmisc",        // Alternative container path
		},
		devicePaths: []string{
			"/host/dev", // Host-mounted path (for DaemonSet)
			"/dev",      // Container path (for regular deployment)
		},
	}
}

// DiscoverDevices finds USB devices matching the given specification
func (u *USBDiscoverer) DiscoverDevices(ctx context.Context, spec *hsmv1alpha1.USBDeviceSpec) ([]USBDevice, error) {
	u.logger.V(1).Info("Starting USB device discovery",
		"vendorId", spec.VendorID,
		"productId", spec.ProductID)

	devices := make([]USBDevice, 0)

	// Scan USB devices in sysfs
	usbDevices, err := u.scanUSBDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to scan USB devices: %w", err)
	}

	// Filter devices based on spec
	for _, device := range usbDevices {
		if u.matchesSpec(device, spec) {
			u.logger.V(1).Info("Found matching USB device",
				"vendorId", device.VendorID,
				"productId", device.ProductID,
				"serial", device.SerialNumber,
				"path", device.DevicePath)
			devices = append(devices, device)
		}
	}

	u.logger.Info("USB device discovery completed",
		"matchedDevices", len(devices))

	return devices, nil
}

// DiscoverByPath finds devices using path-based discovery
func (u *USBDiscoverer) DiscoverByPath(ctx context.Context, pathSpec *hsmv1alpha1.DevicePathSpec) ([]USBDevice, error) {
	u.logger.V(1).Info("Starting path-based device discovery", "path", pathSpec.Path)

	devices := make([]USBDevice, 0)

	// Handle glob patterns
	matches, err := filepath.Glob(pathSpec.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to glob path %s: %w", pathSpec.Path, err)
	}

	for _, match := range matches {
		// Check if the path exists and is accessible
		if _, err := os.Stat(match); err != nil {
			u.logger.V(2).Info("Skipping inaccessible device path", "path", match, "error", err)
			continue
		}

		// Create USB device entry for path-based discovery
		device := USBDevice{
			DevicePath: match,
			DeviceInfo: map[string]string{
				"discovery-method": "path",
				"permissions":      pathSpec.Permissions,
			},
		}

		// Try to get additional device info if possible
		if info := u.getDeviceInfoFromPath(match); info != nil {
			device.VendorID = info["vendor_id"]
			device.ProductID = info["product_id"]
			device.SerialNumber = info["serial"]
			device.Manufacturer = info["manufacturer"]
			device.Product = info["product"]
			for k, v := range info {
				device.DeviceInfo[k] = v
			}
		}

		devices = append(devices, device)
	}

	u.logger.Info("Path-based device discovery completed",
		"matchedDevices", len(devices))

	return devices, nil
}

// scanUSBDevices scans the USB subsystem for devices
func (u *USBDiscoverer) scanUSBDevices(_ context.Context) ([]USBDevice, error) {
	devices := make([]USBDevice, 0)

	// Try different USB sysfs paths
	var usbSysPath string
	for _, path := range u.usbSysPaths {
		if _, err := os.Stat(path); err == nil {
			usbSysPath = path
			u.logger.V(1).Info("Using USB sysfs path", "path", usbSysPath)
			break
		}
	}

	if usbSysPath == "" {
		u.logger.V(1).Info("No USB sysfs path available", "tried", u.usbSysPaths)
		return devices, nil
	}

	err := filepath.WalkDir(usbSysPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip if not a directory or if it doesn't look like a USB device
		if !d.IsDir() {
			return nil
		}

		// Check if this is a USB device directory (e.g., 1-1.2)
		name := d.Name()
		if !regexp.MustCompile(`^\d+-[\d.]+$`).MatchString(name) {
			return nil
		}

		device := u.parseUSBDevice(path)
		if device == nil {
			u.logger.V(2).Info("Failed to parse USB device", "path", path)
			return nil
		}

		if device != nil {
			devices = append(devices, *device)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk USB devices: %w", err)
	}

	return devices, nil
}

// parseUSBDevice parses USB device information from sysfs
func (u *USBDiscoverer) parseUSBDevice(devicePath string) *USBDevice {
	device := &USBDevice{
		DeviceInfo: make(map[string]string),
	}

	// Read vendor ID
	if vendorID, err := u.readSysfsFile(filepath.Join(devicePath, "idVendor")); err == nil {
		device.VendorID = strings.TrimSpace(vendorID)
	}

	// Read product ID
	if productID, err := u.readSysfsFile(filepath.Join(devicePath, "idProduct")); err == nil {
		device.ProductID = strings.TrimSpace(productID)
	}

	// Read serial number
	if serial, err := u.readSysfsFile(filepath.Join(devicePath, "serial")); err == nil {
		device.SerialNumber = strings.TrimSpace(serial)
	}

	// Read manufacturer
	if manufacturer, err := u.readSysfsFile(filepath.Join(devicePath, "manufacturer")); err == nil {
		device.Manufacturer = strings.TrimSpace(manufacturer)
	}

	// Read product name
	if product, err := u.readSysfsFile(filepath.Join(devicePath, "product")); err == nil {
		device.Product = strings.TrimSpace(product)
	}

	// Skip devices without vendor/product IDs
	if device.VendorID == "" || device.ProductID == "" {
		return nil
	}

	// Try to find associated device paths
	device.DevicePath = u.findDevicePaths(device.VendorID, device.ProductID, device.SerialNumber)

	// Add additional device info
	device.DeviceInfo["sysfs-path"] = devicePath
	device.DeviceInfo["discovery-method"] = "usb"

	return device
}

// findDevicePaths attempts to find device paths for a USB device
func (u *USBDiscoverer) findDevicePaths(_, _, _ string) string {
	// This is a simplified implementation
	// In a real implementation, you'd want to scan /dev and match devices
	// For now, we'll look for common HSM device paths in all device directories

	commonPaths := []string{
		"ttyUSB0", "ttyUSB1", "ttyUSB2", "ttyUSB3",
		"ttyACM0", "ttyACM1", "ttyACM2", "ttyACM3",
		"sc-hsm", "pkcs11",
	}

	// Try both host-mounted and container paths
	for _, devPath := range u.devicePaths {
		for _, deviceName := range commonPaths {
			fullPath := filepath.Join(devPath, deviceName)
			if _, err := os.Stat(fullPath); err == nil {
				u.logger.V(2).Info("Found device path", "path", fullPath)
				// Return the path that would be accessible to the application
				// If we're using host-mounted paths, convert back to container paths
				if strings.HasPrefix(devPath, "/host/") {
					return filepath.Join("/dev", deviceName)
				}
				return fullPath
			}
		}
	}

	return ""
}

// readSysfsFile reads a single-line file from sysfs
func (u *USBDiscoverer) readSysfsFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := file.Close(); err != nil {
			// Log the error but don't fail the operation
			u.logger.V(2).Info("Failed to close sysfs file", "path", path, "error", err)
		}
	}()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		return scanner.Text(), nil
	}

	return "", fmt.Errorf("empty file or read error")
}

// getDeviceInfoFromPath attempts to get device info from a device path
func (u *USBDiscoverer) getDeviceInfoFromPath(devicePath string) map[string]string {
	// This is a placeholder implementation
	// In a real implementation, you'd use udev or similar to get device info
	info := make(map[string]string)

	// Try to determine device type from path
	if strings.Contains(devicePath, "ttyUSB") || strings.Contains(devicePath, "ttyACM") {
		info["device_type"] = "serial"
	} else if strings.Contains(devicePath, "sc-hsm") {
		info["device_type"] = "hsm"
		info["vendor_id"] = "20a0"  // Example: Pico HSM vendor ID
		info["product_id"] = "4230" // Example: Pico HSM product ID
	}

	return info
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
