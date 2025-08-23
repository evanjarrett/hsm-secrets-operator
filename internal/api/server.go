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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/go-playground/validator/v10"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/discovery"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// Server represents the HSM REST API server that proxies requests to agent pods
type Server struct {
	client           client.Client
	agentManager     *agent.Manager
	mirroringManager *discovery.MirroringManager
	validator        *validator.Validate
	logger           logr.Logger
	router           *gin.Engine
	proxyClient      *ProxyClient
}

// NewServer creates a new API server instance that proxies to agents
func NewServer(k8sClient client.Client, agentManager *agent.Manager, mirroringManager *discovery.MirroringManager, logger logr.Logger) *Server {
	s := &Server{
		client:           k8sClient,
		agentManager:     agentManager,
		mirroringManager: mirroringManager,
		validator:        validator.New(),
		logger:           logger.WithName("api-server"),
	}

	// Create ProxyClient instance
	s.proxyClient = NewProxyClient(s, s.logger)

	s.setupRouter()
	return s
}

// setupRouter configures the HTTP routes
func (s *Server) setupRouter() {
	// Set gin mode to release for production
	gin.SetMode(gin.ReleaseMode)

	s.router = gin.New()

	// Add middleware
	s.router.Use(gin.Recovery())
	s.router.Use(s.loggingMiddleware())
	s.router.Use(s.corsMiddleware())

	// Set up proxy routes
	s.setupProxyRoutes()
}

// Start starts the API server on the specified port
func (s *Server) Start(port int) error {
	addr := fmt.Sprintf(":%d", port)
	s.logger.Info("Starting API server", "addr", addr)
	return s.router.Run(addr)
}

// handleHealth handles health check requests
func (s *Server) handleHealth(c *gin.Context) {
	// In proxy mode, check if any agents are available
	_, agentErr := s.findAvailableAgent(c.Request.Context(), "secrets")
	hsmConnected := agentErr == nil
	replicationEnabled := s.mirroringManager != nil
	activeNodes := 0

	if s.mirroringManager != nil {
		// Count active nodes (simplified - in real implementation would check actual node health)
		activeNodes = 1 // Current node
	}

	status := "healthy"
	if !hsmConnected {
		status = "degraded"
	}

	health := HealthStatus{
		Status:             status,
		HSMConnected:       hsmConnected,
		ReplicationEnabled: replicationEnabled,
		ActiveNodes:        activeNodes,
		Timestamp:          time.Now(),
	}

	s.sendResponse(c, http.StatusOK, "Health check completed", health)
}

// All HSM operations are now proxied to agents - no direct handlers needed

// sendResponse sends a successful API response
func (s *Server) sendResponse(c *gin.Context, statusCode int, message string, data any) {
	response := APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	}
	c.JSON(statusCode, response)
}

// sendError sends an error API response
func (s *Server) sendError(c *gin.Context, statusCode int, code, message string, details map[string]any) {
	response := APIResponse{
		Success: false,
		Error: &APIError{
			Code:    code,
			Message: message,
			Details: details,
		},
	}
	c.JSON(statusCode, response)
}

// loggingMiddleware provides request logging
func (s *Server) loggingMiddleware() gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return fmt.Sprintf("%s - [%s] \"%s %s %s %d %s \"%s\" %s\"\n",
			param.ClientIP,
			param.TimeStamp.Format(time.RFC1123),
			param.Method,
			param.Path,
			param.Request.Proto,
			param.StatusCode,
			param.Latency,
			param.Request.UserAgent(),
			param.ErrorMessage,
		)
	})
}

// corsMiddleware provides CORS headers
func (s *Server) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// findAvailableAgent finds an available HSM agent for handling requests
func (s *Server) findAvailableAgent(ctx context.Context, namespace string) (string, error) {
	if s.agentManager == nil {
		return "", fmt.Errorf("agent manager not available")
	}

	// List all HSMDevices to find one with an active agent
	var hsmDeviceList hsmv1alpha1.HSMDeviceList
	if err := s.client.List(ctx, &hsmDeviceList, client.InNamespace(namespace)); err != nil {
		return "", fmt.Errorf("failed to list HSM devices: %w", err)
	}

	// Check if any device has an active agent with pod IPs
	for _, device := range hsmDeviceList.Items {
		if podIPs, err := s.agentManager.GetAgentPodIPs(device.Name); err == nil && len(podIPs) > 0 {
			// Return the device name (we'll use AgentManager to get the actual client)
			return device.Name, nil
		}
	}

	return "", fmt.Errorf("no available HSM agents found")
}

// createGRPCClient creates a gRPC client for the specified device using AgentManager
func (s *Server) createGRPCClient(ctx context.Context, deviceName, _ string) (hsm.Client, error) {
	// Use the AgentManager to create a gRPC client directly
	if s.agentManager == nil {
		return nil, fmt.Errorf("agent manager not available")
	}

	// Create gRPC client using AgentManager's existing method
	grpcClient, err := s.agentManager.CreateSingleGRPCClient(ctx, deviceName, s.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client for device %s: %w", deviceName, err)
	}

	return grpcClient, nil
}
