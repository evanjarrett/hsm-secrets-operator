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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/security"
)

func TestJWTAuthenticationIntegration(t *testing.T) {
	// Set up test dependencies
	scheme := runtime.NewScheme()

	// Create fake Kubernetes client
	k8sInterface := fake.NewSimpleClientset()

	// Create controller-runtime fake client
	fakeClient := fakeclient.NewClientBuilder().WithScheme(scheme).Build()

	// Create agent manager
	agentManager := agent.NewManager(fakeClient, "test-namespace", "test-agent:latest", nil)

	// Create API server
	logger := log.Log.WithName("test")
	server := NewServer(fakeClient, agentManager, "test-namespace", k8sInterface, 8090, logger)

	t.Run("Invalid JWT Token", func(t *testing.T) {
		// Test with invalid JWT token
		req := httptest.NewRequest("GET", "/api/v1/hsm/info", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		// Should be unauthorized
		assert.Equal(t, http.StatusUnauthorized, w.Code)

		var response map[string]any
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		errorObj, ok := response["error"].(map[string]any)
		require.True(t, ok, "error should be an object")
		assert.Contains(t, errorObj["message"], "invalid")
	})

	t.Run("Missing Authorization Header", func(t *testing.T) {
		// Test with no authorization header
		req := httptest.NewRequest("GET", "/api/v1/hsm/info", nil)
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		// Should be unauthorized
		assert.Equal(t, http.StatusUnauthorized, w.Code)

		var response map[string]any
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		errorObj, ok := response["error"].(map[string]any)
		require.True(t, ok, "error should be an object")
		assert.Contains(t, errorObj["message"], "missing authorization header")
	})

	t.Run("Malformed Authorization Header", func(t *testing.T) {
		// Test with malformed authorization header (no Bearer prefix)
		req := httptest.NewRequest("GET", "/api/v1/hsm/info", nil)
		req.Header.Set("Authorization", "invalid-format-token")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		// Should be unauthorized
		assert.Equal(t, http.StatusUnauthorized, w.Code)

		var response map[string]any
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		errorObj, ok := response["error"].(map[string]any)
		require.True(t, ok, "error should be an object")
		assert.Contains(t, errorObj["message"], "invalid authorization header format")
	})

	t.Run("Health Endpoint Accessible Without Auth", func(t *testing.T) {
		// Health endpoint should not require authentication
		req := httptest.NewRequest("GET", "/api/v1/health", nil)
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		// Should succeed without authentication
		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]any
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, true, response["success"])
	})

	t.Run("Auth Token Endpoint Accessible Without Auth", func(t *testing.T) {
		// Token generation endpoint should not require authentication
		tokenRequest := security.TokenRequest{
			K8sToken: "test-token",
		}
		requestBody, err := json.Marshal(tokenRequest)
		require.NoError(t, err)

		req := httptest.NewRequest("POST", "/api/v1/auth/token", bytes.NewBuffer(requestBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		// The endpoint should be accessible but will return 401 due to invalid K8s token validation
		// This verifies the endpoint doesn't require JWT auth but still validates the K8s token
		assert.Equal(t, http.StatusUnauthorized, w.Code, "Should fail due to invalid K8s token, not missing JWT")

		// Verify it's a token validation error, not auth middleware error
		var response map[string]any
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		// Should contain details about the K8s token failure, not JWT auth failure
		errorObj, ok := response["error"].(map[string]any)
		require.True(t, ok, "error should be an object")
		assert.Contains(t, errorObj["message"], "failed to generate")
	})

	t.Run("JWT Authentication Enabled", func(t *testing.T) {
		// Verify that the server has JWT authentication enabled
		assert.NotNil(t, server.authenticator, "API server should have JWT authenticator enabled")

		// Test that protected endpoints are actually protected
		protectedEndpoints := []string{
			"/api/v1/hsm/info",
			"/api/v1/hsm/status",
			"/api/v1/hsm/secrets",
		}

		for _, endpoint := range protectedEndpoints {
			req := httptest.NewRequest("GET", endpoint, nil)
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code,
				"Endpoint %s should require authentication", endpoint)
		}
	})
}

func TestWebUIJWTWorkflow(t *testing.T) {
	t.Run("Web UI Static Files and Routing", func(t *testing.T) {
		// Test that the web UI is properly served and routed
		scheme := runtime.NewScheme()

		k8sInterface := fake.NewSimpleClientset()
		fakeClient := fakeclient.NewClientBuilder().WithScheme(scheme).Build()
		agentManager := agent.NewManager(fakeClient, "test-namespace", "test-agent:latest", nil)
		logger := log.Log.WithName("test-webui")
		server := NewServer(fakeClient, agentManager, "test-namespace", k8sInterface, 8090, logger)

		// Test redirect from root to web UI
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusFound, w.Code, "Should redirect to web UI")
		assert.Equal(t, "/web/", w.Header().Get("Location"), "Should redirect to /web/")

		t.Logf("✅ Web UI routing test completed successfully")
		t.Logf("✅ Root path redirects to: %s", w.Header().Get("Location"))
	})

	t.Run("Authentication Structure", func(t *testing.T) {
		// Test the authentication structure that the web UI expects
		scheme := runtime.NewScheme()

		k8sInterface := fake.NewSimpleClientset()
		fakeClient := fakeclient.NewClientBuilder().WithScheme(scheme).Build()
		agentManager := agent.NewManager(fakeClient, "test-namespace", "test-agent:latest", nil)
		logger := log.Log.WithName("test-auth-structure")
		server := NewServer(fakeClient, agentManager, "test-namespace", k8sInterface, 8090, logger)

		// Test auth token endpoint structure (should accept JSON)
		tokenRequest := map[string]string{
			"k8s_token": "invalid-but-proper-format",
		}
		requestBody, err := json.Marshal(tokenRequest)
		require.NoError(t, err)

		req := httptest.NewRequest("POST", "/api/v1/auth/token", bytes.NewBuffer(requestBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		// Should process the request (will fail on token validation, but that's expected)
		assert.NotEqual(t, http.StatusNotFound, w.Code, "Auth endpoint should exist")
		assert.NotEqual(t, http.StatusMethodNotAllowed, w.Code, "POST should be allowed")

		// Should return JSON error
		var response map[string]any
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err, "Response should be valid JSON")

		t.Logf("✅ Authentication structure test completed")
		t.Logf("✅ Auth endpoint accepts JSON and returns structured errors")
	})
}
