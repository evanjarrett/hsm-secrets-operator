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
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	hsmv1 "github.com/evanjarrett/hsm-secrets-operator/api/proto/hsm/v1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// GRPCClient implements the HSM client interface using gRPC
type GRPCClient struct {
	client   hsmv1.HSMAgentClient
	conn     *grpc.ClientConn
	logger   logr.Logger
	endpoint string
	timeout  time.Duration
}

// NewGRPCClient creates a new gRPC-based HSM client
func NewGRPCClient(endpoint string, logger logr.Logger) (*GRPCClient, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint cannot be empty")
	}

	// Create gRPC connection with conservative keepalive settings
	// Reduce ping frequency to prevent "too_many_pings" errors
	conn, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second, // Reduced from 10s to 30s
			Timeout:             10 * time.Second, // Increased from 3s to 10s
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to agent at %s: %w", endpoint, err)
	}

	client := hsmv1.NewHSMAgentClient(conn)

	return &GRPCClient{
		client:   client,
		conn:     conn,
		logger:   logger.WithName("grpc-client"),
		endpoint: endpoint,
		timeout:  30 * time.Second,
	}, nil
}

// Initialize establishes connection to the HSM agent
func (c *GRPCClient) Initialize(ctx context.Context, config hsm.Config) error {
	// Test connection by getting info
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	info, err := c.GetInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize gRPC client: %w", err)
	}

	c.logger.Info("gRPC client initialized", "hsm_label", info.Label, "endpoint", c.endpoint)
	return nil
}

// Close terminates the gRPC connection
func (c *GRPCClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// GetInfo returns information about the HSM device
func (c *GRPCClient) GetInfo(ctx context.Context) (*hsm.HSMInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.GetInfo(ctx, &hsmv1.GetInfoRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get HSM info: %w", err)
	}

	return &hsm.HSMInfo{
		Label:           resp.HsmInfo.Label,
		Manufacturer:    resp.HsmInfo.Manufacturer,
		Model:           resp.HsmInfo.Model,
		SerialNumber:    resp.HsmInfo.SerialNumber,
		FirmwareVersion: resp.HsmInfo.FirmwareVersion,
	}, nil
}

// ReadSecret reads secret data from the specified HSM path
func (c *GRPCClient) ReadSecret(ctx context.Context, path string) (hsm.SecretData, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.ReadSecret(ctx, &hsmv1.ReadSecretRequest{
		Path: path,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read secret: %w", err)
	}

	// Convert protobuf format to hsm.SecretData
	secretData := make(hsm.SecretData)
	if resp.SecretData != nil {
		for key, value := range resp.SecretData.Data {
			secretData[key] = value
		}
	}

	return secretData, nil
}

// WriteSecret writes secret data and metadata to the specified HSM path
func (c *GRPCClient) WriteSecret(ctx context.Context, path string, data hsm.SecretData, metadata *hsm.SecretMetadata) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Convert hsm.SecretData to protobuf format
	pbData := make(map[string][]byte)
	for key, value := range data {
		pbData[key] = value
	}

	req := &hsmv1.WriteSecretRequest{
		Path: path,
		SecretData: &hsmv1.SecretData{
			Data: pbData,
		},
	}

	// Convert metadata if provided
	if metadata != nil {
		req.Metadata = &hsmv1.SecretMetadata{
			Description: metadata.Description,
			Labels:      metadata.Labels,
			Format:      metadata.Format,
			DataType:    metadata.DataType,
			CreatedAt:   metadata.CreatedAt,
			Source:      metadata.Source,
		}
	}

	_, err := c.client.WriteSecret(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to write secret with metadata: %w", err)
	}

	return nil
}

// ReadMetadata reads metadata for a secret at the given path
func (c *GRPCClient) ReadMetadata(ctx context.Context, path string) (*hsm.SecretMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.ReadMetadata(ctx, &hsmv1.ReadMetadataRequest{
		Path: path,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	if resp.Metadata == nil {
		return nil, nil
	}

	return &hsm.SecretMetadata{
		Description: resp.Metadata.Description,
		Labels:      resp.Metadata.Labels,
		Format:      resp.Metadata.Format,
		DataType:    resp.Metadata.DataType,
		CreatedAt:   resp.Metadata.CreatedAt,
		Source:      resp.Metadata.Source,
	}, nil
}

// DeleteSecret removes secret data from the specified HSM path
func (c *GRPCClient) DeleteSecret(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	_, err := c.client.DeleteSecret(ctx, &hsmv1.DeleteSecretRequest{
		Path: path,
	})
	if err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}

	return nil
}

// ListSecrets returns a list of secret paths
func (c *GRPCClient) ListSecrets(ctx context.Context, prefix string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.ListSecrets(ctx, &hsmv1.ListSecretsRequest{
		Prefix: prefix,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	return resp.Paths, nil
}

// GetChecksum returns the SHA256 checksum of the secret data at the given path
func (c *GRPCClient) GetChecksum(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.GetChecksum(ctx, &hsmv1.GetChecksumRequest{
		Path: path,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get checksum: %w", err)
	}

	return resp.Checksum, nil
}

// IsConnected returns true if the HSM agent is connected and responsive
func (c *GRPCClient) IsConnected() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := c.client.IsConnected(ctx, &hsmv1.IsConnectedRequest{})
	if err != nil {
		// Check if this is a connection error vs HSM not connected
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.Unavailable, codes.DeadlineExceeded:
				// gRPC connection issues
				return false
			default:
				// Other errors might still mean the agent is reachable
				c.logger.V(1).Info("IsConnected check failed", "error", err)
				return false
			}
		}
		return false
	}

	return resp.Connected
}

// SetTimeout configures request timeout
func (c *GRPCClient) SetTimeout(timeout time.Duration) {
	c.timeout = timeout
}

// GetEndpoint returns the gRPC endpoint
func (c *GRPCClient) GetEndpoint() string {
	return c.endpoint
}
