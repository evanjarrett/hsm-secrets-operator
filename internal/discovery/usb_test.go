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

	hsmv1alpha1 "tangled.org/evan.jarrett.net/hsm-secrets-operator/api/v1alpha1"
)

func TestNewUSBDiscoverer(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	assert.NotNil(t, discoverer)
	assert.NotNil(t, discoverer.udev)
	assert.NotNil(t, discoverer.eventChannel)
	assert.NotNil(t, discoverer.activeSpecs)
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

func TestMatchesCriteria(t *testing.T) {
	discoverer := NewUSBDiscoverer()
	// Stub a deterministic allowlist (20a0:4230 is the masquerade Nitrokey ID).
	discoverer.allowlist = &CCIDAllowlist{pairs: map[string]bool{"20a0:4230": true}}

	pico := USBDevice{VendorID: "20a0", ProductID: "4230", SerialNumber: "AAAA"}
	picoNew := USBDevice{VendorID: "2e8a", ProductID: "10fd", SerialNumber: "BBBB"}
	picoNew2 := USBDevice{VendorID: "2e8a", ProductID: "10fd", SerialNumber: "CCCC"}

	tests := []struct {
		name     string
		device   USBDevice
		criteria MatchCriteria
		want     bool
	}{
		{
			name:     "autoDiscovery hits allowlist",
			device:   pico,
			criteria: MatchCriteria{AutoDiscovery: true},
			want:     true,
		},
		{
			name:     "autoDiscovery misses non-allowlisted device",
			device:   picoNew,
			criteria: MatchCriteria{AutoDiscovery: true},
			want:     false,
		},
		{
			name:   "explicit spec overrides allowlist (bridge new firmware)",
			device: picoNew,
			criteria: MatchCriteria{
				Specs: []hsmv1alpha1.USBDeviceSpec{{VendorID: "2e8a", ProductID: "10fd"}},
			},
			want: true,
		},
		{
			name:   "union: auto for fleet plus explicit bridge",
			device: picoNew,
			criteria: MatchCriteria{
				AutoDiscovery: true,
				Specs:         []hsmv1alpha1.USBDeviceSpec{{VendorID: "2e8a", ProductID: "10fd"}},
			},
			want: true,
		},
		{
			name:   "serial cherry-pick selects exactly one of two same-model devices",
			device: picoNew,
			criteria: MatchCriteria{
				Specs: []hsmv1alpha1.USBDeviceSpec{{VendorID: "2e8a", ProductID: "10fd", SerialNumber: "BBBB"}},
			},
			want: true,
		},
		{
			name:   "serial cherry-pick rejects the other same-model device",
			device: picoNew2,
			criteria: MatchCriteria{
				Specs: []hsmv1alpha1.USBDeviceSpec{{VendorID: "2e8a", ProductID: "10fd", SerialNumber: "BBBB"}},
			},
			want: false,
		},
		{
			name:     "empty criteria matches nothing",
			device:   pico,
			criteria: MatchCriteria{},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, discoverer.matchesCriteria(tt.device, tt.criteria))
		})
	}
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

	criteria := MatchCriteria{
		AutoDiscovery: true,
		Specs:         []hsmv1alpha1.USBDeviceSpec{{VendorID: "20a0", ProductID: "4230"}},
	}

	// Test adding criteria
	discoverer.AddSpecForMonitoring("test-device", criteria)
	assert.Equal(t, criteria, discoverer.activeSpecs["test-device"])
	assert.Equal(t, []string{"test-device"}, discoverer.MonitoredDeviceNames())

	// Test removing criteria
	discoverer.RemoveSpecFromMonitoring("test-device")
	_, exists := discoverer.activeSpecs["test-device"]
	assert.False(t, exists)
	assert.Empty(t, discoverer.MonitoredDeviceNames())
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
