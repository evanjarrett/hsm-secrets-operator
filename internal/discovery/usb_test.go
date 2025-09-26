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
	"testing"

	"github.com/stretchr/testify/assert"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
)

func TestNewUSBDiscoverer(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	assert.NotNil(t, discoverer)
	assert.NotNil(t, discoverer.udev)
	assert.NotNil(t, discoverer.eventChannel)
	assert.NotNil(t, discoverer.activeSpecs)
}

func TestNewUSBDiscovererWithMethod(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{"auto detection", "auto"},
		{"sysfs detection", "sysfs"},
		{"legacy detection", "legacy"},
		{"custom method", "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discoverer := NewUSBDiscovererWithMethod(tt.method)
			assert.NotNil(t, discoverer)
			// Method parameter is ignored for compatibility, udev is always used
		})
	}
}

func TestUSBDevice(t *testing.T) {
	device := USBDevice{
		VendorID:     "20a0",
		ProductID:    "4230",
		SerialNumber: "TEST123",
		DevicePath:   "/dev/ttyUSB0",
		Manufacturer: "Test Manufacturer",
		Product:      "Test Device",
		DeviceInfo: map[string]string{
			"test": "value",
		},
	}

	assert.Equal(t, "20a0", device.VendorID)
	assert.Equal(t, "4230", device.ProductID)
	assert.Equal(t, "TEST123", device.SerialNumber)
	assert.Equal(t, "/dev/ttyUSB0", device.DevicePath)
	assert.Equal(t, "Test Manufacturer", device.Manufacturer)
	assert.Equal(t, "Test Device", device.Product)
	assert.Equal(t, "value", device.DeviceInfo["test"])
}

func TestMatchesSpec(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	device := USBDevice{
		VendorID:     "20a0",
		ProductID:    "4230",
		SerialNumber: "TEST123",
	}

	tests := []struct {
		name     string
		spec     *hsmv1alpha1.USBDeviceSpec
		expected bool
	}{
		{
			name: "exact match",
			spec: &hsmv1alpha1.USBDeviceSpec{
				VendorID:     "20a0",
				ProductID:    "4230",
				SerialNumber: "TEST123",
			},
			expected: true,
		},
		{
			name: "vendor and product match, no serial",
			spec: &hsmv1alpha1.USBDeviceSpec{
				VendorID:  "20a0",
				ProductID: "4230",
			},
			expected: true,
		},
		{
			name: "case insensitive vendor ID",
			spec: &hsmv1alpha1.USBDeviceSpec{
				VendorID:  "20A0",
				ProductID: "4230",
			},
			expected: true,
		},
		{
			name: "case insensitive product ID",
			spec: &hsmv1alpha1.USBDeviceSpec{
				VendorID:  "20a0",
				ProductID: "4230",
			},
			expected: true,
		},
		{
			name: "vendor ID mismatch",
			spec: &hsmv1alpha1.USBDeviceSpec{
				VendorID:  "1234",
				ProductID: "4230",
			},
			expected: false,
		},
		{
			name: "product ID mismatch",
			spec: &hsmv1alpha1.USBDeviceSpec{
				VendorID:  "20a0",
				ProductID: "1234",
			},
			expected: false,
		},
		{
			name: "serial number mismatch",
			spec: &hsmv1alpha1.USBDeviceSpec{
				VendorID:     "20a0",
				ProductID:    "4230",
				SerialNumber: "DIFFERENT",
			},
			expected: false,
		},
		{
			name:     "empty spec matches all",
			spec:     &hsmv1alpha1.USBDeviceSpec{},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := discoverer.matchesSpec(device, tt.spec)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetWellKnownHSMSpecs(t *testing.T) {
	specs := GetWellKnownHSMSpecs()

	assert.NotEmpty(t, specs)

	// Check Pico HSM
	picoSpec, exists := specs[hsmv1alpha1.HSMDeviceTypePicoHSM]
	assert.True(t, exists)
	assert.Equal(t, "20a0", picoSpec.VendorID)
	assert.Equal(t, "4230", picoSpec.ProductID)

	// Check SmartCard HSM
	smartCardSpec, exists := specs[hsmv1alpha1.HSMDeviceTypeSmartCardHSM]
	assert.True(t, exists)
	assert.Equal(t, "04e6", smartCardSpec.VendorID)
	assert.Equal(t, "5816", smartCardSpec.ProductID)
}

func TestUSBEvent(t *testing.T) {
	device := USBDevice{
		VendorID:     "20a0",
		ProductID:    "4230",
		SerialNumber: "TEST123",
		DevicePath:   "/dev/ttyUSB0",
	}

	event := USBEvent{
		Action:        "add",
		Device:        device,
		HSMDeviceName: "test-hsm",
	}

	assert.Equal(t, "add", event.Action)
	assert.Equal(t, device, event.Device)
	assert.Equal(t, "test-hsm", event.HSMDeviceName)
}

func TestAddSpecForMonitoring(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	spec := &hsmv1alpha1.USBDeviceSpec{
		VendorID:  "20a0",
		ProductID: "4230",
	}

	// Test adding spec
	discoverer.AddSpecForMonitoring("test-device", spec)
	assert.Equal(t, spec, discoverer.activeSpecs["test-device"])

	// Test removing spec
	discoverer.RemoveSpecFromMonitoring("test-device")
	_, exists := discoverer.activeSpecs["test-device"]
	assert.False(t, exists)
}

func TestGetEventChannel(t *testing.T) {
	discoverer := NewUSBDiscoverer()
	channel := discoverer.GetEventChannel()
	assert.NotNil(t, channel)
}

func TestIsEventMonitoringActive(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	// Initially inactive
	assert.False(t, discoverer.IsEventMonitoringActive())

	// After setting monitor (simulated)
	discoverer.monitor = discoverer.udev.NewMonitorFromNetlink("udev")
	assert.True(t, discoverer.IsEventMonitoringActive())

	// After stopping
	discoverer.StopEventMonitoring()
	assert.False(t, discoverer.IsEventMonitoringActive())
}

// Benchmark tests
func BenchmarkMatchesSpec(b *testing.B) {
	discoverer := NewUSBDiscoverer()
	device := USBDevice{
		VendorID:     "20a0",
		ProductID:    "4230",
		SerialNumber: "TEST123",
	}
	spec := &hsmv1alpha1.USBDeviceSpec{
		VendorID:  "20a0",
		ProductID: "4230",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		discoverer.matchesSpec(device, spec)
	}
}
