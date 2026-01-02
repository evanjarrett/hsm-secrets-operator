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
		// Authentication endpoints (no auth required)
		if s.authenticator != nil {
			authGroup := v1.Group("/auth")
			{
				authGroup.POST("/token", s.authenticator.HandleTokenGeneration())
			}
		}

		// HSM operations group - use ProxyClient methods directly as handlers
		hsmGroup := v1.Group("/hsm")
		{
			// HSM device info and status
			hsmGroup.GET("/info", s.proxyClient.GetInfo)
			hsmGroup.GET("/status", s.proxyClient.IsConnected)

			// Secret operations
			secretsGroup := hsmGroup.Group("/secrets")
			{
				// List secrets
				secretsGroup.GET("", s.proxyClient.ListSecrets)

				// Secret-specific operations
				secretsGroup.GET("/:path", s.proxyClient.ReadSecret)
				secretsGroup.POST("/:path", s.proxyClient.WriteSecret)
				secretsGroup.PUT("/:path", s.proxyClient.WriteSecret)
				secretsGroup.DELETE("/:path", s.proxyClient.DeleteSecret)
				secretsGroup.DELETE("/:path/:key", s.proxyClient.DeleteSecretKey)

				// Secret metadata and checksum
				secretsGroup.GET("/:path/metadata", s.proxyClient.ReadMetadata)
				secretsGroup.GET("/:path/checksum", s.proxyClient.GetChecksum)
			}

			// PIN operations
			hsmGroup.POST("/change-pin", s.proxyClient.ChangePIN)

			// Mirror operations
			hsmGroup.POST("/mirror/sync", s.handleMirrorSync)
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
