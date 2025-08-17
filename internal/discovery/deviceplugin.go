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
	"fmt"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
)

const (
	// ResourceNamePrefix is the prefix for HSM device resources
	ResourceNamePrefix = "hsm.j5t.io"
)

// Device represents a managed HSM device
type Device struct {
	ID           string
	DevicePath   string
	SerialNumber string
	Available    bool
	NodeName     string
	DeviceInfo   map[string]string
}

// HSMDeviceManager manages HSM devices for Kubernetes integration
type HSMDeviceManager struct {
	logger       logr.Logger
	resourceName string
	deviceType   hsmv1alpha1.HSMDeviceType
	devices      map[string]*Device
	devicesMutex sync.RWMutex
}

// NewHSMDeviceManager creates a new HSM device manager
func NewHSMDeviceManager(deviceType hsmv1alpha1.HSMDeviceType, resourceName string) *HSMDeviceManager {
	return &HSMDeviceManager{
		logger:       ctrl.Log.WithName("hsm-device-manager").WithValues("deviceType", deviceType),
		resourceName: resourceName,
		deviceType:   deviceType,
		devices:      make(map[string]*Device),
	}
}

// UpdateDevices updates the list of managed devices
func (m *HSMDeviceManager) UpdateDevices(discoveredDevices []hsmv1alpha1.DiscoveredDevice) {
	m.devicesMutex.Lock()
	defer m.devicesMutex.Unlock()

	// Clear existing devices
	m.devices = make(map[string]*Device)

	// Add discovered devices
	for _, discovered := range discoveredDevices {
		deviceID := m.generateDeviceID(discovered)

		m.devices[deviceID] = &Device{
			ID:           deviceID,
			DevicePath:   discovered.DevicePath,
			SerialNumber: discovered.SerialNumber,
			Available:    discovered.Available,
			NodeName:     discovered.NodeName,
			DeviceInfo:   discovered.DeviceInfo,
		}

		m.logger.V(1).Info("Updated device",
			"deviceId", deviceID,
			"path", discovered.DevicePath,
			"available", discovered.Available)
	}

	m.logger.Info("Updated device list", "deviceCount", len(m.devices))
}

// GetAvailableDevices returns a list of available devices
func (m *HSMDeviceManager) GetAvailableDevices() []*Device {
	m.devicesMutex.RLock()
	defer m.devicesMutex.RUnlock()

	var available []*Device
	for _, device := range m.devices {
		if device.Available {
			available = append(available, device)
		}
	}

	return available
}

// GetDevice returns a device by ID
func (m *HSMDeviceManager) GetDevice(deviceID string) (*Device, bool) {
	m.devicesMutex.RLock()
	defer m.devicesMutex.RUnlock()

	device, exists := m.devices[deviceID]
	return device, exists
}

// GetDevicesForNode returns all devices for a specific node
func (m *HSMDeviceManager) GetDevicesForNode(nodeName string) []*Device {
	m.devicesMutex.RLock()
	defer m.devicesMutex.RUnlock()

	var nodeDevices []*Device
	for _, device := range m.devices {
		if device.NodeName == nodeName {
			nodeDevices = append(nodeDevices, device)
		}
	}

	return nodeDevices
}

// GetResourceName returns the Kubernetes resource name for this device type
func (m *HSMDeviceManager) GetResourceName() string {
	return fmt.Sprintf("%s/%s", ResourceNamePrefix, strings.ToLower(string(m.deviceType)))
}

// generateDeviceID generates a unique device ID
func (m *HSMDeviceManager) generateDeviceID(device hsmv1alpha1.DiscoveredDevice) string {
	// Create a unique ID based on node name, device path, and serial
	parts := []string{
		device.NodeName,
		strings.ReplaceAll(device.DevicePath, "/", "_"),
	}

	if device.SerialNumber != "" {
		parts = append(parts, device.SerialNumber)
	}

	return strings.Join(parts, "-")
}
