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
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	hsmv1 "github.com/evanjarrett/hsm-secrets-operator/api/proto/hsm/v1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

func TestGRPCClientServerIntegration(t *testing.T) {
	// Create a mock HSM client for the server
	mockHSMClient := hsm.NewMockClient()

	// Initialize the mock client
	ctx := context.Background()
	err := mockHSMClient.Initialize(ctx, hsm.Config{})
	require.NoError(t, err)

	// Pre-populate some test data
	testData := map[string]hsm.SecretData{
		"test-secret": {
			"username": []byte("testuser"),
			"password": []byte("testpass"),
		},
		"api-credentials": {
			"api_key": []byte("secret-key"),
			"token":   []byte("secret-token"),
		},
	}

	for path, data := range testData {
		err := mockHSMClient.WriteSecret(ctx, path, data)
		require.NoError(t, err)
	}

	// Start a real gRPC server
	logger := logr.Discard()
	grpcServer := NewGRPCServer(mockHSMClient, 0, 0, logger)

	// Find available port
	listener, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Logf("Failed to close listener: %v", err)
	}

	// Start server on the found port
	go func() {
		lis, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		if err != nil {
			t.Logf("Failed to listen: %v", err)
			return
		}

		server := grpc.NewServer()
		hsmv1.RegisterHSMAgentServer(server, grpcServer)

		if err := server.Serve(lis); err != nil {
			t.Logf("Server stopped: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Create gRPC client
	endpoint := "localhost:" + strconv.Itoa(port)
	client, err := NewGRPCClient(endpoint, logger)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, client.Close())
	}()

	// Test client operations
	t.Run("GetInfo", func(t *testing.T) {
		info, err := client.GetInfo(ctx)
		require.NoError(t, err)
		assert.Equal(t, "Mock HSM Token", info.Label)
		assert.Equal(t, "Test Manufacturer", info.Manufacturer)
	})

	t.Run("IsConnected", func(t *testing.T) {
		connected := client.IsConnected()
		assert.True(t, connected)
	})

	t.Run("ReadExistingSecret", func(t *testing.T) {
		data, err := client.ReadSecret(ctx, "test-secret")
		require.NoError(t, err)
		assert.Equal(t, []byte("testuser"), data["username"])
		assert.Equal(t, []byte("testpass"), data["password"])
	})

	t.Run("WriteAndReadNewSecret", func(t *testing.T) {
		newSecret := hsm.SecretData{
			"db_host":     []byte("localhost"),
			"db_password": []byte("secret123"),
		}

		err := client.WriteSecret(ctx, "new-secret", newSecret)
		require.NoError(t, err)

		// Read it back
		readData, err := client.ReadSecret(ctx, "new-secret")
		require.NoError(t, err)
		assert.Equal(t, newSecret, readData)
	})

	t.Run("WriteSecretWithMetadata", func(t *testing.T) {
		secretData := hsm.SecretData{
			"certificate": []byte("-----BEGIN CERTIFICATE-----"),
		}
		metadata := &hsm.SecretMetadata{
			Description: "Production SSL certificate",
			Labels:      map[string]string{"env": "prod", "type": "ssl"},
			Format:      "pem",
			DataType:    "pem",
			CreatedAt:   "2025-01-01T00:00:00Z",
			Source:      "integration-test",
		}

		err := client.WriteSecretWithMetadata(ctx, "ssl-cert", secretData, metadata)
		require.NoError(t, err)

		// Verify the data
		readData, err := client.ReadSecret(ctx, "ssl-cert")
		require.NoError(t, err)
		assert.Equal(t, secretData, readData)

		// Verify the metadata
		readMetadata, err := client.ReadMetadata(ctx, "ssl-cert")
		require.NoError(t, err)
		require.NotNil(t, readMetadata)
		assert.Equal(t, "Production SSL certificate", readMetadata.Description)
		assert.Equal(t, map[string]string{"env": "prod", "type": "ssl"}, readMetadata.Labels)
		assert.Equal(t, "pem", readMetadata.Format)
		assert.Equal(t, "pem", readMetadata.DataType)
	})

	t.Run("ListSecrets", func(t *testing.T) {
		paths, err := client.ListSecrets(ctx, "")
		require.NoError(t, err)

		// Should include pre-populated and newly created secrets
		assert.Contains(t, paths, "test-secret")
		assert.Contains(t, paths, "api-credentials")
		assert.Contains(t, paths, "new-secret")
		assert.Contains(t, paths, "ssl-cert")

		// Test with prefix
		apiPaths, err := client.ListSecrets(ctx, "api")
		require.NoError(t, err)
		assert.Contains(t, apiPaths, "api-credentials")
		assert.NotContains(t, apiPaths, "test-secret")
	})

	t.Run("GetChecksum", func(t *testing.T) {
		checksum, err := client.GetChecksum(ctx, "test-secret")
		require.NoError(t, err)
		assert.NotEmpty(t, checksum)

		// Should be consistent
		checksum2, err := client.GetChecksum(ctx, "test-secret")
		require.NoError(t, err)
		assert.Equal(t, checksum, checksum2)
	})

	t.Run("DeleteSecret", func(t *testing.T) {
		// First create a secret to delete
		tempData := hsm.SecretData{"temp": []byte("data")}
		err := client.WriteSecret(ctx, "temp-secret", tempData)
		require.NoError(t, err)

		// Verify it exists
		_, err = client.ReadSecret(ctx, "temp-secret")
		require.NoError(t, err)

		// Delete it
		err = client.DeleteSecret(ctx, "temp-secret")
		require.NoError(t, err)

		// Verify it's gone
		_, err = client.ReadSecret(ctx, "temp-secret")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("ErrorHandling", func(t *testing.T) {
		// Test reading non-existent secret
		_, err := client.ReadSecret(ctx, "non-existent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")

		// Test with empty path
		_, err = client.ReadSecret(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "path is required")

		err = client.WriteSecret(ctx, "", hsm.SecretData{"key": []byte("value")})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "path is required")
	})
}

func TestGRPCClientTimeouts(t *testing.T) {
	logger := logr.Discard()

	// Test connection to non-existent server
	client, err := NewGRPCClient("localhost:99999", logger)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, client.Close())
	}()

	// Set very short timeout
	client.SetTimeout(100 * time.Millisecond)

	ctx := context.Background()

	// These should timeout
	_, err = client.GetInfo(ctx)
	assert.Error(t, err)

	connected := client.IsConnected()
	assert.False(t, connected)
}
