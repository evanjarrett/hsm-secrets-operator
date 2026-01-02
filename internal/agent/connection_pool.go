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
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"

	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// ClientWrapper wraps an HSM client to track usage and manage lifecycle
type ClientWrapper struct {
	client   hsm.Client
	pool     *ConnectionPool
	endpoint string
}

// Implement hsm.Client interface methods by delegating to wrapped client
func (cw *ClientWrapper) Initialize(ctx context.Context, config hsm.Config) error {
	return cw.client.Initialize(ctx, config)
}

func (cw *ClientWrapper) ReadSecret(ctx context.Context, path string) (hsm.SecretData, error) {
	return cw.client.ReadSecret(ctx, path)
}

func (cw *ClientWrapper) DeleteSecret(ctx context.Context, path string) error {
	return cw.client.DeleteSecret(ctx, path)
}

func (cw *ClientWrapper) DeleteSecretKey(ctx context.Context, path, key string) error {
	return cw.client.DeleteSecretKey(ctx, path, key)
}

func (cw *ClientWrapper) ListSecrets(ctx context.Context, prefix string) ([]string, error) {
	return cw.client.ListSecrets(ctx, prefix)
}

func (cw *ClientWrapper) WriteSecret(ctx context.Context, path string, data hsm.SecretData, metadata *hsm.SecretMetadata) error {
	return cw.client.WriteSecret(ctx, path, data, metadata)
}

func (cw *ClientWrapper) ReadMetadata(ctx context.Context, path string) (*hsm.SecretMetadata, error) {
	return cw.client.ReadMetadata(ctx, path)
}

func (cw *ClientWrapper) GetInfo(ctx context.Context) (*hsm.HSMInfo, error) {
	return cw.client.GetInfo(ctx)
}

func (cw *ClientWrapper) GetChecksum(ctx context.Context, path string) (string, error) {
	return cw.client.GetChecksum(ctx, path)
}

func (cw *ClientWrapper) IsConnected() bool {
	return cw.client.IsConnected()
}

func (cw *ClientWrapper) ChangePIN(ctx context.Context, oldPIN, newPIN string) error {
	return cw.client.ChangePIN(ctx, oldPIN, newPIN)
}

func (cw *ClientWrapper) Close() error {
	// Decrease reference count when closed
	cw.pool.mutex.Lock()
	if pooled, exists := cw.pool.clients[cw.endpoint]; exists {
		newRefCount := atomic.AddInt32(&pooled.RefCount, -1)
		if newRefCount <= 0 {
			pooled.InUse = false
		}
		cw.pool.logger.V(1).Info("Client reference decreased", "endpoint", cw.endpoint, "refCount", newRefCount)
	}
	cw.pool.mutex.Unlock()

	// Note: Don't close the underlying client here, let the pool manage it
	return nil
}

// PooledClient represents a cached gRPC client with metadata
type PooledClient struct {
	Client      hsm.Client
	Endpoint    string
	CreatedAt   time.Time
	LastUsed    time.Time
	UsageCount  int64     // Track how many times this client has been used
	InUse       bool      // Track if client is currently being used
	RefCount    int32     // Active reference count (atomic operations)
	LastOpStart time.Time // When the last operation started
}

// ConnectionPoolMetrics tracks connection pool performance
type ConnectionPoolMetrics struct {
	TotalConnections      int64
	SuccessfulConnections int64
	FailedConnections     int64
	ConnectionReuses      int64
	HealthCheckPasses     int64
	HealthCheckFailures   int64
	ConnectionTimeouts    int64
	RetryAttempts         int64
}

// ConnectionPool manages a pool of gRPC connections to HSM agents
type ConnectionPool struct {
	clients  map[string]*PooledClient // endpoint -> client
	mutex    sync.RWMutex
	logger   logr.Logger
	stopChan chan struct{}
	stopOnce sync.Once
	metrics  ConnectionPoolMetrics
}

// NewConnectionPool creates a new connection pool
func NewConnectionPool(logger logr.Logger) *ConnectionPool {
	pool := &ConnectionPool{
		clients:  make(map[string]*PooledClient),
		logger:   logger.WithName("connection-pool"),
		stopChan: make(chan struct{}),
	}

	pool.logger.Info("Connection pool created - connections will be kept alive until pod termination")
	return pool
}

// GetClient returns a cached client or creates a new one
func (cp *ConnectionPool) GetClient(ctx context.Context, endpoint string, logger logr.Logger) (hsm.Client, error) {
	return cp.getClientWithRetry(ctx, endpoint, logger, 3) // Retry up to 3 times
}

// getClientWithRetry implements retry logic for client creation
func (cp *ConnectionPool) getClientWithRetry(ctx context.Context, endpoint string, logger logr.Logger, maxRetries int) (hsm.Client, error) {
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		client, err := cp.getClientAttempt(ctx, endpoint, logger)
		if err == nil {
			// Perform health check on the returned client
			if wrapper, ok := client.(*ClientWrapper); ok {
				if !wrapper.IsConnected() {
					cp.logger.Info("Health check failed on new client", "endpoint", endpoint,
						"attempt", attempt, "error", "client not connected")
					// Remove the client and try again
					cp.RemoveClient(endpoint)
					lastErr = fmt.Errorf("health check failed: client not connected")
					if attempt < maxRetries {
						backoffDuration := time.Duration(attempt) * time.Second
						cp.logger.Info("Retrying client creation after backoff",
							"endpoint", endpoint, "attempt", attempt+1, "backoff", backoffDuration.String())
						time.Sleep(backoffDuration)
						continue
					}
				}
			}
			return client, nil
		}

		lastErr = err
		if attempt < maxRetries {
			cp.metrics.RetryAttempts++
			backoffDuration := time.Duration(attempt) * time.Second
			cp.logger.Info("Client creation failed, retrying after backoff",
				"endpoint", endpoint, "attempt", attempt, "error", err, "backoff", backoffDuration.String())
			time.Sleep(backoffDuration)
		}
	}

	return nil, fmt.Errorf("failed to create client after %d attempts: %w", maxRetries, lastErr)
}

// getClientAttempt performs a single attempt to get or create a client
func (cp *ConnectionPool) getClientAttempt(ctx context.Context, endpoint string, logger logr.Logger) (hsm.Client, error) {
	cp.mutex.Lock()
	defer cp.mutex.Unlock()

	now := time.Now()

	// Check if we have a cached client
	if pooled, exists := cp.clients[endpoint]; exists {
		// Check if client is still connected
		if pooled.Client.IsConnected() {
			// Update usage tracking
			pooled.LastUsed = now
			pooled.UsageCount++
			pooled.InUse = true
			pooled.LastOpStart = now
			atomic.AddInt32(&pooled.RefCount, 1)

			cp.logger.V(1).Info("Reusing cached gRPC client", "endpoint", endpoint,
				"age", now.Sub(pooled.CreatedAt).String(), "usage_count", pooled.UsageCount, "refCount", pooled.RefCount)
			cp.metrics.ConnectionReuses++
			return &ClientWrapper{client: pooled.Client, pool: cp, endpoint: endpoint}, nil
		} else {
			// Client is disconnected, remove it and create a new one
			cp.logger.Info("Cached client is disconnected, removing", "endpoint", endpoint,
				"age", now.Sub(pooled.CreatedAt).String(), "usage_count", pooled.UsageCount)
			if err := pooled.Client.Close(); err != nil {
				cp.logger.V(1).Info("Error closing disconnected client", "endpoint", endpoint, "error", err)
			}
			delete(cp.clients, endpoint)
		}
	}

	// Create new client
	cp.logger.V(1).Info("Creating new gRPC client", "endpoint", endpoint)
	cp.metrics.TotalConnections++
	client, err := NewGRPCClient(endpoint, logger)
	if err != nil {
		cp.metrics.FailedConnections++
		return nil, fmt.Errorf("failed to create gRPC client: %w", err)
	}

	// Initialize the connection
	if err := client.Initialize(ctx, hsm.Config{}); err != nil {
		cp.metrics.FailedConnections++
		if closeErr := client.Close(); closeErr != nil {
			cp.logger.V(1).Info("Error closing client after failed initialization",
				"endpoint", endpoint, "error", closeErr)
		}
		return nil, fmt.Errorf("failed to initialize gRPC client: %w", err)
	}

	// Cache the client
	cp.clients[endpoint] = &PooledClient{
		Client:      client,
		Endpoint:    endpoint,
		CreatedAt:   now,
		LastUsed:    now,
		UsageCount:  1,
		InUse:       true,
		RefCount:    1,
		LastOpStart: now,
	}

	cp.logger.Info("Created and cached new gRPC client", "endpoint", endpoint)
	cp.metrics.SuccessfulConnections++
	return &ClientWrapper{client: client, pool: cp, endpoint: endpoint}, nil
}

// RemoveClient removes a client from the pool (useful when agent pods restart)
func (cp *ConnectionPool) RemoveClient(endpoint string) {
	cp.mutex.Lock()
	defer cp.mutex.Unlock()

	if pooled, exists := cp.clients[endpoint]; exists {
		cp.logger.Info("Removing client from pool", "endpoint", endpoint)
		if err := pooled.Client.Close(); err != nil {
			cp.logger.V(1).Info("Error closing removed client", "endpoint", endpoint, "error", err)
		}
		delete(cp.clients, endpoint)
	}
}

// Close closes all connections and stops the pool
func (cp *ConnectionPool) Close() {
	cp.stopOnce.Do(func() {
		close(cp.stopChan)

		cp.mutex.Lock()
		defer cp.mutex.Unlock()

		cp.logger.Info("Closing connection pool", "cached_clients", len(cp.clients))
		for endpoint, pooled := range cp.clients {
			if err := pooled.Client.Close(); err != nil {
				cp.logger.V(1).Info("Error closing pooled client", "endpoint", endpoint, "error", err)
			}
		}
		cp.clients = make(map[string]*PooledClient)
	})
}

// GetStats returns pool statistics
func (cp *ConnectionPool) GetStats() map[string]any {
	cp.mutex.RLock()
	defer cp.mutex.RUnlock()

	now := time.Now()
	stats := make(map[string]any)
	stats["active_connections"] = len(cp.clients)
	stats["connection_lifetime"] = "permanent"

	var totalUsage int64
	inUseCount := 0
	clientDetails := make([]map[string]any, 0, len(cp.clients))

	for endpoint, pooled := range cp.clients {
		totalUsage += pooled.UsageCount
		if pooled.InUse {
			inUseCount++
		}

		clientDetails = append(clientDetails, map[string]any{
			"endpoint":              endpoint,
			"age_seconds":           now.Sub(pooled.CreatedAt).Seconds(),
			"last_used_seconds_ago": now.Sub(pooled.LastUsed).Seconds(),
			"usage_count":           pooled.UsageCount,
			"in_use":                pooled.InUse,
		})
	}

	stats["clients_in_use"] = inUseCount
	stats["total_usage_count"] = totalUsage
	stats["client_details"] = clientDetails

	// Add connection pool metrics
	stats["metrics"] = map[string]any{
		"total_connections":      cp.metrics.TotalConnections,
		"successful_connections": cp.metrics.SuccessfulConnections,
		"failed_connections":     cp.metrics.FailedConnections,
		"connection_reuses":      cp.metrics.ConnectionReuses,
		"health_check_passes":    cp.metrics.HealthCheckPasses,
		"health_check_failures":  cp.metrics.HealthCheckFailures,
		"connection_timeouts":    cp.metrics.ConnectionTimeouts,
		"retry_attempts":         cp.metrics.RetryAttempts,
	}

	return stats
}

// HealthCheckClient verifies that a client connection is still healthy
func (cp *ConnectionPool) HealthCheckClient(ctx context.Context, endpoint string) error {
	cp.mutex.RLock()
	pooled, exists := cp.clients[endpoint]
	cp.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("no client found for endpoint %s", endpoint)
	}

	// Check if the client is still connected
	if !pooled.Client.IsConnected() {
		cp.metrics.HealthCheckFailures++
		cp.logger.Info("Health check failed for client, removing from pool",
			"endpoint", endpoint, "error", "client not connected")
		cp.RemoveClient(endpoint)
		return fmt.Errorf("health check failed: client not connected")
	}

	cp.metrics.HealthCheckPasses++
	cp.logger.V(1).Info("Health check passed for client", "endpoint", endpoint)
	return nil
}
