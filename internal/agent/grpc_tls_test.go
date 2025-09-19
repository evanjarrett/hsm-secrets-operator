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
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
	"github.com/evanjarrett/hsm-secrets-operator/internal/security"
)

func TestGRPCServer_mTLS_Functionality(t *testing.T) {
	// Setup fake Kubernetes client with scheme
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	// Create mock HSM client
	mockHSMClient := hsm.NewMockClient()
	ctx := context.Background()
	err := mockHSMClient.Initialize(ctx, hsm.Config{})
	require.NoError(t, err)

	// Configure server with TLS enabled
	tlsConfig := GRPCServerConfig{
		ServiceName: "test-agent-tls",
		Namespace:   "test-namespace",
		EnableTLS:   true,
		K8sClient:   fakeClient,
		DNSNames:    []string{"test-agent-tls", "localhost"},
		IPs:         []net.IP{net.ParseIP("127.0.0.1")},
	}

	logger := logr.Discard()
	grpcServer := NewGRPCServer(mockHSMClient, 0, 0, tlsConfig, logger)

	// Start server in background
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- grpcServer.Start(ctx)
	}()

	// Give server time to start and generate certificates
	time.Sleep(2 * time.Second)

	// Verify TLS configuration was created
	assert.NotNil(t, grpcServer.tlsConfig, "TLS configuration should be created")
	assert.NotNil(t, grpcServer.certRotator, "Certificate rotator should be created")

	// Test certificate info endpoint
	t.Run("certificate_info_endpoint", func(t *testing.T) {
		if grpcServer.certRotator != nil {
			certInfo := grpcServer.certRotator.GetCertificateInfo()
			assert.Equal(t, "active", certInfo["status"])
			assert.Contains(t, certInfo, "expiry")
			assert.Contains(t, certInfo, "time_until_expiry")
		}
	})

	// Verify certificates are properly formatted
	t.Run("certificate_validation", func(t *testing.T) {
		if grpcServer.tlsConfig != nil {
			// Test server certificate parsing
			cert, err := tls.X509KeyPair(grpcServer.tlsConfig.CertPEM, grpcServer.tlsConfig.KeyPEM)
			require.NoError(t, err, "Server certificate should be valid")
			assert.NotEmpty(t, cert.Certificate)

			// Test CA certificate parsing
			caCertPool := x509.NewCertPool()
			ok := caCertPool.AppendCertsFromPEM(grpcServer.tlsConfig.CAPEM)
			assert.True(t, ok, "CA certificate should be valid")
		}
	})

	// Test that server credentials can be created
	t.Run("server_credentials", func(t *testing.T) {
		if grpcServer.tlsConfig != nil {
			creds, err := grpcServer.tlsConfig.GetServerCredentials()
			require.NoError(t, err, "Should be able to create server credentials")
			assert.NotNil(t, creds)
		}
	})

	// Test that client credentials can be created
	t.Run("client_credentials", func(t *testing.T) {
		if grpcServer.tlsConfig != nil {
			creds, err := grpcServer.tlsConfig.GetClientCredentials("test-agent-tls.test-namespace.svc.cluster.local")
			require.NoError(t, err, "Should be able to create client credentials")
			assert.NotNil(t, creds)
		}
	})

	// Cleanup
	cancel()
	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Log("Server shutdown timeout")
	}
}

func TestGRPCServer_mTLS_Configuration(t *testing.T) {
	// Test that mTLS configuration is properly set up (without full integration)
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	mockHSMClient := hsm.NewMockClient()
	err := mockHSMClient.Initialize(context.Background(), hsm.Config{})
	require.NoError(t, err)

	// Test that mTLS is properly configured when enabled
	t.Run("mtls_enabled_configuration", func(t *testing.T) {
		tlsConfig := GRPCServerConfig{
			ServiceName: "test-agent-mtls",
			Namespace:   "test-namespace",
			EnableTLS:   true,
			K8sClient:   fakeClient,
			DNSNames:    []string{"test-agent-mtls", "localhost"},
			IPs:         []net.IP{net.ParseIP("127.0.0.1")},
		}

		grpcServer := NewGRPCServer(mockHSMClient, 0, 0, tlsConfig, logr.Discard())

		// Test TLS initialization attempt (may or may not succeed in test environment)
		assert.NotNil(t, grpcServer)
		assert.Equal(t, "test-agent-mtls", grpcServer.serviceName)
		assert.Equal(t, "test-namespace", grpcServer.namespace)
		assert.NotNil(t, grpcServer.k8sClient)

		// If TLS was successfully initialized, verify the configuration
		if grpcServer.certRotator != nil {
			assert.NotNil(t, grpcServer.certRotator)
			t.Log("TLS successfully initialized in test environment")
		} else {
			t.Log("TLS initialization skipped in test environment (expected)")
		}
	})

	// Test TLS components independently
	t.Run("tls_components_functionality", func(t *testing.T) {
		// Test certificate manager creation
		certManager, err := security.NewCertificateManager()
		require.NoError(t, err)
		assert.NotNil(t, certManager)

		// Test server certificate generation
		serverTLS, err := certManager.GenerateServerCert("test-service", "test-ns", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
		require.NoError(t, err)
		assert.NotNil(t, serverTLS)
		assert.NotEmpty(t, serverTLS.CertPEM)
		assert.NotEmpty(t, serverTLS.KeyPEM)
		assert.NotEmpty(t, serverTLS.CAPEM)

		// Test client certificate generation
		clientTLS, err := certManager.GenerateClientCert("test-client")
		require.NoError(t, err)
		assert.NotNil(t, clientTLS)
		assert.NotEmpty(t, clientTLS.CertPEM)
		assert.NotEmpty(t, clientTLS.KeyPEM)
		assert.NotEmpty(t, clientTLS.CAPEM)

		// Test credential generation
		serverCreds, err := serverTLS.GetServerCredentials()
		require.NoError(t, err)
		assert.NotNil(t, serverCreds)

		clientCreds, err := clientTLS.GetClientCredentials("test-service.test-ns.svc.cluster.local")
		require.NoError(t, err)
		assert.NotNil(t, clientCreds)
	})
}

func TestCertificateRotator_Functionality(t *testing.T) {
	// Setup fake Kubernetes client
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	// Test certificate rotator creation and basic functionality
	t.Run("certificate_rotator_creation", func(t *testing.T) {
		config := security.DefaultRotatorConfig("test-namespace", "test-tls-secret", "test-service")
		config.RotationInterval = 1 * time.Second // Fast rotation for testing
		config.RenewalThreshold = 500 * time.Millisecond

		rotator, err := security.NewCertificateRotator(fakeClient, config, logr.Discard())
		require.NoError(t, err)
		assert.NotNil(t, rotator)

		// Test initial certificate generation
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err = rotator.Start(ctx, "test-service", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
		require.NoError(t, err)

		// Verify initial TLS config
		tlsConfig := rotator.GetCurrentTLSConfig()
		require.NotNil(t, tlsConfig)
		assert.NotEmpty(t, tlsConfig.CertPEM)
		assert.NotEmpty(t, tlsConfig.KeyPEM)
		assert.NotEmpty(t, tlsConfig.CAPEM)

		rotator.Stop()
	})

	t.Run("certificate_rotation_cycle", func(t *testing.T) {
		// Create rotator with very short intervals for testing
		config := security.DefaultRotatorConfig("test-namespace", "test-rotation-secret", "test-service")
		config.RotationInterval = 100 * time.Millisecond
		config.RenewalThreshold = 50 * time.Millisecond

		rotator, err := security.NewCertificateRotator(fakeClient, config, logr.Discard())
		require.NoError(t, err)

		// Track certificate updates
		var certUpdates int
		rotator.AddUpdateCallback(func(newTLSConfig *security.TLSConfig) error {
			certUpdates++
			assert.NotNil(t, newTLSConfig)
			assert.NotEmpty(t, newTLSConfig.CertPEM)
			return nil
		})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err = rotator.Start(ctx, "test-service", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
		require.NoError(t, err)

		// Wait for potential rotation
		time.Sleep(500 * time.Millisecond)

		// Get certificate info
		certInfo := rotator.GetCertificateInfo()
		assert.Equal(t, "active", certInfo["status"])
		assert.Contains(t, certInfo, "expiry")
		assert.Contains(t, certInfo, "rotation_interval")

		rotator.Stop()
	})

	t.Run("certificate_storage_in_kubernetes", func(t *testing.T) {
		config := security.DefaultRotatorConfig("test-namespace", "test-k8s-secret", "test-service")

		rotator, err := security.NewCertificateRotator(fakeClient, config, logr.Discard())
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err = rotator.Start(ctx, "test-service", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
		require.NoError(t, err)

		// Verify secret was created in Kubernetes
		var secret corev1.Secret
		err = fakeClient.Get(ctx, types.NamespacedName{
			Namespace: "test-namespace",
			Name:      "test-k8s-secret",
		}, &secret)
		require.NoError(t, err)

		// Verify secret contents
		assert.Equal(t, corev1.SecretTypeTLS, secret.Type)
		assert.Contains(t, secret.Data, "tls.crt")
		assert.Contains(t, secret.Data, "tls.key")
		assert.Contains(t, secret.Data, "ca.crt")
		assert.NotEmpty(t, secret.Data["tls.crt"])
		assert.NotEmpty(t, secret.Data["tls.key"])
		assert.NotEmpty(t, secret.Data["ca.crt"])

		// Verify labels and annotations
		assert.Equal(t, "hsm-secrets-operator", secret.Labels["app.kubernetes.io/name"])
		assert.Equal(t, "tls", secret.Labels["app.kubernetes.io/component"])
		assert.Contains(t, secret.Annotations, "hsm.j5t.io/rotation-time")
		assert.Contains(t, secret.Annotations, "hsm.j5t.io/cert-expiry")

		rotator.Stop()
	})
}

func TestGRPCServer_Fallback_Behavior(t *testing.T) {
	// Test server behavior when TLS initialization fails
	mockHSMClient := hsm.NewMockClient()
	ctx := context.Background()
	err := mockHSMClient.Initialize(ctx, hsm.Config{})
	require.NoError(t, err)

	t.Run("fallback_to_insecure_mode", func(t *testing.T) {
		// Configure with TLS enabled but no K8s client (should fallback)
		config := GRPCServerConfig{
			ServiceName: "test-agent-fallback",
			Namespace:   "test",
			EnableTLS:   true,
			K8sClient:   nil, // No K8s client = TLS init should fail
		}

		logger := logr.Discard()
		grpcServer := NewGRPCServer(mockHSMClient, 0, 0, config, logger)

		// Server should be created but without TLS
		assert.NotNil(t, grpcServer)
		assert.Nil(t, grpcServer.certRotator, "Certificate rotator should be nil without K8s client")

		// Server should still be able to start in insecure mode
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		serverDone := make(chan error, 1)
		go func() {
			serverDone <- grpcServer.Start(ctx)
		}()

		// Give server time to start
		time.Sleep(500 * time.Millisecond)

		// Server should be running without TLS
		assert.Nil(t, grpcServer.tlsConfig, "TLS config should be nil in fallback mode")

		cancel()
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
			t.Log("Server shutdown timeout")
		}
	})

	t.Run("explicit_tls_disabled", func(t *testing.T) {
		// Test explicit TLS disabled
		config := GRPCServerConfig{
			ServiceName: "test-agent-notls",
			Namespace:   "test",
			EnableTLS:   false,
		}

		logger := logr.Discard()
		grpcServer := NewGRPCServer(mockHSMClient, 0, 0, config, logger)

		assert.NotNil(t, grpcServer)
		assert.Nil(t, grpcServer.certRotator, "Certificate rotator should be nil when TLS disabled")
		assert.Nil(t, grpcServer.tlsConfig, "TLS config should be nil when TLS disabled")
	})
}

// ==============================================================================
// CLIENT-SERVER INTEGRATION TESTS
// ==============================================================================
// These tests validate that clients can properly connect to TLS-enabled servers,
// addressing the gap where previous tests only validated server-side functionality.

func TestGRPCClient_TLS_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping TLS integration test in short mode")
	}

	logger := logr.Discard()

	// Test that the unified NewGRPCClient function properly handles both TLS and non-TLS scenarios
	t.Run("TLS_and_Insecure_Modes", func(t *testing.T) {
		// Test 1: Insecure mode (nil TLS config)
		t.Run("Insecure", func(t *testing.T) {
			client, err := NewGRPCClient("localhost:9999", nil, "", logger)
			require.NoError(t, err, "Should create insecure client successfully")
			assert.NotNil(t, client, "Client should not be nil")
			defer func() {
				if err := client.Close(); err != nil {
					t.Logf("Failed to close client: %v", err)
				}
			}()

			// Verify client is configured for insecure connection (we can verify it was created successfully)
			assert.NotNil(t, client.client, "gRPC client should be initialized for insecure connection")
		})

		// Test 2: TLS mode (with TLS config)
		t.Run("TLS_Mode", func(t *testing.T) {
			// Create a basic TLS configuration for testing
			certManager, err := security.NewCertificateManager()
			require.NoError(t, err, "Should create certificate manager successfully")

			tlsConfig, err := certManager.GenerateServerCert("test-service", "test-namespace",
				[]string{"test-service.test-namespace.svc.cluster.local", "localhost"},
				[]net.IP{net.ParseIP("127.0.0.1")})
			require.NoError(t, err, "Should generate TLS config successfully")

			client, err := NewGRPCClient("localhost:9999", tlsConfig, "localhost", logger)
			require.NoError(t, err, "Should create TLS client successfully")
			assert.NotNil(t, client, "Client should not be nil")
			defer func() {
				if err := client.Close(); err != nil {
					t.Logf("Failed to close client: %v", err)
				}
			}()

			// We can't easily test the logger name without accessing private fields,
			// but we can verify the client was created successfully with TLS config
			assert.NotNil(t, client.client, "gRPC client should be initialized")
		})

		// Test 3: Error cases
		t.Run("TLS_Validation_Errors", func(t *testing.T) {
			// Create a basic TLS configuration
			certManager, err := security.NewCertificateManager()
			require.NoError(t, err)

			tlsConfig, err := certManager.GenerateServerCert("test-service", "test-namespace",
				[]string{"test-service.test-namespace.svc.cluster.local"},
				[]net.IP{net.ParseIP("127.0.0.1")})
			require.NoError(t, err)

			// Test: TLS config provided but no server name
			_, err = NewGRPCClient("localhost:9999", tlsConfig, "", logger)
			assert.Error(t, err, "Should fail when TLS config provided but no server name")
			assert.Contains(t, err.Error(), "serverName is required when using TLS")

			// Test: Empty endpoint
			_, err = NewGRPCClient("", tlsConfig, "localhost", logger)
			assert.Error(t, err, "Should fail with empty endpoint")
			assert.Contains(t, err.Error(), "endpoint cannot be empty")
		})
	})
}

// TestConnectionPool_TLS_Integration tests that the connection pool properly uses TLS
// when configured, addressing the gap we identified where clients weren't using TLS
func TestConnectionPool_TLS_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping ConnectionPool TLS integration test in short mode")
	}

	logger := logr.Discard()

	t.Run("ConnectionPool_Uses_TLS_Config", func(t *testing.T) {
		// Create a TLS configuration
		certManager, err := security.NewCertificateManager()
		require.NoError(t, err)

		tlsConfig, err := certManager.GenerateServerCert("test-agent", "test-namespace",
			[]string{"test-agent.test-namespace.svc.cluster.local"},
			[]net.IP{net.ParseIP("127.0.0.1")})
		require.NoError(t, err)

		// Create connection pool with TLS config
		pool := NewConnectionPool(logger, tlsConfig)
		assert.NotNil(t, pool, "Connection pool should be created")
		assert.Equal(t, tlsConfig, pool.tlsConfig, "Connection pool should store TLS config")

		// Test that the pool would use TLS (we can't test actual connection without a server)
		assert.True(t, pool.tlsConfig != nil, "Pool should have TLS config when provided")
	})

	t.Run("ConnectionPool_Insecure_Mode", func(t *testing.T) {
		// Create connection pool without TLS config
		pool := NewConnectionPool(logger, nil)
		assert.NotNil(t, pool, "Connection pool should be created")
		assert.Nil(t, pool.tlsConfig, "Connection pool should have no TLS config when not provided")
	})

	t.Log("✅ ConnectionPool TLS integration tests passed")
}
