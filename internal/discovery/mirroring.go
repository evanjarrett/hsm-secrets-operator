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
	"sort"
	"sync"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// MirroredSecretData represents secret data with metadata for mirroring
type MirroredSecretData struct {
	Path         string            `json:"path"`
	Data         hsm.SecretData    `json:"data"`
	Checksum     string            `json:"checksum"`
	LastModified time.Time         `json:"lastModified"`
	SourceNode   string            `json:"sourceNode"`
	Metadata     map[string]string `json:"metadata"`
}

// MirroringManager handles HSM device mirroring and cross-node synchronization
type MirroringManager struct {
	client      client.Client
	logger      logr.Logger
	mutex       sync.RWMutex
	hsmClients  map[string]hsm.Client
	mirrorCache map[string]*MirroredSecretData
	nodeHealth  map[string]time.Time
	currentNode string
}

// NewMirroringManager creates a new mirroring manager
func NewMirroringManager(client client.Client, nodeName string) *MirroringManager {
	return &MirroringManager{
		client:      client,
		logger:      ctrl.Log.WithName("hsm-mirroring-manager"),
		hsmClients:  make(map[string]hsm.Client),
		mirrorCache: make(map[string]*MirroredSecretData),
		nodeHealth:  make(map[string]time.Time),
		currentNode: nodeName,
	}
}

// RegisterHSMClient registers an HSM client for a specific node
func (m *MirroringManager) RegisterHSMClient(nodeName string, client hsm.Client) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.hsmClients[nodeName] = client
	m.nodeHealth[nodeName] = time.Now()
	m.logger.Info("Registered HSM client for node", "node", nodeName)
}

// SyncDevices synchronizes secrets across mirrored HSM devices
func (m *MirroringManager) SyncDevices(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
	if hsmDevice.Spec.Mirroring == nil || hsmDevice.Spec.Mirroring.Policy == hsmv1alpha1.MirroringPolicyNone {
		return nil
	}

	m.logger.Info("Starting device synchronization",
		"device", hsmDevice.Name,
		"policy", hsmDevice.Spec.Mirroring.Policy)

	// Determine primary and mirror nodes
	primaryNode, mirrorNodes, err := m.determineMirrorTopology(ctx, hsmDevice)
	if err != nil {
		return fmt.Errorf("failed to determine mirror topology: %w", err)
	}

	// Sync secrets from primary to mirrors
	if err := m.syncFromPrimary(ctx, hsmDevice, primaryNode, mirrorNodes); err != nil {
		return fmt.Errorf("failed to sync from primary: %w", err)
	}

	// Update mirroring status
	if err := m.updateMirroringStatus(ctx, hsmDevice, primaryNode, mirrorNodes); err != nil {
		return fmt.Errorf("failed to update mirroring status: %w", err)
	}

	return nil
}

// determineMirrorTopology determines which nodes should be primary vs mirrors
func (m *MirroringManager) determineMirrorTopology(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) (string, []string, error) {
	availableNodes := make([]string, 0)

	// Collect nodes with available devices
	for _, device := range hsmDevice.Status.DiscoveredDevices {
		if device.Available && device.Health != "Unhealthy" {
			availableNodes = append(availableNodes, device.NodeName)
		}
	}

	if len(availableNodes) == 0 {
		return "", nil, fmt.Errorf("no healthy devices available for mirroring")
	}

	// Determine primary node
	primaryNode := ""
	if hsmDevice.Spec.Mirroring.PrimaryNode != "" {
		// Use specified primary if available and healthy
		for _, node := range availableNodes {
			if node == hsmDevice.Spec.Mirroring.PrimaryNode {
				primaryNode = node
				break
			}
		}
	}

	if primaryNode == "" {
		// Choose primary based on health and availability
		sort.Strings(availableNodes) // Deterministic selection
		primaryNode = availableNodes[0]
	}

	// Determine mirror nodes
	mirrorNodes := make([]string, 0)
	for _, node := range availableNodes {
		if node != primaryNode {
			// Check if node should be a mirror target
			if len(hsmDevice.Spec.Mirroring.TargetNodes) == 0 {
				// Mirror to all available nodes if no targets specified
				mirrorNodes = append(mirrorNodes, node)
			} else {
				// Only mirror to specified target nodes
				for _, target := range hsmDevice.Spec.Mirroring.TargetNodes {
					if node == target {
						mirrorNodes = append(mirrorNodes, node)
						break
					}
				}
			}
		}
	}

	m.logger.V(1).Info("Determined mirror topology",
		"primary", primaryNode,
		"mirrors", mirrorNodes)

	return primaryNode, mirrorNodes, nil
}

// syncFromPrimary synchronizes secrets from the primary node to mirror nodes
func (m *MirroringManager) syncFromPrimary(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, primaryNode string, mirrorNodes []string) error {
	m.mutex.RLock()
	primaryClient, exists := m.hsmClients[primaryNode]
	m.mutex.RUnlock()

	if !exists || !primaryClient.IsConnected() {
		return fmt.Errorf("primary HSM client not available on node %s", primaryNode)
	}

	// List all secrets on the primary device
	secrets, err := m.listSecretsFromHSM(ctx, primaryClient, hsmDevice)
	if err != nil {
		return fmt.Errorf("failed to list secrets from primary: %w", err)
	}

	m.logger.Info("Found secrets on primary", "count", len(secrets), "primary", primaryNode)

	// Sync each secret to mirror nodes
	for _, secretPath := range secrets {
		if err := m.syncSecretToMirrors(ctx, secretPath, primaryClient, primaryNode, mirrorNodes); err != nil {
			m.logger.Error(err, "Failed to sync secret to mirrors",
				"secret", secretPath, "primary", primaryNode)
			continue
		}
	}

	return nil
}

// syncSecretToMirrors syncs a single secret to all mirror nodes
func (m *MirroringManager) syncSecretToMirrors(ctx context.Context, secretPath string, primaryClient hsm.Client, primaryNode string, mirrorNodes []string) error {
	// Read secret from primary
	secretData, err := primaryClient.ReadSecret(ctx, secretPath)
	if err != nil {
		return fmt.Errorf("failed to read secret from primary: %w", err)
	}

	// Calculate checksum
	checksum := hsm.CalculateChecksum(secretData)

	// Create mirrored data entry
	mirroredData := &MirroredSecretData{
		Path:         secretPath,
		Data:         secretData,
		Checksum:     checksum,
		LastModified: time.Now(),
		SourceNode:   primaryNode,
		Metadata: map[string]string{
			"source-node": primaryNode,
			"sync-time":   time.Now().Format(time.RFC3339),
		},
	}

	// Update cache
	m.mutex.Lock()
	m.mirrorCache[secretPath] = mirroredData
	m.mutex.Unlock()

	// Sync to mirror nodes (readonly)
	for _, mirrorNode := range mirrorNodes {
		if err := m.syncToMirrorNode(ctx, mirroredData, mirrorNode); err != nil {
			m.logger.Error(err, "Failed to sync to mirror node",
				"secret", secretPath, "mirror", mirrorNode)
		}
	}

	return nil
}

// syncToMirrorNode syncs secret data to a specific mirror node
func (m *MirroringManager) syncToMirrorNode(ctx context.Context, data *MirroredSecretData, mirrorNode string) error {
	m.mutex.RLock()
	mirrorClient, exists := m.hsmClients[mirrorNode]
	m.mutex.RUnlock()

	if !exists || !mirrorClient.IsConnected() {
		return fmt.Errorf("mirror HSM client not available on node %s", mirrorNode)
	}

	// For readonly mirrors, we store the secret data in a readonly format
	// In a real implementation, this might involve writing to a readonly partition
	// or using HSM-specific mirroring capabilities

	// Check if secret already exists and is up to date
	existingChecksum, err := mirrorClient.GetChecksum(ctx, data.Path)
	if err == nil && existingChecksum == data.Checksum {
		// Secret is already up to date
		m.logger.V(2).Info("Secret already up to date on mirror",
			"secret", data.Path, "mirror", mirrorNode)
		return nil
	}

	// Write secret to mirror (readonly)
	if err := mirrorClient.WriteSecret(ctx, data.Path, data.Data); err != nil {
		return fmt.Errorf("failed to write secret to mirror: %w", err)
	}

	m.logger.V(1).Info("Successfully synced secret to mirror",
		"secret", data.Path, "mirror", mirrorNode)

	return nil
}

// listSecretsFromHSM lists all secrets from an HSM client
func (m *MirroringManager) listSecretsFromHSM(ctx context.Context, client hsm.Client, hsmDevice *hsmv1alpha1.HSMDevice) ([]string, error) {
	// This is a simplified implementation
	// In a real implementation, you would use HSM-specific APIs to list secrets

	var secrets []string

	// For demo purposes, we'll simulate some secret paths
	// In reality, this would query the HSM for all available secret paths
	basePaths := []string{
		"secrets/default/database-credentials",
		"secrets/production/api-keys",
		"secrets/staging/certificates",
	}

	for _, path := range basePaths {
		// Check if secret exists
		if _, err := client.ReadSecret(ctx, path); err == nil {
			secrets = append(secrets, path)
		}
	}

	return secrets, nil
}

// GetReadOnlyAccess provides readonly access to secrets from mirrors when primary is down
func (m *MirroringManager) GetReadOnlyAccess(ctx context.Context, secretPath string, hsmDevice *hsmv1alpha1.HSMDevice) (hsm.SecretData, error) {
	m.logger.Info("Attempting readonly access", "secret", secretPath)

	// Check cache first
	m.mutex.RLock()
	cachedData, exists := m.mirrorCache[secretPath]
	m.mutex.RUnlock()

	if exists && time.Since(cachedData.LastModified) < time.Hour {
		m.logger.V(1).Info("Using cached mirror data", "secret", secretPath)
		return cachedData.Data, nil
	}

	// Try to read from available mirror nodes
	for _, device := range hsmDevice.Status.DiscoveredDevices {
		if device.Role == hsmv1alpha1.DeviceRoleReadOnly && device.Available {
			m.mutex.RLock()
			mirrorClient, exists := m.hsmClients[device.NodeName]
			m.mutex.RUnlock()

			if exists && mirrorClient.IsConnected() {
				if data, err := mirrorClient.ReadSecret(ctx, secretPath); err == nil {
					m.logger.Info("Successfully read from mirror",
						"secret", secretPath, "mirror", device.NodeName)
					return data, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("no readable mirrors available for secret %s", secretPath)
}

// HandleFailover handles failover from a failed primary to a healthy mirror
func (m *MirroringManager) HandleFailover(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice) error {
	if hsmDevice.Spec.Mirroring == nil || !hsmDevice.Spec.Mirroring.AutoFailover {
		return nil
	}

	m.logger.Info("Handling device failover", "device", hsmDevice.Name)

	// Find a healthy mirror to promote to primary
	var newPrimary string
	for _, device := range hsmDevice.Status.DiscoveredDevices {
		if device.Role == hsmv1alpha1.DeviceRoleReadOnly && device.Available {
			newPrimary = device.NodeName
			break
		}
	}

	if newPrimary == "" {
		return fmt.Errorf("no healthy mirrors available for failover")
	}

	// Update device roles
	for i, device := range hsmDevice.Status.DiscoveredDevices {
		if device.NodeName == newPrimary {
			hsmDevice.Status.DiscoveredDevices[i].Role = hsmv1alpha1.DeviceRolePrimary
		}
	}

	// Update mirroring status
	if hsmDevice.Status.Mirroring != nil {
		hsmDevice.Status.Mirroring.PrimaryNode = newPrimary
		hsmDevice.Status.Mirroring.FailoverCount++
	}

	// Update the HSMDevice status
	if err := m.client.Status().Update(ctx, hsmDevice); err != nil {
		return fmt.Errorf("failed to update device status after failover: %w", err)
	}

	m.logger.Info("Successfully failed over to new primary",
		"device", hsmDevice.Name, "newPrimary", newPrimary)

	return nil
}

// updateMirroringStatus updates the mirroring status in the HSMDevice
func (m *MirroringManager) updateMirroringStatus(ctx context.Context, hsmDevice *hsmv1alpha1.HSMDevice, primaryNode string, mirrorNodes []string) error {
	now := metav1.Now()

	if hsmDevice.Status.Mirroring == nil {
		hsmDevice.Status.Mirroring = &hsmv1alpha1.MirroringStatus{}
	}

	hsmDevice.Status.Mirroring.Enabled = true
	hsmDevice.Status.Mirroring.PrimaryNode = primaryNode
	hsmDevice.Status.Mirroring.MirroredNodes = mirrorNodes
	hsmDevice.Status.Mirroring.LastSyncTime = &now

	// Update device roles
	for i, device := range hsmDevice.Status.DiscoveredDevices {
		if device.NodeName == primaryNode {
			hsmDevice.Status.DiscoveredDevices[i].Role = hsmv1alpha1.DeviceRolePrimary
		} else {
			for _, mirrorNode := range mirrorNodes {
				if device.NodeName == mirrorNode {
					hsmDevice.Status.DiscoveredDevices[i].Role = hsmv1alpha1.DeviceRoleReadOnly
					hsmDevice.Status.DiscoveredDevices[i].MirroredFrom = primaryNode
					hsmDevice.Status.DiscoveredDevices[i].LastSyncTime = &now
					break
				}
			}
		}
	}

	return nil
}

// IsNodeHealthy checks if a node is healthy based on last seen time
func (m *MirroringManager) IsNodeHealthy(nodeName string, maxAge time.Duration) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	lastSeen, exists := m.nodeHealth[nodeName]
	if !exists {
		return false
	}

	return time.Since(lastSeen) <= maxAge
}

// UpdateNodeHealth updates the health status of a node
func (m *MirroringManager) UpdateNodeHealth(nodeName string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.nodeHealth[nodeName] = time.Now()
}
