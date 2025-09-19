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
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hsmv1 "github.com/evanjarrett/hsm-secrets-operator/api/proto/hsm/v1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

func TestNewGRPCServer(t *testing.T) {
	mockClient := hsm.NewMockClient()
	logger := logr.Discard()
	server := NewGRPCServer(mockClient, 9090, 8080, logger)

	assert.NotNil(t, server)
	assert.Equal(t, mockClient, server.hsmClient)
	assert.Equal(t, 9090, server.port)
	assert.Equal(t, 8080, server.healthPort)
}

func TestGRPCServerGetInfo(t *testing.T) {
	mockClient := hsm.NewMockClient()
	logger := logr.Discard()
	server := NewGRPCServer(mockClient, 9090, 8080, logger)

	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		// Initialize mock client
		err := mockClient.Initialize(ctx, hsm.Config{})
		require.NoError(t, err)

		resp, err := server.GetInfo(ctx, &hsmv1.GetInfoRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp.HsmInfo)
		assert.Equal(t, "Mock HSM Token", resp.HsmInfo.Label)
		assert.Equal(t, "Test Manufacturer", resp.HsmInfo.Manufacturer)
		assert.Equal(t, "Mock HSM v1.0", resp.HsmInfo.Model)
		assert.Equal(t, "MOCK123456", resp.HsmInfo.SerialNumber)
		assert.Equal(t, "1.0.0", resp.HsmInfo.FirmwareVersion)
	})

	t.Run("client not connected", func(t *testing.T) {
		// Create server with nil client
		server := NewGRPCServer(nil, 9090, 8080, logger)

		resp, err := server.GetInfo(ctx, &hsmv1.GetInfoRequest{})
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "HSM client not connected")
	})
}

func TestGRPCServerReadSecret(t *testing.T) {
	mockClient := hsm.NewMockClient()
	logger := logr.Discard()
	server := NewGRPCServer(mockClient, 9090, 8080, logger)

	ctx := context.Background()
	err := mockClient.Initialize(ctx, hsm.Config{})
	require.NoError(t, err)

	// Add test data
	testData := hsm.SecretData{
		"username": []byte("testuser"),
		"password": []byte("testpass"),
	}
	err = mockClient.WriteSecret(ctx, "test-secret", testData, nil)
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		req := &hsmv1.ReadSecretRequest{Path: "test-secret"}
		resp, err := server.ReadSecret(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.SecretData)
		assert.Equal(t, []byte("testuser"), resp.SecretData.Data["username"])
		assert.Equal(t, []byte("testpass"), resp.SecretData.Data["password"])
	})

	t.Run("empty path", func(t *testing.T) {
		req := &hsmv1.ReadSecretRequest{Path: ""}
		resp, err := server.ReadSecret(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "path is required")
	})

	t.Run("client not connected", func(t *testing.T) {
		server := NewGRPCServer(nil, 9090, 8080, logger)
		req := &hsmv1.ReadSecretRequest{Path: "test-secret"}
		resp, err := server.ReadSecret(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "HSM client not connected")
	})
}

func TestGRPCServerWriteSecret(t *testing.T) {
	mockClient := hsm.NewMockClient()
	logger := logr.Discard()
	server := NewGRPCServer(mockClient, 9090, 8080, logger)

	ctx := context.Background()
	err := mockClient.Initialize(ctx, hsm.Config{})
	require.NoError(t, err)

	t.Run("success with metadata", func(t *testing.T) {
		req := &hsmv1.WriteSecretRequest{
			Path: "secret-with-metadata",
			SecretData: &hsmv1.SecretData{
				Data: map[string][]byte{
					"certificate": []byte("-----BEGIN CERTIFICATE-----"),
				},
			},
			Metadata: &hsmv1.SecretMetadata{
				Description: "Production SSL certificate",
				Labels:      map[string]string{"env": "prod", "type": "ssl"},
				Format:      "pem",
				DataType:    "pem",
				CreatedAt:   "2025-01-01T00:00:00Z",
				Source:      "test",
			},
		}

		resp, err := server.WriteSecret(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify data was written
		data, err := mockClient.ReadSecret(ctx, "secret-with-metadata")
		require.NoError(t, err)
		assert.Equal(t, []byte("-----BEGIN CERTIFICATE-----"), data["certificate"])

		// Verify metadata was written
		metadata, err := mockClient.ReadMetadata(ctx, "secret-with-metadata")
		require.NoError(t, err)
		require.NotNil(t, metadata)
		assert.Equal(t, "Production SSL certificate", metadata.Description)
		assert.Equal(t, map[string]string{"env": "prod", "type": "ssl"}, metadata.Labels)
		assert.Equal(t, "pem", metadata.Format)
	})

	t.Run("success without metadata", func(t *testing.T) {
		req := &hsmv1.WriteSecretRequest{
			Path: "secret-no-metadata",
			SecretData: &hsmv1.SecretData{
				Data: map[string][]byte{"data": []byte("test")},
			},
			Metadata: nil,
		}

		resp, err := server.WriteSecret(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("validation errors", func(t *testing.T) {
		// Empty path
		req := &hsmv1.WriteSecretRequest{
			Path: "",
			SecretData: &hsmv1.SecretData{
				Data: map[string][]byte{"key": []byte("value")},
			},
		}

		resp, err := server.WriteSecret(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "path is required")

		// Nil secret data
		req = &hsmv1.WriteSecretRequest{
			Path:       "test",
			SecretData: nil,
		}

		resp, err = server.WriteSecret(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "secret data is required")
	})
}

func TestGRPCServerReadMetadata(t *testing.T) {
	mockClient := hsm.NewMockClient()
	logger := logr.Discard()
	server := NewGRPCServer(mockClient, 9090, 8080, logger)

	ctx := context.Background()
	err := mockClient.Initialize(ctx, hsm.Config{})
	require.NoError(t, err)

	// Write secret with metadata
	testData := hsm.SecretData{"data": []byte("test")}
	metadata := &hsm.SecretMetadata{
		Description: "Test description",
		Labels:      map[string]string{"type": "test"},
		Format:      "json",
		DataType:    "json",
		CreatedAt:   "2025-01-01T12:00:00Z",
		Source:      "unit-test",
	}
	err = mockClient.WriteSecret(ctx, "metadata-test", testData, metadata)
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		req := &hsmv1.ReadMetadataRequest{Path: "metadata-test"}
		resp, err := server.ReadMetadata(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Metadata)
		assert.Equal(t, "Test description", resp.Metadata.Description)
		assert.Equal(t, map[string]string{"type": "test"}, resp.Metadata.Labels)
		assert.Equal(t, "json", resp.Metadata.Format)
		assert.Equal(t, "json", resp.Metadata.DataType)
	})

	t.Run("no metadata", func(t *testing.T) {
		// Write secret without metadata
		err := mockClient.WriteSecret(ctx, "no-metadata", hsm.SecretData{"key": []byte("value")}, nil)
		require.NoError(t, err)

		req := &hsmv1.ReadMetadataRequest{Path: "no-metadata"}
		resp, err := server.ReadMetadata(ctx, req)
		// Mock client returns error when metadata doesn't exist
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to read metadata")
	})

	t.Run("empty path", func(t *testing.T) {
		req := &hsmv1.ReadMetadataRequest{Path: ""}
		resp, err := server.ReadMetadata(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "path is required")
	})
}

func TestGRPCServerDeleteSecret(t *testing.T) {
	mockClient := hsm.NewMockClient()
	logger := logr.Discard()
	server := NewGRPCServer(mockClient, 9090, 8080, logger)

	ctx := context.Background()
	err := mockClient.Initialize(ctx, hsm.Config{})
	require.NoError(t, err)

	// Write test secret
	testData := hsm.SecretData{"temp": []byte("data")}
	err = mockClient.WriteSecret(ctx, "delete-test", testData, nil)
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		// Verify secret exists
		_, err := mockClient.ReadSecret(ctx, "delete-test")
		require.NoError(t, err)

		// Delete it
		req := &hsmv1.DeleteSecretRequest{Path: "delete-test"}
		resp, err := server.DeleteSecret(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify it's gone
		_, err = mockClient.ReadSecret(ctx, "delete-test")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("empty path", func(t *testing.T) {
		req := &hsmv1.DeleteSecretRequest{Path: ""}
		resp, err := server.DeleteSecret(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "path is required")
	})
}

func TestGRPCServerListSecrets(t *testing.T) {
	mockClient := hsm.NewMockClient()
	logger := logr.Discard()
	server := NewGRPCServer(mockClient, 9090, 8080, logger)

	ctx := context.Background()
	err := mockClient.Initialize(ctx, hsm.Config{})
	require.NoError(t, err)

	// Add test secrets
	secrets := map[string]hsm.SecretData{
		"app/secret1": {"data": []byte("data1")},
		"app/secret2": {"data": []byte("data2")},
		"db/secret":   {"data": []byte("data3")},
	}

	for path, data := range secrets {
		err := mockClient.WriteSecret(ctx, path, data, nil)
		require.NoError(t, err)
	}

	t.Run("list all", func(t *testing.T) {
		req := &hsmv1.ListSecretsRequest{Prefix: ""}
		resp, err := server.ListSecrets(ctx, req)
		require.NoError(t, err)

		// Should include our test secrets plus any pre-populated ones
		assert.Contains(t, resp.Paths, "app/secret1")
		assert.Contains(t, resp.Paths, "app/secret2")
		assert.Contains(t, resp.Paths, "db/secret")
	})

	t.Run("list with prefix", func(t *testing.T) {
		req := &hsmv1.ListSecretsRequest{Prefix: "app/"}
		resp, err := server.ListSecrets(ctx, req)
		require.NoError(t, err)

		assert.Contains(t, resp.Paths, "app/secret1")
		assert.Contains(t, resp.Paths, "app/secret2")
		assert.NotContains(t, resp.Paths, "db/secret")
	})
}

func TestGRPCServerGetChecksum(t *testing.T) {
	mockClient := hsm.NewMockClient()
	logger := logr.Discard()
	server := NewGRPCServer(mockClient, 9090, 8080, logger)

	ctx := context.Background()
	err := mockClient.Initialize(ctx, hsm.Config{})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		// Write a secret first since checksum needs data to exist
		err := mockClient.WriteSecret(ctx, "checksum-test", hsm.SecretData{"data": []byte("test")}, nil)
		require.NoError(t, err)

		req := &hsmv1.GetChecksumRequest{Path: "checksum-test"}
		resp, err := server.GetChecksum(ctx, req)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.Checksum)

		// Should be consistent
		resp2, err := server.GetChecksum(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, resp.Checksum, resp2.Checksum)
	})

	t.Run("empty path", func(t *testing.T) {
		req := &hsmv1.GetChecksumRequest{Path: ""}
		resp, err := server.GetChecksum(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "path is required")
	})
}

func TestGRPCServerIsConnected(t *testing.T) {
	mockClient := hsm.NewMockClient()
	logger := logr.Discard()
	server := NewGRPCServer(mockClient, 9090, 8080, logger)

	ctx := context.Background()

	t.Run("connected", func(t *testing.T) {
		err := mockClient.Initialize(ctx, hsm.Config{})
		require.NoError(t, err)

		req := &hsmv1.IsConnectedRequest{}
		resp, err := server.IsConnected(ctx, req)
		require.NoError(t, err)
		assert.True(t, resp.Connected)
	})

	t.Run("not connected", func(t *testing.T) {
		err := mockClient.Close()
		require.NoError(t, err)

		req := &hsmv1.IsConnectedRequest{}
		resp, err := server.IsConnected(ctx, req)
		require.NoError(t, err)
		assert.False(t, resp.Connected)
	})

	t.Run("nil client", func(t *testing.T) {
		server := NewGRPCServer(nil, 9090, 8080, logger)

		req := &hsmv1.IsConnectedRequest{}
		resp, err := server.IsConnected(ctx, req)
		require.NoError(t, err)
		assert.False(t, resp.Connected)
	})
}

func TestGRPCServerHealth(t *testing.T) {
	mockClient := hsm.NewMockClient()
	logger := logr.Discard()
	server := NewGRPCServer(mockClient, 9090, 8080, logger)

	ctx := context.Background()

	t.Run("healthy", func(t *testing.T) {
		err := mockClient.Initialize(ctx, hsm.Config{})
		require.NoError(t, err)

		req := &hsmv1.HealthRequest{}
		resp, err := server.Health(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, healthyStatus, resp.Status)
		assert.Equal(t, "Agent is running normally", resp.Message)
	})

	t.Run("degraded", func(t *testing.T) {
		err := mockClient.Close()
		require.NoError(t, err)

		req := &hsmv1.HealthRequest{}
		resp, err := server.Health(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, degradedStatus, resp.Status)
		assert.Equal(t, "HSM client not connected", resp.Message)
	})

	t.Run("nil client", func(t *testing.T) {
		server := NewGRPCServer(nil, 9090, 8080, logger)

		req := &hsmv1.HealthRequest{}
		resp, err := server.Health(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, degradedStatus, resp.Status)
		assert.Equal(t, "HSM client not connected", resp.Message)
	})
}
