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
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	ctrl "sigs.k8s.io/controller-runtime"

	hsmv1 "github.com/evanjarrett/hsm-secrets-operator/api/proto/hsm/v1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

func TestGRPCServer_ChangePIN(t *testing.T) {
	ctx := context.Background()
	logger := ctrl.Log.WithName("test")

	tests := []struct {
		name         string
		request      *hsmv1.ChangePINRequest
		setupClient  func() hsm.Client
		wantErr      bool
		expectedCode codes.Code
	}{
		{
			name: "successful PIN change",
			request: &hsmv1.ChangePINRequest{
				OldPin: "123456",
				NewPin: "654321",
			},
			setupClient: func() hsm.Client {
				client := hsm.NewMockClient()
				config := hsm.DefaultConfig()
				config.PINProvider = hsm.NewStaticPINProvider("123456")
				_ = client.Initialize(ctx, config)
				return client
			},
			wantErr: false,
		},
		{
			name: "empty old PIN",
			request: &hsmv1.ChangePINRequest{
				OldPin: "",
				NewPin: "654321",
			},
			setupClient: func() hsm.Client {
				client := hsm.NewMockClient()
				config := hsm.DefaultConfig()
				config.PINProvider = hsm.NewStaticPINProvider("123456")
				_ = client.Initialize(ctx, config)
				return client
			},
			wantErr:      true,
			expectedCode: codes.InvalidArgument,
		},
		{
			name: "empty new PIN",
			request: &hsmv1.ChangePINRequest{
				OldPin: "123456",
				NewPin: "",
			},
			setupClient: func() hsm.Client {
				client := hsm.NewMockClient()
				config := hsm.DefaultConfig()
				config.PINProvider = hsm.NewStaticPINProvider("123456")
				_ = client.Initialize(ctx, config)
				return client
			},
			wantErr:      true,
			expectedCode: codes.InvalidArgument,
		},
		{
			name: "same old and new PIN",
			request: &hsmv1.ChangePINRequest{
				OldPin: "123456",
				NewPin: "123456",
			},
			setupClient: func() hsm.Client {
				client := hsm.NewMockClient()
				config := hsm.DefaultConfig()
				config.PINProvider = hsm.NewStaticPINProvider("123456")
				_ = client.Initialize(ctx, config)
				return client
			},
			wantErr:      true,
			expectedCode: codes.InvalidArgument,
		},
		{
			name: "HSM not connected",
			request: &hsmv1.ChangePINRequest{
				OldPin: "123456",
				NewPin: "654321",
			},
			setupClient: func() hsm.Client {
				// Return disconnected client
				return hsm.NewMockClient()
			},
			wantErr:      true,
			expectedCode: codes.Unavailable,
		},
		{
			name: "incorrect old PIN",
			request: &hsmv1.ChangePINRequest{
				OldPin: "wrong",
				NewPin: "654321",
			},
			setupClient: func() hsm.Client {
				client := hsm.NewMockClient()
				config := hsm.DefaultConfig()
				config.PINProvider = hsm.NewStaticPINProvider("123456")
				_ = client.Initialize(ctx, config)
				return client
			},
			wantErr:      true,
			expectedCode: codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup gRPC server with test client
			hsmClient := tt.setupClient()
			server := &GRPCServer{
				hsmClient: hsmClient,
				logger:    logger,
			}

			// Call ChangePIN
			resp, err := server.ChangePIN(ctx, tt.request)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				} else {
					// Check error code
					if st, ok := status.FromError(err); ok {
						if st.Code() != tt.expectedCode {
							t.Errorf("Expected error code %v, got %v", tt.expectedCode, st.Code())
						}
					} else {
						t.Errorf("Expected gRPC status error, got %v", err)
					}
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if resp == nil {
					t.Error("Expected response but got nil")
				}
			}
		})
	}
}

func TestGRPCServer_ChangePIN_NoClient(t *testing.T) {
	ctx := context.Background()
	logger := ctrl.Log.WithName("test")

	// Create server with no HSM client
	server := &GRPCServer{
		hsmClient: nil,
		logger:    logger,
	}

	request := &hsmv1.ChangePINRequest{
		OldPin: "123456",
		NewPin: "654321",
	}

	resp, err := server.ChangePIN(ctx, request)

	if err == nil {
		t.Error("Expected error for nil HSM client, but got none")
	}

	if resp != nil {
		t.Error("Expected nil response for error case")
	}

	// Check error code
	if st, ok := status.FromError(err); ok {
		if st.Code() != codes.Internal {
			t.Errorf("Expected error code %v, got %v", codes.Internal, st.Code())
		}
	} else {
		t.Errorf("Expected gRPC status error, got %v", err)
	}
}
