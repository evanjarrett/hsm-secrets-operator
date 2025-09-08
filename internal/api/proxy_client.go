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
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"

	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// WriteResult represents the result of writing to a single device
type WriteResult struct {
	DeviceName string
	Error      error
}

// checksumResult represents the result of getting checksum from a single device
type checksumResult struct {
	deviceName string
	checksum   string
	err        error
}

// secretResult represents the result of reading secret from a single device
type secretResult struct {
	deviceName string
	data       hsm.SecretData
	metadata   *hsm.SecretMetadata
	err        error
}

// metadataResult represents the result of reading metadata from a single device
type metadataResult struct {
	deviceName string
	metadata   *hsm.SecretMetadata
	err        error
}

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

// Helper function to parse timestamp from metadata
func parseTimestampFromMetadata(metadata *hsm.SecretMetadata) int64 {
	if metadata == nil || metadata.Labels == nil {
		return 0
	}

	// Try RFC3339 timestamp first
	if syncTimestamp, exists := metadata.Labels["sync.timestamp"]; exists {
		if parsedTime, err := time.Parse(time.RFC3339, syncTimestamp); err == nil {
			return parsedTime.Unix()
		}
	}

	// Fall back to Unix timestamp in sync.version
	if syncVersion, exists := metadata.Labels["sync.version"]; exists {
		var timestamp int64
		if n, err := fmt.Sscanf(syncVersion, "%d", &timestamp); n == 1 && err == nil {
			return timestamp
		}
	}

	return 0
}

// isSecretDeleted checks if a secret is marked as deleted via tombstone metadata
func isSecretDeleted(metadata *hsm.SecretMetadata) bool {
	if metadata == nil || metadata.Labels == nil {
		return false
	}
	return metadata.Labels["sync.deleted"] == "true"
}

// findConsensusChecksum finds the most common checksum among results and logs inconsistencies
func (p *ProxyClient) findConsensusChecksum(results []checksumResult, path string) (string, error) {
	if len(results) == 0 {
		return "", fmt.Errorf("checksum not found on any HSM device")
	}

	// Count occurrences of each checksum
	checksumCounts := make(map[string]int)
	for _, result := range results {
		checksumCounts[result.checksum]++
	}

	// Find the most common checksum (consensus)
	var consensusChecksum string
	var maxCount int
	for checksum, count := range checksumCounts {
		if count > maxCount {
			consensusChecksum = checksum
			maxCount = count
		}
	}

	// Log checksum inconsistencies
	if len(checksumCounts) > 1 {
		inconsistentDevices := make([]string, 0)
		for _, result := range results {
			if result.checksum != consensusChecksum {
				inconsistentDevices = append(inconsistentDevices, result.deviceName)
			}
		}
		p.logger.Info("Checksum inconsistency detected, using consensus",
			"path", path,
			"consensus", consensusChecksum,
			"consensus_count", maxCount,
			"total_devices", len(results),
			"inconsistent_devices", inconsistentDevices)
	}

	return consensusChecksum, nil
}

// logMultiDeviceOperation logs when operations are performed across multiple devices with sync information
func (p *ProxyClient) logMultiDeviceOperation(deviceNames []string, selectedDevice, operationName, path, syncDetails string) {
	p.logger.Info(fmt.Sprintf("%s found on multiple devices, using most recent version", operationName),
		"path", path,
		"devices", deviceNames,
		"selected", selectedDevice,
		"syncDetails", syncDetails)
}

// findMostRecentSecretResult finds the most recent secret result based on metadata timestamps
func (p *ProxyClient) findMostRecentSecretResult(results []secretResult, path string) (hsm.SecretData, error) {
	if len(results) == 0 {
		return nil, fmt.Errorf("secret not found on any HSM device")
	}

	// Find most recent version based on metadata timestamps
	bestResult := results[0]
	bestTimestamp := parseTimestampFromMetadata(bestResult.metadata)

	for _, result := range results[1:] {
		timestamp := parseTimestampFromMetadata(result.metadata)
		if timestamp > bestTimestamp {
			bestResult = result
			bestTimestamp = timestamp
		}
	}

	// Log sync issues when multiple devices have different versions
	if len(results) > 1 {
		deviceNames := make([]string, len(results))
		for i, result := range results {
			deviceNames[i] = result.deviceName
		}
		p.logMultiDeviceOperation(deviceNames, bestResult.deviceName, "Secret", path, fmt.Sprintf("timestamp: %d", bestTimestamp))
	}

	// Check if the most recent result is a tombstone (deleted secret)
	if isSecretDeleted(bestResult.metadata) {
		return nil, fmt.Errorf("secret not found on any HSM device")
	}

	return bestResult.data, nil
}

// findMostRecentMetadataResult finds the most recent metadata result based on timestamps
func (p *ProxyClient) findMostRecentMetadataResult(results []metadataResult, path string) (*hsm.SecretMetadata, error) {
	if len(results) == 0 {
		return nil, fmt.Errorf("metadata not found on any HSM device")
	}

	// Find most recent version based on metadata timestamps
	bestResult := results[0]
	bestTimestamp := parseTimestampFromMetadata(bestResult.metadata)

	for _, result := range results[1:] {
		timestamp := parseTimestampFromMetadata(result.metadata)
		if timestamp > bestTimestamp {
			bestResult = result
			bestTimestamp = timestamp
		}
	}

	// Log sync issues when multiple devices have different versions
	if len(results) > 1 {
		deviceNames := make([]string, len(results))
		for i, result := range results {
			deviceNames[i] = result.deviceName
		}
		p.logMultiDeviceOperation(deviceNames, bestResult.deviceName, "Metadata", path, fmt.Sprintf("timestamp: %d", bestTimestamp))
	}

	// Return the most recent metadata, even if it's a tombstone
	// This allows callers to see deletion information when needed
	return bestResult.metadata, nil
}

// validatePathParam validates the path parameter and sends error if missing
// Returns (path, true) on success, or ("", false) if path is missing (error already sent to client)
func (p *ProxyClient) validatePathParam(c *gin.Context) (string, bool) {
	path := c.Param("path")
	if path == "" {
		p.server.sendError(c, http.StatusBadRequest, "missing_path", "Secret path is required", nil)
		return "", false
	}
	return path, true
}

// getAllAvailableGRPCClients returns all available gRPC clients for mirroring operations
// Returns (clients, true) on success, or (nil, false) if no agents available (error already sent to client)
func (p *ProxyClient) getAllAvailableGRPCClients(c *gin.Context) (map[string]hsm.Client, bool) {
	// Use operator namespace where HSMPools and agents are located
	namespace := p.server.operatorNamespace

	// Get all available devices
	devices, err := p.server.getAllAvailableAgents(c.Request.Context(), namespace)
	if err != nil {
		p.server.sendError(c, http.StatusServiceUnavailable, "no_agents", "No HSM agents available", map[string]any{
			"error": err.Error(),
		})
		return nil, false
	}

	clients := make(map[string]hsm.Client)
	p.clientsMutex.Lock()
	defer p.clientsMutex.Unlock()

	for _, deviceName := range devices {
		// Try to get existing client for this device
		if client, exists := p.grpcClients[deviceName]; exists && client.IsConnected() {
			clients[deviceName] = client
			continue
		}

		// Close existing client for this device if it exists but is not connected
		if oldClient, exists := p.grpcClients[deviceName]; exists {
			if closeErr := oldClient.Close(); closeErr != nil {
				p.logger.V(1).Info("Error closing old gRPC client", "device", deviceName, "error", closeErr)
			}
			delete(p.grpcClients, deviceName)
		}

		// Create new gRPC client
		grpcClient, err := p.server.createGRPCClient(c.Request.Context(), deviceName, namespace)
		if err != nil {
			p.logger.V(1).Info("Failed to create gRPC client", "device", deviceName, "error", err)
			continue
		}

		// Cache and include the client
		p.grpcClients[deviceName] = grpcClient
		clients[deviceName] = grpcClient
		p.logger.V(1).Info("Created new gRPC client", "device", deviceName)
	}

	if len(clients) == 0 {
		p.server.sendError(c, http.StatusServiceUnavailable, "no_agents", "No HSM agents available", nil)
		return nil, false
	}

	return clients, true
}

// GetInfo handles GET /hsm/info
func (p *ProxyClient) GetInfo(c *gin.Context) {
	clients, ok := p.getAllAvailableGRPCClients(c)
	if !ok {
		return
	}

	type infoResult struct {
		deviceName string
		info       *hsm.HSMInfo
		err        error
	}

	resultsChan := make(chan infoResult, len(clients))
	for deviceName, grpcClient := range clients {
		go func(deviceName string, grpcClient hsm.Client) {
			info, err := grpcClient.GetInfo(c)
			resultsChan <- infoResult{deviceName, info, err}
		}(deviceName, grpcClient)
	}

	// Collect successful results
	deviceInfos := make(map[string]*hsm.HSMInfo, len(clients))
	for i := 0; i < len(clients); i++ {
		result := <-resultsChan
		if result.err == nil {
			deviceInfos[result.deviceName] = result.info
		} else {
			p.logger.V(1).Info("Failed to get info from device", "device", result.deviceName, "error", result.err)
		}
	}

	if len(deviceInfos) == 0 {
		p.server.sendError(c, http.StatusInternalServerError, "info_failed", "Failed to get info from any HSM device", nil)
		return
	}

	response := GetInfoResponse{DeviceInfos: deviceInfos}
	p.server.sendResponse(c, http.StatusOK, "HSM info retrieved successfully", response)
}

// ListSecrets handles GET /hsm/secrets
func (p *ProxyClient) ListSecrets(c *gin.Context) {
	prefix := c.Query("prefix")

	clients, ok := p.getAllAvailableGRPCClients(c)
	if !ok {
		return
	}
	type secretsResult struct {
		deviceName string
		secrets    []string
		err        error
	}

	resultsChan := make(chan secretsResult, len(clients))
	for deviceName, grpcClient := range clients {
		go func(deviceName string, grpcClient hsm.Client) {
			secrets, err := grpcClient.ListSecrets(c, prefix)
			resultsChan <- secretsResult{deviceName, secrets, err}
		}(deviceName, grpcClient)
	}

	// Collect results and deduplicate
	secretsSet := make(map[string]bool)
	for i := 0; i < len(clients); i++ {
		result := <-resultsChan
		if result.err != nil {
			p.logger.V(1).Info("Failed to list secrets from device", "device", result.deviceName, "error", result.err)
			continue
		}

		// Add all secrets from this device to the union set
		for _, secretPath := range result.secrets {
			secretsSet[secretPath] = true
		}
	}

	// Convert set to slice
	allSecrets := make([]string, 0, len(secretsSet))
	for secretPath := range secretsSet {
		allSecrets = append(allSecrets, secretPath)
	}

	response := ListSecretsResponse{
		Secrets: allSecrets,
		Count:   len(allSecrets),
		Prefix:  prefix,
	}
	p.server.sendResponse(c, http.StatusOK, "Secrets listed successfully", response)
}

// ReadSecret handles GET /hsm/secrets/:path
func (p *ProxyClient) ReadSecret(c *gin.Context) {
	path, ok := p.validatePathParam(c)
	if !ok {
		return
	}

	clients, ok := p.getAllAvailableGRPCClients(c)
	if !ok {
		return
	}

	// Read from all devices in parallel
	resultsChan := make(chan secretResult, len(clients))
	for deviceName, grpcClient := range clients {
		go func(deviceName string, grpcClient hsm.Client) {
			// Read secret data
			data, err := grpcClient.ReadSecret(c.Request.Context(), path)
			if err != nil {
				resultsChan <- secretResult{deviceName: deviceName, err: err}
				return
			}

			// Read metadata for timestamp comparison
			metadata, metaErr := grpcClient.ReadMetadata(c.Request.Context(), path)
			if metaErr != nil {
				p.logger.V(1).Info("Failed to read metadata for version comparison", "device", deviceName, "path", path, "error", metaErr)
			}

			resultsChan <- secretResult{
				deviceName: deviceName,
				data:       data,
				metadata:   metadata,
			}
		}(deviceName, grpcClient)
	}

	// Collect successful results
	var successfulResults []secretResult
	for i := 0; i < len(clients); i++ {
		result := <-resultsChan
		if result.err != nil {
			p.logger.V(1).Info("Failed to read secret from device", "device", result.deviceName, "path", path, "error", result.err)
			continue
		}
		successfulResults = append(successfulResults, result)
	}

	// Use helper to find most recent result based on timestamps
	data, err := p.findMostRecentSecretResult(successfulResults, path)
	if err != nil {
		p.server.sendError(c, http.StatusNotFound, "secret_not_found", err.Error(), nil)
		return
	}

	response := ReadSecretResponse{Path: path, Data: data}
	p.server.sendResponse(c, http.StatusOK, "Secret read successfully", response)
}

// WriteSecret handles POST/PUT /hsm/secrets/:path with mirroring support
func (p *ProxyClient) WriteSecret(c *gin.Context) {
	path, ok := p.validatePathParam(c)
	if !ok {
		return
	}

	// Parse request body
	var req struct {
		Data     map[string]string   `json:"data" binding:"required"`
		Metadata *hsm.SecretMetadata `json:"metadata,omitempty"`
		Mirror   *bool               `json:"mirror,omitempty"` // Enable/disable mirroring for this request
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

	// Get all available clients for mirroring
	clients, ok := p.getAllAvailableGRPCClients(c)
	if !ok {
		return
	}

	// Add mirroring metadata
	metadata := req.Metadata
	if metadata == nil {
		metadata = &hsm.SecretMetadata{Labels: make(map[string]string)}
	}
	if metadata.Labels == nil {
		metadata.Labels = make(map[string]string)
	}
	metadata.Labels["sync.version"] = fmt.Sprintf("%d", time.Now().Unix())
	metadata.Labels["sync.timestamp"] = time.Now().Format(time.RFC3339)

	// Write to all devices in parallel
	results := p.writeToAllDevices(c.Request.Context(), clients, path, data, metadata)

	// Check results - log failures but succeed if at least one device works
	successful := 0
	for deviceName, result := range results {
		if result.Error == nil {
			successful++
		} else {
			p.logger.Error(result.Error, "Failed to write to device", "device", deviceName, "path", path)
		}
	}

	// Succeed if we wrote to at least one device
	if successful > 0 {
		if successful < len(clients) {
			p.logger.Info("Secret written to subset of devices",
				"path", path,
				"successful", successful,
				"total", len(clients))
		}

		response := WriteSecretResponse{
			Path: path,
			Keys: len(data),
		}

		p.server.sendResponse(c, http.StatusCreated, "Secret written successfully", response)
	} else {
		// All devices failed
		p.server.sendError(c, http.StatusInternalServerError, "write_failed", "Failed to write secret to any HSM device", nil)
	}
}

// DeleteSecret handles DELETE /hsm/secrets/:path with mirroring support
func (p *ProxyClient) DeleteSecret(c *gin.Context) {
	path, ok := p.validatePathParam(c)
	if !ok {
		return
	}

	// Get all available clients for mirroring delete
	clients, ok := p.getAllAvailableGRPCClients(c)
	if !ok {
		return
	}

	// Perform tombstone deletion from all devices in parallel
	results := p.tombstoneDeleteFromAllDevices(c.Request.Context(), clients, path)

	// Check results
	successful := 0
	var errors []string
	deviceResults := make(map[string]any)

	for deviceName, result := range results {
		deviceResults[deviceName] = map[string]any{
			"success": result.Error == nil,
			"error": func() string {
				if result.Error != nil {
					return result.Error.Error()
				}
				return ""
			}(),
		}

		if result.Error == nil {
			successful++
		} else {
			errors = append(errors, fmt.Sprintf("%s: %v", deviceName, result.Error))
			p.logger.Error(result.Error, "Failed to delete from device", "device", deviceName, "path", path)
		}
	}

	// Consider the operation successful if we deleted from at least one device
	if successful > 0 {
		response := DeleteSecretResponse{
			Path:          path,
			Devices:       len(clients),
			DeviceResults: deviceResults,
		}
		if len(errors) > 0 {
			response.Warnings = errors
		}

		statusCode := http.StatusOK
		message := "Secret deleted successfully"
		if successful < len(clients) {
			statusCode = http.StatusPartialContent // 206 indicates partial success
			message = fmt.Sprintf("Secret deleted from %d/%d devices", successful, len(clients))
		}

		p.server.sendResponse(c, statusCode, message, response)
	} else {
		// All devices failed
		p.server.sendError(c, http.StatusInternalServerError, "delete_failed", "Failed to delete secret from any HSM device", map[string]any{
			"errors":        errors,
			"deviceResults": deviceResults,
			"path":          path,
		})
	}
}

// ReadMetadata handles GET /hsm/secrets/:path/metadata
func (p *ProxyClient) ReadMetadata(c *gin.Context) {
	path, ok := p.validatePathParam(c)
	if !ok {
		return
	}

	clients, ok := p.getAllAvailableGRPCClients(c)
	if !ok {
		return
	}

	// Read metadata from all devices in parallel
	resultsChan := make(chan metadataResult, len(clients))
	for deviceName, grpcClient := range clients {
		go func(deviceName string, grpcClient hsm.Client) {
			metadata, err := grpcClient.ReadMetadata(c.Request.Context(), path)
			resultsChan <- metadataResult{
				deviceName: deviceName,
				metadata:   metadata,
				err:        err,
			}
		}(deviceName, grpcClient)
	}

	// Collect successful results
	var successfulResults []metadataResult
	for i := 0; i < len(clients); i++ {
		result := <-resultsChan
		if result.err != nil {
			p.logger.V(1).Info("Failed to read metadata from device", "device", result.deviceName, "path", path, "error", result.err)
			continue
		}
		successfulResults = append(successfulResults, result)
	}

	// Use helper to find most recent result based on timestamps
	metadata, err := p.findMostRecentMetadataResult(successfulResults, path)
	if err != nil {
		p.server.sendError(c, http.StatusNotFound, "metadata_not_found", err.Error(), nil)
		return
	}

	response := ReadMetadataResponse{Path: path, Metadata: metadata}
	p.server.sendResponse(c, http.StatusOK, "Metadata read successfully", response)
}

// GetChecksum handles GET /hsm/secrets/:path/checksum
func (p *ProxyClient) GetChecksum(c *gin.Context) {
	path, ok := p.validatePathParam(c)
	if !ok {
		return
	}

	clients, ok := p.getAllAvailableGRPCClients(c)
	if !ok {
		return
	}

	// Get checksums from all devices in parallel
	resultsChan := make(chan checksumResult, len(clients))
	for deviceName, grpcClient := range clients {
		go func(deviceName string, grpcClient hsm.Client) {
			checksum, err := grpcClient.GetChecksum(c.Request.Context(), path)
			resultsChan <- checksumResult{
				deviceName: deviceName,
				checksum:   checksum,
				err:        err,
			}
		}(deviceName, grpcClient)
	}

	// Collect successful results
	var successfulResults []checksumResult
	for i := 0; i < len(clients); i++ {
		result := <-resultsChan
		if result.err != nil {
			p.logger.V(1).Info("Failed to get checksum from device", "device", result.deviceName, "path", path, "error", result.err)
			continue
		}
		successfulResults = append(successfulResults, result)
	}

	// Use helper to find consensus checksum
	consensusChecksum, err := p.findConsensusChecksum(successfulResults, path)
	if err != nil {
		p.server.sendError(c, http.StatusNotFound, "checksum_not_found", err.Error(), nil)
		return
	}

	response := GetChecksumResponse{Path: path, Checksum: consensusChecksum}
	p.server.sendResponse(c, http.StatusOK, "Checksum retrieved successfully", response)
}

// IsConnected handles GET /hsm/status
func (p *ProxyClient) IsConnected(c *gin.Context) {
	clients, ok := p.getAllAvailableGRPCClients(c)
	if !ok {
		return
	}

	devices := make(map[string]bool, len(clients))
	connectedCount := 0

	for deviceName, grpcClient := range clients {
		connected := grpcClient.IsConnected()
		devices[deviceName] = connected

		if connected {
			connectedCount++
		}
	}

	// Log connectivity issues for operational visibility
	if connectedCount == 0 {
		p.logger.Info("No HSM devices are connected", "totalDevices", len(clients))
	} else if connectedCount < len(clients) {
		p.logger.Info("Partial HSM connectivity detected",
			"connectedDevices", connectedCount,
			"totalDevices", len(clients))
	}

	response := IsConnectedResponse{
		Devices:      devices,
		TotalDevices: len(clients),
	}

	p.server.sendResponse(c, http.StatusOK, "HSM connection status retrieved", response)
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

// writeToAllDevices writes secret data to all devices in parallel
func (p *ProxyClient) writeToAllDevices(ctx context.Context, clients map[string]hsm.Client, path string, data hsm.SecretData, metadata *hsm.SecretMetadata) map[string]WriteResult {
	results := make(map[string]WriteResult)
	resultsMutex := sync.Mutex{}
	wg := sync.WaitGroup{}

	for deviceName, client := range clients {
		wg.Add(1)
		go func(deviceName string, client hsm.Client) {
			defer wg.Done()

			var err error
			if metadata != nil {
				err = client.WriteSecretWithMetadata(ctx, path, data, metadata)
			} else {
				err = client.WriteSecret(ctx, path, data)
			}

			resultsMutex.Lock()
			results[deviceName] = WriteResult{
				DeviceName: deviceName,
				Error:      err,
			}
			resultsMutex.Unlock()
		}(deviceName, client)
	}

	wg.Wait()
	return results
}

// tombstoneDeleteFromAllDevices performs tombstone deletion on all devices in parallel
// Deletes secret data but leaves tombstone metadata to prevent resurrection
func (p *ProxyClient) tombstoneDeleteFromAllDevices(ctx context.Context, clients map[string]hsm.Client, path string) map[string]WriteResult {
	results := make(map[string]WriteResult)
	resultsMutex := sync.Mutex{}
	wg := sync.WaitGroup{}

	// Create tombstone metadata
	tombstoneMetadata := &hsm.SecretMetadata{
		Labels: map[string]string{
			"sync.deleted":   "true",
			"sync.timestamp": time.Now().Format(time.RFC3339),
			"sync.version":   "0",
		},
	}

	for deviceName, client := range clients {
		wg.Add(1)
		go func(deviceName string, client hsm.Client) {
			defer wg.Done()

			var err error

			// First, delete the secret data
			if deleteErr := client.DeleteSecret(ctx, path); deleteErr != nil {
				// If delete fails, still try to write tombstone metadata
				p.logger.V(1).Info("Failed to delete secret data, will still create tombstone",
					"device", deviceName, "path", path, "error", deleteErr)
			}

			// Write tombstone metadata (empty data with deletion markers)
			emptyData := make(hsm.SecretData)
			err = client.WriteSecretWithMetadata(ctx, path, emptyData, tombstoneMetadata)

			resultsMutex.Lock()
			results[deviceName] = WriteResult{
				DeviceName: deviceName,
				Error:      err,
			}
			resultsMutex.Unlock()
		}(deviceName, client)
	}

	wg.Wait()
	return results
}

// Interface compliance methods (unused in HTTP mode but required for hsm.Client interface)
func (p *ProxyClient) Initialize(ctx context.Context, config hsm.Config) error { return nil }
