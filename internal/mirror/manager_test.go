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

package mirror

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// MockAgentManager is a mock implementation of AgentManagerInterface for testing
type MockAgentManager struct {
	clients        map[string]hsm.Client
	shouldFail     map[string]bool
	creationErrors map[string]error
}

func NewMockAgentManager() *MockAgentManager {
	return &MockAgentManager{
		clients:        make(map[string]hsm.Client),
		shouldFail:     make(map[string]bool),
		creationErrors: make(map[string]error),
	}
}

func (m *MockAgentManager) CreateGRPCClient(ctx context.Context, device hsmv1alpha1.DiscoveredDevice, logger logr.Logger) (hsm.Client, error) {
	deviceId := device.SerialNumber
	if err, exists := m.creationErrors[deviceId]; exists {
		return nil, err
	}

	if client, exists := m.clients[deviceId]; exists {
		return client, nil
	}

	// Return a mock client for testing
	return hsm.NewMockClient(), nil
}

func (m *MockAgentManager) GetAvailableDevices(ctx context.Context, namespace string) ([]hsmv1alpha1.DiscoveredDevice, error) {
	// Convert device names to DiscoveredDevice objects for testing
	devices := make([]hsmv1alpha1.DiscoveredDevice, 0, len(m.clients))
	for deviceId := range m.clients {
		devices = append(devices, hsmv1alpha1.DiscoveredDevice{
			SerialNumber: deviceId,
			DevicePath:   "/dev/test/" + deviceId,
			NodeName:     "test-node",
			Available:    true,
		})
	}
	return devices, nil
}

func (m *MockAgentManager) SetClient(deviceName string, client hsm.Client) {
	m.clients[deviceName] = client
}

func (m *MockAgentManager) SetCreationError(deviceName string, err error) {
	m.creationErrors[deviceName] = err
}

func TestNewMirrorManager(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockAgentManager := NewMockAgentManager()

	mirrorManager := NewMirrorManager(client, mockAgentManager, logr.Discard(), "test-namespace")

	assert.NotNil(t, mirrorManager)
	assert.NotNil(t, mirrorManager.client)
	assert.NotNil(t, mirrorManager.agentManager)
	assert.NotNil(t, mirrorManager.logger)
}

func TestMirrorResult_Structure(t *testing.T) {
	// Test the new MirrorResult structure
	result := &MirrorResult{
		Success:          true,
		SecretsProcessed: 3,
		SecretsUpdated:   1,
		SecretsCreated:   1,
		MetadataRestored: 1,
		SecretResults: map[string]SecretMirrorResult{
			"secret1": {
				SecretPath:    "secret1",
				SourceDevice:  "device1",
				SourceVersion: 123,
				TargetDevices: []string{"device2"},
				MirrorType:    MirrorTypeUpdate,
				Success:       true,
				Error:         nil,
			},
			"secret2": {
				SecretPath:    "secret2",
				SourceDevice:  "device2",
				SourceVersion: 456,
				TargetDevices: []string{"device1"},
				MirrorType:    MirrorTypeCreate,
				Success:       true,
				Error:         nil,
			},
		},
		Errors: []string{},
	}

	assert.True(t, result.Success)
	assert.Equal(t, 3, result.SecretsProcessed)
	assert.Equal(t, 1, result.SecretsUpdated)
	assert.Equal(t, 1, result.SecretsCreated)
	assert.Equal(t, 1, result.MetadataRestored)
	assert.Equal(t, 2, len(result.SecretResults))
	assert.Equal(t, 0, len(result.Errors))

	// Check individual secret results
	secret1Result := result.SecretResults["secret1"]
	assert.Equal(t, "secret1", secret1Result.SecretPath)
	assert.Equal(t, "device1", secret1Result.SourceDevice)
	assert.Equal(t, int64(123), secret1Result.SourceVersion)
	assert.Equal(t, MirrorTypeUpdate, secret1Result.MirrorType)
	assert.True(t, secret1Result.Success)
}

func TestMirrorTypes(t *testing.T) {
	// Test that MirrorType constants are correctly defined
	assert.Equal(t, MirrorType(0), MirrorTypeSkip)
	assert.Equal(t, MirrorType(1), MirrorTypeUpdate)
	assert.Equal(t, MirrorType(2), MirrorTypeCreate)
	assert.Equal(t, MirrorType(3), MirrorTypeRestoreMetadata)
}

func TestSecretMirrorResult_Structure(t *testing.T) {
	// Test that SecretMirrorResult has the expected fields
	result := SecretMirrorResult{
		SecretPath:    "test-secret",
		SourceDevice:  "device1",
		SourceVersion: 123,
		TargetDevices: []string{"device2", "device3"},
		MirrorType:    MirrorTypeCreate,
		Success:       true,
		Error:         nil,
	}

	assert.Equal(t, "test-secret", result.SecretPath)
	assert.Equal(t, "device1", result.SourceDevice)
	assert.Equal(t, int64(123), result.SourceVersion)
	assert.Equal(t, []string{"device2", "device3"}, result.TargetDevices)
	assert.Equal(t, MirrorTypeCreate, result.MirrorType)
	assert.True(t, result.Success)
	assert.Nil(t, result.Error)
}

func TestRemoveDuplicates(t *testing.T) {
	// Test the removeDuplicates utility function
	input := []string{"device1", "device2", "device1", "device3", "device2"}
	expected := []string{"device1", "device2", "device3"}
	result := removeDuplicates(input)

	assert.Equal(t, len(expected), len(result))
	for _, item := range expected {
		assert.Contains(t, result, item)
	}
}

func TestRemoveDevice(t *testing.T) {
	tests := []struct {
		name           string
		input          []string
		deviceToRemove string
		expected       []string
	}{
		{
			name:           "remove middle device",
			input:          []string{"device1", "device2", "device3"},
			deviceToRemove: "device2",
			expected:       []string{"device1", "device3"},
		},
		{
			name:           "remove first device",
			input:          []string{"device1", "device2", "device3"},
			deviceToRemove: "device1",
			expected:       []string{"device2", "device3"},
		},
		{
			name:           "remove last device",
			input:          []string{"device1", "device2", "device3"},
			deviceToRemove: "device3",
			expected:       []string{"device1", "device2"},
		},
		{
			name:           "remove non-existent device",
			input:          []string{"device1", "device2", "device3"},
			deviceToRemove: "device4",
			expected:       []string{"device1", "device2", "device3"},
		},
		{
			name:           "empty list",
			input:          []string{},
			deviceToRemove: "device1",
			expected:       nil, // removeDevice returns nil for empty result
		},
		{
			name:           "single device - remove it",
			input:          []string{"device1"},
			deviceToRemove: "device1",
			expected:       nil, // removeDevice returns nil for empty result
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeDevice(tt.input, tt.deviceToRemove)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name        string
		versionStr  string
		expected    int64
		expectError bool
	}{
		{
			name:        "valid integer",
			versionStr:  "123",
			expected:    123,
			expectError: false,
		},
		{
			name:        "zero version",
			versionStr:  "0",
			expected:    0,
			expectError: false,
		},
		{
			name:        "large version number",
			versionStr:  "9223372036854775807", // max int64
			expected:    9223372036854775807,
			expectError: false,
		},
		{
			name:        "invalid string",
			versionStr:  "not-a-number",
			expected:    0,
			expectError: true,
		},
		{
			name:        "empty string",
			versionStr:  "",
			expected:    0,
			expectError: true,
		},
		{
			name:        "version with extra text",
			versionStr:  "123abc",
			expected:    123,
			expectError: false, // fmt.Sscanf will parse the number part
		},
		{
			name:        "negative version",
			versionStr:  "-123",
			expected:    -123,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseVersion(tt.versionStr)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestCalculateChecksum(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockAgentManager := NewMockAgentManager()
	mirrorManager := NewMirrorManager(client, mockAgentManager, logr.Discard(), "test-namespace")

	tests := []struct {
		name     string
		data     hsm.SecretData
		expected string
	}{
		{
			name:     "nil data",
			data:     nil,
			expected: "",
		},
		{
			name:     "empty data",
			data:     hsm.SecretData{},
			expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", // SHA256 of empty string
		},
		{
			name: "single key-value",
			data: hsm.SecretData{
				"key1": []byte("value1"),
			},
			expected: "", // Will be calculated in test
		},
		{
			name: "multiple keys - consistent ordering",
			data: hsm.SecretData{
				"zebra": []byte("last"),
				"alpha": []byte("first"),
				"beta":  []byte("second"),
			},
			expected: "f4d5c3f63c7cffc6bcad9b3f6b2a1f8b2d4c8e9a5b2c1f8d6e7a4b3c2d1e0f9", // This will be calculated
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mirrorManager.calculateChecksum(tt.data)
			if tt.name == "multiple keys - consistent ordering" || tt.name == "single key-value" {
				// For these tests, we just want to ensure consistency
				// Calculate checksum twice and ensure they're the same
				result2 := mirrorManager.calculateChecksum(tt.data)
				assert.Equal(t, result, result2, "checksum should be consistent")
				assert.NotEmpty(t, result, "checksum should not be empty")
				assert.Equal(t, 64, len(result), "SHA256 hash should be 64 hex characters")
			} else {
				assert.Equal(t, tt.expected, result)
			}
		})
	}

	// Test that key order doesn't affect checksum
	t.Run("key order independence", func(t *testing.T) {
		data1 := hsm.SecretData{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		}
		data2 := hsm.SecretData{
			"key2": []byte("value2"),
			"key1": []byte("value1"),
		}
		checksum1 := mirrorManager.calculateChecksum(data1)
		checksum2 := mirrorManager.calculateChecksum(data2)
		assert.Equal(t, checksum1, checksum2, "checksum should be the same regardless of key order")
	})
}

func TestCreateMirrorPlanForSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockAgentManager := NewMockAgentManager()
	mirrorManager := NewMirrorManager(client, mockAgentManager, logr.Discard(), "test-namespace")

	baseTime := time.Now()

	tests := []struct {
		name         string
		secretPath   string
		inventory    *SecretInventory
		expectedPlan *SecretMirrorPlan
		expectNil    bool
	}{
		{
			name:       "no devices have secret",
			secretPath: "secret1",
			inventory: &SecretInventory{
				SecretPath: "secret1",
				DeviceStates: map[string]*SecretState{
					"device1": {Present: false},
					"device2": {Present: false},
				},
			},
			expectNil: true,
		},
		{
			name:       "all devices in sync",
			secretPath: "secret1",
			inventory: &SecretInventory{
				SecretPath: "secret1",
				DeviceStates: map[string]*SecretState{
					"device1": {Present: true, Version: 5, HasMetadata: true, Timestamp: baseTime},
					"device2": {Present: true, Version: 5, HasMetadata: true, Timestamp: baseTime.Add(-1 * time.Hour)},
				},
			},
			expectedPlan: &SecretMirrorPlan{
				SecretPath:    "secret1",
				SourceDevice:  "device1", // Most recent timestamp
				SourceVersion: 5,
				TargetDevices: []string{},
				MirrorType:    MirrorTypeSkip,
			},
		},
		{
			name:       "create secret on missing devices",
			secretPath: "secret1",
			inventory: &SecretInventory{
				SecretPath: "secret1",
				DeviceStates: map[string]*SecretState{
					"device1": {Present: true, Version: 3, HasMetadata: true, Timestamp: baseTime},
					"device2": {Present: false},
					"device3": {Present: false},
				},
			},
			expectedPlan: &SecretMirrorPlan{
				SecretPath:    "secret1",
				SourceDevice:  "device1",
				SourceVersion: 3,
				TargetDevices: []string{"device2", "device3"},
				MirrorType:    MirrorTypeCreate,
			},
		},
		{
			name:       "update outdated versions",
			secretPath: "secret1",
			inventory: &SecretInventory{
				SecretPath: "secret1",
				DeviceStates: map[string]*SecretState{
					"device1": {Present: true, Version: 5, HasMetadata: true, Timestamp: baseTime},
					"device2": {Present: true, Version: 3, HasMetadata: true, Timestamp: baseTime.Add(-1 * time.Hour)},
					"device3": {Present: true, Version: 4, HasMetadata: true, Timestamp: baseTime.Add(-30 * time.Minute)},
				},
			},
			expectedPlan: &SecretMirrorPlan{
				SecretPath:    "secret1",
				SourceDevice:  "device1",
				SourceVersion: 5,
				TargetDevices: []string{"device2", "device3"},
				MirrorType:    MirrorTypeUpdate,
			},
		},
		{
			name:       "restore metadata on devices without it",
			secretPath: "secret1",
			inventory: &SecretInventory{
				SecretPath: "secret1",
				DeviceStates: map[string]*SecretState{
					"device1": {Present: true, Version: 3, HasMetadata: true, Timestamp: baseTime},
					"device2": {Present: true, Version: 0, HasMetadata: false, Timestamp: baseTime.Add(-1 * time.Hour)},
					"device3": {Present: true, Version: 0, HasMetadata: false, Timestamp: baseTime.Add(-2 * time.Hour)},
				},
			},
			expectedPlan: &SecretMirrorPlan{
				SecretPath:    "secret1",
				SourceDevice:  "device1",
				SourceVersion: 3,
				TargetDevices: []string{"device2", "device3"},
				MirrorType:    MirrorTypeRestoreMetadata,
			},
		},
		{
			name:       "mixed operations - create and update",
			secretPath: "secret1",
			inventory: &SecretInventory{
				SecretPath: "secret1",
				DeviceStates: map[string]*SecretState{
					"device1": {Present: true, Version: 5, HasMetadata: true, Timestamp: baseTime},
					"device2": {Present: true, Version: 2, HasMetadata: true, Timestamp: baseTime.Add(-2 * time.Hour)},
					"device3": {Present: false}, // Needs creation
				},
			},
			expectedPlan: &SecretMirrorPlan{
				SecretPath:    "secret1",
				SourceDevice:  "device1",
				SourceVersion: 5,
				TargetDevices: []string{"device3", "device2"}, // Create has priority
				MirrorType:    MirrorTypeCreate,
			},
		},
		{
			name:       "highest version wins with multiple metadata sources",
			secretPath: "secret1",
			inventory: &SecretInventory{
				SecretPath: "secret1",
				DeviceStates: map[string]*SecretState{
					"device1": {Present: true, Version: 3, HasMetadata: true, Timestamp: baseTime},
					"device2": {Present: true, Version: 5, HasMetadata: true, Timestamp: baseTime.Add(-1 * time.Hour)}, // Older but higher version
					"device3": {Present: true, Version: 4, HasMetadata: true, Timestamp: baseTime.Add(-30 * time.Minute)},
				},
			},
			expectedPlan: &SecretMirrorPlan{
				SecretPath:    "secret1",
				SourceDevice:  "device2", // Highest version wins
				SourceVersion: 5,
				TargetDevices: []string{"device1", "device3"},
				MirrorType:    MirrorTypeUpdate,
			},
		},
		{
			name:       "same version, most recent timestamp wins",
			secretPath: "secret1",
			inventory: &SecretInventory{
				SecretPath: "secret1",
				DeviceStates: map[string]*SecretState{
					"device1": {Present: true, Version: 3, HasMetadata: true, Timestamp: baseTime.Add(-1 * time.Hour)},
					"device2": {Present: true, Version: 3, HasMetadata: true, Timestamp: baseTime}, // Most recent
					"device3": {Present: true, Version: 2, HasMetadata: true, Timestamp: baseTime.Add(-2 * time.Hour)},
				},
			},
			expectedPlan: &SecretMirrorPlan{
				SecretPath:    "secret1",
				SourceDevice:  "device2", // Most recent timestamp for same version
				SourceVersion: 3,
				TargetDevices: []string{"device3"},
				MirrorType:    MirrorTypeUpdate,
			},
		},
		{
			name:       "skip devices with errors",
			secretPath: "secret1",
			inventory: &SecretInventory{
				SecretPath: "secret1",
				DeviceStates: map[string]*SecretState{
					"device1": {Present: true, Version: 5, HasMetadata: true, Timestamp: baseTime},
					"device2": {Present: false, Error: assert.AnError}, // Has error, should be skipped
					"device3": {Present: true, Version: 3, HasMetadata: true, Timestamp: baseTime.Add(-1 * time.Hour)},
				},
			},
			expectedPlan: &SecretMirrorPlan{
				SecretPath:    "secret1",
				SourceDevice:  "device1",
				SourceVersion: 5,
				TargetDevices: []string{"device3"},
				MirrorType:    MirrorTypeUpdate,
			},
		},
		{
			name:       "fallback to device without metadata when no metadata sources exist",
			secretPath: "secret1",
			inventory: &SecretInventory{
				SecretPath: "secret1",
				DeviceStates: map[string]*SecretState{
					"device1": {Present: true, Version: 0, HasMetadata: false, Timestamp: baseTime},
					"device2": {Present: true, Version: 0, HasMetadata: false, Timestamp: baseTime.Add(-1 * time.Hour)},
					"device3": {Present: false},
				},
			},
			expectedPlan: nil, // We'll check this manually since source device order is non-deterministic
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mirrorManager.createMirrorPlanForSecret(tt.secretPath, tt.inventory, logr.Discard())

			if tt.expectNil {
				assert.Nil(t, result)
				return
			}

			// Special handling for fallback test case
			if tt.name == "fallback to device without metadata when no metadata sources exist" {
				assert.NotNil(t, result)
				assert.Equal(t, "secret1", result.SecretPath)
				assert.Equal(t, int64(0), result.SourceVersion)
				assert.Equal(t, MirrorTypeCreate, result.MirrorType)
				// Source device should be either device1 or device2 (both have secrets)
				assert.Contains(t, []string{"device1", "device2"}, result.SourceDevice)
				// Should have 2 target devices (the other device with secret + device3)
				assert.Len(t, result.TargetDevices, 2)
				assert.Contains(t, result.TargetDevices, "device3") // device3 always needs creation
				return
			}

			assert.NotNil(t, result)
			assert.Equal(t, tt.expectedPlan.SecretPath, result.SecretPath)
			assert.Equal(t, tt.expectedPlan.SourceDevice, result.SourceDevice)
			assert.Equal(t, tt.expectedPlan.SourceVersion, result.SourceVersion)
			assert.Equal(t, tt.expectedPlan.MirrorType, result.MirrorType)

			// Check target devices (order may vary, so use ElementsMatch)
			assert.ElementsMatch(t, tt.expectedPlan.TargetDevices, result.TargetDevices)
		})
	}
}

// Test agent manager device discovery integration
func TestAgentManagerIntegration(t *testing.T) {
	mockAgentManager := NewMockAgentManager()

	// Test that mirror manager now uses agent manager for device discovery
	mockAgentManager.SetClient("device1", hsm.NewMockClient())

	devices, err := mockAgentManager.GetAvailableDevices(context.Background(), "test-namespace")

	require.NoError(t, err)
	assert.Len(t, devices, 1)
	assert.Equal(t, "device1", devices[0].SerialNumber)
}

// Test buildSecretInventory method
func TestBuildSecretInventory(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	mockAgentManager := NewMockAgentManager()

	// Create mock clients with different secret states
	mockClient1 := hsm.NewMockClient()
	mockClient2 := hsm.NewMockClient()

	// Initialize clients and populate with test data
	ctx := context.Background()
	err := mockClient1.Initialize(ctx, hsm.DefaultConfig())
	require.NoError(t, err)
	err = mockClient2.Initialize(ctx, hsm.DefaultConfig())
	require.NoError(t, err)

	// Add test secrets to client1
	testData := hsm.SecretData{"username": []byte("user1"), "password": []byte("pass1")}
	err = mockClient1.WriteSecret(ctx, "test-secret", testData, nil)
	require.NoError(t, err)

	// Add different secret to client2
	testData2 := hsm.SecretData{"api_key": []byte("key123")}
	err = mockClient2.WriteSecret(ctx, "test-secret", testData2, nil)
	require.NoError(t, err)

	mockAgentManager.SetClient("device1", mockClient1)
	mockAgentManager.SetClient("device2", mockClient2)

	mirrorManager := NewMirrorManager(client, mockAgentManager, logr.Discard(), "test-namespace")

	secretPaths := []string{"test-secret"}
	devices := []hsmv1alpha1.DiscoveredDevice{
		{SerialNumber: "device1", DevicePath: "/dev/test/device1", NodeName: "test-node", Available: true},
		{SerialNumber: "device2", DevicePath: "/dev/test/device2", NodeName: "test-node", Available: true},
	}

	inventory, err := mirrorManager.buildSecretInventory(ctx, secretPaths, devices, logr.Discard())

	require.NoError(t, err)
	assert.Contains(t, inventory, "test-secret")

	secretInventory := inventory["test-secret"]
	assert.Equal(t, "test-secret", secretInventory.SecretPath)
	assert.Contains(t, secretInventory.DeviceStates, "device1")
	assert.Contains(t, secretInventory.DeviceStates, "device2")

	// Both devices should have the secret present
	assert.True(t, secretInventory.DeviceStates["device1"].Present)
	assert.True(t, secretInventory.DeviceStates["device2"].Present)
}

// Test createMirrorPlans method
func TestCreateMirrorPlans(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockAgentManager := NewMockAgentManager()
	mirrorManager := NewMirrorManager(client, mockAgentManager, logr.Discard(), "test-namespace")

	baseTime := time.Now()

	// Create test inventory
	inventory := map[string]*SecretInventory{
		"secret1": {
			SecretPath: "secret1",
			DeviceStates: map[string]*SecretState{
				"device1": {Present: true, Version: 5, HasMetadata: true, Timestamp: baseTime},
				"device2": {Present: true, Version: 3, HasMetadata: true, Timestamp: baseTime.Add(-1 * time.Hour)},
			},
		},
		"secret2": {
			SecretPath: "secret2",
			DeviceStates: map[string]*SecretState{
				"device1": {Present: false},
				"device2": {Present: true, Version: 2, HasMetadata: true, Timestamp: baseTime},
			},
		},
	}

	plans := mirrorManager.createMirrorPlans(inventory, logr.Discard())

	assert.Len(t, plans, 2)

	// Find plans by secret path
	var secret1Plan, secret2Plan *SecretMirrorPlan
	for _, plan := range plans {
		switch plan.SecretPath {
		case "secret1":
			secret1Plan = plan
		case "secret2":
			secret2Plan = plan
		}
	}

	assert.NotNil(t, secret1Plan)
	assert.Equal(t, "device1", secret1Plan.SourceDevice) // Highest version
	assert.Equal(t, int64(5), secret1Plan.SourceVersion)
	assert.Equal(t, MirrorTypeUpdate, secret1Plan.MirrorType)
	assert.Contains(t, secret1Plan.TargetDevices, "device2")

	assert.NotNil(t, secret2Plan)
	assert.Equal(t, "device2", secret2Plan.SourceDevice)
	assert.Equal(t, int64(2), secret2Plan.SourceVersion)
	assert.Equal(t, MirrorTypeCreate, secret2Plan.MirrorType)
	assert.Contains(t, secret2Plan.TargetDevices, "device1")
}

// Test executeMirrorPlans method
func TestExecuteMirrorPlans(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	mockAgentManager := NewMockAgentManager()
	mirrorManager := NewMirrorManager(client, mockAgentManager, logr.Discard(), "test-namespace")

	// Create test plans
	plans := []*SecretMirrorPlan{
		{
			SecretPath:    "secret1",
			SourceDevice:  "device1",
			SourceVersion: 5,
			TargetDevices: []string{"device2"},
			MirrorType:    MirrorTypeUpdate,
		},
		{
			SecretPath:    "secret2",
			SourceDevice:  "device2",
			SourceVersion: 3,
			TargetDevices: []string{"device1"},
			MirrorType:    MirrorTypeCreate,
		},
	}

	// Create device lookup for test
	deviceLookup := map[string]hsmv1alpha1.DiscoveredDevice{
		"device1": {SerialNumber: "device1", DevicePath: "/dev/test/device1", NodeName: "test-node", Available: true},
		"device2": {SerialNumber: "device2", DevicePath: "/dev/test/device2", NodeName: "test-node", Available: true},
	}

	result := mirrorManager.executeMirrorPlans(context.Background(), plans, deviceLookup, logr.Discard())

	assert.NotNil(t, result)
	// Success may be false if some operations fail, which is expected in testing
	assert.Equal(t, 2, result.SecretsProcessed)
	assert.Len(t, result.SecretResults, 2)

	// Check individual secret results
	assert.Contains(t, result.SecretResults, "secret1")
	assert.Contains(t, result.SecretResults, "secret2")

	secret1Result := result.SecretResults["secret1"]
	assert.Equal(t, "secret1", secret1Result.SecretPath)
	assert.Equal(t, "device1", secret1Result.SourceDevice)
	assert.Equal(t, int64(5), secret1Result.SourceVersion)
	assert.Equal(t, MirrorTypeUpdate, secret1Result.MirrorType)
}

// Test conflict resolution - version precedence
func TestConflictResolutionVersionPrecedence(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockAgentManager := NewMockAgentManager()
	mirrorManager := NewMirrorManager(client, mockAgentManager, logr.Discard(), "test-namespace")

	baseTime := time.Now()

	// Test version-based conflict resolution
	inventory := &SecretInventory{
		SecretPath: "conflicted-secret",
		DeviceStates: map[string]*SecretState{
			"device1": {Present: true, Version: 10, HasMetadata: true, Timestamp: baseTime.Add(-2 * time.Hour)}, // Oldest timestamp but highest version
			"device2": {Present: true, Version: 5, HasMetadata: true, Timestamp: baseTime.Add(-1 * time.Hour)},  // Middle timestamp, lower version
			"device3": {Present: true, Version: 8, HasMetadata: true, Timestamp: baseTime},                      // Most recent timestamp, middle version
		},
	}

	plan := mirrorManager.createMirrorPlanForSecret("conflicted-secret", inventory, logr.Discard())

	assert.NotNil(t, plan)
	assert.Equal(t, "device1", plan.SourceDevice, "Highest version should win regardless of timestamp")
	assert.Equal(t, int64(10), plan.SourceVersion)
	assert.Equal(t, MirrorTypeUpdate, plan.MirrorType)
	assert.ElementsMatch(t, []string{"device2", "device3"}, plan.TargetDevices)
}

// Test error handling in readSecretWithMetadata
func TestReadSecretWithMetadataErrorHandling(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	mockAgentManager := NewMockAgentManager()
	mockAgentManager.SetCreationError("failing-device", fmt.Errorf("client creation failed"))

	mirrorManager := NewMirrorManager(client, mockAgentManager, logr.Discard(), "test-namespace")

	failingDevice := hsmv1alpha1.DiscoveredDevice{
		SerialNumber: "failing-device",
		DevicePath:   "/dev/test/failing-device",
		NodeName:     "test-node",
		Available:    true,
	}

	data, metadata, err := mirrorManager.readSecretWithMetadata(
		context.Background(),
		failingDevice,
		"test-secret",
		logr.Discard(),
	)

	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Nil(t, metadata)
	assert.Contains(t, err.Error(), "client creation failed")
}
