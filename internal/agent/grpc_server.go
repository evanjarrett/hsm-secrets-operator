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
	"net"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hsmv1 "github.com/evanjarrett/hsm-secrets-operator/api/proto/hsm/v1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

const (
	healthyStatus  = "healthy"
	degradedStatus = "degraded"
)

// GRPCServer represents the HSM agent gRPC server
type GRPCServer struct {
	hsmv1.UnimplementedHSMAgentServer
	hsmClient  hsm.Client
	logger     logr.Logger
	deviceName string
	port       int
	healthPort int
	startTime  time.Time
}

// NewGRPCServer creates a new HSM agent gRPC server
func NewGRPCServer(hsmClient hsm.Client, deviceName string, port, healthPort int, logger logr.Logger) *GRPCServer {
	return &GRPCServer{
		hsmClient:  hsmClient,
		logger:     logger.WithName("grpc-server"),
		deviceName: deviceName,
		port:       port,
		healthPort: healthPort,
		startTime:  time.Now(),
	}
}

// Start starts both the gRPC server and health server
func (s *GRPCServer) Start(ctx context.Context) error {
	// Start health server in background (HTTP for simplicity with probes)
	go s.startHealthServer(ctx)

	// Start gRPC server
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.port, err)
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(s.loggingInterceptor),
	)

	// Register the HSM agent service
	hsmv1.RegisterHSMAgentServer(grpcServer, s)

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		s.logger.Info("Shutting down gRPC server")
		grpcServer.GracefulStop()
	}()

	s.logger.Info("Starting HSM agent gRPC server", "port", s.port, "device", s.deviceName)
	return grpcServer.Serve(lis)
}

// startHealthServer starts the HTTP health server
func (s *GRPCServer) startHealthServer(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.healthPort),
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			s.logger.Error(err, "Failed to shutdown health server")
		}
	}()

	s.logger.Info("Starting health server", "port", s.healthPort)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		s.logger.Error(err, "Health server failed")
	}
}

// GetInfo returns information about the HSM device
func (s *GRPCServer) GetInfo(ctx context.Context, req *hsmv1.GetInfoRequest) (*hsmv1.GetInfoResponse, error) {
	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		return nil, status.Error(codes.Unavailable, "HSM client not connected")
	}

	info, err := s.hsmClient.GetInfo(ctx)
	if err != nil {
		s.logger.Error(err, "Failed to get HSM info")
		return nil, status.Errorf(codes.Internal, "failed to get HSM info: %v", err)
	}

	return &hsmv1.GetInfoResponse{
		HsmInfo: &hsmv1.HSMInfo{
			Label:           info.Label,
			Manufacturer:    info.Manufacturer,
			Model:           info.Model,
			SerialNumber:    info.SerialNumber,
			FirmwareVersion: info.FirmwareVersion,
		},
	}, nil
}

// ReadSecret reads secret data from the specified HSM path
func (s *GRPCServer) ReadSecret(ctx context.Context, req *hsmv1.ReadSecretRequest) (*hsmv1.ReadSecretResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		return nil, status.Error(codes.Unavailable, "HSM client not connected")
	}

	data, err := s.hsmClient.ReadSecret(ctx, req.Path)
	if err != nil {
		s.logger.Error(err, "Failed to read secret", "path", req.Path)
		return nil, status.Errorf(codes.Internal, "failed to read secret: %v", err)
	}

	// Convert hsm.SecretData to protobuf format
	pbData := make(map[string][]byte)
	for key, value := range data {
		pbData[key] = value
	}

	s.logger.V(1).Info("Successfully read secret", "path", req.Path, "keys_count", len(pbData))

	return &hsmv1.ReadSecretResponse{
		SecretData: &hsmv1.SecretData{Data: pbData},
	}, nil
}

// WriteSecret writes secret data to the specified HSM path
func (s *GRPCServer) WriteSecret(ctx context.Context, req *hsmv1.WriteSecretRequest) (*hsmv1.WriteSecretResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	if req.SecretData == nil {
		return nil, status.Error(codes.InvalidArgument, "secret data is required")
	}

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		return nil, status.Error(codes.Unavailable, "HSM client not connected")
	}

	// Convert protobuf format to hsm.SecretData
	hsmData := make(hsm.SecretData)
	for key, value := range req.SecretData.Data {
		hsmData[key] = value
	}

	if err := s.hsmClient.WriteSecret(ctx, req.Path, hsmData); err != nil {
		s.logger.Error(err, "Failed to write secret", "path", req.Path)
		return nil, status.Errorf(codes.Internal, "failed to write secret: %v", err)
	}

	s.logger.V(1).Info("Successfully wrote secret", "path", req.Path, "keys_count", len(hsmData))
	return &hsmv1.WriteSecretResponse{}, nil
}

// WriteSecretWithMetadata writes secret data and metadata to the specified HSM path
func (s *GRPCServer) WriteSecretWithMetadata(ctx context.Context, req *hsmv1.WriteSecretWithMetadataRequest) (*hsmv1.WriteSecretWithMetadataResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	if req.SecretData == nil {
		return nil, status.Error(codes.InvalidArgument, "secret data is required")
	}

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		return nil, status.Error(codes.Unavailable, "HSM client not connected")
	}

	// Convert protobuf format to hsm.SecretData
	hsmData := make(hsm.SecretData)
	for key, value := range req.SecretData.Data {
		hsmData[key] = value
	}

	// Convert protobuf metadata to hsm.SecretMetadata
	var metadata *hsm.SecretMetadata
	if req.Metadata != nil {
		metadata = &hsm.SecretMetadata{
			Description: req.Metadata.Description,
			Labels:      req.Metadata.Labels,
			Format:      req.Metadata.Format,
			DataType:    req.Metadata.DataType,
			CreatedAt:   req.Metadata.CreatedAt,
			Source:      req.Metadata.Source,
		}
	}

	if err := s.hsmClient.WriteSecretWithMetadata(ctx, req.Path, hsmData, metadata); err != nil {
		s.logger.Error(err, "Failed to write secret with metadata", "path", req.Path)
		return nil, status.Errorf(codes.Internal, "failed to write secret with metadata: %v", err)
	}

	s.logger.V(1).Info("Successfully wrote secret with metadata", "path", req.Path, "keys_count", len(hsmData))
	return &hsmv1.WriteSecretWithMetadataResponse{}, nil
}

// ReadMetadata reads metadata for a secret at the given path
func (s *GRPCServer) ReadMetadata(ctx context.Context, req *hsmv1.ReadMetadataRequest) (*hsmv1.ReadMetadataResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		return nil, status.Error(codes.Unavailable, "HSM client not connected")
	}

	metadata, err := s.hsmClient.ReadMetadata(ctx, req.Path)
	if err != nil {
		s.logger.Error(err, "Failed to read metadata", "path", req.Path)
		return nil, status.Errorf(codes.Internal, "failed to read metadata: %v", err)
	}

	var pbMetadata *hsmv1.SecretMetadata
	if metadata != nil {
		pbMetadata = &hsmv1.SecretMetadata{
			Description: metadata.Description,
			Labels:      metadata.Labels,
			Format:      metadata.Format,
			DataType:    metadata.DataType,
			CreatedAt:   metadata.CreatedAt,
			Source:      metadata.Source,
		}
	}

	return &hsmv1.ReadMetadataResponse{
		Metadata: pbMetadata,
	}, nil
}

// DeleteSecret removes secret data from the specified HSM path
func (s *GRPCServer) DeleteSecret(ctx context.Context, req *hsmv1.DeleteSecretRequest) (*hsmv1.DeleteSecretResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		return nil, status.Error(codes.Unavailable, "HSM client not connected")
	}

	if err := s.hsmClient.DeleteSecret(ctx, req.Path); err != nil {
		s.logger.Error(err, "Failed to delete secret", "path", req.Path)
		return nil, status.Errorf(codes.Internal, "failed to delete secret: %v", err)
	}

	s.logger.V(1).Info("Successfully deleted secret", "path", req.Path)
	return &hsmv1.DeleteSecretResponse{}, nil
}

// ListSecrets returns a list of secret paths
func (s *GRPCServer) ListSecrets(ctx context.Context, req *hsmv1.ListSecretsRequest) (*hsmv1.ListSecretsResponse, error) {
	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		return nil, status.Error(codes.Unavailable, "HSM client not connected")
	}

	paths, err := s.hsmClient.ListSecrets(ctx, req.Prefix)
	if err != nil {
		s.logger.Error(err, "Failed to list secrets", "prefix", req.Prefix)
		return nil, status.Errorf(codes.Internal, "failed to list secrets: %v", err)
	}

	s.logger.V(1).Info("Successfully listed secrets", "prefix", req.Prefix, "count", len(paths))
	return &hsmv1.ListSecretsResponse{
		Paths: paths,
	}, nil
}

// GetChecksum returns the SHA256 checksum of the secret data at the given path
func (s *GRPCServer) GetChecksum(ctx context.Context, req *hsmv1.GetChecksumRequest) (*hsmv1.GetChecksumResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		return nil, status.Error(codes.Unavailable, "HSM client not connected")
	}

	checksum, err := s.hsmClient.GetChecksum(ctx, req.Path)
	if err != nil {
		s.logger.Error(err, "Failed to get checksum", "path", req.Path)
		return nil, status.Errorf(codes.Internal, "failed to get checksum: %v", err)
	}

	return &hsmv1.GetChecksumResponse{
		Checksum: checksum,
	}, nil
}

// IsConnected returns true if the HSM is connected and responsive
func (s *GRPCServer) IsConnected(ctx context.Context, req *hsmv1.IsConnectedRequest) (*hsmv1.IsConnectedResponse, error) {
	connected := s.hsmClient != nil && s.hsmClient.IsConnected()
	return &hsmv1.IsConnectedResponse{
		Connected: connected,
	}, nil
}

// Health check for gRPC health protocol
func (s *GRPCServer) Health(ctx context.Context, req *hsmv1.HealthRequest) (*hsmv1.HealthResponse, error) {
	healthStatus := healthyStatus
	message := "Agent is running normally"

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		healthStatus = degradedStatus
		message = "HSM client not connected"
	}

	return &hsmv1.HealthResponse{
		Status:  healthStatus,
		Message: message,
	}, nil
}

// handleHealthz handles liveness probe requests (HTTP)
func (s *GRPCServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	healthStatus := healthyStatus
	hsmConnected := s.hsmClient != nil && s.hsmClient.IsConnected()

	if !hsmConnected {
		healthStatus = degradedStatus
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	w.Header().Set("Content-Type", "application/json")
	uptime := time.Since(s.startTime).String()
	if _, err := fmt.Fprintf(w, `{"status":"%s","deviceName":"%s","hsmConnected":%t,"uptime":"%s","timestamp":"%s"}`,
		healthStatus, s.deviceName, hsmConnected, uptime, time.Now().Format(time.RFC3339)); err != nil {
		s.logger.Error(err, "Failed to write health response")
	}
}

// handleReadyz handles readiness probe requests (HTTP)
func (s *GRPCServer) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprintf(w, `{"status":"not_ready","reason":"hsm_not_connected"}`); err != nil {
			s.logger.Error(err, "Failed to write readiness response")
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	if _, err := fmt.Fprintf(w, `{"status":"ready"}`); err != nil {
		s.logger.Error(err, "Failed to write readiness response")
	}
}

// loggingInterceptor provides gRPC request logging
func (s *GRPCServer) loggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()

	// Extract request-specific details
	logFields := []interface{}{
		"method", info.FullMethod,
		"device", s.deviceName,
	}

	// Add request-specific fields based on the method
	switch r := req.(type) {
	case *hsmv1.ReadSecretRequest:
		logFields = append(logFields, "path", r.Path)
	case *hsmv1.WriteSecretRequest:
		logFields = append(logFields, "path", r.Path)
		if r.SecretData != nil {
			logFields = append(logFields, "keys_count", len(r.SecretData.Data))
		}
	case *hsmv1.WriteSecretWithMetadataRequest:
		logFields = append(logFields, "path", r.Path)
		if r.SecretData != nil {
			logFields = append(logFields, "keys_count", len(r.SecretData.Data))
		}
	case *hsmv1.DeleteSecretRequest:
		logFields = append(logFields, "path", r.Path)
	case *hsmv1.ListSecretsRequest:
		logFields = append(logFields, "prefix", r.Prefix)
	case *hsmv1.GetChecksumRequest:
		logFields = append(logFields, "path", r.Path)
	case *hsmv1.ReadMetadataRequest:
		logFields = append(logFields, "path", r.Path)
	}

	s.logger.Info("gRPC request started", logFields...)

	resp, err := handler(ctx, req)

	duration := time.Since(start)
	code := codes.OK
	errorMsg := ""
	if err != nil {
		if st, ok := status.FromError(err); ok {
			code = st.Code()
			errorMsg = st.Message()
		} else {
			code = codes.Unknown
			errorMsg = err.Error()
		}
	}

	// Log completion with result details
	resultFields := append(logFields,
		"code", code.String(),
		"duration", duration,
		"success", err == nil,
	)

	if err != nil {
		resultFields = append(resultFields, "error", errorMsg)
		s.logger.Error(err, "gRPC request failed", resultFields...)
	} else {
		// Add response-specific fields for successful requests
		switch r := resp.(type) {
		case *hsmv1.ReadSecretResponse:
			if r.SecretData != nil {
				resultFields = append(resultFields, "keys_returned", len(r.SecretData.Data))
			}
		case *hsmv1.ListSecretsResponse:
			resultFields = append(resultFields, "secrets_count", len(r.Paths))
		}
		s.logger.Info("gRPC request completed", resultFields...)
	}

	return resp, err
}
