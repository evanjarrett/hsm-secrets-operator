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

package sync

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// MockGRPCClient implements hsm.Client for testing
type MockGRPCClient struct {
	mock.Mock
}

func (m *MockGRPCClient) Initialize(ctx context.Context, config hsm.Config) error {
	args := m.Called(ctx, config)
	return args.Error(0)
}

func (m *MockGRPCClient) IsConnected() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockGRPCClient) ReadSecret(ctx context.Context, path string) (hsm.SecretData, error) {
	args := m.Called(ctx, path)
	return args.Get(0).(hsm.SecretData), args.Error(1)
}

func (m *MockGRPCClient) WriteSecret(ctx context.Context, path string, data hsm.SecretData) error {
	args := m.Called(ctx, path, data)
	return args.Error(0)
}

func (m *MockGRPCClient) WriteSecretWithMetadata(ctx context.Context, path string, data hsm.SecretData, metadata *hsm.SecretMetadata) error {
	args := m.Called(ctx, path, data, metadata)
	return args.Error(0)
}

func (m *MockGRPCClient) DeleteSecret(ctx context.Context, path string) error {
	args := m.Called(ctx, path)
	return args.Error(0)
}

func (m *MockGRPCClient) ListSecrets(ctx context.Context, prefix string) ([]string, error) {
	args := m.Called(ctx, prefix)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockGRPCClient) GetInfo(ctx context.Context) (map[string]any, error) {
	args := m.Called(ctx)
	return args.Get(0).(map[string]any), args.Error(1)
}

func (m *MockGRPCClient) GetChecksum(ctx context.Context, path string) (string, error) {
	args := m.Called(ctx, path)
	return args.String(0), args.Error(1)
}

func (m *MockGRPCClient) ReadMetadata(ctx context.Context, path string) (*hsm.SecretMetadata, error) {
	args := m.Called(ctx, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*hsm.SecretMetadata), args.Error(1)
}

func (m *MockGRPCClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockAgentManager implements AgentManagerInterface for testing
type MockAgentManager struct {
	mock.Mock
}

func (m *MockAgentManager) CreateSingleGRPCClient(ctx context.Context, deviceName string, logger logr.Logger) (hsm.Client, error) {
	args := m.Called(ctx, deviceName, logger)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(hsm.Client), args.Error(1)
}

func TestSyncManager_CalculateChecksum(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockAgentManager := &MockAgentManager{}

	syncManager := NewSyncManager(client, mockAgentManager, logr.Discard())

	// Test with nil data
	checksum := syncManager.calculateChecksum(nil)
	assert.Equal(t, "", checksum)

	// Test with empty data
	checksum = syncManager.calculateChecksum(hsm.SecretData{})
	assert.NotEqual(t, "", checksum)

	// Test with actual data
	data1 := hsm.SecretData{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
	}
	checksum1 := syncManager.calculateChecksum(data1)
	assert.NotEqual(t, "", checksum1)

	// Same data should produce same checksum
	data2 := hsm.SecretData{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
	}
	checksum2 := syncManager.calculateChecksum(data2)
	assert.Equal(t, checksum1, checksum2)

	// Different data should produce different checksum
	data3 := hsm.SecretData{
		"key1": []byte("different"),
		"key2": []byte("value2"),
	}
	checksum3 := syncManager.calculateChecksum(data3)
	assert.NotEqual(t, checksum1, checksum3)

	// Key order shouldn't matter
	data4 := hsm.SecretData{
		"key2": []byte("value2"),
		"key1": []byte("value1"),
	}
	checksum4 := syncManager.calculateChecksum(data4)
	assert.Equal(t, checksum1, checksum4)
}

func TestSyncManager_UpdateHSMSecretStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)

	hsmSecret := &hsmv1alpha1.HSMSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Spec: hsmv1alpha1.HSMSecretSpec{
			AutoSync: true,
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hsmSecret).WithStatusSubresource(&hsmv1alpha1.HSMSecret{}).Build()
	mockAgentManager := &MockAgentManager{}

	syncManager := NewSyncManager(client, mockAgentManager, logr.Discard())

	// Test successful sync result
	result := &SyncResult{
		Success:          true,
		ConflictDetected: false,
		PrimaryDevice:    "device1",
		DeviceResults: map[string]DeviceResult{
			"device1": {
				Online:    true,
				Checksum:  "abc123",
				Version:   1,
				Error:     nil,
				Timestamp: time.Now(),
			},
			"device2": {
				Online:    true,
				Checksum:  "abc123",
				Version:   1,
				Error:     nil,
				Timestamp: time.Now(),
			},
		},
		ResolvedData: hsm.SecretData{
			"key": []byte("value"),
		},
	}

	ctx := context.Background()
	err := syncManager.UpdateHSMSecretStatus(ctx, hsmSecret, result)
	assert.NoError(t, err)

	// Verify status was updated
	assert.Equal(t, hsmv1alpha1.SyncStatusInSync, hsmSecret.Status.SyncStatus)
	assert.Equal(t, "device1", hsmSecret.Status.PrimaryDevice)
	assert.False(t, hsmSecret.Status.SyncConflict)
	assert.Equal(t, "", hsmSecret.Status.LastError)
	assert.Len(t, hsmSecret.Status.DeviceSyncStatus, 2)

	// Check device sync status
	for _, deviceSync := range hsmSecret.Status.DeviceSyncStatus {
		assert.True(t, deviceSync.Online)
		assert.Equal(t, "abc123", deviceSync.Checksum)
		assert.Equal(t, int64(1), deviceSync.Version)
		assert.Equal(t, hsmv1alpha1.SyncStatusInSync, deviceSync.Status)
		assert.Empty(t, deviceSync.LastError)
	}
}

func TestSyncManager_DetectConflicts(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockAgentManager := &MockAgentManager{}

	syncManager := NewSyncManager(client, mockAgentManager, logr.Discard())

	// Test with no conflicts (same checksums)
	deviceResults := map[string]DeviceResult{
		"device1": {
			Online:   true,
			Checksum: "abc123",
			Version:  1,
			Error:    nil,
		},
		"device2": {
			Online:   true,
			Checksum: "abc123", // Same checksum
			Version:  1,
			Error:    nil,
		},
	}

	conflict := syncManager.detectConflicts(deviceResults)
	assert.False(t, conflict)

	// Test with conflicts (different checksums)
	deviceResults = map[string]DeviceResult{
		"device1": {
			Online:   true,
			Checksum: "abc123",
			Version:  1,
			Error:    nil,
		},
		"device2": {
			Online:   true,
			Checksum: "def456", // Different checksum
			Version:  2,
			Error:    nil,
		},
	}

	conflict = syncManager.detectConflicts(deviceResults)
	assert.True(t, conflict)
}

func TestSyncManager_SelectPrimaryDevice(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockAgentManager := &MockAgentManager{}

	syncManager := NewSyncManager(client, mockAgentManager, logr.Discard())

	// Test with existing primary device
	hsmSecret := &hsmv1alpha1.HSMSecret{
		Status: hsmv1alpha1.HSMSecretStatus{
			PrimaryDevice: "device1",
		},
	}

	deviceResults := map[string]DeviceResult{
		"device1": {
			Online:   true,
			Checksum: "abc123",
			Version:  1,
			Error:    nil,
		},
		"device2": {
			Online:   true,
			Checksum: "def456",
			Version:  2,
			Error:    nil,
		},
	}

	primary := syncManager.selectPrimaryDevice(deviceResults, hsmSecret)
	assert.Equal(t, "device1", primary)

	// Test with no existing primary - should choose highest version
	hsmSecret.Status.PrimaryDevice = ""
	primary = syncManager.selectPrimaryDevice(deviceResults, hsmSecret)
	assert.Equal(t, "device2", primary) // device2 has version 2 vs device1's version 1

	// Test with primary device offline - should fallback to highest version
	hsmSecret.Status.PrimaryDevice = "device1"
	deviceResults["device1"] = DeviceResult{
		Online:   false, // Offline
		Checksum: "abc123",
		Version:  1,
		Error:    nil,
	}

	primary = syncManager.selectPrimaryDevice(deviceResults, hsmSecret)
	assert.Equal(t, "device2", primary)
}
