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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
)

func TestNewHSMDeviceManager(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")

	assert.NotNil(t, manager)
	assert.Equal(t, hsmv1alpha1.HSMDeviceTypePicoHSM, manager.deviceType)
	assert.Equal(t, "hsm.j5t.io/picohsm", manager.resourceName)
	assert.NotEmpty(t, manager.socket)
	assert.NotNil(t, manager.devices)
	assert.NotNil(t, manager.stop)
	assert.NotNil(t, manager.health)
	assert.NotNil(t, manager.ctx)
}

func TestDevice(t *testing.T) {
	device := &Device{
		ID:           "test-device-1",
		DevicePath:   "/dev/ttyUSB0",
		SerialNumber: "TEST123",
		Available:    true,
		NodeName:     "worker-1",
		DeviceInfo: map[string]string{
			"vendor_id": "20a0",
		},
	}

	assert.Equal(t, "test-device-1", device.ID)
	assert.Equal(t, "/dev/ttyUSB0", device.DevicePath)
	assert.Equal(t, "TEST123", device.SerialNumber)
	assert.True(t, device.Available)
	assert.Equal(t, "worker-1", device.NodeName)
	assert.Equal(t, "20a0", device.DeviceInfo["vendor_id"])
}

func TestUpdateDevices(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")

	discoveredDevices := []hsmv1alpha1.DiscoveredDevice{
		{
			DevicePath:   "/dev/ttyUSB0",
			SerialNumber: "TEST123",
			Available:    true,
			NodeName:     "worker-1",
			DeviceInfo: map[string]string{
				"vendor_id": "20a0",
			},
		},
		{
			DevicePath:   "/dev/ttyUSB1",
			SerialNumber: "TEST456",
			Available:    false,
			NodeName:     "worker-2",
			DeviceInfo: map[string]string{
				"vendor_id": "20a0",
			},
		},
	}

	manager.UpdateDevices(discoveredDevices)

	// Check that devices were updated
	assert.Len(t, manager.devices, 2)

	// Check first device
	deviceID1 := manager.generateDeviceID(discoveredDevices[0])
	device1, exists := manager.GetDevice(deviceID1)
	require.True(t, exists)
	assert.Equal(t, "/dev/ttyUSB0", device1.DevicePath)
	assert.Equal(t, "TEST123", device1.SerialNumber)
	assert.True(t, device1.Available)
	assert.Equal(t, "worker-1", device1.NodeName)

	// Check second device
	deviceID2 := manager.generateDeviceID(discoveredDevices[1])
	device2, exists := manager.GetDevice(deviceID2)
	require.True(t, exists)
	assert.Equal(t, "/dev/ttyUSB1", device2.DevicePath)
	assert.Equal(t, "TEST456", device2.SerialNumber)
	assert.False(t, device2.Available)
	assert.Equal(t, "worker-2", device2.NodeName)
}

func TestGetAvailableDevices(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")

	discoveredDevices := []hsmv1alpha1.DiscoveredDevice{
		{
			DevicePath:   "/dev/ttyUSB0",
			SerialNumber: "TEST123",
			Available:    true,
			NodeName:     "worker-1",
		},
		{
			DevicePath:   "/dev/ttyUSB1",
			SerialNumber: "TEST456",
			Available:    false,
			NodeName:     "worker-1",
		},
		{
			DevicePath:   "/dev/ttyUSB2",
			SerialNumber: "TEST789",
			Available:    true,
			NodeName:     "worker-2",
		},
	}

	manager.UpdateDevices(discoveredDevices)

	availableDevices := manager.GetAvailableDevices()
	assert.Len(t, availableDevices, 2)

	// Check that only available devices are returned
	for _, device := range availableDevices {
		assert.True(t, device.Available)
	}
}

func TestGetDevice(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")

	discoveredDevice := hsmv1alpha1.DiscoveredDevice{
		DevicePath:   "/dev/ttyUSB0",
		SerialNumber: "TEST123",
		Available:    true,
		NodeName:     "worker-1",
	}

	manager.UpdateDevices([]hsmv1alpha1.DiscoveredDevice{discoveredDevice})

	deviceID := manager.generateDeviceID(discoveredDevice)

	// Test existing device
	device, exists := manager.GetDevice(deviceID)
	assert.True(t, exists)
	assert.Equal(t, deviceID, device.ID)

	// Test non-existent device
	_, exists = manager.GetDevice("non-existent")
	assert.False(t, exists)
}

func TestGetDevicesForNode(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")

	discoveredDevices := []hsmv1alpha1.DiscoveredDevice{
		{
			DevicePath:   "/dev/ttyUSB0",
			SerialNumber: "TEST123",
			Available:    true,
			NodeName:     "worker-1",
		},
		{
			DevicePath:   "/dev/ttyUSB1",
			SerialNumber: "TEST456",
			Available:    false,
			NodeName:     "worker-1",
		},
		{
			DevicePath:   "/dev/ttyUSB2",
			SerialNumber: "TEST789",
			Available:    true,
			NodeName:     "worker-2",
		},
	}

	manager.UpdateDevices(discoveredDevices)

	// Get devices for worker-1
	worker1Devices := manager.GetDevicesForNode("worker-1")
	assert.Len(t, worker1Devices, 2)
	for _, device := range worker1Devices {
		assert.Equal(t, "worker-1", device.NodeName)
	}

	// Get devices for worker-2
	worker2Devices := manager.GetDevicesForNode("worker-2")
	assert.Len(t, worker2Devices, 1)
	assert.Equal(t, "worker-2", worker2Devices[0].NodeName)

	// Get devices for non-existent node
	nonExistentDevices := manager.GetDevicesForNode("non-existent")
	assert.Empty(t, nonExistentDevices)
}

func TestGetResourceName(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")
	assert.Equal(t, "hsm.j5t.io/picohsm", manager.GetResourceName())

	manager2 := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypeSmartCardHSM, "smartcard-hsm")
	assert.Equal(t, "hsm.j5t.io/smartcard-hsm", manager2.GetResourceName())
}

func TestGenerateDeviceID(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")

	tests := []struct {
		name     string
		device   hsmv1alpha1.DiscoveredDevice
		expected string
	}{
		{
			name: "device with serial",
			device: hsmv1alpha1.DiscoveredDevice{
				NodeName:     "worker-1",
				DevicePath:   "/dev/ttyUSB0",
				SerialNumber: "TEST123",
			},
			expected: "worker-1-_dev_ttyUSB0-TEST123",
		},
		{
			name: "device without serial",
			device: hsmv1alpha1.DiscoveredDevice{
				NodeName:   "worker-2",
				DevicePath: "/dev/ttyUSB1",
			},
			expected: "worker-2-_dev_ttyUSB1",
		},
		{
			name: "device with complex path",
			device: hsmv1alpha1.DiscoveredDevice{
				NodeName:     "worker-3",
				DevicePath:   "/dev/bus/usb/001/002",
				SerialNumber: "ABC123",
			},
			expected: "worker-3-_dev_bus_usb_001_002-ABC123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.generateDeviceID(tt.device)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetDevicePluginOptions(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")
	ctx := context.Background()

	options, err := manager.GetDevicePluginOptions(ctx, &pluginapi.Empty{})
	require.NoError(t, err)

	assert.NotNil(t, options)
	assert.False(t, options.PreStartRequired)
	assert.False(t, options.GetPreferredAllocationAvailable)
}

func TestGetPluginDevices(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")

	discoveredDevices := []hsmv1alpha1.DiscoveredDevice{
		{
			DevicePath:   "/dev/ttyUSB0",
			SerialNumber: "TEST123",
			Available:    true,
			NodeName:     "worker-1",
		},
		{
			DevicePath:   "/dev/ttyUSB1",
			SerialNumber: "TEST456",
			Available:    false,
			NodeName:     "worker-1",
		},
	}

	manager.UpdateDevices(discoveredDevices)

	pluginDevices := manager.getPluginDevices()
	assert.Len(t, pluginDevices, 2)

	// Find the healthy device
	var healthyDevice, unhealthyDevice *pluginapi.Device
	for _, device := range pluginDevices {
		switch device.Health {
		case pluginapi.Healthy:
			healthyDevice = device
		case pluginapi.Unhealthy:
			unhealthyDevice = device
		}
	}

	require.NotNil(t, healthyDevice)
	require.NotNil(t, unhealthyDevice)

	assert.Equal(t, pluginapi.Healthy, healthyDevice.Health)
	assert.Equal(t, pluginapi.Unhealthy, unhealthyDevice.Health)
}

func TestAllocate(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")
	ctx := context.Background()

	// Set up devices
	discoveredDevice := hsmv1alpha1.DiscoveredDevice{
		DevicePath:   "/dev/ttyUSB0",
		SerialNumber: "TEST123",
		Available:    true,
		NodeName:     "worker-1",
	}
	manager.UpdateDevices([]hsmv1alpha1.DiscoveredDevice{discoveredDevice})

	deviceID := manager.generateDeviceID(discoveredDevice)

	// Create allocation request
	req := &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{
			{
				DevicesIDs: []string{deviceID},
			},
		},
	}

	resp, err := manager.Allocate(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.ContainerResponses, 1)

	containerResp := resp.ContainerResponses[0]
	require.Len(t, containerResp.Devices, 1)

	deviceSpec := containerResp.Devices[0]
	assert.Equal(t, "/dev/ttyUSB0", deviceSpec.ContainerPath)
	assert.Equal(t, "/dev/ttyUSB0", deviceSpec.HostPath)
	assert.Equal(t, "rw", deviceSpec.Permissions)

	// Check environment variables
	expectedEnvKey1 := "HSM_DEVICE_" + "WORKER-1-_DEV_TTYUSB0-TEST123"
	expectedEnvKey2 := "HSM_SERIAL_" + "WORKER-1-_DEV_TTYUSB0-TEST123"

	assert.Equal(t, "/dev/ttyUSB0", containerResp.Envs[expectedEnvKey1])
	assert.Equal(t, "TEST123", containerResp.Envs[expectedEnvKey2])
}

func TestAllocateNonExistentDevice(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")
	ctx := context.Background()

	// Create allocation request for non-existent device
	req := &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{
			{
				DevicesIDs: []string{"non-existent-device"},
			},
		},
	}

	resp, err := manager.Allocate(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.ContainerResponses, 1)

	containerResp := resp.ContainerResponses[0]
	// Should have no devices allocated
	assert.Empty(t, containerResp.Devices)
}

func TestGetPreferredAllocation(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")
	ctx := context.Background()

	req := &pluginapi.PreferredAllocationRequest{}
	resp, err := manager.GetPreferredAllocation(ctx, req)

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestPreStartContainer(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")
	ctx := context.Background()

	req := &pluginapi.PreStartContainerRequest{}
	resp, err := manager.PreStartContainer(ctx, req)

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestManagerStop(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")

	// Start a context that should be cancelled
	select {
	case <-manager.ctx.Done():
		t.Error("Context should not be done initially")
	case <-time.After(10 * time.Millisecond):
		// Good, context is not done
	}

	// Stop the manager
	manager.Stop()

	// Context should now be cancelled
	select {
	case <-manager.ctx.Done():
		// Good, context was cancelled
	case <-time.After(100 * time.Millisecond):
		t.Error("Context should have been cancelled after Stop()")
	}

	// Stop channel should be closed
	select {
	case <-manager.stop:
		// Good, stop channel was closed
	case <-time.After(100 * time.Millisecond):
		t.Error("Stop channel should have been closed after Stop()")
	}
}

func TestUpdateDevicesRaceCondition(t *testing.T) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")

	// Simulate concurrent access
	done := make(chan bool, 2)

	// Goroutine 1: Update devices
	go func() {
		for i := 0; i < 100; i++ {
			discoveredDevices := []hsmv1alpha1.DiscoveredDevice{
				{
					DevicePath:   "/dev/ttyUSB0",
					SerialNumber: "TEST123",
					Available:    true,
					NodeName:     "worker-1",
				},
			}
			manager.UpdateDevices(discoveredDevices)
		}
		done <- true
	}()

	// Goroutine 2: Read devices
	go func() {
		for i := 0; i < 100; i++ {
			manager.GetAvailableDevices()
			manager.GetDevicesForNode("worker-1")
		}
		done <- true
	}()

	// Wait for both goroutines to complete
	<-done
	<-done

	// Should not panic due to race conditions
}

// Benchmark tests
func BenchmarkUpdateDevices(b *testing.B) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")

	discoveredDevices := []hsmv1alpha1.DiscoveredDevice{
		{
			DevicePath:   "/dev/ttyUSB0",
			SerialNumber: "TEST123",
			Available:    true,
			NodeName:     "worker-1",
		},
		{
			DevicePath:   "/dev/ttyUSB1",
			SerialNumber: "TEST456",
			Available:    false,
			NodeName:     "worker-2",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.UpdateDevices(discoveredDevices)
	}
}

func BenchmarkGetAvailableDevices(b *testing.B) {
	manager := NewHSMDeviceManager(hsmv1alpha1.HSMDeviceTypePicoHSM, "pico-hsm")

	// Set up test data
	discoveredDevices := make([]hsmv1alpha1.DiscoveredDevice, 100)
	for i := 0; i < 100; i++ {
		discoveredDevices[i] = hsmv1alpha1.DiscoveredDevice{
			DevicePath:   "/dev/ttyUSB0",
			SerialNumber: "TEST123",
			Available:    i%2 == 0, // Half available, half not
			NodeName:     "worker-1",
		}
	}
	manager.UpdateDevices(discoveredDevices)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.GetAvailableDevices()
	}
}
