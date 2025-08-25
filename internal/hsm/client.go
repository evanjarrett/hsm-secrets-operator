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

// SecretMetadata contains metadata about an HSM secret
type SecretMetadata struct {
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Format      string            `json:"format,omitempty"`
	DataType    string            `json:"dataType,omitempty"`
	CreatedAt   string            `json:"createdAt,omitempty"`
	Source      string            `json:"source,omitempty"`
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

	// WriteSecretWithMetadata writes secret data and metadata to the specified HSM path
	WriteSecretWithMetadata(ctx context.Context, path string, data SecretData, metadata *SecretMetadata) error

	// ReadMetadata reads metadata for a secret at the given path
	ReadMetadata(ctx context.Context, path string) (*SecretMetadata, error)

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

	// UseSlotID indicates whether SlotID should be used (vs auto-discovery)
	UseSlotID bool

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
// NOTE: PKCS11LibraryPath must be set from HSMDevice.Spec.PKCS11.LibraryPath
func DefaultConfig() Config {
	return Config{
		PKCS11LibraryPath: "", // Must be configured per-device
		SlotID:            0,
		ConnectionTimeout: 30 * time.Second,
		RetryAttempts:     3,
		RetryDelay:        2 * time.Second,
	}
}

// ConfigFromHSMDevice creates a Config from HSMDevice spec
func ConfigFromHSMDevice(hsmDevice HSMDeviceSpec, pin string) Config {
	config := DefaultConfig()

	if hsmDevice.PKCS11 != nil {
		config.PKCS11LibraryPath = hsmDevice.PKCS11.LibraryPath
		config.SlotID = uint(hsmDevice.PKCS11.SlotId)
		config.TokenLabel = hsmDevice.PKCS11.TokenLabel
	}

	config.PIN = pin
	return config
}

// HSMDeviceSpec represents the HSMDevice spec for config creation
// This avoids importing the full v1alpha1 package in the hsm package
type HSMDeviceSpec struct {
	PKCS11 *PKCS11Config `json:"pkcs11,omitempty"`
}

type PKCS11Config struct {
	LibraryPath string `json:"libraryPath,omitempty"`
	SlotId      int32  `json:"slotId,omitempty"`
	TokenLabel  string `json:"tokenLabel,omitempty"`
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
