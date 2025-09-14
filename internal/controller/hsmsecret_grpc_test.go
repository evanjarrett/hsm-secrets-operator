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

// NOTE: This test was moved from test/e2e/ to internal/controller/
// It's an integration test that doesn't require Kind/real K8s cluster setup,
// only gRPC communication testing between controller and agent components.

package controller

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hsmv1 "github.com/evanjarrett/hsm-secrets-operator/api/proto/hsm/v1"
	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

func TestHSMSecretControllerGRPCIntegration(t *testing.T) {
	t.Skip("TODO: Fix gRPC integration test - moved from e2e, needs controller setup work")
	// Set up scheme with required types
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	// Create test HSMDevice
	hsmDevice := &hsmv1alpha1.HSMDevice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hsm-device",
			Namespace: "default",
		},
		Spec: hsmv1alpha1.HSMDeviceSpec{
			DeviceType: hsmv1alpha1.HSMDeviceTypePicoHSM,
			Discovery: &hsmv1alpha1.DiscoverySpec{
				USB: &hsmv1alpha1.USBDeviceSpec{
					VendorID:  "20a0",
					ProductID: "4230",
				},
			},
		},
	}

	// Start a mock gRPC agent server
	mockHSMClient := hsm.NewMockClient()
	err := mockHSMClient.Initialize(context.Background(), hsm.Config{})
	require.NoError(t, err)

	// Pre-populate test data
	testData := hsm.SecretData{
		"username": []byte("testuser"),
		"password": []byte("testpass123"),
		"api_key":  []byte("secret-api-key"),
	}
	err = mockHSMClient.WriteSecret(context.Background(), "existing-secret", testData, nil)
	require.NoError(t, err)

	// Start gRPC server
	logger := logr.Discard()
	grpcServer := agent.NewGRPCServer(mockHSMClient, 0, 0, logger)

	// Start server on agent port 9090 for testing
	server := grpc.NewServer()
	hsmv1.RegisterHSMAgentServer(server, grpcServer)

	go func() {
		lis, err := net.Listen("tcp", ":9090")
		if err != nil {
			t.Logf("Failed to listen: %v", err)
			return
		}
		if err := server.Serve(lis); err != nil {
			t.Logf("Server stopped: %v", err)
		}
	}()
	defer server.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Create agent manager and add fake agent info with correct port mapping
	agentManager := agent.NewManager(nil, "default", nil)
	agentManager.SetAgentInfo("test-hsm-device", &agent.AgentInfo{
		PodIPs:    []string{"127.0.0.1"},
		Status:    agent.AgentStatusReady,
		AgentName: "hsm-agent-test-hsm-device",
		Namespace: "default",
	})

	// Create fake client with test objects
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(hsmDevice).
		Build()

	// Create controller
	reconciler := &HSMSecretReconciler{
		Client:       fakeClient,
		Scheme:       scheme,
		AgentManager: agentManager,
	}

	ctx := context.Background()

	t.Run("ImportExistingSecret", func(t *testing.T) {
		// Create HSMSecret that should import existing data
		hsmSecret := &hsmv1alpha1.HSMSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "existing-secret",
				Namespace: "default",
			},
			Spec: hsmv1alpha1.HSMSecretSpec{
				SecretName: "existing-secret",
				AutoSync:   true,
			},
		}

		err := fakeClient.Create(ctx, hsmSecret)
		require.NoError(t, err)

		// Reconcile
		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "existing-secret",
				Namespace: "default",
			},
		}

		_, err = reconciler.Reconcile(ctx, req)
		require.NoError(t, err)

		// Check that Secret was created
		secret := &corev1.Secret{}
		err = fakeClient.Get(ctx, types.NamespacedName{
			Name:      "existing-secret",
			Namespace: "default",
		}, secret)
		require.NoError(t, err)

		// Verify secret data
		assert.Equal(t, []byte("testuser"), secret.Data["username"])
		assert.Equal(t, []byte("testpass123"), secret.Data["password"])
		assert.Equal(t, []byte("secret-api-key"), secret.Data["api_key"])

		// Check HSMSecret status
		err = fakeClient.Get(ctx, types.NamespacedName{
			Name:      "existing-secret",
			Namespace: "default",
		}, hsmSecret)
		require.NoError(t, err)

		assert.Equal(t, "InSync", hsmSecret.Status.SyncStatus)
		assert.NotEmpty(t, hsmSecret.Status.HSMChecksum)
		assert.NotEmpty(t, hsmSecret.Status.SecretChecksum)
		assert.NotNil(t, hsmSecret.Status.LastSyncTime)
	})

	t.Run("CreateNewSecret", func(t *testing.T) {
		// Create Secret first
		newSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"db_host":     []byte("localhost"),
				"db_password": []byte("newpass123"),
				"db_name":     []byte("myapp"),
			},
		}

		err := fakeClient.Create(ctx, newSecret)
		require.NoError(t, err)

		// Create HSMSecret
		hsmSecret := &hsmv1alpha1.HSMSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-secret",
				Namespace: "default",
			},
			Spec: hsmv1alpha1.HSMSecretSpec{
				SecretName: "new-secret",
				AutoSync:   true,
			},
		}

		err = fakeClient.Create(ctx, hsmSecret)
		require.NoError(t, err)

		// Reconcile
		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "new-secret",
				Namespace: "default",
			},
		}

		_, err = reconciler.Reconcile(ctx, req)
		require.NoError(t, err)

		// Verify data was written to HSM via gRPC
		testDevice := hsmv1alpha1.DiscoveredDevice{
			SerialNumber: "test-device",
			DevicePath:   "/dev/test/test-device",
			NodeName:     "test-node",
			Available:    true,
		}
		agentClient, err := agentManager.CreateGRPCClient(ctx, testDevice, logger) // TODO Fix Test
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, agentClient.Close())
		}()

		hsmData, err := agentClient.ReadSecret(ctx, "new-secret")
		require.NoError(t, err)
		assert.Equal(t, []byte("localhost"), hsmData["db_host"])
		assert.Equal(t, []byte("newpass123"), hsmData["db_password"])
		assert.Equal(t, []byte("myapp"), hsmData["db_name"])

		// Check HSMSecret status
		err = fakeClient.Get(ctx, types.NamespacedName{
			Name:      "new-secret",
			Namespace: "default",
		}, hsmSecret)
		require.NoError(t, err)

		assert.Equal(t, "InSync", hsmSecret.Status.SyncStatus)
		assert.NotEmpty(t, hsmSecret.Status.HSMChecksum)
		assert.NotEmpty(t, hsmSecret.Status.SecretChecksum)
	})

	t.Run("AgentNotAvailable", func(t *testing.T) {
		// Remove agent info to simulate unavailable agent
		agentManager.RemoveAgentFromTracking("test-hsm-device")

		// Create HSMSecret
		hsmSecret := &hsmv1alpha1.HSMSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "unavailable-agent",
				Namespace: "default",
			},
			Spec: hsmv1alpha1.HSMSecretSpec{
				SecretName: "unavailable-agent",
				AutoSync:   true,
			},
		}

		err := fakeClient.Create(ctx, hsmSecret)
		require.NoError(t, err)

		// Reconcile should handle the error gracefully
		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "unavailable-agent",
				Namespace: "default",
			},
		}

		_, err = reconciler.Reconcile(ctx, req)
		// Should not return error (will be requeued)
		assert.NoError(t, err)

		// Check HSMSecret status shows error
		err = fakeClient.Get(ctx, types.NamespacedName{
			Name:      "unavailable-agent",
			Namespace: "default",
		}, hsmSecret)
		require.NoError(t, err)

		// Status should indicate error or pending
		assert.Contains(t, []string{"Error", "Pending"}, hsmSecret.Status.SyncStatus)

		// Restore agent info for cleanup
		agentManager.SetAgentInfo("test-hsm-device", &agent.AgentInfo{
			PodIPs:    []string{"127.0.0.1"},
			Status:    agent.AgentStatusReady,
			AgentName: "hsm-agent-test-hsm-device",
			Namespace: "default",
		})
	})

	t.Run("SecretDeletion", func(t *testing.T) {
		// Create and sync a secret first
		testSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "delete-test",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"temp_data": []byte("temporary"),
			},
		}

		err := fakeClient.Create(ctx, testSecret)
		require.NoError(t, err)

		hsmSecret := &hsmv1alpha1.HSMSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "delete-test",
				Namespace: "default",
			},
			Spec: hsmv1alpha1.HSMSecretSpec{
				SecretName: "delete-test",
				AutoSync:   true,
			},
		}

		err = fakeClient.Create(ctx, hsmSecret)
		require.NoError(t, err)

		// Initial reconcile to sync
		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "delete-test",
				Namespace: "default",
			},
		}

		_, err = reconciler.Reconcile(ctx, req)
		require.NoError(t, err)

		// Verify data exists in HSM
		testDevice2 := hsmv1alpha1.DiscoveredDevice{
			SerialNumber: "test-device-2",
			DevicePath:   "/dev/test/test-device-2",
			NodeName:     "test-node",
			Available:    true,
		}
		agentClient, err := agentManager.CreateGRPCClient(ctx, testDevice2, logger) // TODO Fix Test
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, agentClient.Close())
		}()

		_, err = agentClient.ReadSecret(ctx, "delete-test")
		require.NoError(t, err)

		// Delete HSMSecret
		err = fakeClient.Delete(ctx, hsmSecret)
		require.NoError(t, err)

		// Reconcile deletion
		_, err = reconciler.Reconcile(ctx, req)
		// Should not error even if object is not found
		assert.NoError(t, err)

		// Verify data is removed from HSM
		_, err = agentClient.ReadSecret(ctx, "delete-test")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestHSMSecretControllerGRPCErrors(t *testing.T) {
	t.Skip("TODO: Fix gRPC integration test - moved from e2e, needs controller setup work")
	// Set up scheme
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	// Create test HSMDevice
	hsmDevice := &hsmv1alpha1.HSMDevice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hsm-device",
			Namespace: "default",
		},
		Spec: hsmv1alpha1.HSMDeviceSpec{
			DeviceType: hsmv1alpha1.HSMDeviceTypePicoHSM,
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(hsmDevice).
		Build()

	// Create agent manager with invalid endpoint
	agentManager := agent.NewManager(nil, "default", nil)
	agentManager.SetAgentInfo("test-hsm-device", &agent.AgentInfo{
		PodIPs:    []string{"127.0.0.1:99999"}, // Non-existent port
		Status:    agent.AgentStatusReady,
		AgentName: "hsm-agent-test-hsm-device",
		Namespace: "default",
	})

	// Create controller
	reconciler := &HSMSecretReconciler{
		Client:       fakeClient,
		Scheme:       scheme,
		AgentManager: agentManager,
	}

	ctx := context.Background()

	t.Run("ConnectionFailure", func(t *testing.T) {
		// Create HSMSecret
		hsmSecret := &hsmv1alpha1.HSMSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "connection-failure",
				Namespace: "default",
			},
			Spec: hsmv1alpha1.HSMSecretSpec{
				SecretName: "connection-failure",
				AutoSync:   true,
			},
		}

		err := fakeClient.Create(ctx, hsmSecret)
		require.NoError(t, err)

		// Reconcile should handle connection failure gracefully
		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "connection-failure",
				Namespace: "default",
			},
		}

		_, err = reconciler.Reconcile(ctx, req)
		// Should not return error (will be requeued)
		assert.NoError(t, err)

		// Check status indicates error
		err = fakeClient.Get(ctx, types.NamespacedName{
			Name:      "connection-failure",
			Namespace: "default",
		}, hsmSecret)
		require.NoError(t, err)

		assert.Contains(t, []string{"Error", "Pending"}, hsmSecret.Status.SyncStatus)
		assert.NotEmpty(t, hsmSecret.Status.LastError)
	})
}
