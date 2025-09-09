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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
)

// MockImageResolver for testing
type MockImageResolver struct{}

func (m *MockImageResolver) GetImage(ctx context.Context, defaultImage string) string {
	return "test-image:latest"
}

func TestGetAllAvailableAgents(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))

	tests := []struct {
		name            string
		agentManager    *agent.Manager
		expectedDevices []string
		expectError     bool
	}{
		{
			name:            "nil agent manager",
			agentManager:    nil,
			expectedDevices: nil,
			expectError:     true,
		},
		{
			name: "valid agent manager with no devices",
			agentManager: func() *agent.Manager {
				fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
				return agent.NewManager(fakeClient, "test-namespace", nil)
			}(),
			expectedDevices: nil,
			expectError:     true, // GetAvailableDevices returns error when no devices found
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create server instance
			server := &Server{
				agentManager: tt.agentManager,
				logger:       logr.Discard(),
			}

			devices, err := server.getAllAvailableAgents(ctx, "test-namespace")

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, tt.expectedDevices, devices)
			}
		})
	}
}

func TestNewServer(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockImageResolver := &MockImageResolver{}
	agentManager := agent.NewTestManager(client, "test-namespace", mockImageResolver)
	logger := logr.Discard()

	server := NewServer(client, agentManager, "test-namespace", logger)

	assert.NotNil(t, server)
	assert.Equal(t, client, server.client)
	assert.Equal(t, agentManager, server.agentManager)
	assert.Equal(t, "test-namespace", server.operatorNamespace)
	assert.NotNil(t, server.logger)
	assert.NotNil(t, server.proxyClient)
}

func TestServerStart(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockImageResolver := &MockImageResolver{}
	agentManager := agent.NewTestManager(client, "test-namespace", mockImageResolver)
	logger := logr.Discard()

	server := NewServer(client, agentManager, "test-namespace", logger)

	// Test that server can be created and has expected configuration
	assert.NotNil(t, server)
	assert.NotNil(t, server.router)
	assert.NotNil(t, server.proxyClient)
}

// Test sendResponse method
func TestServer_SendResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := &Server{
		logger: logr.Discard(),
	}

	tests := []struct {
		name       string
		statusCode int
		message    string
		data       any
	}{
		{
			name:       "successful response with data",
			statusCode: http.StatusOK,
			message:    "Operation successful",
			data:       map[string]string{"key": "value"},
		},
		{
			name:       "created response with nil data",
			statusCode: http.StatusCreated,
			message:    "Resource created",
			data:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			server.sendResponse(c, tt.statusCode, tt.message, tt.data)

			assert.Equal(t, tt.statusCode, w.Code)
			assert.Contains(t, w.Body.String(), "\"success\":true")
			assert.Contains(t, w.Body.String(), tt.message)
		})
	}
}

// Test sendError method
func TestServer_SendError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := &Server{
		logger: logr.Discard(),
	}

	tests := []struct {
		name       string
		statusCode int
		code       string
		message    string
		details    map[string]any
	}{
		{
			name:       "bad request error",
			statusCode: http.StatusBadRequest,
			code:       "invalid_request",
			message:    "Request validation failed",
			details:    map[string]any{"field": "path"},
		},
		{
			name:       "internal server error",
			statusCode: http.StatusInternalServerError,
			code:       "internal_error",
			message:    "Something went wrong",
			details:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			server.sendError(c, tt.statusCode, tt.code, tt.message, tt.details)

			assert.Equal(t, tt.statusCode, w.Code)
			assert.Contains(t, w.Body.String(), "\"success\":false")
			assert.Contains(t, w.Body.String(), tt.code)
			assert.Contains(t, w.Body.String(), tt.message)
		})
	}
}

// Test CORS middleware
func TestServer_CorsMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := &Server{
		logger: logr.Discard(),
	}

	tests := []struct {
		name           string
		method         string
		expectedStatus int
	}{
		{
			name:           "OPTIONS request",
			method:         "OPTIONS",
			expectedStatus: http.StatusNoContent, // 204
		},
		{
			name:           "GET request with CORS headers",
			method:         "GET",
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest(tt.method, "/test", nil)

			middleware := server.corsMiddleware()
			middleware(c)

			if tt.method == "OPTIONS" {
				// OPTIONS should abort with 204
				assert.Equal(t, http.StatusNoContent, w.Code)
			}

			// Check CORS headers are set
			assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
			assert.Equal(t, "GET, POST, PUT, DELETE, OPTIONS", w.Header().Get("Access-Control-Allow-Methods"))
			assert.Equal(t, "Content-Type, Authorization", w.Header().Get("Access-Control-Allow-Headers"))
		})
	}
}

// Test logging middleware
func TestServer_LoggingMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := &Server{
		logger: logr.Discard(),
	}

	// Test that logging middleware doesn't panic
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/test", nil)

	middleware := server.loggingMiddleware()

	// Should not panic
	assert.NotPanics(t, func() {
		middleware(c)
	})
}

// Test getAllAvailableAgents with nil manager
func TestServer_GetAllAvailableAgents_NilManager(t *testing.T) {
	server := &Server{
		agentManager:      nil,
		operatorNamespace: "test-namespace",
		logger:            logr.Discard(),
	}

	ctx := context.Background()
	devices, err := server.getAllAvailableAgents(ctx, "test-namespace")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent manager not available")
	assert.Nil(t, devices)
}

// Test createGRPCClient with nil manager
func TestServer_CreateGRPCClient_NilManager(t *testing.T) {
	server := &Server{
		agentManager: nil,
		logger:       logr.Discard(),
	}

	ctx := context.Background()
	client, err := server.createGRPCClient(ctx, "test-device", "test-ns")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent manager not available")
	assert.Nil(t, client)
}
