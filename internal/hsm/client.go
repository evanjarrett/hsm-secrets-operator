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
	"crypto/sha256"
	"fmt"
	"time"
)

// SecretData represents secret key-value pairs
type SecretData map[string][]byte

// HSMInfo contains information about the HSM device
type HSMInfo struct {
	Label           string
	Manufacturer    string
	Model           string
	SerialNumber    string
	FirmwareVersion string
}

// Client defines the interface for HSM operations
type Client interface {
	// Initialize establishes connection to the HSM
	Initialize(ctx context.Context, config Config) error

	// Close terminates the HSM connection
	Close() error

	// GetInfo returns information about the HSM device
	GetInfo(ctx context.Context) (*HSMInfo, error)

	// ReadSecret reads secret data from the specified HSM path
	ReadSecret(ctx context.Context, path string) (SecretData, error)

	// WriteSecret writes secret data to the specified HSM path
	WriteSecret(ctx context.Context, path string, data SecretData) error

	// DeleteSecret removes secret data from the specified HSM path
	DeleteSecret(ctx context.Context, path string) error

	// ListSecrets returns a list of secret paths
	ListSecrets(ctx context.Context, prefix string) ([]string, error)

	// GetChecksum returns the SHA256 checksum of the secret data at the given path
	GetChecksum(ctx context.Context, path string) (string, error)

	// IsConnected returns true if the HSM is connected and responsive
	IsConnected() bool
}

// Config holds HSM client configuration
type Config struct {
	// PKCS11LibraryPath is the path to the PKCS#11 library
	PKCS11LibraryPath string

	// SlotID is the HSM slot identifier
	SlotID uint

	// PIN is the user PIN for authentication
	PIN string

	// TokenLabel is the token label to use
	TokenLabel string

	// ConnectionTimeout for HSM operations
	ConnectionTimeout time.Duration

	// RetryAttempts for failed operations
	RetryAttempts int

	// RetryDelay between retry attempts
	RetryDelay time.Duration
}

// DefaultConfig returns a default HSM configuration
func DefaultConfig() Config {
	return Config{
		PKCS11LibraryPath: "/usr/lib/opensc-pkcs11.so",
		SlotID:            0,
		ConnectionTimeout: 30 * time.Second,
		RetryAttempts:     3,
		RetryDelay:        2 * time.Second,
	}
}

// CalculateChecksum calculates SHA256 checksum of secret data
func CalculateChecksum(data SecretData) string {
	h := sha256.New()

	// Sort keys for consistent checksum
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}

	// Simple sort (for production, use sort.Strings)
	for i := 0; i < len(keys)-1; i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	for _, key := range keys {
		h.Write([]byte(key))
		h.Write(data[key])
	}

	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}
