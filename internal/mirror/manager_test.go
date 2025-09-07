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

func (m *MockAgentManager) CreateGRPCClient(ctx context.Context, deviceName, namespace string, logger logr.Logger) (hsm.Client, error) {
	// Return a mock client for testing
	return hsm.NewMockClient(), nil
}

func TestNewMirrorManager(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hsmv1alpha1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockAgentManager := &MockAgentManager{}

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
	// Test the removeDevice utility function
	input := []string{"device1", "device2", "device3"}
	result := removeDevice(input, "device2")
	expected := []string{"device1", "device3"}

	assert.Equal(t, expected, result)
}
