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
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
)

const (
	// ResourceNamePrefix is the prefix for HSM device resources
	ResourceNamePrefix = "hsm.j5t.io"
	// DevicePluginPath is the standard path for device plugins
	DevicePluginPath = "/var/lib/kubelet/device-plugins/"
	// KubeletSocket is the registration socket path
	KubeletSocket = "/var/lib/kubelet/plugins_registry/kubelet.sock"
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
// It implements the Kubernetes Device Plugin API
type HSMDeviceManager struct {
	logger       logr.Logger
	resourceName string
	deviceType   hsmv1alpha1.HSMDeviceType
	devices      map[string]*Device
	devicesMutex sync.RWMutex

	// Device Plugin fields
	socket string
	server *grpc.Server
	stop   chan struct{}
	health chan *pluginapi.Device
	ctx    context.Context
	cancel context.CancelFunc
}

// NewHSMDeviceManager creates a new HSM device manager
func NewHSMDeviceManager(deviceType hsmv1alpha1.HSMDeviceType, resourceName string) *HSMDeviceManager {
	ctx, cancel := context.WithCancel(context.Background())
	fullResourceName := fmt.Sprintf("%s/%s", ResourceNamePrefix, strings.ToLower(string(deviceType)))
	socketName := fmt.Sprintf("%s.sock", strings.ReplaceAll(fullResourceName, "/", "_"))
	socketPath := filepath.Join(DevicePluginPath, socketName)

	return &HSMDeviceManager{
		logger:       ctrl.Log.WithName("hsm-device-manager").WithValues("deviceType", deviceType),
		resourceName: fullResourceName,
		deviceType:   deviceType,
		devices:      make(map[string]*Device),
		socket:       socketPath,
		stop:         make(chan struct{}),
		health:       make(chan *pluginapi.Device),
		ctx:          ctx,
		cancel:       cancel,
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
	return m.resourceName
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

// Start starts the device plugin service
func (m *HSMDeviceManager) Start() error {
	m.logger.Info("Starting HSM device plugin", "socket", m.socket, "resourceName", m.resourceName)

	// Remove existing socket if it exists
	if err := os.Remove(m.socket); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %v", err)
	}

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(m.socket), 0755); err != nil {
		return fmt.Errorf("failed to create socket directory: %v", err)
	}

	// Start gRPC server
	lis, err := net.Listen("unix", m.socket)
	if err != nil {
		return fmt.Errorf("failed to listen on socket %s: %v", m.socket, err)
	}

	m.server = grpc.NewServer()
	pluginapi.RegisterDevicePluginServer(m.server, m)

	go func() {
		defer func() {
			if err := lis.Close(); err != nil {
				m.logger.Error(err, "Failed to close listener")
			}
		}()
		if err := m.server.Serve(lis); err != nil {
			m.logger.Error(err, "Device plugin server error")
		}
	}()

	// Register with kubelet
	if err := m.register(); err != nil {
		return fmt.Errorf("failed to register with kubelet: %v", err)
	}

	m.logger.Info("HSM device plugin started successfully")
	return nil
}

// Stop stops the device plugin service
func (m *HSMDeviceManager) Stop() {
	m.logger.Info("Stopping HSM device plugin")
	m.cancel()
	close(m.stop)

	if m.server != nil {
		m.server.Stop()
	}

	if err := os.Remove(m.socket); err != nil && !os.IsNotExist(err) {
		m.logger.Error(err, "Failed to remove socket")
	}
}

// Device Plugin API Implementation

// GetDevicePluginOptions returns device plugin options
func (m *HSMDeviceManager) GetDevicePluginOptions(ctx context.Context, empty *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{
		PreStartRequired:                false,
		GetPreferredAllocationAvailable: false,
	}, nil
}

// ListAndWatch returns a stream of List of Devices
func (m *HSMDeviceManager) ListAndWatch(empty *pluginapi.Empty, server pluginapi.DevicePlugin_ListAndWatchServer) error {
	m.logger.Info("Starting ListAndWatch")

	for {
		select {
		case <-m.stop:
			m.logger.Info("ListAndWatch stopped")
			return nil
		case <-m.health:
			// Send updated device list on health changes
			devices := m.getPluginDevices()
			if err := server.Send(&pluginapi.ListAndWatchResponse{Devices: devices}); err != nil {
				m.logger.Error(err, "Failed to send device list")
				return err
			}
		default:
			// Send initial device list
			devices := m.getPluginDevices()
			if err := server.Send(&pluginapi.ListAndWatchResponse{Devices: devices}); err != nil {
				m.logger.Error(err, "Failed to send device list")
				return err
			}

			// Wait before next update
			time.Sleep(30 * time.Second)
		}
	}
}

// Allocate is called during pod creation
func (m *HSMDeviceManager) Allocate(ctx context.Context, req *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	m.logger.Info("Allocate called", "requests", len(req.ContainerRequests))

	responses := &pluginapi.AllocateResponse{}

	for _, containerReq := range req.ContainerRequests {
		containerResp := &pluginapi.ContainerAllocateResponse{}

		// For each requested device, provide the device path
		for _, deviceID := range containerReq.DevicesIDs {
			if device, exists := m.GetDevice(deviceID); exists {
				// Mount the device into the container
				containerResp.Devices = append(containerResp.Devices, &pluginapi.DeviceSpec{
					ContainerPath: device.DevicePath,
					HostPath:      device.DevicePath,
					Permissions:   "rw",
				})

				// Add environment variables
				if containerResp.Envs == nil {
					containerResp.Envs = make(map[string]string)
				}
				containerResp.Envs[fmt.Sprintf("HSM_DEVICE_%s", strings.ToUpper(deviceID))] = device.DevicePath
				containerResp.Envs[fmt.Sprintf("HSM_SERIAL_%s", strings.ToUpper(deviceID))] = device.SerialNumber

				m.logger.Info("Allocated device", "deviceID", deviceID, "path", device.DevicePath)
			}
		}

		responses.ContainerResponses = append(responses.ContainerResponses, containerResp)
	}

	return responses, nil
}

// GetPreferredAllocation returns preferred allocation
func (m *HSMDeviceManager) GetPreferredAllocation(ctx context.Context, req *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	return &pluginapi.PreferredAllocationResponse{}, nil
}

// PreStartContainer is called before each container start
func (m *HSMDeviceManager) PreStartContainer(ctx context.Context, req *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

// Helper methods

// register registers the device plugin with kubelet
func (m *HSMDeviceManager) register() error {
	// Try different kubelet registration socket paths (different k8s versions use different paths)
	registrationSockets := []string{
		"/var/lib/kubelet/plugins_registry/kubelet.sock", // Kubernetes 1.11+
		"/var/lib/kubelet/plugins/kubelet.sock",          // Older Kubernetes
		filepath.Join(DevicePluginPath, "kubelet.sock"),  // Legacy path (shouldn't exist but try anyway)
	}

	var conn *grpc.ClientConn
	var err error

	for _, socket := range registrationSockets {
		m.logger.V(1).Info("Trying kubelet registration socket", "path", socket)
		conn, err = grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			// Try to actually connect to verify the socket works
			client := pluginapi.NewRegistrationClient(conn)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			// Test the connection with a simple RPC call
			req := &pluginapi.RegisterRequest{
				Version:      pluginapi.Version,
				Endpoint:     filepath.Base(m.socket),
				ResourceName: m.resourceName,
				Options:      &pluginapi.DevicePluginOptions{PreStartRequired: false},
			}

			_, err = client.Register(ctx, req)
			if err == nil {
				m.logger.Info("Successfully registered with kubelet", "socket", socket)
				if closeErr := conn.Close(); closeErr != nil {
					m.logger.Error(closeErr, "Failed to close connection after successful registration")
				}
				return nil
			}
			if closeErr := conn.Close(); closeErr != nil {
				m.logger.Error(closeErr, "Failed to close connection after failed registration")
			}
			m.logger.V(1).Info("Registration failed on socket", "socket", socket, "error", err)
		}
	}

	return fmt.Errorf("failed to register with kubelet on any socket path: %v", err)
}

// getPluginDevices converts internal devices to plugin API devices
func (m *HSMDeviceManager) getPluginDevices() []*pluginapi.Device {
	m.devicesMutex.RLock()
	defer m.devicesMutex.RUnlock()

	pluginDevices := make([]*pluginapi.Device, 0, len(m.devices))

	for _, device := range m.devices {
		health := pluginapi.Unhealthy
		if device.Available {
			health = pluginapi.Healthy
		}

		pluginDevices = append(pluginDevices, &pluginapi.Device{
			ID:     device.ID,
			Health: health,
		})
	}

	m.logger.V(1).Info("Generated plugin devices", "count", len(pluginDevices))
	return pluginDevices
}
