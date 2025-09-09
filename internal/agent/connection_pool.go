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
	"time"

	"github.com/go-logr/logr"

	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// PooledClient represents a cached gRPC client with metadata
type PooledClient struct {
	Client    hsm.Client
	Endpoint  string
	CreatedAt time.Time
	LastUsed  time.Time
}

// ConnectionPool manages a pool of gRPC connections to HSM agents
type ConnectionPool struct {
	clients         map[string]*PooledClient // endpoint -> client
	mutex           sync.RWMutex
	logger          logr.Logger
	maxAge          time.Duration // Maximum age before client is recreated
	cleanupInterval time.Duration
	stopChan        chan struct{}
	stopOnce        sync.Once
}

// NewConnectionPool creates a new connection pool
func NewConnectionPool(logger logr.Logger) *ConnectionPool {
	pool := &ConnectionPool{
		clients:         make(map[string]*PooledClient),
		logger:          logger.WithName("connection-pool"),
		maxAge:          5 * time.Minute, // Recreate connections every 5 minutes
		cleanupInterval: 1 * time.Minute, // Cleanup every minute
		stopChan:        make(chan struct{}),
	}

	// Start background cleanup goroutine
	go pool.cleanupLoop()

	return pool
}

// GetClient returns a cached client or creates a new one
func (cp *ConnectionPool) GetClient(ctx context.Context, endpoint string, logger logr.Logger) (hsm.Client, error) {
	cp.mutex.Lock()
	defer cp.mutex.Unlock()

	now := time.Now()

	// Check if we have a cached client
	if pooled, exists := cp.clients[endpoint]; exists {
		// Check if client is still valid and not too old
		if now.Sub(pooled.CreatedAt) < cp.maxAge {
			// Update last used time
			pooled.LastUsed = now
			cp.logger.V(1).Info("Reusing cached gRPC client", "endpoint", endpoint,
				"age", now.Sub(pooled.CreatedAt).String())
			return pooled.Client, nil
		} else {
			// Client is too old, close it and remove from cache
			cp.logger.V(1).Info("gRPC client expired, closing", "endpoint", endpoint,
				"age", now.Sub(pooled.CreatedAt).String())
			if err := pooled.Client.Close(); err != nil {
				cp.logger.V(1).Info("Error closing expired client", "endpoint", endpoint, "error", err)
			}
			delete(cp.clients, endpoint)
		}
	}

	// Create new client
	cp.logger.V(1).Info("Creating new gRPC client", "endpoint", endpoint)
	client, err := NewGRPCClient(endpoint, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client: %w", err)
	}

	// Initialize the connection
	if err := client.Initialize(ctx, hsm.Config{}); err != nil {
		if closeErr := client.Close(); closeErr != nil {
			cp.logger.V(1).Info("Error closing client after failed initialization",
				"endpoint", endpoint, "error", closeErr)
		}
		return nil, fmt.Errorf("failed to initialize gRPC client: %w", err)
	}

	// Cache the client
	cp.clients[endpoint] = &PooledClient{
		Client:    client,
		Endpoint:  endpoint,
		CreatedAt: now,
		LastUsed:  now,
	}

	cp.logger.Info("Created and cached new gRPC client", "endpoint", endpoint)
	return client, nil
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

// cleanupLoop periodically removes unused/expired connections
func (cp *ConnectionPool) cleanupLoop() {
	ticker := time.NewTicker(cp.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cp.cleanup()
		case <-cp.stopChan:
			return
		}
	}
}

// cleanup removes expired connections
func (cp *ConnectionPool) cleanup() {
	cp.mutex.Lock()
	defer cp.mutex.Unlock()

	now := time.Now()
	var toRemove []string

	for endpoint, pooled := range cp.clients {
		// Remove if too old or unused for too long
		if now.Sub(pooled.CreatedAt) > cp.maxAge || now.Sub(pooled.LastUsed) > cp.maxAge {
			toRemove = append(toRemove, endpoint)
		}
	}

	if len(toRemove) > 0 {
		cp.logger.V(1).Info("Cleaning up expired connections", "count", len(toRemove))
		for _, endpoint := range toRemove {
			if pooled, exists := cp.clients[endpoint]; exists {
				if err := pooled.Client.Close(); err != nil {
					cp.logger.V(1).Info("Error closing expired client during cleanup",
						"endpoint", endpoint, "error", err)
				}
				delete(cp.clients, endpoint)
			}
		}
	}
}

// GetStats returns pool statistics
func (cp *ConnectionPool) GetStats() map[string]interface{} {
	cp.mutex.RLock()
	defer cp.mutex.RUnlock()

	stats := make(map[string]interface{})
	stats["active_connections"] = len(cp.clients)

	endpoints := make([]string, 0, len(cp.clients))
	for endpoint := range cp.clients {
		endpoints = append(endpoints, endpoint)
	}
	stats["endpoints"] = endpoints

	return stats
}
