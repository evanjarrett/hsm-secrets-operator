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

package api

import (
	"context"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"

	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// ProxyClient handles HTTP requests and proxies them to gRPC clients
// It has methods that match the HTTP endpoints and handle the full request/response cycle
type ProxyClient struct {
	server       *Server
	logger       logr.Logger
	grpcClients  map[string]hsm.Client // deviceName -> gRPC client
	clientsMutex sync.RWMutex
}

// NewProxyClient creates a new ProxyClient that handles HTTP routing
func NewProxyClient(server *Server, logger logr.Logger) *ProxyClient {
	return &ProxyClient{
		server:      server,
		logger:      logger.WithName("proxy-client"),
		grpcClients: make(map[string]hsm.Client),
	}
}

// getOrCreateGRPCClient returns the cached gRPC client for a device or creates a new one
func (p *ProxyClient) getOrCreateGRPCClient(c *gin.Context) (hsm.Client, error) {
	// Extract namespace
	namespace := c.GetHeader("X-Namespace")
	if namespace == "" {
		namespace = "secrets"
	}

	// Find available agent
	deviceName, err := p.server.findAvailableAgent(c.Request.Context(), namespace)
	if err != nil {
		return nil, err
	}

	// Try to get existing client for this device with read lock
	p.clientsMutex.RLock()
	if client, exists := p.grpcClients[deviceName]; exists && client.IsConnected() {
		p.clientsMutex.RUnlock()
		return client, nil
	}
	p.clientsMutex.RUnlock()

	// Need to create/recreate client with write lock
	p.clientsMutex.Lock()
	defer p.clientsMutex.Unlock()

	// Double-check in case another goroutine created it
	if client, exists := p.grpcClients[deviceName]; exists && client.IsConnected() {
		return client, nil
	}

	// Close existing client for this device if it exists
	if oldClient, exists := p.grpcClients[deviceName]; exists {
		if closeErr := oldClient.Close(); closeErr != nil {
			p.logger.V(1).Info("Error closing old gRPC client", "device", deviceName, "error", closeErr)
		}
		delete(p.grpcClients, deviceName)
	}

	// Create new gRPC client
	grpcClient, err := p.server.createGRPCClient(c.Request.Context(), deviceName, namespace)
	if err != nil {
		return nil, err
	}

	// Cache the client for this device
	p.grpcClients[deviceName] = grpcClient
	p.logger.V(1).Info("Created new gRPC client", "device", deviceName)
	return grpcClient, nil
}

// GetInfo handles GET /hsm/info
func (p *ProxyClient) GetInfo(c *gin.Context) {
	grpcClient, err := p.getOrCreateGRPCClient(c)
	if err != nil {
		p.server.sendError(c, http.StatusServiceUnavailable, "no_agent", "No HSM agents available", map[string]any{
			"error": err.Error(),
		})
		return
	}

	info, err := grpcClient.GetInfo(c.Request.Context())
	if err != nil {
		p.server.sendError(c, http.StatusInternalServerError, "grpc_error", "Failed to get HSM info", map[string]any{
			"error": err.Error(),
		})
		return
	}

	p.server.sendResponse(c, http.StatusOK, "HSM info retrieved successfully", info)
}

// ListSecrets handles GET /hsm/secrets
func (p *ProxyClient) ListSecrets(c *gin.Context) {
	prefix := c.Query("prefix")

	grpcClient, err := p.getOrCreateGRPCClient(c)
	if err != nil {
		p.server.sendError(c, http.StatusServiceUnavailable, "no_agent", "No HSM agents available", map[string]any{
			"error": err.Error(),
		})
		return
	}

	secrets, err := grpcClient.ListSecrets(c.Request.Context(), prefix)
	if err != nil {
		p.server.sendError(c, http.StatusInternalServerError, "grpc_error", "Failed to list secrets from HSM", map[string]any{
			"error": err.Error(),
		})
		return
	}

	response := map[string]any{
		"secrets": secrets,
		"count":   len(secrets),
		"prefix":  prefix,
	}
	p.server.sendResponse(c, http.StatusOK, "Secrets listed successfully", response)
}

// ReadSecret handles GET /hsm/secrets/:path
func (p *ProxyClient) ReadSecret(c *gin.Context) {
	path := c.Param("path")
	if path == "" {
		p.server.sendError(c, http.StatusBadRequest, "missing_path", "Secret path is required", nil)
		return
	}

	grpcClient, err := p.getOrCreateGRPCClient(c)
	if err != nil {
		p.server.sendError(c, http.StatusServiceUnavailable, "no_agent", "No HSM agents available", map[string]any{
			"error": err.Error(),
		})
		return
	}

	data, err := grpcClient.ReadSecret(c.Request.Context(), path)
	if err != nil {
		p.server.sendError(c, http.StatusInternalServerError, "grpc_error", "Failed to read secret from HSM", map[string]any{
			"error": err.Error(),
			"path":  path,
		})
		return
	}

	response := map[string]any{
		"path": path,
		"data": data,
	}
	p.server.sendResponse(c, http.StatusOK, "Secret read successfully", response)
}

// WriteSecret handles POST/PUT /hsm/secrets/:path
func (p *ProxyClient) WriteSecret(c *gin.Context) {
	path := c.Param("path")
	if path == "" {
		p.server.sendError(c, http.StatusBadRequest, "missing_path", "Secret path is required", nil)
		return
	}

	// Parse request body
	var req struct {
		Data     map[string]string   `json:"data" binding:"required"`
		Metadata *hsm.SecretMetadata `json:"metadata,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		p.server.sendError(c, http.StatusBadRequest, "parse_error", "Failed to parse request body", map[string]any{
			"error": err.Error(),
		})
		return
	}

	// Convert string data to byte data
	data := make(hsm.SecretData)
	for key, value := range req.Data {
		data[key] = []byte(value)
	}

	grpcClient, err := p.getOrCreateGRPCClient(c)
	if err != nil {
		p.server.sendError(c, http.StatusServiceUnavailable, "no_agent", "No HSM agents available", map[string]any{
			"error": err.Error(),
		})
		return
	}

	if req.Metadata != nil {
		err = grpcClient.WriteSecretWithMetadata(c.Request.Context(), path, data, req.Metadata)
	} else {
		err = grpcClient.WriteSecret(c.Request.Context(), path, data)
	}

	if err != nil {
		p.server.sendError(c, http.StatusInternalServerError, "grpc_error", "Failed to write secret to HSM", map[string]any{
			"error": err.Error(),
			"path":  path,
		})
		return
	}

	response := map[string]any{
		"path": path,
		"keys": len(data),
	}
	if req.Metadata != nil {
		response["metadata"] = req.Metadata
	}
	p.server.sendResponse(c, http.StatusCreated, "Secret written successfully", response)
}

// DeleteSecret handles DELETE /hsm/secrets/:path
func (p *ProxyClient) DeleteSecret(c *gin.Context) {
	path := c.Param("path")
	if path == "" {
		p.server.sendError(c, http.StatusBadRequest, "missing_path", "Secret path is required", nil)
		return
	}

	grpcClient, err := p.getOrCreateGRPCClient(c)
	if err != nil {
		p.server.sendError(c, http.StatusServiceUnavailable, "no_agent", "No HSM agents available", map[string]any{
			"error": err.Error(),
		})
		return
	}

	err = grpcClient.DeleteSecret(c.Request.Context(), path)
	if err != nil {
		p.server.sendError(c, http.StatusInternalServerError, "grpc_error", "Failed to delete secret from HSM", map[string]any{
			"error": err.Error(),
			"path":  path,
		})
		return
	}

	response := map[string]any{
		"path": path,
	}
	p.server.sendResponse(c, http.StatusOK, "Secret deleted successfully", response)
}

// ReadMetadata handles GET /hsm/secrets/:path/metadata
func (p *ProxyClient) ReadMetadata(c *gin.Context) {
	path := c.Param("path")
	if path == "" {
		p.server.sendError(c, http.StatusBadRequest, "missing_path", "Secret path is required", nil)
		return
	}

	grpcClient, err := p.getOrCreateGRPCClient(c)
	if err != nil {
		p.server.sendError(c, http.StatusServiceUnavailable, "no_agent", "No HSM agents available", map[string]any{
			"error": err.Error(),
		})
		return
	}

	metadata, err := grpcClient.ReadMetadata(c.Request.Context(), path)
	if err != nil {
		p.server.sendError(c, http.StatusInternalServerError, "grpc_error", "Failed to read metadata from HSM", map[string]any{
			"error": err.Error(),
			"path":  path,
		})
		return
	}

	response := map[string]any{
		"path":     path,
		"metadata": metadata,
	}
	p.server.sendResponse(c, http.StatusOK, "Metadata read successfully", response)
}

// GetChecksum handles GET /hsm/secrets/:path/checksum
func (p *ProxyClient) GetChecksum(c *gin.Context) {
	path := c.Param("path")
	if path == "" {
		p.server.sendError(c, http.StatusBadRequest, "missing_path", "Secret path is required", nil)
		return
	}

	grpcClient, err := p.getOrCreateGRPCClient(c)
	if err != nil {
		p.server.sendError(c, http.StatusServiceUnavailable, "no_agent", "No HSM agents available", map[string]any{
			"error": err.Error(),
		})
		return
	}

	checksum, err := grpcClient.GetChecksum(c.Request.Context(), path)
	if err != nil {
		p.server.sendError(c, http.StatusInternalServerError, "grpc_error", "Failed to get checksum from HSM", map[string]any{
			"error": err.Error(),
			"path":  path,
		})
		return
	}

	response := map[string]any{
		"path":     path,
		"checksum": checksum,
	}
	p.server.sendResponse(c, http.StatusOK, "Checksum retrieved successfully", response)
}

// IsConnected handles GET /hsm/status
func (p *ProxyClient) IsConnected(c *gin.Context) {
	grpcClient, err := p.getOrCreateGRPCClient(c)
	if err != nil {
		p.server.sendError(c, http.StatusServiceUnavailable, "no_agent", "No HSM agents available", map[string]any{
			"error": err.Error(),
		})
		return
	}

	connected := grpcClient.IsConnected()

	response := map[string]any{
		"connected": connected,
	}

	status := http.StatusOK
	message := "HSM connection status retrieved"
	if !connected {
		status = http.StatusServiceUnavailable
		message = "HSM is not connected"
	}

	p.server.sendResponse(c, status, message, response)
}

// Close closes all cached gRPC clients
func (p *ProxyClient) Close() error {
	p.clientsMutex.Lock()
	defer p.clientsMutex.Unlock()

	var lastErr error
	for deviceName, client := range p.grpcClients {
		if err := client.Close(); err != nil {
			p.logger.Error(err, "Failed to close gRPC client", "device", deviceName)
			lastErr = err
		}
	}

	// Clear the map
	p.grpcClients = make(map[string]hsm.Client)
	return lastErr
}

// CleanupDisconnectedClients removes disconnected clients from the cache
func (p *ProxyClient) CleanupDisconnectedClients() {
	p.clientsMutex.Lock()
	defer p.clientsMutex.Unlock()

	for deviceName, client := range p.grpcClients {
		if !client.IsConnected() {
			p.logger.V(1).Info("Removing disconnected gRPC client", "device", deviceName)
			if closeErr := client.Close(); closeErr != nil {
				p.logger.V(1).Info("Error closing disconnected gRPC client", "device", deviceName, "error", closeErr)
			}
			delete(p.grpcClients, deviceName)
		}
	}
}

// GetClientCount returns the number of cached gRPC clients
func (p *ProxyClient) GetClientCount() int {
	p.clientsMutex.RLock()
	defer p.clientsMutex.RUnlock()
	return len(p.grpcClients)
}

// Interface compliance methods (unused in HTTP mode but required for hsm.Client interface)
func (p *ProxyClient) Initialize(ctx context.Context, config hsm.Config) error { return nil }
