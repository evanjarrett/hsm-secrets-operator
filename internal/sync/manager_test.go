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

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// MockAgentManager is a mock implementation of AgentManagerInterface for testing
type MockAgentManager struct{}

func (m *MockAgentManager) CreateSingleGRPCClient(ctx context.Context, deviceName, namespace string, logger logr.Logger) (hsm.Client, error) {
	// Return a mock client for testing
	return hsm.NewMockClient(), nil
}

func TestNewSyncManager(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockAgentManager := &MockAgentManager{}

	syncManager := NewSyncManager(client, mockAgentManager, logr.Discard())

	assert.NotNil(t, syncManager)
	assert.NotNil(t, syncManager.client)
	assert.NotNil(t, syncManager.agentManager)
	assert.NotNil(t, syncManager.logger)
}

func TestSyncResult_Structure(t *testing.T) {
	// Test the new SyncResult structure
	result := &SyncResult{
		Success:          true,
		SecretsProcessed: 3,
		SecretsUpdated:   1,
		SecretsCreated:   1,
		MetadataRestored: 1,
		SecretResults: map[string]SecretSyncResult{
			"secret1": {
				SecretPath:    "secret1",
				SourceDevice:  "device1",
				SourceVersion: 123,
				TargetDevices: []string{"device2"},
				SyncType:      SyncTypeUpdate,
				Success:       true,
				Error:         nil,
			},
			"secret2": {
				SecretPath:    "secret2",
				SourceDevice:  "device2",
				SourceVersion: 456,
				TargetDevices: []string{"device1"},
				SyncType:      SyncTypeCreate,
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
	assert.Equal(t, SyncTypeUpdate, secret1Result.SyncType)
	assert.True(t, secret1Result.Success)
}

func TestSyncTypes(t *testing.T) {
	// Test that SyncType constants are correctly defined
	assert.Equal(t, SyncType(0), SyncTypeSkip)
	assert.Equal(t, SyncType(1), SyncTypeUpdate)
	assert.Equal(t, SyncType(2), SyncTypeCreate)
	assert.Equal(t, SyncType(3), SyncTypeRestoreMetadata)
}

func TestSecretSyncResult_Structure(t *testing.T) {
	// Test that SecretSyncResult has the expected fields
	result := SecretSyncResult{
		SecretPath:    "test-secret",
		SourceDevice:  "device1",
		SourceVersion: 123,
		TargetDevices: []string{"device2", "device3"},
		SyncType:      SyncTypeCreate,
		Success:       true,
		Error:         nil,
	}

	assert.Equal(t, "test-secret", result.SecretPath)
	assert.Equal(t, "device1", result.SourceDevice)
	assert.Equal(t, int64(123), result.SourceVersion)
	assert.Equal(t, []string{"device2", "device3"}, result.TargetDevices)
	assert.Equal(t, SyncTypeCreate, result.SyncType)
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
	// Test the removeDevice utility function
	input := []string{"device1", "device2", "device3"}
	result := removeDevice(input, "device2")
	expected := []string{"device1", "device3"}

	assert.Equal(t, expected, result)
}
