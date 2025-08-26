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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
)

func stringPtr(s string) *string {
	return &s
}

func TestShouldHandleSecret(t *testing.T) {
	reconciler := &HSMSecretReconciler{
		OperatorNamespace: "hsm-operator-system",
		OperatorName:      "hsm-secrets-operator-controller-manager",
	}

	tests := []struct {
		name     string
		secret   *hsmv1alpha1.HSMSecret
		expected bool
	}{
		{
			name: "no parentRef - should not handle (explicit association required)",
			secret: &hsmv1alpha1.HSMSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
				Spec: hsmv1alpha1.HSMSecretSpec{
					// No parentRef
				},
			},
			expected: false,
		},
		{
			name: "matching parentRef - should handle",
			secret: &hsmv1alpha1.HSMSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "production",
				},
				Spec: hsmv1alpha1.HSMSecretSpec{
					ParentRef: &hsmv1alpha1.ParentReference{
						Name:      "hsm-secrets-operator-controller-manager",
						Namespace: stringPtr("hsm-operator-system"),
					},
				},
			},
			expected: true,
		},
		{
			name: "different parent name - should not handle",
			secret: &hsmv1alpha1.HSMSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "production",
				},
				Spec: hsmv1alpha1.HSMSecretSpec{
					ParentRef: &hsmv1alpha1.ParentReference{
						Name:      "other-operator",
						Namespace: stringPtr("hsm-operator-system"),
					},
				},
			},
			expected: false,
		},
		{
			name: "different parent namespace - should not handle",
			secret: &hsmv1alpha1.HSMSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "production",
				},
				Spec: hsmv1alpha1.HSMSecretSpec{
					ParentRef: &hsmv1alpha1.ParentReference{
						Name:      "hsm-secrets-operator-controller-manager",
						Namespace: stringPtr("other-operator-system"),
					},
				},
			},
			expected: false,
		},
		{
			name: "parentRef without namespace (should use operator namespace) - should handle",
			secret: &hsmv1alpha1.HSMSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "production",
				},
				Spec: hsmv1alpha1.HSMSecretSpec{
					ParentRef: &hsmv1alpha1.ParentReference{
						Name: "hsm-secrets-operator-controller-manager",
						// Namespace is nil, should default to operator namespace
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.shouldHandleSecret(tt.secret)
			if result != tt.expected {
				t.Errorf("shouldHandleSecret() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
