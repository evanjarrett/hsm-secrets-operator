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
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/go-playground/validator/v10"

	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// Server represents the HSM agent HTTP server
type Server struct {
	hsmClient  hsm.Client
	validator  *validator.Validate
	logger     logr.Logger
	router     *gin.Engine
	deviceName string
	port       int
	healthPort int
}

// AgentRequest represents a generic HSM operation request
type AgentRequest struct {
	Path string         `json:"path" validate:"required"`
	Data map[string]any `json:"data,omitempty"`
}

// AgentResponse represents a generic HSM operation response
type AgentResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    any         `json:"data,omitempty"`
	Error   *AgentError `json:"error,omitempty"`
}

// AgentError represents an error response
type AgentError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// HealthStatus represents the health status of the agent
type HealthStatus struct {
	Status       string    `json:"status"`
	DeviceName   string    `json:"deviceName"`
	HSMConnected bool      `json:"hsmConnected"`
	Timestamp    time.Time `json:"timestamp"`
	Uptime       string    `json:"uptime"`
}

// NewServer creates a new HSM agent server
func NewServer(hsmClient hsm.Client, deviceName string, port, healthPort int, logger logr.Logger) *Server {
	s := &Server{
		hsmClient:  hsmClient,
		validator:  validator.New(),
		logger:     logger.WithName("agent-server"),
		deviceName: deviceName,
		port:       port,
		healthPort: healthPort,
	}

	s.setupRouter()
	return s
}

// setupRouter configures the HTTP routes
func (s *Server) setupRouter() {
	gin.SetMode(gin.ReleaseMode)
	s.router = gin.New()

	// Add middleware
	s.router.Use(gin.Recovery())
	s.router.Use(s.loggingMiddleware())
	s.router.Use(s.authMiddleware())

	// API v1 routes
	v1 := s.router.Group("/api/v1")
	{
		// HSM operations
		hsmGroup := v1.Group("/hsm")
		{
			hsmGroup.GET("/info", s.handleGetInfo)
			hsmGroup.GET("/secrets/:path", s.handleReadSecret)
			hsmGroup.POST("/secrets/:path", s.handleWriteSecret)
			hsmGroup.PUT("/secrets/:path", s.handleWriteSecret)
			hsmGroup.DELETE("/secrets/:path", s.handleDeleteSecret)
			hsmGroup.GET("/secrets", s.handleListSecrets)
			hsmGroup.GET("/checksum/:path", s.handleGetChecksum)
		}
	}

	// Health endpoints (separate router for different port)
	s.setupHealthRouter()
}

// setupHealthRouter sets up health check routes
func (s *Server) setupHealthRouter() {
	// Health checks will be handled by a separate handler function
	// This is called during Start()
}

// Start starts both the main API server and health server
func (s *Server) Start(ctx context.Context) error {
	// Start health server in background
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", s.handleHealthz)
	healthMux.HandleFunc("/readyz", s.handleReadyz)

	healthServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.healthPort),
		Handler: healthMux,
	}

	go func() {
		s.logger.Info("Starting health server", "port", s.healthPort)
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error(err, "Health server failed")
		}
	}()

	// Start main API server
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: s.router,
	}

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		s.logger.Info("Shutting down servers")
		if err := healthServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Error(err, "Failed to shutdown health server")
		}
		if err := server.Shutdown(shutdownCtx); err != nil {
			s.logger.Error(err, "Failed to shutdown main server")
		}
	}()

	s.logger.Info("Starting HSM agent server", "port", s.port, "device", s.deviceName)
	return server.ListenAndServe()
}

// handleGetInfo handles HSM info requests
func (s *Server) handleGetInfo(c *gin.Context) {
	ctx := c.Request.Context()

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		s.sendError(c, http.StatusServiceUnavailable, "hsm_unavailable", "HSM client not connected", nil)
		return
	}

	info, err := s.hsmClient.GetInfo(ctx)
	if err != nil {
		s.logger.Error(err, "Failed to get HSM info")
		s.sendError(c, http.StatusInternalServerError, "hsm_error", "Failed to get HSM info", map[string]any{
			"error": err.Error(),
		})
		return
	}

	s.sendResponse(c, http.StatusOK, "HSM info retrieved", info)
}

// handleReadSecret handles secret read requests
func (s *Server) handleReadSecret(c *gin.Context) {
	ctx := c.Request.Context()
	path := c.Param("path")

	if path == "" {
		s.sendError(c, http.StatusBadRequest, "invalid_path", "Path parameter is required", nil)
		return
	}

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		s.sendError(c, http.StatusServiceUnavailable, "hsm_unavailable", "HSM client not connected", nil)
		return
	}

	data, err := s.hsmClient.ReadSecret(ctx, path)
	if err != nil {
		s.logger.Error(err, "Failed to read secret", "path", path)
		s.sendError(c, http.StatusInternalServerError, "read_error", "Failed to read secret", map[string]any{
			"path":  path,
			"error": err.Error(),
		})
		return
	}

	// Calculate checksum
	checksum := hsm.CalculateChecksum(data)

	response := map[string]any{
		"path":     path,
		"data":     data,
		"checksum": checksum,
	}

	s.sendResponse(c, http.StatusOK, "Secret read successfully", response)
}

// handleWriteSecret handles secret write requests
func (s *Server) handleWriteSecret(c *gin.Context) {
	ctx := c.Request.Context()
	path := c.Param("path")

	if path == "" {
		s.sendError(c, http.StatusBadRequest, "invalid_path", "Path parameter is required", nil)
		return
	}

	var req AgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.sendError(c, http.StatusBadRequest, "invalid_request", "Invalid JSON payload", map[string]any{
			"error": err.Error(),
		})
		return
	}

	// Use path from URL parameter, not request body
	req.Path = path

	if err := s.validator.Struct(&req); err != nil {
		s.sendError(c, http.StatusBadRequest, "validation_failed", "Request validation failed", map[string]any{
			"error": err.Error(),
		})
		return
	}

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		s.sendError(c, http.StatusServiceUnavailable, "hsm_unavailable", "HSM client not connected", nil)
		return
	}

	// Convert request data to HSM format
	hsmData := make(hsm.SecretData)
	for key, value := range req.Data {
		switch v := value.(type) {
		case string:
			hsmData[key] = []byte(v)
		case []byte:
			hsmData[key] = v
		default:
			// Convert to string as fallback
			hsmData[key] = fmt.Appendf(nil, "%v", v)
		}
	}

	if err := s.hsmClient.WriteSecret(ctx, req.Path, hsmData); err != nil {
		s.logger.Error(err, "Failed to write secret", "path", req.Path)
		s.sendError(c, http.StatusInternalServerError, "write_error", "Failed to write secret", map[string]any{
			"path":  req.Path,
			"error": err.Error(),
		})
		return
	}

	// Calculate checksum for response
	checksum := hsm.CalculateChecksum(hsmData)

	response := map[string]any{
		"path":     req.Path,
		"checksum": checksum,
	}

	s.sendResponse(c, http.StatusCreated, "Secret written successfully", response)
}

// handleDeleteSecret handles secret deletion requests
func (s *Server) handleDeleteSecret(c *gin.Context) {
	ctx := c.Request.Context()
	path := c.Param("path")

	if path == "" {
		s.sendError(c, http.StatusBadRequest, "invalid_path", "Path parameter is required", nil)
		return
	}

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		s.sendError(c, http.StatusServiceUnavailable, "hsm_unavailable", "HSM client not connected", nil)
		return
	}

	if err := s.hsmClient.DeleteSecret(ctx, path); err != nil {
		s.logger.Error(err, "Failed to delete secret", "path", path)
		s.sendError(c, http.StatusInternalServerError, "delete_error", "Failed to delete secret", map[string]any{
			"path":  path,
			"error": err.Error(),
		})
		return
	}

	response := map[string]any{
		"path": path,
	}

	s.sendResponse(c, http.StatusOK, "Secret deleted successfully", response)
}

// handleListSecrets handles secret listing requests
func (s *Server) handleListSecrets(c *gin.Context) {
	ctx := c.Request.Context()
	prefix := c.Query("prefix")

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		s.sendError(c, http.StatusServiceUnavailable, "hsm_unavailable", "HSM client not connected", nil)
		return
	}

	paths, err := s.hsmClient.ListSecrets(ctx, prefix)
	if err != nil {
		s.logger.Error(err, "Failed to list secrets", "prefix", prefix)
		s.sendError(c, http.StatusInternalServerError, "list_error", "Failed to list secrets", map[string]any{
			"prefix": prefix,
			"error":  err.Error(),
		})
		return
	}

	response := map[string]any{
		"prefix": prefix,
		"paths":  paths,
		"count":  len(paths),
	}

	s.sendResponse(c, http.StatusOK, "Secrets listed successfully", response)
}

// handleGetChecksum handles checksum requests
func (s *Server) handleGetChecksum(c *gin.Context) {
	ctx := c.Request.Context()
	path := c.Param("path")

	if path == "" {
		s.sendError(c, http.StatusBadRequest, "invalid_path", "Path parameter is required", nil)
		return
	}

	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		s.sendError(c, http.StatusServiceUnavailable, "hsm_unavailable", "HSM client not connected", nil)
		return
	}

	checksum, err := s.hsmClient.GetChecksum(ctx, path)
	if err != nil {
		s.logger.Error(err, "Failed to get checksum", "path", path)
		s.sendError(c, http.StatusInternalServerError, "checksum_error", "Failed to get checksum", map[string]any{
			"path":  path,
			"error": err.Error(),
		})
		return
	}

	response := map[string]any{
		"path":     path,
		"checksum": checksum,
	}

	s.sendResponse(c, http.StatusOK, "Checksum retrieved successfully", response)
}

// handleHealthz handles liveness probe requests
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	status := HealthStatus{
		Status:       "healthy",
		DeviceName:   s.deviceName,
		HSMConnected: s.hsmClient != nil && s.hsmClient.IsConnected(),
		Timestamp:    time.Now(),
		Uptime:       "unknown", // Could track actual uptime
	}

	if !status.HSMConnected {
		status.Status = "degraded"
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	w.Header().Set("Content-Type", "application/json")
	// Simple JSON encoding without external dependencies
	if _, err := fmt.Fprintf(w, `{"status":"%s","deviceName":"%s","hsmConnected":%t,"timestamp":"%s"}`,
		status.Status, status.DeviceName, status.HSMConnected, status.Timestamp.Format(time.RFC3339)); err != nil {
		s.logger.Error(err, "Failed to write health response")
	}
}

// handleReadyz handles readiness probe requests
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	// Agent is ready if HSM client is connected
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

// sendResponse sends a successful API response
func (s *Server) sendResponse(c *gin.Context, statusCode int, message string, data any) {
	response := AgentResponse{
		Success: true,
		Message: message,
		Data:    data,
	}
	c.JSON(statusCode, response)
}

// sendError sends an error API response
func (s *Server) sendError(c *gin.Context, statusCode int, code, message string, details map[string]any) {
	response := AgentResponse{
		Success: false,
		Error: &AgentError{
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
		s.logger.Info("HTTP request",
			"method", param.Method,
			"path", param.Path,
			"status", param.StatusCode,
			"latency", param.Latency,
			"ip", param.ClientIP,
		)
		return ""
	})
}

// authMiddleware provides basic authentication/authorization
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// For now, no authentication - this runs in a secure cluster
		// In production, you might want to add:
		// - Service account token validation
		// - mTLS client certificate validation
		// - Custom authentication headers

		// Add request context
		c.Set("device_name", s.deviceName)
		c.Next()
	}
}
