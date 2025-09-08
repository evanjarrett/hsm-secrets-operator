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
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
)

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
