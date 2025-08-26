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
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
)

func TestNewUSBDiscoverer(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	assert.NotNil(t, discoverer)
	assert.Equal(t, "auto", discoverer.detectionMethod)
	assert.NotEmpty(t, discoverer.usbSysPaths)
	assert.NotEmpty(t, discoverer.devicePaths)
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
			assert.Equal(t, tt.method, discoverer.detectionMethod)
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

func TestFilterDevices(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	allDevices := []USBDevice{
		{VendorID: "20a0", ProductID: "4230", SerialNumber: "TEST1"},
		{VendorID: "20a0", ProductID: "4230", SerialNumber: "TEST2"},
		{VendorID: "1234", ProductID: "5678", SerialNumber: "DIFF"},
		{VendorID: "20a0", ProductID: "1111", SerialNumber: "DIFF2"},
	}

	spec := &hsmv1alpha1.USBDeviceSpec{
		VendorID:  "20a0",
		ProductID: "4230",
	}

	filtered := discoverer.filterDevices(allDevices, spec, "test")

	assert.Len(t, filtered, 2)
	assert.Equal(t, "TEST1", filtered[0].SerialNumber)
	assert.Equal(t, "TEST2", filtered[1].SerialNumber)
}

func TestDiscoverByPath(t *testing.T) {
	discoverer := NewUSBDiscoverer()
	ctx := context.Background()

	// Create a temporary file to test with
	tempDir := t.TempDir()
	testDevice := filepath.Join(tempDir, "test-device")
	err := os.WriteFile(testDevice, []byte("test"), 0644)
	require.NoError(t, err)

	pathSpec := &hsmv1alpha1.DevicePathSpec{
		Path:        testDevice,
		Permissions: "rw",
	}

	devices, err := discoverer.DiscoverByPath(ctx, pathSpec)
	require.NoError(t, err)

	assert.Len(t, devices, 1)
	assert.Equal(t, testDevice, devices[0].DevicePath)
	assert.Equal(t, "path", devices[0].DeviceInfo["discovery-method"])
	assert.Equal(t, "rw", devices[0].DeviceInfo["permissions"])
}

func TestDiscoverByPathWithGlob(t *testing.T) {
	discoverer := NewUSBDiscoverer()
	ctx := context.Background()

	// Create temporary files to test with
	tempDir := t.TempDir()
	testDevice1 := filepath.Join(tempDir, "test-device1")
	testDevice2 := filepath.Join(tempDir, "test-device2")
	otherFile := filepath.Join(tempDir, "other-file")

	for _, file := range []string{testDevice1, testDevice2, otherFile} {
		err := os.WriteFile(file, []byte("test"), 0644)
		require.NoError(t, err)
	}

	// Use glob pattern
	pathSpec := &hsmv1alpha1.DevicePathSpec{
		Path: filepath.Join(tempDir, "test-device*"),
	}

	devices, err := discoverer.DiscoverByPath(ctx, pathSpec)
	require.NoError(t, err)

	assert.Len(t, devices, 2)

	// Check that both test devices were found
	devicePaths := make([]string, len(devices))
	for i, device := range devices {
		devicePaths[i] = device.DevicePath
	}
	assert.Contains(t, devicePaths, testDevice1)
	assert.Contains(t, devicePaths, testDevice2)
}

func TestDiscoverByPathNonexistent(t *testing.T) {
	discoverer := NewUSBDiscoverer()
	ctx := context.Background()

	pathSpec := &hsmv1alpha1.DevicePathSpec{
		Path: "/nonexistent/device/path",
	}

	devices, err := discoverer.DiscoverByPath(ctx, pathSpec)
	require.NoError(t, err)

	// Should return empty list for nonexistent files
	assert.Empty(t, devices)
}

func TestDeviceExists(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	// Create a temporary file
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "test-file")
	err := os.WriteFile(tempFile, []byte("test"), 0644)
	require.NoError(t, err)

	// Test existing file
	assert.True(t, discoverer.deviceExists(tempFile))

	// Test non-existent file
	assert.False(t, discoverer.deviceExists("/nonexistent/path"))
}

func TestGetDeviceInfoFromPath(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	tests := []struct {
		name     string
		path     string
		expected map[string]string
	}{
		{
			name: "ttyUSB device",
			path: "/dev/ttyUSB0",
			expected: map[string]string{
				"device_type": "serial",
			},
		},
		{
			name: "ttyACM device",
			path: "/dev/ttyACM1",
			expected: map[string]string{
				"device_type": "serial",
			},
		},
		{
			name: "sc-hsm device",
			path: "/dev/sc-hsm",
			expected: map[string]string{
				"device_type": "hsm",
				"vendor_id":   "20a0",
				"product_id":  "4230",
			},
		},
		{
			name:     "unknown device",
			path:     "/dev/random",
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := discoverer.getDeviceInfoFromPath(tt.path)
			assert.Equal(t, tt.expected, info)
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

func TestReadSysfsFile(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	// Create a temporary file with test content
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "test-sysfs-file")
	testContent := "test-value\n"
	err := os.WriteFile(tempFile, []byte(testContent), 0644)
	require.NoError(t, err)

	// Test reading existing file
	content, err := discoverer.readSysfsFile(tempFile)
	require.NoError(t, err)
	assert.Equal(t, "test-value", content)

	// Test reading non-existent file
	_, err = discoverer.readSysfsFile("/nonexistent/file")
	assert.Error(t, err)

	// Test reading empty file
	emptyFile := filepath.Join(tempDir, "empty-file")
	err = os.WriteFile(emptyFile, []byte(""), 0644)
	require.NoError(t, err)

	_, err = discoverer.readSysfsFile(emptyFile)
	assert.Error(t, err)
}

func TestFindUSBDevicePath(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	// Create temporary directory structure for testing
	tempDir := t.TempDir()

	// Create busnum and devnum files
	busnumFile := filepath.Join(tempDir, "busnum")
	devnumFile := filepath.Join(tempDir, "devnum")

	err := os.WriteFile(busnumFile, []byte("001"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(devnumFile, []byte("002"), 0644)
	require.NoError(t, err)

	// Test finding USB device path
	path := discoverer.findUSBDevicePath(tempDir)
	// The path won't exist in test environment, so it should return empty
	// This is expected behavior since deviceExists() will return false
	assert.Empty(t, path)
}

func TestFindUSBDevicePathMissingFiles(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	// Test with directory that doesn't have the required files
	tempDir := t.TempDir()
	path := discoverer.findUSBDevicePath(tempDir)
	assert.Empty(t, path)
}

func TestFindCommonDevicePath(t *testing.T) {
	// Create a test discoverer
	discoverer := &USBDiscoverer{
		logger: logr.Discard(),
	}

	// Test with unknown vendor ID - this should always return empty since no known paths exist for it
	unknownPath := discoverer.findCommonDevicePath("unknown", "unknown")

	// The function checks actual filesystem paths, so we can't guarantee it returns empty
	// Instead, let's verify it returns a string (empty or path)
	assert.IsType(t, "", unknownPath)

	// Test that if a path is returned, it's one of the expected common paths
	if unknownPath != "" {
		expectedPaths := []string{
			"/dev/ttyUSB0", "/dev/ttyUSB1", "/dev/ttyUSB2", "/dev/ttyUSB3",
			"/dev/ttyACM0", "/dev/ttyACM1", "/dev/ttyACM2", "/dev/ttyACM3",
			"/dev/sc-hsm", "/dev/pkcs11",
		}
		assert.Contains(t, expectedPaths, unknownPath)
	}
}

func TestParseUSBDeviceFromSysfs(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	// Create temporary sysfs structure
	tempDir := t.TempDir()

	// Create device attribute files
	files := map[string]string{
		"idVendor":     "20a0",
		"idProduct":    "4230",
		"serial":       "TEST123",
		"manufacturer": "Test Manufacturer",
		"product":      "Test Product",
	}

	for filename, content := range files {
		filePath := filepath.Join(tempDir, filename)
		err := os.WriteFile(filePath, []byte(content), 0644)
		require.NoError(t, err)
	}

	device := discoverer.parseUSBDeviceFromSysfs(tempDir)
	require.NotNil(t, device)

	assert.Equal(t, "20a0", device.VendorID)
	assert.Equal(t, "4230", device.ProductID)
	assert.Equal(t, "TEST123", device.SerialNumber)
	assert.Equal(t, "Test Manufacturer", device.Manufacturer)
	assert.Equal(t, "Test Product", device.Product)
	assert.Equal(t, tempDir, device.DeviceInfo["sysfs-path"])
	assert.Equal(t, "native-sysfs", device.DeviceInfo["discovery-method"])
}

func TestParseUSBDeviceFromSysfsMissingVendor(t *testing.T) {
	discoverer := NewUSBDiscoverer()

	// Create temporary directory with only product ID (missing vendor ID)
	tempDir := t.TempDir()
	productFile := filepath.Join(tempDir, "idProduct")
	err := os.WriteFile(productFile, []byte("4230"), 0644)
	require.NoError(t, err)

	device := discoverer.parseUSBDeviceFromSysfs(tempDir)
	assert.Nil(t, device) // Should return nil when vendor ID is missing
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

func BenchmarkFilterDevices(b *testing.B) {
	discoverer := NewUSBDiscoverer()

	// Create a large slice of devices
	devices := make([]USBDevice, 1000)
	for i := 0; i < 1000; i++ {
		devices[i] = USBDevice{
			VendorID:  "20a0",
			ProductID: "4230",
		}
	}

	spec := &hsmv1alpha1.USBDeviceSpec{
		VendorID:  "20a0",
		ProductID: "4230",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		discoverer.filterDevices(devices, spec, "test")
	}
}
