//go:build !cgo
// +build !cgo

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

package hsm

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

// PKCS11Client stub implementation when CGO is disabled
type PKCS11Client struct {
	logger logr.Logger
}

// NewPKCS11Client creates a new PKCS#11 HSM client stub
func NewPKCS11Client() *PKCS11Client {
	return &PKCS11Client{
		logger: ctrl.Log.WithName("hsm-pkcs11-client-nocgo"),
	}
}

// Initialize returns an error indicating PKCS#11 is not available
func (c *PKCS11Client) Initialize(ctx context.Context, config Config) error {
	return fmt.Errorf("PKCS#11 support not available: binary was built without CGO")
}

// Close is a no-op for the stub implementation
func (c *PKCS11Client) Close() error {
	return nil
}

// GetInfo returns an error indicating PKCS#11 is not available
func (c *PKCS11Client) GetInfo(ctx context.Context) (*HSMInfo, error) {
	return nil, fmt.Errorf("PKCS#11 support not available: binary was built without CGO")
}

// ReadSecret returns an error indicating PKCS#11 is not available
func (c *PKCS11Client) ReadSecret(ctx context.Context, path string) (SecretData, error) {
	return nil, fmt.Errorf("PKCS#11 support not available: binary was built without CGO")
}

// WriteSecret returns an error indicating PKCS#11 is not available
func (c *PKCS11Client) WriteSecret(ctx context.Context, path string, data SecretData) error {
	return fmt.Errorf("PKCS#11 support not available: binary was built without CGO")
}

// DeleteSecret returns an error indicating PKCS#11 is not available
func (c *PKCS11Client) DeleteSecret(ctx context.Context, path string) error {
	return fmt.Errorf("PKCS#11 support not available: binary was built without CGO")
}

// ListSecrets returns an error indicating PKCS#11 is not available
func (c *PKCS11Client) ListSecrets(ctx context.Context, prefix string) ([]string, error) {
	return nil, fmt.Errorf("PKCS#11 support not available: binary was built without CGO")
}

// GetChecksum returns an error indicating PKCS#11 is not available
func (c *PKCS11Client) GetChecksum(ctx context.Context, path string) (string, error) {
	return "", fmt.Errorf("PKCS#11 support not available: binary was built without CGO")
}

// IsConnected always returns false for the stub implementation
func (c *PKCS11Client) IsConnected() bool {
	return false
}

// WithRetry returns an error indicating PKCS#11 is not available
func (c *PKCS11Client) WithRetry(ctx context.Context, operation func() error) error {
	return fmt.Errorf("PKCS#11 support not available: binary was built without CGO")
}
