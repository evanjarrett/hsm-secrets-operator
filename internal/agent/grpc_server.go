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
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hsmv1 "github.com/evanjarrett/hsm-secrets-operator/api/proto/hsm/v1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
	"github.com/evanjarrett/hsm-secrets-operator/internal/security"
)

const (
	healthyStatus  = "healthy"
	degradedStatus = "degraded"
)

// GRPCServer represents the HSM agent gRPC server
type GRPCServer struct {
	hsmv1.UnimplementedHSMAgentServer
	hsmClient   hsm.Client
	logger      logr.Logger
	port        int
	healthPort  int
	startTime   time.Time
	tlsConfig   *security.TLSConfig
	certRotator *security.CertificateRotator
	rateLimiter *security.RateLimiter
	validator   *security.InputValidator
	grpcServer  *grpc.Server
	k8sClient   client.Client
	serviceName string
	namespace   string
	dnsNames    []string
	ips         []net.IP
}

// GRPCServerConfig configures the gRPC server
type GRPCServerConfig struct {
	ServiceName string
	Namespace   string
	DNSNames    []string
	IPs         []net.IP
	K8sClient   client.Client
	EnableTLS   bool
}

// NewGRPCServer creates a new HSM agent gRPC server with optional mTLS
func NewGRPCServer(hsmClient hsm.Client, port, healthPort int, config GRPCServerConfig, logger logr.Logger) *GRPCServer {
	server := &GRPCServer{
		hsmClient:   hsmClient,
		logger:      logger.WithName("grpc-server"),
		port:        port,
		healthPort:  healthPort,
		startTime:   time.Now(),
		rateLimiter: security.NewRateLimiter(),
		validator:   security.NewInputValidator(),
		k8sClient:   config.K8sClient,
		serviceName: config.ServiceName,
		namespace:   config.Namespace,
		dnsNames:    config.DNSNames,
		ips:         config.IPs,
	}

	// Initialize certificate rotation if TLS is enabled and k8s client is available
	if config.EnableTLS && config.K8sClient != nil {
		if err := server.initializeTLS(); err != nil {
			logger.Error(err, "Failed to initialize TLS, falling back to insecure mode")
		}
	}

	return server
}

// initializeTLS sets up certificate rotation for the server
func (s *GRPCServer) initializeTLS() error {
	if s.serviceName == "" {
		s.serviceName = "hsm-agent"
	}
	if s.namespace == "" {
		return fmt.Errorf("namespace not configured in GRPCServerConfig")
	}

	// Create certificate rotator
	rotatorConfig := security.DefaultRotatorConfig(
		s.namespace,
		fmt.Sprintf("%s-tls", s.serviceName),
		s.serviceName,
	)
	rotatorConfig.DNSNames = s.dnsNames
	rotatorConfig.IPs = s.ips

	rotator, err := security.NewCertificateRotator(s.k8sClient, rotatorConfig, s.logger)
	if err != nil {
		return fmt.Errorf("failed to create certificate rotator: %w", err)
	}

	s.certRotator = rotator

	// Register callback for certificate updates
	s.certRotator.AddUpdateCallback(s.onCertificateUpdate)

	return nil
}

// Start starts both the gRPC server and health server
func (s *GRPCServer) Start(ctx context.Context) error {
	// Start health server in background (HTTP for simplicity with probes)
	go s.startHealthServer(ctx)

	// Start certificate rotation if enabled
	if s.certRotator != nil {
		if err := s.certRotator.Start(ctx, s.serviceName, s.dnsNames, s.ips); err != nil {
			s.logger.Error(err, "Failed to start certificate rotator, falling back to insecure mode")
			s.certRotator = nil
		} else {
			s.logger.Info("Certificate rotator started successfully")
			// Get initial TLS config
			s.tlsConfig = s.certRotator.GetCurrentTLSConfig()
		}
	}

	// Start gRPC server with current configuration
	return s.startGRPCServer(ctx)
}

// startGRPCServer creates and starts the gRPC server with current TLS configuration
func (s *GRPCServer) startGRPCServer(ctx context.Context) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.port, err)
	}

	// Configure server with security interceptors and TLS if available
	var serverOptions []grpc.ServerOption

	// Add TLS credentials if configured
	if s.tlsConfig != nil {
		creds, err := s.tlsConfig.GetServerCredentials()
		if err != nil {
			s.logger.Error(err, "Failed to get server credentials, continuing without TLS")
		} else {
			serverOptions = append(serverOptions, grpc.Creds(creds))
			s.logger.Info("gRPC server configured with mTLS")
		}
	}

	if s.tlsConfig == nil {
		s.logger.Info("gRPC server running without TLS (development mode)")
	}

	// Add security interceptors
	serverOptions = append(serverOptions,
		grpc.ChainUnaryInterceptor(
			security.SecurityInterceptor(s.rateLimiter, s.validator),
			s.loggingInterceptor,
		),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             15 * time.Second, // Allow pings every 15s minimum
			PermitWithoutStream: true,             // Allow pings without active streams
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    60 * time.Second, // Send pings every 60s if no activity
			Timeout: 10 * time.Second, // Wait 10s for ping response
		}),
	)

	s.grpcServer = grpc.NewServer(serverOptions...)

	// Register the HSM agent service
	hsmv1.RegisterHSMAgentServer(s.grpcServer, s)

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		s.logger.Info("Shutting down gRPC server")
		if s.certRotator != nil {
			s.certRotator.Stop()
		}
		s.grpcServer.GracefulStop()
	}()

	s.logger.Info("Starting HSM agent gRPC server", "port", s.port, "tls_enabled", s.tlsConfig != nil)
	return s.grpcServer.Serve(lis)
}

// onCertificateUpdate handles certificate rotation updates
func (s *GRPCServer) onCertificateUpdate(newTLSConfig *security.TLSConfig) error {
	s.logger.Info("Certificate rotation detected, updating TLS configuration")

	// Update the TLS config - the next gRPC connection will use the new certificates
	s.tlsConfig = newTLSConfig

	s.logger.Info("TLS configuration updated successfully")
	return nil
}

// startHealthServer starts the HTTP health server
func (s *GRPCServer) startHealthServer(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/cert-info", s.handleCertInfo)

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

// WriteSecret writes secret data and metadata to the specified HSM path
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

	if err := s.hsmClient.WriteSecret(ctx, req.Path, hsmData, metadata); err != nil {
		s.logger.Error(err, "Failed to write secret with metadata", "path", req.Path)
		return nil, status.Errorf(codes.Internal, "failed to write secret with metadata: %v", err)
	}

	s.logger.V(1).Info("Successfully wrote secret with metadata", "path", req.Path, "keys_count", len(hsmData))
	return &hsmv1.WriteSecretResponse{}, nil
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
	if _, err := fmt.Fprintf(w, `{"status":"%s","hsmConnected":%t,"uptime":"%s","timestamp":"%s"}`,
		healthStatus, hsmConnected, uptime, time.Now().Format(time.RFC3339)); err != nil {
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

// handleCertInfo handles certificate information requests (HTTP)
func (s *GRPCServer) handleCertInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.certRotator == nil {
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprintf(w, `{"tls_enabled":false,"mode":"development"}`); err != nil {
			s.logger.Error(err, "Failed to write cert info response")
		}
		return
	}

	certInfo := s.certRotator.GetCertificateInfo()
	certInfo["tls_enabled"] = true
	certInfo["mode"] = "production"

	// Convert to JSON manually for simplicity
	certStatus := certInfo["status"].(string)

	response := fmt.Sprintf(`{
		"tls_enabled": true,
		"mode": "production",
		"status": "%s"`, certStatus)

	if expiry, ok := certInfo["expiry"].(string); ok {
		response += fmt.Sprintf(`,
		"certificate_expiry": "%s"`, expiry)
	}

	if timeUntilExpiry, ok := certInfo["time_until_expiry"].(string); ok {
		response += fmt.Sprintf(`,
		"time_until_expiry": "%s"`, timeUntilExpiry)
	}

	if needsRenewal, ok := certInfo["needs_renewal"].(bool); ok {
		response += fmt.Sprintf(`,
		"needs_renewal": %t`, needsRenewal)
	}

	if rotationInterval, ok := certInfo["rotation_interval"].(string); ok {
		response += fmt.Sprintf(`,
		"rotation_interval": "%s"`, rotationInterval)
	}

	response += "}"

	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(w, "%s", response); err != nil {
		s.logger.Error(err, "Failed to write cert info response")
	}
}

// loggingInterceptor provides gRPC request logging
func (s *GRPCServer) loggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()

	// Extract request-specific details
	logFields := []interface{}{
		"method", info.FullMethod,
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
