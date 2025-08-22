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

package hsm

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

// MockClient implements the Client interface for testing
type MockClient struct {
	logger    logr.Logger
	mutex     sync.RWMutex
	connected bool
	secrets   map[string]SecretData
	metadata  map[string]*SecretMetadata
	config    Config
}

// NewMockClient creates a new mock HSM client for testing
func NewMockClient() *MockClient {
	return &MockClient{
		logger:   ctrl.Log.WithName("hsm-mock-client"),
		secrets:  make(map[string]SecretData),
		metadata: make(map[string]*SecretMetadata),
	}
}

// Initialize simulates HSM connection
func (m *MockClient) Initialize(ctx context.Context, config Config) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.config = config
	m.connected = true

	// Pre-populate with some test data
	m.secrets["secrets/default/test-secret"] = SecretData{
		"username": []byte("testuser"),
		"password": []byte("testpass123"),
	}

	m.secrets["secrets/production/database-credentials"] = SecretData{
		"host":     []byte("db.example.com"),
		"username": []byte("produser"),
		"password": []byte("prod-secret-password"),
		"database": []byte("application"),
	}

	m.logger.Info("Mock HSM client initialized", "secretCount", len(m.secrets))
	return nil
}

// Close simulates HSM disconnection
func (m *MockClient) Close() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.connected = false
	m.logger.Info("Mock HSM client closed")
	return nil
}

// GetInfo returns mock HSM device information
func (m *MockClient) GetInfo(ctx context.Context) (*HSMInfo, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if !m.connected {
		return nil, fmt.Errorf("HSM not connected")
	}

	return &HSMInfo{
		Label:           "Mock HSM Token",
		Manufacturer:    "Test Manufacturer",
		Model:           "Mock HSM v1.0",
		SerialNumber:    "MOCK123456",
		FirmwareVersion: "1.0.0",
	}, nil
}

// ReadSecret reads secret data from mock storage
func (m *MockClient) ReadSecret(ctx context.Context, path string) (SecretData, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if !m.connected {
		return nil, fmt.Errorf("HSM not connected")
	}

	data, exists := m.secrets[path]
	if !exists {
		return nil, fmt.Errorf("secret not found at path: %s", path)
	}

	// Return a copy to avoid data races
	result := make(SecretData)
	for k, v := range data {
		result[k] = append([]byte(nil), v...)
	}

	m.logger.V(1).Info("Read secret from mock HSM",
		"path", path, "keys", len(result))
	return result, nil
}

// WriteSecret writes secret data to mock storage
func (m *MockClient) WriteSecret(ctx context.Context, path string, data SecretData) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if !m.connected {
		return fmt.Errorf("HSM not connected")
	}

	// Store a copy to avoid data races
	stored := make(SecretData)
	for k, v := range data {
		stored[k] = append([]byte(nil), v...)
	}

	m.secrets[path] = stored
	m.logger.Info("Wrote secret to mock HSM",
		"path", path, "keys", len(data))
	return nil
}

// WriteSecretWithMetadata writes secret data and metadata to the specified HSM path
func (m *MockClient) WriteSecretWithMetadata(ctx context.Context, path string, data SecretData, metadata *SecretMetadata) error {
	if err := m.WriteSecret(ctx, path, data); err != nil {
		return err
	}

	if metadata != nil {
		m.mutex.Lock()
		defer m.mutex.Unlock()
		m.metadata[path] = metadata
		m.logger.V(1).Info("Wrote metadata to mock HSM", "path", path)
	}

	return nil
}

// ReadMetadata reads metadata for a secret at the given path
func (m *MockClient) ReadMetadata(ctx context.Context, path string) (*SecretMetadata, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if !m.connected {
		return nil, fmt.Errorf("HSM not connected")
	}

	metadata, exists := m.metadata[path]
	if !exists {
		return nil, fmt.Errorf("metadata not found for path: %s", path)
	}

	return metadata, nil
}

// DeleteSecret removes secret data from mock storage
func (m *MockClient) DeleteSecret(ctx context.Context, path string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if !m.connected {
		return fmt.Errorf("HSM not connected")
	}

	if _, exists := m.secrets[path]; !exists {
		return fmt.Errorf("secret not found at path: %s", path)
	}

	delete(m.secrets, path)
	delete(m.metadata, path) // Also delete metadata
	m.logger.Info("Deleted secret from mock HSM", "path", path)
	return nil
}

// ListSecrets returns a list of secret paths with the given prefix
func (m *MockClient) ListSecrets(ctx context.Context, prefix string) ([]string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if !m.connected {
		return nil, fmt.Errorf("HSM not connected")
	}

	var paths []string
	for path := range m.secrets {
		if strings.HasPrefix(path, prefix) {
			paths = append(paths, path)
		}
	}

	m.logger.V(1).Info("Listed secrets from mock HSM",
		"prefix", prefix, "count", len(paths))
	return paths, nil
}

// GetChecksum returns the SHA256 checksum of the secret data at the given path
func (m *MockClient) GetChecksum(ctx context.Context, path string) (string, error) {
	data, err := m.ReadSecret(ctx, path)
	if err != nil {
		return "", fmt.Errorf("failed to read secret for checksum: %w", err)
	}

	checksum := CalculateChecksum(data)
	m.logger.V(2).Info("Calculated checksum for mock secret",
		"path", path, "checksum", checksum)

	return checksum, nil
}

// IsConnected returns the mock connection status
func (m *MockClient) IsConnected() bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.connected
}

// AddSecret adds a secret to the mock storage for testing
func (m *MockClient) AddSecret(path string, data SecretData) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	stored := make(SecretData)
	for k, v := range data {
		stored[k] = append([]byte(nil), v...)
	}

	m.secrets[path] = stored
}

// GetAllSecrets returns all secrets in mock storage for testing
func (m *MockClient) GetAllSecrets() map[string]SecretData {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	result := make(map[string]SecretData)
	for path, data := range m.secrets {
		copied := make(SecretData)
		for k, v := range data {
			copied[k] = append([]byte(nil), v...)
		}
		result[path] = copied
	}

	return result
}
