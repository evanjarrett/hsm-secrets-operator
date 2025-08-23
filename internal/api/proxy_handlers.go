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
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleProxyRequest handles all HSM API requests by converting to gRPC calls
func (s *Server) handleProxyRequest(c *gin.Context) {
	// Extract namespace from request or use default
	namespace := c.GetHeader("X-Namespace")
	if namespace == "" {
		namespace = "secrets" // Default namespace
	}
	
	// Find available agent (returns device name)
	deviceName, err := s.findAvailableAgent(c.Request.Context(), namespace)
	if err != nil {
		s.sendError(c, http.StatusServiceUnavailable, "no_agent", "No HSM agents available", map[string]any{
			"error": err.Error(),
		})
		return
	}

	s.logger.V(1).Info("Converting HTTP request to gRPC call",
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"device", deviceName)

	// Convert HTTP request to gRPC call
	s.proxyToAgent(c, deviceName, c.Request.URL.Path)
}

// setupProxyRoutes sets up proxy routes for HSM operations
func (s *Server) setupProxyRoutes() {
	// Serve web UI static files
	s.router.Static("/web", "./web")
	s.router.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/web/")
	})

	// Create API v1 group
	v1 := s.router.Group("/api/v1")
	{
		// HSM operations group - proxy everything to agents
		hsmGroup := v1.Group("/hsm")
		{
			// Proxy all HSM operations to agents
			hsmGroup.Any("/*path", s.handleProxyRequest)
		}

		// Health and info endpoints can stay local
		v1.GET("/health", s.handleHealth)
		v1.GET("/info", s.handleInfo)
	}
}

// handleInfo provides information about the API proxy
func (s *Server) handleInfo(c *gin.Context) {
	info := map[string]any{
		"service":     "HSM Secrets Operator API",
		"version":     "v1alpha1",
		"mode":        "proxy",
		"description": "Proxies HSM operations to agent pods",
	}

	s.sendResponse(c, http.StatusOK, "API information", info)
}
