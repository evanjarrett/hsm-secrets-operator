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
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/go-playground/validator/v10"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// Server represents the HSM REST API server that proxies requests to agent pods
type Server struct {
	client       client.Client
	agentManager *agent.Manager
	validator    *validator.Validate
	logger       logr.Logger
	router       *gin.Engine
	proxyClient  *ProxyClient
}

// NewServer creates a new API server instance that proxies to agents
func NewServer(k8sClient client.Client, agentManager *agent.Manager, logger logr.Logger) *Server {
	s := &Server{
		client:       k8sClient,
		agentManager: agentManager,
		validator:    validator.New(),
		logger:       logger.WithName("api-server"),
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
	// Check if multiple agents are available for replication
	agents, _ := s.getAllAvailableAgents(c.Request.Context(), "secrets")
	hsmConnected := len(agents) > 0
	replicationEnabled := len(agents) > 1
	activeNodes := len(agents)

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
	agents, err := s.getAllAvailableAgents(ctx, namespace)
	if err != nil {
		return "", err
	}
	if len(agents) == 0 {
		return "", fmt.Errorf("no available HSM agents found")
	}
	return agents[0], nil
}

// getAllAvailableAgents finds all available HSM agents for mirroring operations
func (s *Server) getAllAvailableAgents(ctx context.Context, namespace string) ([]string, error) {
	if s.agentManager == nil {
		return nil, fmt.Errorf("agent manager not available")
	}

	// List all HSMPools to find all with active agents
	var hsmPoolList hsmv1alpha1.HSMPoolList
	if err := s.client.List(ctx, &hsmPoolList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list HSM pools: %w", err)
	}

	var availableDevices []string
	// Check all pools that have active agents
	for _, pool := range hsmPoolList.Items {
		if pool.Status.Phase != hsmv1alpha1.HSMPoolPhaseReady {
			continue
		}

		// Extract device name from pool name (remove "-pool" suffix)
		deviceName := strings.TrimSuffix(pool.Name, "-pool")

		if podIPs, err := s.agentManager.GetAgentPodIPs(ctx, deviceName, namespace); err == nil && len(podIPs) > 0 {
			availableDevices = append(availableDevices, deviceName)
		}
	}

	if len(availableDevices) == 0 {
		return nil, fmt.Errorf("no available HSM agents found")
	}

	return availableDevices, nil
}

// createGRPCClient creates a gRPC client for the specified device using AgentManager
func (s *Server) createGRPCClient(ctx context.Context, deviceName, namespace string) (hsm.Client, error) {
	// Use the AgentManager to create a gRPC client directly
	if s.agentManager == nil {
		return nil, fmt.Errorf("agent manager not available")
	}

	// Create gRPC client using AgentManager's existing method
	grpcClient, err := s.agentManager.CreateSingleGRPCClient(ctx, deviceName, namespace, s.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client for device %s: %w", deviceName, err)
	}

	return grpcClient, nil
}
