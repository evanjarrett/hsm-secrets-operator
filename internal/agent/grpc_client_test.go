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
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	hsmv1 "github.com/evanjarrett/hsm-secrets-operator/api/proto/hsm/v1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// mockHSMAgentServer implements hsmv1.HSMAgentServer for testing
type mockHSMAgentServer struct {
	hsmv1.UnimplementedHSMAgentServer
	connected    bool
	secrets      map[string]map[string][]byte
	metadata     map[string]*hsmv1.SecretMetadata
	returnError  bool
	errorCode    codes.Code
	errorMessage string
}

func newMockHSMAgentServer() *mockHSMAgentServer {
	return &mockHSMAgentServer{
		connected: true,
		secrets:   make(map[string]map[string][]byte),
		metadata:  make(map[string]*hsmv1.SecretMetadata),
	}
}

func (m *mockHSMAgentServer) setError(code codes.Code, message string) {
	m.returnError = true
	m.errorCode = code
	m.errorMessage = message
}

func (m *mockHSMAgentServer) clearError() {
	m.returnError = false
}

func (m *mockHSMAgentServer) GetInfo(ctx context.Context, req *hsmv1.GetInfoRequest) (*hsmv1.GetInfoResponse, error) {
	if m.returnError {
		return nil, status.Error(m.errorCode, m.errorMessage)
	}

	return &hsmv1.GetInfoResponse{
		HsmInfo: &hsmv1.HSMInfo{
			Label:           "Test HSM",
			Manufacturer:    "Test Manufacturer",
			Model:           "Test Model",
			SerialNumber:    "TEST123",
			FirmwareVersion: "1.0.0",
		},
	}, nil
}

func (m *mockHSMAgentServer) ReadSecret(ctx context.Context, req *hsmv1.ReadSecretRequest) (*hsmv1.ReadSecretResponse, error) {
	if m.returnError {
		return nil, status.Error(m.errorCode, m.errorMessage)
	}

	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	secret, exists := m.secrets[req.Path]
	if !exists {
		return nil, status.Error(codes.NotFound, "secret not found")
	}

	return &hsmv1.ReadSecretResponse{
		SecretData: &hsmv1.SecretData{Data: secret},
	}, nil
}

func (m *mockHSMAgentServer) WriteSecret(ctx context.Context, req *hsmv1.WriteSecretRequest) (*hsmv1.WriteSecretResponse, error) {
	if m.returnError {
		return nil, status.Error(m.errorCode, m.errorMessage)
	}

	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	if req.SecretData == nil {
		return nil, status.Error(codes.InvalidArgument, "secret data is required")
	}

	m.secrets[req.Path] = req.SecretData.Data
	return &hsmv1.WriteSecretResponse{}, nil
}

func (m *mockHSMAgentServer) WriteSecretWithMetadata(ctx context.Context, req *hsmv1.WriteSecretWithMetadataRequest) (*hsmv1.WriteSecretWithMetadataResponse, error) {
	if m.returnError {
		return nil, status.Error(m.errorCode, m.errorMessage)
	}

	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	if req.SecretData == nil {
		return nil, status.Error(codes.InvalidArgument, "secret data is required")
	}

	m.secrets[req.Path] = req.SecretData.Data
	if req.Metadata != nil {
		m.metadata[req.Path] = req.Metadata
	}

	return &hsmv1.WriteSecretWithMetadataResponse{}, nil
}

func (m *mockHSMAgentServer) ReadMetadata(ctx context.Context, req *hsmv1.ReadMetadataRequest) (*hsmv1.ReadMetadataResponse, error) {
	if m.returnError {
		return nil, status.Error(m.errorCode, m.errorMessage)
	}

	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	metadata, exists := m.metadata[req.Path]
	if !exists {
		return &hsmv1.ReadMetadataResponse{Metadata: nil}, nil
	}

	return &hsmv1.ReadMetadataResponse{Metadata: metadata}, nil
}

func (m *mockHSMAgentServer) DeleteSecret(ctx context.Context, req *hsmv1.DeleteSecretRequest) (*hsmv1.DeleteSecretResponse, error) {
	if m.returnError {
		return nil, status.Error(m.errorCode, m.errorMessage)
	}

	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	delete(m.secrets, req.Path)
	delete(m.metadata, req.Path)

	return &hsmv1.DeleteSecretResponse{}, nil
}

func (m *mockHSMAgentServer) ListSecrets(ctx context.Context, req *hsmv1.ListSecretsRequest) (*hsmv1.ListSecretsResponse, error) {
	if m.returnError {
		return nil, status.Error(m.errorCode, m.errorMessage)
	}

	var paths []string
	for path := range m.secrets {
		if req.Prefix == "" || len(path) >= len(req.Prefix) && path[:len(req.Prefix)] == req.Prefix {
			paths = append(paths, path)
		}
	}

	return &hsmv1.ListSecretsResponse{Paths: paths}, nil
}

func (m *mockHSMAgentServer) GetChecksum(ctx context.Context, req *hsmv1.GetChecksumRequest) (*hsmv1.GetChecksumResponse, error) {
	if m.returnError {
		return nil, status.Error(m.errorCode, m.errorMessage)
	}

	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	return &hsmv1.GetChecksumResponse{Checksum: "test-checksum-" + req.Path}, nil
}

func (m *mockHSMAgentServer) IsConnected(ctx context.Context, req *hsmv1.IsConnectedRequest) (*hsmv1.IsConnectedResponse, error) {
	if m.returnError {
		return nil, status.Error(m.errorCode, m.errorMessage)
	}

	return &hsmv1.IsConnectedResponse{Connected: m.connected}, nil
}

// setupTestServer creates a test gRPC server with bufconn
func setupTestServer(t *testing.T, mockServer *mockHSMAgentServer) (*grpc.Server, *bufconn.Listener) {
	buffer := 101024 * 1024
	lis := bufconn.Listen(buffer)

	server := grpc.NewServer()
	hsmv1.RegisterHSMAgentServer(server, mockServer)

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Logf("Server exited with error: %v", err)
		}
	}()

	return server, lis
}

// bufDialer returns a dialer for bufconn
func bufDialer(listener *bufconn.Listener) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, url string) (net.Conn, error) {
		return listener.Dial()
	}
}

func TestNewGRPCClient(t *testing.T) {
	logger := logr.Discard()

	t.Run("successful creation", func(t *testing.T) {
		client, err := NewGRPCClient("localhost:9090", "test-device", logger)
		require.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-device", client.deviceName)
		assert.Equal(t, "localhost:9090", client.endpoint)
		assert.Equal(t, 30*time.Second, client.timeout)

		err = client.Close()
		assert.NoError(t, err)
	})

	t.Run("invalid endpoint", func(t *testing.T) {
		// Use an endpoint that will actually fail to connect
		client, err := NewGRPCClient("", "test-device", logger)
		require.Error(t, err)
		assert.Nil(t, client)
	})
}

func TestGRPCClientOperations(t *testing.T) {
	logger := logr.Discard()
	mockServer := newMockHSMAgentServer()
	server, listener := setupTestServer(t, mockServer)
	defer server.Stop()

	// Create client with custom dialer for testing
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(bufDialer(listener)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer func() {
		if err := conn.Close(); err != nil {
			t.Logf("Failed to close connection: %v", err)
		}
	}()

	client := &GRPCClient{
		client:     hsmv1.NewHSMAgentClient(conn),
		conn:       conn,
		logger:     logger,
		deviceName: "test-device",
		endpoint:   "passthrough:///bufnet",
		timeout:    5 * time.Second,
	}

	ctx := context.Background()

	// Skip Initialize test since it tries to reconnect - test GetInfo directly instead

	t.Run("GetInfo", func(t *testing.T) {
		info, err := client.GetInfo(ctx)
		require.NoError(t, err)
		assert.Equal(t, "Test HSM", info.Label)
		assert.Equal(t, "Test Manufacturer", info.Manufacturer)
		assert.Equal(t, "Test Model", info.Model)
		assert.Equal(t, "TEST123", info.SerialNumber)
		assert.Equal(t, "1.0.0", info.FirmwareVersion)
	})

	t.Run("WriteSecret", func(t *testing.T) {
		secretData := hsm.SecretData{
			"username": []byte("testuser"),
			"password": []byte("testpass"),
		}

		err := client.WriteSecret(ctx, "test-secret", secretData)
		assert.NoError(t, err)
	})

	t.Run("ReadSecret", func(t *testing.T) {
		// First write a secret
		secretData := hsm.SecretData{
			"api_key": []byte("secret-key"),
			"token":   []byte("secret-token"),
		}
		err := client.WriteSecret(ctx, "read-test", secretData)
		require.NoError(t, err)

		// Then read it back
		readData, err := client.ReadSecret(ctx, "read-test")
		require.NoError(t, err)
		assert.Equal(t, secretData, readData)
	})

	t.Run("WriteSecretWithMetadata", func(t *testing.T) {
		secretData := hsm.SecretData{
			"data": []byte("test-data"),
		}
		metadata := &hsm.SecretMetadata{
			Label:       "Test Secret",
			Description: "A test secret",
			Tags:        map[string]string{"category": "test", "env": "demo"},
			Format:      "raw",
			DataType:    hsm.DataTypePlaintext,
			CreatedAt:   "2025-01-01T00:00:00Z",
			Source:      "test",
		}

		err := client.WriteSecretWithMetadata(ctx, "metadata-test", secretData, metadata)
		assert.NoError(t, err)
	})

	t.Run("ReadMetadata", func(t *testing.T) {
		// First write secret with metadata
		secretData := hsm.SecretData{"data": []byte("test")}
		metadata := &hsm.SecretMetadata{
			Label:       "Metadata Test",
			Description: "Test metadata reading",
			Tags:        map[string]string{"type": "metadata"},
			Format:      "json",
			DataType:    hsm.DataTypeJson,
			CreatedAt:   "2025-01-01T12:00:00Z",
			Source:      "unit-test",
		}
		err := client.WriteSecretWithMetadata(ctx, "metadata-read-test", secretData, metadata)
		require.NoError(t, err)

		// Then read metadata
		readMetadata, err := client.ReadMetadata(ctx, "metadata-read-test")
		require.NoError(t, err)
		require.NotNil(t, readMetadata)
		assert.Equal(t, "Metadata Test", readMetadata.Label)
		assert.Equal(t, "Test metadata reading", readMetadata.Description)
		assert.Equal(t, map[string]string{"type": "metadata"}, readMetadata.Tags)
		assert.Equal(t, "json", readMetadata.Format)
		assert.Equal(t, hsm.DataTypeJson, readMetadata.DataType)
	})

	t.Run("DeleteSecret", func(t *testing.T) {
		// First write a secret
		secretData := hsm.SecretData{"temp": []byte("data")}
		err := client.WriteSecret(ctx, "delete-test", secretData)
		require.NoError(t, err)

		// Verify it exists
		_, err = client.ReadSecret(ctx, "delete-test")
		require.NoError(t, err)

		// Delete it
		err = client.DeleteSecret(ctx, "delete-test")
		assert.NoError(t, err)

		// Verify it's gone
		_, err = client.ReadSecret(ctx, "delete-test")
		assert.Error(t, err)
	})

	t.Run("ListSecrets", func(t *testing.T) {
		// Write some test secrets
		secrets := map[string]hsm.SecretData{
			"list/secret1": {"data": []byte("data1")},
			"list/secret2": {"data": []byte("data2")},
			"other/secret": {"data": []byte("data3")},
		}

		for path, data := range secrets {
			err := client.WriteSecret(ctx, path, data)
			require.NoError(t, err)
		}

		// List all secrets
		allPaths, err := client.ListSecrets(ctx, "")
		require.NoError(t, err)
		assert.Contains(t, allPaths, "list/secret1")
		assert.Contains(t, allPaths, "list/secret2")
		assert.Contains(t, allPaths, "other/secret")

		// List with prefix
		listPaths, err := client.ListSecrets(ctx, "list/")
		require.NoError(t, err)
		assert.Contains(t, listPaths, "list/secret1")
		assert.Contains(t, listPaths, "list/secret2")
		assert.NotContains(t, listPaths, "other/secret")
	})

	t.Run("GetChecksum", func(t *testing.T) {
		checksum, err := client.GetChecksum(ctx, "test-path")
		require.NoError(t, err)
		assert.Equal(t, "test-checksum-test-path", checksum)
	})

	t.Run("IsConnected", func(t *testing.T) {
		connected := client.IsConnected()
		assert.True(t, connected)

		// Test when server is not connected
		mockServer.connected = false
		connected = client.IsConnected()
		assert.False(t, connected)

		// Reset
		mockServer.connected = true
	})

	t.Run("SetTimeout", func(t *testing.T) {
		newTimeout := 60 * time.Second
		client.SetTimeout(newTimeout)
		assert.Equal(t, newTimeout, client.timeout)
	})

	t.Run("GetEndpoint", func(t *testing.T) {
		endpoint := client.GetEndpoint()
		assert.Equal(t, "passthrough:///bufnet", endpoint)
	})
}

func TestGRPCClientErrorHandling(t *testing.T) {
	logger := logr.Discard()
	mockServer := newMockHSMAgentServer()
	server, listener := setupTestServer(t, mockServer)
	defer server.Stop()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(bufDialer(listener)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer func() {
		if err := conn.Close(); err != nil {
			t.Logf("Failed to close connection: %v", err)
		}
	}()

	client := &GRPCClient{
		client:     hsmv1.NewHSMAgentClient(conn),
		conn:       conn,
		logger:     logger,
		deviceName: "test-device",
		endpoint:   "passthrough:///bufnet",
		timeout:    5 * time.Second,
	}

	ctx := context.Background()

	t.Run("server error handling", func(t *testing.T) {
		// Set server to return error
		mockServer.setError(codes.Internal, "internal server error")
		defer mockServer.clearError()

		_, err := client.GetInfo(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get HSM info")

		_, err = client.ReadSecret(ctx, "test")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read secret")

		err = client.WriteSecret(ctx, "test", hsm.SecretData{"key": []byte("value")})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to write secret")
	})

	t.Run("timeout handling", func(t *testing.T) {
		// Set very short timeout
		client.SetTimeout(1 * time.Nanosecond)

		// This should timeout quickly
		_, err := client.GetInfo(ctx)
		assert.Error(t, err)

		// Reset timeout
		client.SetTimeout(5 * time.Second)
	})

	t.Run("validation errors", func(t *testing.T) {
		// Empty path should cause validation error
		_, err := client.ReadSecret(ctx, "")
		assert.Error(t, err)

		err = client.WriteSecret(ctx, "", hsm.SecretData{"key": []byte("value")})
		assert.Error(t, err)

		err = client.DeleteSecret(ctx, "")
		assert.Error(t, err)

		_, err = client.GetChecksum(ctx, "")
		assert.Error(t, err)
	})
}

func TestGRPCClientConnectionHandling(t *testing.T) {
	logger := logr.Discard()

	t.Run("connection unavailable", func(t *testing.T) {
		// Try to connect to non-existent server
		client, err := NewGRPCClient("localhost:99999", "test-device", logger)
		require.NoError(t, err) // Connection is lazy

		// Initialize should fail
		ctx := context.Background()
		err = client.Initialize(ctx, hsm.Config{})
		assert.Error(t, err)

		// IsConnected should return false
		connected := client.IsConnected()
		assert.False(t, connected)

		err = client.Close()
		assert.NoError(t, err)
	})
}
