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

	"github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/mock"
)

// MockAgentManager provides a mock implementation of the agent manager interface
type MockAgentManager struct {
	mock.Mock
}

func (m *MockAgentManager) GetAvailableDevices(ctx context.Context, namespace string) ([]string, error) {
	args := m.Called(ctx, namespace)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockAgentManager) CreateGRPCClient(ctx context.Context, device v1alpha1.DiscoveredDevice, logger logr.Logger) (hsm.Client, error) {
	args := m.Called(ctx, device, logger)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(hsm.Client), args.Error(1)
}

// MockHSMClient provides a mock implementation of the HSM client interface
type MockHSMClient struct {
	mock.Mock
}

func (m *MockHSMClient) IsConnected() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockHSMClient) GetInfo(ctx context.Context) (*hsm.HSMInfo, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*hsm.HSMInfo), args.Error(1)
}

func (m *MockHSMClient) ListSecrets(ctx context.Context, prefix string) ([]string, error) {
	args := m.Called(ctx, prefix)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockHSMClient) ReadSecret(ctx context.Context, path string) (hsm.SecretData, error) {
	args := m.Called(ctx, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(hsm.SecretData), args.Error(1)
}

func (m *MockHSMClient) WriteSecret(ctx context.Context, path string, data hsm.SecretData, metadata *hsm.SecretMetadata) error {
	args := m.Called(ctx, path, data, metadata)
	return args.Error(0)
}

func (m *MockHSMClient) DeleteSecret(ctx context.Context, path string) error {
	args := m.Called(ctx, path)
	return args.Error(0)
}

func (m *MockHSMClient) ReadMetadata(ctx context.Context, path string) (*hsm.SecretMetadata, error) {
	args := m.Called(ctx, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*hsm.SecretMetadata), args.Error(1)
}

func (m *MockHSMClient) GetChecksum(ctx context.Context, path string) (string, error) {
	args := m.Called(ctx, path)
	return args.String(0), args.Error(1)
}

func (m *MockHSMClient) Initialize(ctx context.Context, config hsm.Config) error {
	args := m.Called(ctx, config)
	return args.Error(0)
}

func (m *MockHSMClient) Close() error {
	args := m.Called()
	return args.Error(0)
}
