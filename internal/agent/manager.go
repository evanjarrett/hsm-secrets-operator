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

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// ManagerInterface defines the interface for HSM agent management
// This allows for easier testing with mocks
type ManagerInterface interface {
	EnsureAgent(ctx context.Context, hsmPool *hsmv1alpha1.HSMPool) error
	CleanupAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error
}

// AgentStatus represents the current status of an agent
type AgentStatus string

const (
	AgentStatusCreating AgentStatus = "Creating"
	AgentStatusReady    AgentStatus = "Ready"
	AgentStatusFailed   AgentStatus = "Failed"
)

// AgentInfo tracks agent state and connections
type AgentInfo struct {
	PodIPs          []string
	CreatedAt       time.Time
	LastHealthCheck time.Time
	Status          AgentStatus
	AgentName       string
	Namespace       string
}

const (
	// AgentNamePrefix is the prefix for HSM agent deployment names
	AgentNamePrefix = "hsm-agent"

	// AgentPort is the port the HSM agent serves on (now gRPC)
	AgentPort = 9090

	// AgentHealthPort is the port for health checks (HTTP for simplicity)
	AgentHealthPort = 8093
)

// Manager handles HSM agent pod lifecycle
type Manager struct {
	client.Client
	AgentImage     string
	AgentNamespace string
	ImageResolver  ImageResolver
	logger         logr.Logger

	// Internal tracking
	activeAgents   map[string]*AgentInfo // deviceName -> AgentInfo
	connectionPool *ConnectionPool       // Shared connection pool for gRPC clients
	mu             sync.RWMutex

	// Test configuration
	TestMode         bool          // Enable test mode for faster operations
	WaitTimeout      time.Duration // Timeout for waiting operations (default: 60s)
	WaitPollInterval time.Duration // Polling interval for waiting operations (default: 2s)
}

// ImageResolver interface for dependency injection
type ImageResolver interface {
	GetImage(ctx context.Context, imageName string) string
}

// deviceWork represents work to be done for a specific device
type deviceWork struct {
	device    hsmv1alpha1.DiscoveredDevice
	agentName string
	agentKey  string
	index     int
}

// NewManager creates a new agent manager
func NewManager(k8sClient client.Client, namespace string, agentImage string, imageResolver ImageResolver) *Manager {
	// Create logger for the manager
	logger := logr.FromContextOrDiscard(context.Background()).WithName("agent-manager")

	m := &Manager{
		Client:         k8sClient,
		AgentImage:     agentImage,
		AgentNamespace: namespace,
		ImageResolver:  imageResolver,
		logger:         logger,
		activeAgents:   make(map[string]*AgentInfo),
		connectionPool: NewConnectionPool(logger),
		// Default production timeouts
		WaitTimeout:      60 * time.Second,
		WaitPollInterval: 2 * time.Second,
	}

	return m
}

// NewTestManager creates a new agent manager optimized for testing
func NewTestManager(k8sClient client.Client, namespace string, agentImage string, imageResolver ImageResolver) *Manager {
	// Create logger for the test manager
	logger := logr.FromContextOrDiscard(context.Background()).WithName("agent-manager-test")

	m := &Manager{
		Client:         k8sClient,
		AgentImage:     agentImage,
		AgentNamespace: namespace,
		ImageResolver:  imageResolver,
		logger:         logger,
		activeAgents:   make(map[string]*AgentInfo),
		connectionPool: NewConnectionPool(logger),
		// Fast test timeouts
		TestMode:         true,
		WaitTimeout:      5 * time.Second,
		WaitPollInterval: 100 * time.Millisecond,
	}
	return m
}

// generateAgentName creates a consistent agent name for an HSM device
func (m *Manager) generateAgentName(hsmPool *hsmv1alpha1.HSMPool) string {
	return fmt.Sprintf("%s-%s", AgentNamePrefix, hsmPool.OwnerReferences[0].Name)
}

// EnsureAgent discovers and tracks existing agent pods for all available devices in the pool
func (m *Manager) EnsureAgent(ctx context.Context, hsmPool *hsmv1alpha1.HSMPool) error {
	// Pre-collect available devices to process
	workItems := make([]deviceWork, 0, len(hsmPool.Status.AggregatedDevices))
	for i, aggregatedDevice := range hsmPool.Status.AggregatedDevices {
		if !aggregatedDevice.Available {
			continue
		}
		workItems = append(workItems, deviceWork{
			device:    aggregatedDevice,
			agentName: fmt.Sprintf("%s-%d", m.generateAgentName(hsmPool), i),
			agentKey:  fmt.Sprintf("%s-%s", hsmPool.OwnerReferences[0].Name, aggregatedDevice.SerialNumber),
			index:     i,
		})
	}

	if len(workItems) == 0 {
		return nil // No available devices to process
	}

	// Process devices to track their agents
	for _, work := range workItems {
		// Check if agent is already tracked and healthy
		m.mu.Lock()
		if agentInfo, exists := m.activeAgents[work.agentKey]; exists {
			if m.isAgentHealthy(ctx, agentInfo) {
				m.mu.Unlock()
				continue // Agent is healthy and tracked
			}
			// Remove unhealthy agent from tracking
			m.removeAgentFromTracking(work.agentKey)
		}
		m.mu.Unlock()

		// Try to discover and track the agent pod (created by controller)
		if err := m.discoverAndTrackAgent(ctx, work, hsmPool.Namespace); err != nil {
			// Agent pod doesn't exist yet or isn't ready - controller will create it
			continue
		}
	}

	return nil
}

// discoverAndTrackAgent finds an existing agent pod and tracks it
func (m *Manager) discoverAndTrackAgent(ctx context.Context, work deviceWork, namespace string) error {
	// Wait for agent pods to be ready and get their IPs
	podIPs, err := m.waitForAgentReady(ctx, work.agentName, namespace)
	if err != nil {
		return fmt.Errorf("agent pods not ready for %s: %w", work.agentName, err)
	}

	// Track the agent (mutex-protected)
	m.mu.Lock()
	agentInfo := &AgentInfo{
		PodIPs:          podIPs,
		CreatedAt:       time.Now(),
		LastHealthCheck: time.Now(),
		Status:          AgentStatusReady,
		AgentName:       work.agentName,
		Namespace:       namespace,
	}
	m.activeAgents[work.agentKey] = agentInfo
	m.mu.Unlock()

	return nil
}

// CleanupAgent removes tracking for all HSM agents for the given device
func (m *Manager) CleanupAgent(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find all tracked agents for this device and remove them from tracking
	var agentsToCleanup []string
	devicePrefix := hsmDevice.Name + "-"

	for agentKey := range m.activeAgents {
		if strings.HasPrefix(agentKey, devicePrefix) {
			agentsToCleanup = append(agentsToCleanup, agentKey)
		}
	}

	// Remove each tracked agent from internal state
	for _, agentKey := range agentsToCleanup {
		m.removeAgentFromTracking(agentKey)
	}

	return nil
}

// Helper functions
// waitForAgentReady waits for agent pods to be ready and returns their IPs
func (m *Manager) waitForAgentReady(ctx context.Context, agentName, namespace string) ([]string, error) {
	// In test mode, simulate immediate readiness for faster tests
	if m.TestMode {
		return []string{"127.0.0.1"}, nil
	}

	timeout := time.After(m.WaitTimeout)
	ticker := time.NewTicker(m.WaitPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for agent pods to be ready after %v", m.WaitTimeout)
		case <-ticker.C:
			pods := &corev1.PodList{}
			err := m.List(ctx, pods,
				client.InNamespace(namespace),
				client.MatchingLabels{"app": agentName},
			)
			if err != nil {
				continue
			}

			var readyPodIPs []string
			for _, pod := range pods.Items {
				if pod.Status.Phase == corev1.PodRunning &&
					len(pod.Status.PodIP) > 0 {
					// Check if all containers are ready
					allReady := true
					for _, condition := range pod.Status.Conditions {
						if condition.Type == corev1.PodReady {
							allReady = condition.Status == corev1.ConditionTrue
							break
						}
					}
					if allReady {
						readyPodIPs = append(readyPodIPs, pod.Status.PodIP)
					}
				}
			}

			if len(readyPodIPs) > 0 {
				return readyPodIPs, nil
			}
		}
	}
}

// GetAgentInfo returns the AgentInfo for a device
func (m *Manager) GetAgentInfo(deviceName string) (*AgentInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agentInfo, exists := m.activeAgents[deviceName]
	return agentInfo, exists
}

// removeAgentFromTracking removes an agent from internal tracking
func (m *Manager) removeAgentFromTracking(deviceName string) {
	delete(m.activeAgents, deviceName)
}

// RemoveAgentFromTracking removes an agent from tracking (public method for testing)
func (m *Manager) RemoveAgentFromTracking(deviceName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeAgentFromTracking(deviceName)
}

// SetAgentInfo sets agent information for testing
func (m *Manager) SetAgentInfo(deviceName string, info *AgentInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.activeAgents[deviceName] = info
}

// isAgentHealthy checks if an agent is healthy by verifying pod IPs
func (m *Manager) isAgentHealthy(ctx context.Context, agentInfo *AgentInfo) bool {
	// Simple health check: ensure pod IPs are still valid
	// In the future, we can add gRPC health checks here
	if len(agentInfo.PodIPs) == 0 {
		return false
	}

	// Check if pods still exist and are running
	pods := &corev1.PodList{}
	err := m.List(ctx, pods,
		client.InNamespace(agentInfo.Namespace),
		client.MatchingLabels{"app": agentInfo.AgentName},
	)
	if err != nil {
		return false
	}

	runningPods := 0
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			runningPods++
		}
	}

	return runningPods > 0
}

// GetAgentPodIPs returns all agent pod IPs for a device type from HSMPool
func (m *Manager) GetAgentPodIPs(hsmPool *hsmv1alpha1.HSMPool) ([]string, error) {
	// Extract device name from pool name (remove "-pool" suffix)
	deviceName := strings.TrimSuffix(hsmPool.Name, "-pool")

	m.mu.RLock()
	defer m.mu.RUnlock()

	var allPodIPs []string

	// Collect pod IPs from all agent instances for this device
	for _, aggregatedDevice := range hsmPool.Status.AggregatedDevices {
		agentKey := fmt.Sprintf("%s-%s", deviceName, aggregatedDevice.SerialNumber)
		if agentInfo, exists := m.activeAgents[agentKey]; exists && len(agentInfo.PodIPs) > 0 {
			allPodIPs = append(allPodIPs, agentInfo.PodIPs...)
		}
	}

	if len(allPodIPs) == 0 {
		return nil, fmt.Errorf("no active agents found for device %s in pool %s", deviceName, hsmPool.Name)
	}

	return allPodIPs, nil
}

// GetGRPCEndpoints returns gRPC endpoints for all agent pods of a device
func (m *Manager) GetGRPCEndpoints(hsmPool *hsmv1alpha1.HSMPool) ([]string, error) {
	podIPs, err := m.GetAgentPodIPs(hsmPool)
	if err != nil {
		return nil, err
	}

	endpoints := make([]string, 0, len(podIPs))
	for _, ip := range podIPs {
		endpoints = append(endpoints, fmt.Sprintf("%s:%d", ip, AgentPort))
	}

	return endpoints, nil
}

// CreateGRPCClient creates a gRPC client to the specific agent pod for the given DiscoveredDevice
func (m *Manager) CreateGRPCClient(ctx context.Context, device hsmv1alpha1.DiscoveredDevice, logger logr.Logger) (hsm.Client, error) {
	// Find the specific agent pod using labels based on the device's serial number
	var podList corev1.PodList
	listOpts := []client.ListOption{
		client.MatchingLabels{
			"app.kubernetes.io/name":      "hsm-agent",
			"app.kubernetes.io/component": "hsm-agent",
			"hsm.j5t.io/serial-number":    device.SerialNumber,
		},
	}

	if err := m.List(ctx, &podList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list agent pods for device %s: %w", device.SerialNumber, err)
	}

	if len(podList.Items) == 0 {
		return nil, fmt.Errorf("no agent pod found for device with serial number %s", device.SerialNumber)
	}

	// Find the first running pod
	var targetPod *corev1.Pod
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase == corev1.PodRunning && len(pod.Status.PodIPs) > 0 {
			targetPod = pod
			break
		}
	}

	if targetPod == nil {
		return nil, fmt.Errorf("no running agent pod found for device with serial number %s", device.SerialNumber)
	}

	// Get the pod IP and create gRPC endpoint
	podIP := targetPod.Status.PodIPs[0].IP
	endpoint := fmt.Sprintf("%s:%d", podIP, AgentPort)

	// Use connection pool to get or create cached client
	// This significantly reduces connection overhead and prevents "too_many_pings" errors
	logger.Info("Creating gRPC client", "endpoint", endpoint, "targetPod", targetPod.Name, "podIP", podIP, "serialNumber", device.SerialNumber)
	grpcClient, err := m.connectionPool.GetClient(ctx, endpoint, logger)
	if err != nil {
		logger.Error(err, "Failed to get pooled gRPC client", "endpoint", endpoint, "serialNumber", device.SerialNumber)
		return nil, fmt.Errorf("failed to get pooled gRPC client for %s: %w", endpoint, err)
	}

	logger.Info("Successfully created gRPC client", "endpoint", endpoint, "serialNumber", device.SerialNumber)
	return grpcClient, nil
}

// GetAvailableDevices finds all devices with ready HSMPools
func (m *Manager) GetAvailableDevices(ctx context.Context, namespace string) ([]hsmv1alpha1.DiscoveredDevice, error) {
	// List all HSMPools cluster-wide to find all ready pools
	var hsmPoolList hsmv1alpha1.HSMPoolList
	if err := m.List(ctx, &hsmPoolList); err != nil {
		m.logger.Error(err, "Failed to list HSM pools for GetAvailableDevices")
		return nil, fmt.Errorf("failed to list HSM pools: %w", err)
	}

	m.logger.Info("Listed HSMPools for GetAvailableDevices", "poolCount", len(hsmPoolList.Items), "requestedNamespace", namespace)

	var availableDevices []hsmv1alpha1.DiscoveredDevice
	// Check all pools that are in Ready phase
	for _, pool := range hsmPoolList.Items {
		m.logger.Info("Checking HSMPool", "name", pool.Name, "phase", pool.Status.Phase, "aggregatedDeviceCount", len(pool.Status.AggregatedDevices))

		if pool.Status.Phase != hsmv1alpha1.HSMPoolPhaseReady {
			m.logger.Info("Skipping HSMPool - not ready", "name", pool.Name, "phase", pool.Status.Phase)
			continue
		}

		availableDevices = append(availableDevices, pool.Status.AggregatedDevices...)
	}

	m.logger.Info("GetAvailableDevices result", "totalAvailableDevices", len(availableDevices))

	if len(availableDevices) == 0 {
		return nil, fmt.Errorf("no available HSM devices found")
	}

	return availableDevices, nil
}

// Close closes the manager and all its resources including the connection pool
func (m *Manager) Close() {
	if m.connectionPool != nil {
		m.connectionPool.Close()
	}
}
