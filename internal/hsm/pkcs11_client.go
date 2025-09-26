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
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	defaultKeyName    = "data"
	metadataKeySuffix = "/_metadata"
	applicationName   = "hsm-secrets-operator"
)

// PKCS11Client implements the Client interface using PKCS#11
type PKCS11Client struct {
	config Config
	logger logr.Logger
	mutex  sync.RWMutex

	// Internal state
	session   *Session // Will be concrete type in CGO, stub in non-CGO
	slot      uint
	connected bool

	// Data object cache for faster lookups (CGO only)
	dataObjects map[string]ObjectHandle
}

// pkcs11Object represents a PKCS#11 data object
type pkcs11Object struct {
	Label  string
	Value  []byte
	Handle ObjectHandle // Will be concrete type in CGO, stub in non-CGO
}

// tokenInfo represents HSM token information
type tokenInfo struct {
	Label           string
	ManufacturerID  string
	Model           string
	SerialNumber    string
	FirmwareVersion string
}

// NewPKCS11Client creates a new PKCS#11 HSM client
func NewPKCS11Client() *PKCS11Client {
	return &PKCS11Client{
		logger:      ctrl.Log.WithName("hsm-pkcs11-client"),
		dataObjects: make(map[string]ObjectHandle),
	}
}

// Initialize establishes connection to the HSM via PKCS#11
func (c *PKCS11Client) Initialize(ctx context.Context, config Config) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.config = config
	c.logger.Info("Initializing HSM connection",
		"library", config.PKCS11LibraryPath,
		"slot", config.SlotID,
		"tokenLabel", config.TokenLabel)

	// Common validation
	if config.PKCS11LibraryPath == "" {
		return fmt.Errorf("PKCS11LibraryPath is required")
	}

	if config.PINProvider == nil {
		return fmt.Errorf("PINProvider is required for HSM authentication")
	}

	// Get PIN from provider
	pin, err := config.PINProvider.GetPIN(ctx)
	if err != nil {
		return fmt.Errorf("failed to get PIN from provider: %w", err)
	}

	// Call platform-specific initialization
	session, slot, err := initializePKCS11(config, pin)
	if err != nil {
		return err
	}

	c.session = session
	c.slot = slot
	c.connected = true
	c.logger.Info("HSM connection established successfully", "slot", slot)
	return nil
}

// Close terminates the HSM connection
func (c *PKCS11Client) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.connected {
		return nil
	}

	c.logger.Info("Closing HSM connection")

	// Call platform-specific cleanup
	if err := closePKCS11(c.session); err != nil {
		c.logger.V(1).Info("Error during PKCS#11 cleanup", "error", err)
	}

	c.connected = false
	c.session = nil
	c.dataObjects = make(map[string]ObjectHandle)

	return nil
}

// GetInfo returns information about the HSM device
func (c *PKCS11Client) GetInfo(ctx context.Context) (*HSMInfo, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if !c.connected {
		return nil, fmt.Errorf("HSM not connected")
	}

	// Get token information via helper
	tokenInfo, err := getTokenInfoPKCS11(c.session, c.slot)
	if err != nil {
		return nil, fmt.Errorf("failed to get token info: %w", err)
	}

	info := &HSMInfo{
		Label:           strings.TrimSpace(tokenInfo.Label),
		Manufacturer:    strings.TrimSpace(tokenInfo.ManufacturerID),
		Model:           strings.TrimSpace(tokenInfo.Model),
		SerialNumber:    strings.TrimSpace(tokenInfo.SerialNumber),
		FirmwareVersion: tokenInfo.FirmwareVersion,
	}

	return info, nil
}

// ReadSecret reads secret data from the specified HSM path
func (c *PKCS11Client) ReadSecret(ctx context.Context, path string) (SecretData, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if !c.connected {
		return nil, fmt.Errorf("HSM not connected")
	}

	c.logger.V(1).Info("Reading secret from HSM", "path", path)

	// Find objects matching the path
	objects, err := findObjectsPKCS11(c.session, path)
	if err != nil {
		return nil, fmt.Errorf("failed to find objects: %w", err)
	}

	data := make(SecretData)
	matchingObjects := 0

	// Process each object
	for _, obj := range objects {
		// Check if this object matches our path
		if !strings.HasPrefix(obj.Label, path) {
			continue
		}

		// Skip metadata objects when reading secrets
		if strings.HasSuffix(obj.Label, metadataKeySuffix) {
			continue
		}

		matchingObjects++

		// Extract key name from label (remove path prefix)
		key := strings.TrimPrefix(obj.Label, path)
		key = strings.TrimPrefix(key, "/")
		if key == "" {
			key = defaultKeyName // Default key name
		}

		data[key] = obj.Value
	}

	if matchingObjects == 0 {
		return nil, fmt.Errorf("secret not found at path: %s", path)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("no valid secret data found at path: %s (found %d objects but no data)", path, matchingObjects)
	}

	c.logger.V(1).Info("Successfully read secret from HSM",
		"path", path, "keys", len(data))

	return data, nil
}

// WriteSecret writes secret data and metadata to the specified HSM path
func (c *PKCS11Client) WriteSecret(ctx context.Context, path string, data SecretData, metadata *SecretMetadata) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.connected {
		return fmt.Errorf("HSM not connected")
	}

	c.logger.V(1).Info("Writing secret to HSM",
		"path", path, "keys", len(data))

	// First, delete any existing objects for this path to avoid duplicates
	if err := deleteSecretObjectsPKCS11(c.session, path); err != nil {
		c.logger.V(1).Info("Failed to delete existing objects (may not exist)", "error", err)
	}

	// Create data objects for each key-value pair
	for key, value := range data {
		label := path
		if key != defaultKeyName {
			label = path + "/" + key
		}

		// Create the object via helper
		handle, err := createObjectPKCS11(c.session, label, value)
		if err != nil {
			return fmt.Errorf("failed to create data object for key '%s': %w", key, err)
		}

		// Cache the object handle for faster future lookups
		c.dataObjects[label] = handle

		c.logger.V(2).Info("Created data object", "path", path, "key", key, "label", label)
	}

	c.logger.Info("Successfully wrote secret to HSM", "path", path)

	if metadata != nil {
		return c.writeMetadata(path, metadata)
	}

	return nil
}

// writeMetadata creates a metadata object for the secret
func (c *PKCS11Client) writeMetadata(path string, metadata *SecretMetadata) error {
	// Serialize metadata to JSON
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to serialize metadata: %w", err)
	}

	// Create metadata object label
	metadataLabel := path + metadataKeySuffix

	// Create the metadata object via helper
	handle, err := createObjectPKCS11(c.session, metadataLabel, metadataJSON)
	if err != nil {
		return fmt.Errorf("failed to create metadata object: %w", err)
	}

	// Cache the metadata object handle
	c.dataObjects[metadataLabel] = handle

	c.logger.V(2).Info("Created metadata object", "path", path, "label", metadataLabel)
	return nil
}

// ReadMetadata reads metadata for a secret at the given path
func (c *PKCS11Client) ReadMetadata(ctx context.Context, path string) (*SecretMetadata, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if !c.connected {
		return nil, fmt.Errorf("HSM not connected")
	}

	metadataLabel := path + metadataKeySuffix

	// Find the metadata object
	objects, err := findObjectsPKCS11(c.session, metadataLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to find metadata object: %w", err)
	}

	// Look for exact match
	for _, obj := range objects {
		if obj.Label == metadataLabel {
			// Parse the JSON metadata
			var metadata SecretMetadata
			if err := json.Unmarshal(obj.Value, &metadata); err != nil {
				return nil, fmt.Errorf("failed to parse metadata JSON: %w", err)
			}
			return &metadata, nil
		}
	}

	return nil, fmt.Errorf("metadata not found for path: %s", path)
}

// DeleteSecret removes secret data from the specified HSM path
func (c *PKCS11Client) DeleteSecret(ctx context.Context, path string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.connected {
		return fmt.Errorf("HSM not connected")
	}

	c.logger.Info("Deleting secret from HSM", "path", path)

	if err := deleteSecretObjectsPKCS11(c.session, path); err != nil {
		return fmt.Errorf("failed to delete secret objects: %w", err)
	}

	// Remove from cache
	for label := range c.dataObjects {
		if strings.HasPrefix(label, path) {
			delete(c.dataObjects, label)
		}
	}

	c.logger.Info("Successfully deleted secret from HSM", "path", path)
	return nil
}

// ListSecrets returns a list of secret paths with the given prefix
func (c *PKCS11Client) ListSecrets(ctx context.Context, prefix string) ([]string, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if !c.connected {
		return nil, fmt.Errorf("HSM not connected")
	}

	c.logger.V(1).Info("Listing secrets from HSM", "prefix", prefix)

	// Find all data objects
	objects, err := findObjectsPKCS11(c.session, "")
	if err != nil {
		return nil, fmt.Errorf("failed to find objects: %w", err)
	}

	// Extract unique paths from object labels
	pathsMap := make(map[string]bool)
	for _, obj := range objects {
		label := obj.Label

		// Skip metadata objects when listing secrets
		if strings.HasSuffix(label, metadataKeySuffix) {
			continue
		}

		// Extract the base path (remove key suffix)
		path := label
		if strings.Contains(label, "/") {
			parts := strings.Split(label, "/")
			if len(parts) > 1 {
				// For data objects, the last part is usually the key name
				// So we extract the parent path as the secret path
				path = strings.Join(parts[:len(parts)-1], "/")
			}
		}

		// Only include paths that match the prefix
		if prefix == "" || strings.HasPrefix(path, prefix) {
			pathsMap[path] = true
		}
	}

	// Convert map to slice
	paths := make([]string, 0, len(pathsMap))
	for path := range pathsMap {
		paths = append(paths, path)
	}

	c.logger.V(1).Info("Successfully listed secrets from HSM",
		"prefix", prefix, "count", len(paths))

	return paths, nil
}

// GetChecksum returns the SHA256 checksum of the secret data at the given path
func (c *PKCS11Client) GetChecksum(ctx context.Context, path string) (string, error) {
	data, err := c.ReadSecret(ctx, path)
	if err != nil {
		return "", fmt.Errorf("failed to read secret for checksum: %w", err)
	}

	checksum := CalculateChecksum(data)
	c.logger.V(2).Info("Calculated checksum for secret",
		"path", path, "checksum", checksum)

	return checksum, nil
}

// IsConnected returns true if the HSM is connected and responsive
func (c *PKCS11Client) IsConnected() bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.connected
}

// ChangePIN changes the HSM PIN from old PIN to new PIN
func (c *PKCS11Client) ChangePIN(ctx context.Context, oldPIN, newPIN string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.connected {
		return fmt.Errorf("HSM not connected")
	}

	c.logger.Info("Changing HSM PIN")

	// Validate PIN parameters
	if oldPIN == "" {
		return fmt.Errorf("old PIN cannot be empty")
	}
	if newPIN == "" {
		return fmt.Errorf("new PIN cannot be empty")
	}
	if oldPIN == newPIN {
		return fmt.Errorf("new PIN must be different from old PIN")
	}

	// Call platform-specific PIN change
	if err := changePINPKCS11(c.session, oldPIN, newPIN); err != nil {
		c.logger.Error(err, "Failed to change HSM PIN")
		return fmt.Errorf("failed to change HSM PIN: %w", err)
	}

	// Invalidate PIN cache after successful PIN change
	if c.config.PINProvider != nil {
		if kubePINProvider, ok := c.config.PINProvider.(*KubernetesPINProvider); ok {
			kubePINProvider.InvalidateCacheAfterPINChange()
			c.logger.V(1).Info("Invalidated PIN cache after successful PIN change")
		}
	}

	c.logger.Info("Successfully changed HSM PIN")
	return nil
}

// WithRetry wraps an operation with retry logic
func (c *PKCS11Client) WithRetry(ctx context.Context, operation func() error) error {
	var lastErr error

	for attempt := 0; attempt <= c.config.RetryAttempts; attempt++ {
		if attempt > 0 {
			c.logger.V(1).Info("Retrying HSM operation",
				"attempt", attempt, "maxAttempts", c.config.RetryAttempts)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.config.RetryDelay):
			}
		}

		if err := operation(); err != nil {
			lastErr = err
			c.logger.V(1).Info("HSM operation failed",
				"attempt", attempt, "error", err)
			continue
		}

		return nil
	}

	return fmt.Errorf("operation failed after %d attempts: %w",
		c.config.RetryAttempts, lastErr)
}
