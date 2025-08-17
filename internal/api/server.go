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
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/go-playground/validator/v10"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/discovery"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// Server represents the HSM REST API server
type Server struct {
	client           client.Client
	hsmClient        hsm.Client
	mirroringManager *discovery.MirroringManager
	validator        *validator.Validate
	logger           logr.Logger
	router           *gin.Engine
}

// NewServer creates a new API server instance
func NewServer(client client.Client, hsmClient hsm.Client, mirroringManager *discovery.MirroringManager, logger logr.Logger) *Server {
	s := &Server{
		client:           client,
		hsmClient:        hsmClient,
		mirroringManager: mirroringManager,
		validator:        validator.New(),
		logger:           logger.WithName("api-server"),
	}

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

	// API v1 routes
	v1 := s.router.Group("/api/v1")
	{
		// Health check
		v1.GET("/health", s.handleHealth)

		// HSM secrets management
		hsm := v1.Group("/hsm")
		{
			secrets := hsm.Group("/secrets")
			{
				secrets.POST("", s.handleCreateSecret)
				secrets.GET("", s.handleListSecrets)
				secrets.GET("/:label", s.handleGetSecret)
				secrets.PUT("/:label", s.handleUpdateSecret)
				secrets.DELETE("/:label", s.handleDeleteSecret)
				secrets.POST("/import", s.handleImportSecret)
			}
		}
	}
}

// Start starts the API server on the specified port
func (s *Server) Start(port int) error {
	addr := fmt.Sprintf(":%d", port)
	s.logger.Info("Starting API server", "addr", addr)
	return s.router.Run(addr)
}

// handleHealth handles health check requests
func (s *Server) handleHealth(c *gin.Context) {

	hsmConnected := s.hsmClient != nil && s.hsmClient.IsConnected()
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

// handleCreateSecret handles secret creation requests
func (s *Server) handleCreateSecret(c *gin.Context) {
	ctx := c.Request.Context()

	var req CreateSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.sendError(c, http.StatusBadRequest, "invalid_request", "Invalid JSON payload", map[string]interface{}{
			"parse_error": err.Error(),
		})
		return
	}

	if err := s.validator.Struct(&req); err != nil {
		s.sendError(c, http.StatusBadRequest, "validation_failed", "Request validation failed", map[string]interface{}{
			"validation_errors": err.Error(),
		})
		return
	}

	// Check if HSM client is available
	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		s.sendError(c, http.StatusServiceUnavailable, "hsm_unavailable", "HSM client is not available", nil)
		return
	}

	// Convert request data to HSM format
	hsmData, err := s.convertToHSMData(req.Data, req.Format)
	if err != nil {
		s.sendError(c, http.StatusBadRequest, "data_conversion_error", err.Error(), nil)
		return
	}

	// Create HSM path from label
	hsmPath := s.generateHSMPath(req.Label, req.ID)

	// Store secret in HSM
	if err := s.hsmClient.WriteSecret(ctx, hsmPath, hsmData); err != nil {
		s.logger.Error(err, "Failed to write secret to HSM", "label", req.Label, "id", req.ID)
		s.sendError(c, http.StatusInternalServerError, "hsm_write_error", "Failed to store secret in HSM", map[string]interface{}{
			"hsm_error": err.Error(),
		})
		return
	}

	// Create corresponding HSMSecret resource in Kubernetes
	if err := s.createHSMSecretResource(ctx, req.Label, hsmPath, req.Description, req.Tags); err != nil {
		s.logger.Error(err, "Failed to create HSMSecret resource", "label", req.Label)
		// Continue - the secret is stored in HSM, just log the error
	}

	s.logger.Info("Secret created successfully", "label", req.Label, "id", req.ID)
	s.sendResponse(c, http.StatusCreated, "Secret created successfully", map[string]interface{}{
		"label": req.Label,
		"id":    req.ID,
		"path":  hsmPath,
	})
}

// handleGetSecret handles secret retrieval requests
func (s *Server) handleGetSecret(c *gin.Context) {
	ctx := c.Request.Context()
	label := c.Param("label")

	if label == "" {
		s.sendError(c, http.StatusBadRequest, "invalid_label", "Label parameter is required", nil)
		return
	}

	// Find HSMSecret resource to get the HSM path
	hsmSecret, err := s.findHSMSecretByLabel(ctx, label)
	if err != nil {
		s.logger.Error(err, "Failed to find HSMSecret resource", "label", label)
		s.sendError(c, http.StatusNotFound, "secret_not_found", "Secret not found", nil)
		return
	}

	// Read from HSM with fallback support
	var hsmData hsm.SecretData
	if s.hsmClient != nil && s.hsmClient.IsConnected() {
		hsmData, err = s.hsmClient.ReadSecret(ctx, hsmSecret.Spec.HSMPath)
		if err != nil && s.mirroringManager != nil {
			// Try readonly fallback
			if hsmDevice, devErr := s.findHSMDevice(ctx); devErr == nil && hsmDevice != nil {
				hsmData, err = s.mirroringManager.GetReadOnlyAccess(ctx, hsmSecret.Spec.HSMPath, hsmDevice)
			}
		}
	} else if s.mirroringManager != nil {
		// Primary HSM unavailable, try readonly access
		if hsmDevice, devErr := s.findHSMDevice(ctx); devErr == nil && hsmDevice != nil {
			hsmData, err = s.mirroringManager.GetReadOnlyAccess(ctx, hsmSecret.Spec.HSMPath, hsmDevice)
		}
	}

	if err != nil {
		s.logger.Error(err, "Failed to read secret from HSM", "label", label)
		s.sendError(c, http.StatusInternalServerError, "hsm_read_error", "Failed to read secret from HSM", nil)
		return
	}

	// Convert HSM data back to API format
	data, err := s.convertFromHSMData(hsmData)
	if err != nil {
		s.sendError(c, http.StatusInternalServerError, "data_conversion_error", err.Error(), nil)
		return
	}

	// Create metadata
	checksum := hsm.CalculateChecksum(hsmData)
	metadata := SecretInfo{
		Label:        label,
		Checksum:     checksum,
		UpdatedAt:    time.Now(),
		IsReplicated: s.mirroringManager != nil,
	}

	secretData := SecretData{
		Data:     data,
		Metadata: metadata,
	}

	s.sendResponse(c, http.StatusOK, "Secret retrieved successfully", secretData)
}

// handleUpdateSecret handles secret update requests
func (s *Server) handleUpdateSecret(c *gin.Context) {
	ctx := c.Request.Context()
	label := c.Param("label")

	var req UpdateSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.sendError(c, http.StatusBadRequest, "invalid_request", "Invalid JSON payload", nil)
		return
	}

	if err := s.validator.Struct(&req); err != nil {
		s.sendError(c, http.StatusBadRequest, "validation_failed", "Request validation failed", nil)
		return
	}

	// Find existing HSMSecret resource
	hsmSecret, err := s.findHSMSecretByLabel(ctx, label)
	if err != nil {
		s.sendError(c, http.StatusNotFound, "secret_not_found", "Secret not found", nil)
		return
	}

	// Check if HSM client is available for write operations
	if s.hsmClient == nil || !s.hsmClient.IsConnected() {
		s.sendError(c, http.StatusServiceUnavailable, "hsm_unavailable", "HSM client is not available for write operations", nil)
		return
	}

	// Convert request data to HSM format (assume JSON for updates)
	hsmData, err := s.convertToHSMData(req.Data, SecretFormatJSON)
	if err != nil {
		s.sendError(c, http.StatusBadRequest, "data_conversion_error", err.Error(), nil)
		return
	}

	// Update secret in HSM
	if err := s.hsmClient.WriteSecret(ctx, hsmSecret.Spec.HSMPath, hsmData); err != nil {
		s.logger.Error(err, "Failed to update secret in HSM", "label", label)
		s.sendError(c, http.StatusInternalServerError, "hsm_write_error", "Failed to update secret in HSM", nil)
		return
	}

	s.logger.Info("Secret updated successfully", "label", label)
	s.sendResponse(c, http.StatusOK, "Secret updated successfully", map[string]interface{}{
		"label": label,
		"path":  hsmSecret.Spec.HSMPath,
	})
}

// handleDeleteSecret handles secret deletion requests
func (s *Server) handleDeleteSecret(c *gin.Context) {
	ctx := c.Request.Context()
	label := c.Param("label")

	// Find existing HSMSecret resource
	hsmSecret, err := s.findHSMSecretByLabel(ctx, label)
	if err != nil {
		s.sendError(c, http.StatusNotFound, "secret_not_found", "Secret not found", nil)
		return
	}

	// Delete the HSMSecret resource (this will trigger cleanup via finalizers)
	if err := s.client.Delete(ctx, hsmSecret); err != nil {
		s.logger.Error(err, "Failed to delete HSMSecret resource", "label", label)
		s.sendError(c, http.StatusInternalServerError, "delete_error", "Failed to delete secret", nil)
		return
	}

	s.logger.Info("Secret deleted successfully", "label", label)
	s.sendResponse(c, http.StatusOK, "Secret deleted successfully", map[string]interface{}{
		"label": label,
	})
}

// handleListSecrets handles secret listing requests
func (s *Server) handleListSecrets(c *gin.Context) {
	ctx := c.Request.Context()

	// Get pagination parameters
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}

	// List HSMSecret resources
	var hsmSecretList hsmv1alpha1.HSMSecretList
	if err := s.client.List(ctx, &hsmSecretList); err != nil {
		s.logger.Error(err, "Failed to list HSMSecret resources")
		s.sendError(c, http.StatusInternalServerError, "list_error", "Failed to list secrets", nil)
		return
	}

	// Convert to API format with pagination
	secrets := make([]SecretInfo, 0)
	start := (page - 1) * pageSize
	end := start + pageSize

	for i, hsmSecret := range hsmSecretList.Items {
		if i >= start && i < end {
			info := SecretInfo{
				Label:        hsmSecret.Name,
				Checksum:     hsmSecret.Status.HSMChecksum,
				IsReplicated: s.mirroringManager != nil,
			}

			if hsmSecret.Status.LastSyncTime != nil {
				info.UpdatedAt = hsmSecret.Status.LastSyncTime.Time
			}

			secrets = append(secrets, info)
		}
	}

	secretList := SecretList{
		Secrets:  secrets,
		Total:    len(hsmSecretList.Items),
		Page:     page,
		PageSize: pageSize,
	}

	s.sendResponse(c, http.StatusOK, "Secrets listed successfully", secretList)
}

// handleImportSecret handles secret import requests
func (s *Server) handleImportSecret(c *gin.Context) {
	ctx := c.Request.Context()

	var req ImportSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.sendError(c, http.StatusBadRequest, "invalid_request", "Invalid JSON payload", nil)
		return
	}

	if err := s.validator.Struct(&req); err != nil {
		s.sendError(c, http.StatusBadRequest, "validation_failed", "Request validation failed", nil)
		return
	}

	// Import logic depends on source
	var data map[string]interface{}
	var err error

	switch req.Source {
	case "kubernetes":
		data, err = s.importFromKubernetes(ctx, req.SecretName, req.SecretNamespace, req.KeyMapping)
	default:
		s.sendError(c, http.StatusBadRequest, "unsupported_source", fmt.Sprintf("Import source '%s' is not supported", req.Source), nil)
		return
	}

	if err != nil {
		s.logger.Error(err, "Failed to import secret", "source", req.Source, "name", req.SecretName)
		s.sendError(c, http.StatusInternalServerError, "import_error", err.Error(), nil)
		return
	}

	// Create the secret using the imported data
	createReq := CreateSecretRequest{
		Label:  req.TargetLabel,
		ID:     req.TargetID,
		Format: req.Format,
		Data:   data,
	}

	// Use existing creation logic
	c.Set("create_request", createReq)
	s.handleCreateSecret(c)
}

// sendResponse sends a successful API response
func (s *Server) sendResponse(c *gin.Context, statusCode int, message string, data interface{}) {
	response := APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	}
	c.JSON(statusCode, response)
}

// sendError sends an error API response
func (s *Server) sendError(c *gin.Context, statusCode int, code, message string, details map[string]interface{}) {
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
