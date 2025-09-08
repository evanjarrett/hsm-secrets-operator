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

package controller

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
)

var _ = Describe("HSMSecret Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		hsmsecret := &hsmv1alpha1.HSMSecret{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind HSMSecret")
			err := k8sClient.Get(ctx, typeNamespacedName, hsmsecret)
			if err != nil && errors.IsNotFound(err) {
				resource := &hsmv1alpha1.HSMSecret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					// TODO(user): Specify other spec details if needed.
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &hsmv1alpha1.HSMSecret{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance HSMSecret")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &HSMSecretReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})

func testStringPtr(s string) *string {
	return &s
}

// Unit tests for the refactored HSMSecretReconciler using standard Go testing
func TestHSMSecretReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))

	tests := []struct {
		name             string
		hsmSecret        *hsmv1alpha1.HSMSecret
		agentManager     *agent.Manager
		expectRequeue    bool
		expectError      bool
		expectedErrorMsg string
	}{
		{
			name:      "HSMSecret not found - should not error",
			hsmSecret: nil, // No HSMSecret in fake client
			agentManager: func() *agent.Manager {
				fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
				return agent.NewManager(fakeClient, "test-namespace", nil)
			}(),
			expectRequeue: false,
			expectError:   false,
		},
		{
			name: "No available devices - should requeue after 2 minutes",
			hsmSecret: &hsmv1alpha1.HSMSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-secret",
					Namespace:  "default",
					Finalizers: []string{"hsmsecret.hsm.j5t.io/finalizer"},
				},
				Spec: hsmv1alpha1.HSMSecretSpec{
					AutoSync: true,
					ParentRef: &hsmv1alpha1.ParentReference{
						Name:      "test-operator",
						Namespace: testStringPtr("test-namespace"),
					},
				},
			},
			agentManager: func() *agent.Manager {
				fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
				return agent.NewManager(fakeClient, "test-namespace", nil)
			}(),
			expectRequeue: true,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create fake client with HSMSecret if provided
			var objs []runtime.Object
			if tt.hsmSecret != nil {
				objs = append(objs, tt.hsmSecret)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objs...).
				Build()

			reconciler := &HSMSecretReconciler{
				Client:            fakeClient,
				Scheme:            scheme,
				AgentManager:      tt.agentManager,
				OperatorNamespace: "test-namespace",
				OperatorName:      "test-operator",
			}

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-secret",
					Namespace: "default",
				},
			}

			result, err := reconciler.Reconcile(ctx, req)

			if tt.expectError {
				assert.Error(t, err)
				if tt.expectedErrorMsg != "" {
					assert.Contains(t, err.Error(), tt.expectedErrorMsg)
				}
			} else {
				assert.NoError(t, err)
			}

			if tt.expectRequeue {
				assert.True(t, result.RequeueAfter > 0, "Expected RequeueAfter but got %+v", result)
			} else if !tt.expectError {
				assert.Equal(t, int64(0), result.RequeueAfter.Nanoseconds(), "Did not expect RequeueAfter but got %+v", result)
			}
		})
	}
}

func TestHSMSecretReconciler_ReconcileWithAgentManager(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))

	t.Run("reconciler uses agent manager for device discovery", func(t *testing.T) {
		ctx := context.Background()

		hsmSecret := &hsmv1alpha1.HSMSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-secret",
				Namespace:  "default",
				Finalizers: []string{"hsmsecret.hsm.j5t.io/finalizer"},
			},
			Spec: hsmv1alpha1.HSMSecretSpec{
				AutoSync: true,
				ParentRef: &hsmv1alpha1.ParentReference{
					Name:      "test-operator",
					Namespace: testStringPtr("test-namespace"),
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(hsmSecret).
			Build()

		agentManager := agent.NewManager(fakeClient, "test-namespace", nil)

		reconciler := &HSMSecretReconciler{
			Client:            fakeClient,
			Scheme:            scheme,
			AgentManager:      agentManager,
			OperatorNamespace: "test-namespace",
			OperatorName:      "test-operator",
		}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-secret",
				Namespace: "default",
			},
		}

		result, err := reconciler.Reconcile(ctx, req)

		// Should not error, but should requeue since no devices available
		assert.NoError(t, err)
		assert.True(t, result.RequeueAfter > 0, "Expected RequeueAfter due to no available devices")
	})
}
