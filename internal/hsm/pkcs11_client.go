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
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/miekg/pkcs11"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	defaultKeyName = "data"
)

// PKCS11Client implements the Client interface using PKCS#11
type PKCS11Client struct {
	config Config
	logger logr.Logger
	mutex  sync.RWMutex

	// PKCS#11 objects
	ctx       *pkcs11.Ctx
	session   pkcs11.SessionHandle
	slot      uint
	connected bool

	// Data object cache for faster lookups
	dataObjects map[string]pkcs11.ObjectHandle
}

// NewPKCS11Client creates a new PKCS#11 HSM client
func NewPKCS11Client() *PKCS11Client {
	return &PKCS11Client{
		logger:      ctrl.Log.WithName("hsm-pkcs11-client"),
		dataObjects: make(map[string]pkcs11.ObjectHandle),
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

	// Validate configuration
	if config.PKCS11LibraryPath == "" {
		return fmt.Errorf("PKCS11LibraryPath is required")
	}

	if config.PIN == "" {
		return fmt.Errorf("PIN is required for HSM authentication")
	}

	// Initialize PKCS#11 context
	c.ctx = pkcs11.New(config.PKCS11LibraryPath)
	if c.ctx == nil {
		return fmt.Errorf("failed to create PKCS#11 context for library: %s", config.PKCS11LibraryPath)
	}

	// Initialize the library
	if err := c.ctx.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize PKCS#11 library: %w", err)
	}

	// Find the slot
	slots, err := c.ctx.GetSlotList(true) // true = only slots with tokens
	if err != nil {
		if finErr := c.ctx.Finalize(); finErr != nil {
			c.logger.V(1).Info("Failed to finalize PKCS#11 context", "error", finErr)
		}
		c.ctx.Destroy()
		return fmt.Errorf("failed to get slot list: %w", err)
	}

	if len(slots) == 0 {
		if finErr := c.ctx.Finalize(); finErr != nil {
			c.logger.V(1).Info("Failed to finalize PKCS#11 context", "error", finErr)
		}
		c.ctx.Destroy()
		return fmt.Errorf("no slots with tokens found")
	}

	// Use specified slot ID or find by token label
	var targetSlot uint
	found := false

	if config.UseSlotID {
		// Use specified slot ID
		for _, slot := range slots {
			if slot == config.SlotID {
				targetSlot = slot
				found = true
				break
			}
		}
		if !found {
			if finErr := c.ctx.Finalize(); finErr != nil {
				c.logger.V(1).Info("Failed to finalize PKCS#11 context", "error", finErr)
			}
			c.ctx.Destroy()
			return fmt.Errorf("specified slot ID %d not found", config.SlotID)
		}
	} else if config.TokenLabel != "" {
		// Find slot by token label
		for _, slot := range slots {
			tokenInfo, err := c.ctx.GetTokenInfo(slot)
			if err != nil {
				c.logger.V(1).Info("Failed to get token info for slot", "slot", slot, "error", err)
				continue
			}
			if strings.TrimSpace(tokenInfo.Label) == config.TokenLabel {
				targetSlot = slot
				found = true
				break
			}
		}
		if !found {
			if finErr := c.ctx.Finalize(); finErr != nil {
				c.logger.V(1).Info("Failed to finalize PKCS#11 context", "error", finErr)
			}
			c.ctx.Destroy()
			return fmt.Errorf("token with label '%s' not found", config.TokenLabel)
		}
	} else {
		// Use first available slot
		targetSlot = slots[0]
	}

	c.slot = targetSlot
	c.logger.Info("Using HSM slot", "slot", targetSlot)

	// Open session
	session, err := c.ctx.OpenSession(targetSlot, pkcs11.CKF_SERIAL_SESSION|pkcs11.CKF_RW_SESSION)
	if err != nil {
		if finErr := c.ctx.Finalize(); finErr != nil {
			c.logger.V(1).Info("Failed to finalize PKCS#11 context", "error", finErr)
		}
		c.ctx.Destroy()
		return fmt.Errorf("failed to open session: %w", err)
	}
	c.session = session

	// Login with PIN
	if err := c.ctx.Login(session, pkcs11.CKU_USER, config.PIN); err != nil {
		if closeErr := c.ctx.CloseSession(session); closeErr != nil {
			c.logger.V(1).Info("Failed to close session", "error", closeErr)
		}
		if finErr := c.ctx.Finalize(); finErr != nil {
			c.logger.V(1).Info("Failed to finalize PKCS#11 context", "error", finErr)
		}
		c.ctx.Destroy()
		return fmt.Errorf("failed to login with PIN: %w", err)
	}

	c.connected = true
	c.logger.Info("HSM connection established successfully", "slot", targetSlot)
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

	// Logout and close session
	if c.ctx != nil && c.session != 0 {
		if logoutErr := c.ctx.Logout(c.session); logoutErr != nil {
			c.logger.V(1).Info("Failed to logout from HSM session", "error", logoutErr)
		}
		if closeErr := c.ctx.CloseSession(c.session); closeErr != nil {
			c.logger.V(1).Info("Failed to close HSM session", "error", closeErr)
		}
	}

	// Finalize and destroy context
	if c.ctx != nil {
		if finErr := c.ctx.Finalize(); finErr != nil {
			c.logger.V(1).Info("Failed to finalize PKCS#11 context", "error", finErr)
		}
		c.ctx.Destroy()
	}

	c.connected = false
	c.session = 0
	c.ctx = nil
	c.dataObjects = make(map[string]pkcs11.ObjectHandle)

	return nil
}

// GetInfo returns information about the HSM device
func (c *PKCS11Client) GetInfo(ctx context.Context) (*HSMInfo, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if !c.connected {
		return nil, fmt.Errorf("HSM not connected")
	}

	// Get token information from PKCS#11
	tokenInfo, err := c.ctx.GetTokenInfo(c.slot)
	if err != nil {
		return nil, fmt.Errorf("failed to get token info: %w", err)
	}

	// Get slot information
	slotInfo, slotErr := c.ctx.GetSlotInfo(c.slot)
	if slotErr != nil {
		c.logger.V(1).Info("Failed to get slot info", "error", slotErr)
	}

	info := &HSMInfo{
		Label:           strings.TrimSpace(tokenInfo.Label),
		Manufacturer:    strings.TrimSpace(tokenInfo.ManufacturerID),
		Model:           strings.TrimSpace(tokenInfo.Model),
		SerialNumber:    strings.TrimSpace(tokenInfo.SerialNumber),
		FirmwareVersion: fmt.Sprintf("%d.%d", tokenInfo.FirmwareVersion.Major, tokenInfo.FirmwareVersion.Minor),
	}

	// Add slot info if available
	if slotErr == nil {
		if info.Manufacturer == "" {
			info.Manufacturer = strings.TrimSpace(slotInfo.ManufacturerID)
		}
		if info.Model == "" {
			info.Model = strings.TrimSpace(slotInfo.SlotDescription)
		}
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

	// Find data objects with the path as label prefix
	template := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_DATA),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, path+"*"), // Use wildcard for prefix search
	}

	if err := c.ctx.FindObjectsInit(c.session, template); err != nil {
		return nil, fmt.Errorf("failed to initialize object search: %w", err)
	}
	defer func() {
		if finalErr := c.ctx.FindObjectsFinal(c.session); finalErr != nil {
			c.logger.V(1).Info("Failed to finalize object search", "error", finalErr)
		}
	}()

	// Get all matching objects
	objs, _, err := c.ctx.FindObjects(c.session, 100) // Max 100 objects
	if err != nil {
		return nil, fmt.Errorf("failed to find objects: %w", err)
	}

	if len(objs) == 0 {
		return nil, fmt.Errorf("secret not found at path: %s", path)
	}

	data := make(SecretData)

	// Read each data object
	for _, obj := range objs {
		// Get the label to determine the key name
		labelAttr, err := c.ctx.GetAttributeValue(c.session, obj, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_LABEL, nil),
		})
		if err != nil {
			c.logger.V(1).Info("Failed to get object label", "error", err)
			continue
		}

		if len(labelAttr) == 0 || len(labelAttr[0].Value) == 0 {
			c.logger.V(1).Info("Object has no label, skipping")
			continue
		}

		label := string(labelAttr[0].Value)
		// Extract key name from label (remove path prefix)
		key := strings.TrimPrefix(label, path)
		key = strings.TrimPrefix(key, "/")
		if key == "" {
			key = defaultKeyName // Default key name
		}

		// Get the actual data value
		valueAttr, err := c.ctx.GetAttributeValue(c.session, obj, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_VALUE, nil),
		})
		if err != nil {
			c.logger.V(1).Info("Failed to get object value", "key", key, "error", err)
			continue
		}

		if len(valueAttr) > 0 && len(valueAttr[0].Value) > 0 {
			data[key] = valueAttr[0].Value
		}
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("no valid secret data found at path: %s", path)
	}

	c.logger.V(1).Info("Successfully read secret from HSM",
		"path", path, "keys", len(data))

	return data, nil
}

// WriteSecret writes secret data to the specified HSM path
func (c *PKCS11Client) WriteSecret(ctx context.Context, path string, data SecretData) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.connected {
		return fmt.Errorf("HSM not connected")
	}

	c.logger.V(1).Info("Writing secret to HSM",
		"path", path, "keys", len(data))

	// First, delete any existing objects for this path to avoid duplicates
	if err := c.deleteSecretObjects(path); err != nil {
		c.logger.V(1).Info("Failed to delete existing objects (may not exist)", "error", err)
	}

	// Create data objects for each key-value pair
	for key, value := range data {
		label := path
		if key != defaultKeyName {
			label = path + "/" + key
		}

		template := []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_DATA),
			pkcs11.NewAttribute(pkcs11.CKA_LABEL, label),
			pkcs11.NewAttribute(pkcs11.CKA_VALUE, value),
			pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),      // Store persistently
			pkcs11.NewAttribute(pkcs11.CKA_PRIVATE, true),    // Require authentication
			pkcs11.NewAttribute(pkcs11.CKA_MODIFIABLE, true), // Allow updates
		}

		obj, err := c.ctx.CreateObject(c.session, template)
		if err != nil {
			return fmt.Errorf("failed to create data object for key '%s': %w", key, err)
		}

		// Cache the object handle for faster future lookups
		c.dataObjects[label] = obj

		c.logger.V(2).Info("Created data object", "path", path, "key", key, "label", label)
	}

	c.logger.Info("Successfully wrote secret to HSM", "path", path)
	return nil
}

// deleteSecretObjects removes all data objects matching the given path prefix
func (c *PKCS11Client) deleteSecretObjects(path string) error {
	// Find data objects with the path as label prefix
	template := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_DATA),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, path+"*"), // Use wildcard for prefix search
	}

	if err := c.ctx.FindObjectsInit(c.session, template); err != nil {
		return fmt.Errorf("failed to initialize object search: %w", err)
	}
	defer func() {
		if finalErr := c.ctx.FindObjectsFinal(c.session); finalErr != nil {
			c.logger.V(1).Info("Failed to finalize object search", "error", finalErr)
		}
	}()

	// Get all matching objects
	objs, _, err := c.ctx.FindObjects(c.session, 100) // Max 100 objects
	if err != nil {
		return fmt.Errorf("failed to find objects: %w", err)
	}

	// Delete each object
	for _, obj := range objs {
		if err := c.ctx.DestroyObject(c.session, obj); err != nil {
			c.logger.V(1).Info("Failed to delete object", "object", obj, "error", err)
			continue
		}

		// Remove from cache
		for label, cachedObj := range c.dataObjects {
			if cachedObj == obj {
				delete(c.dataObjects, label)
				break
			}
		}
	}

	return nil
}

// DeleteSecret removes secret data from the specified HSM path
func (c *PKCS11Client) DeleteSecret(ctx context.Context, path string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.connected {
		return fmt.Errorf("HSM not connected")
	}

	c.logger.Info("Deleting secret from HSM", "path", path)

	if err := c.deleteSecretObjects(path); err != nil {
		return fmt.Errorf("failed to delete secret objects: %w", err)
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

	// Find data objects with the prefix as label prefix
	template := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_DATA),
	}

	// Add prefix filter if specified
	if prefix != "" {
		template = append(template, pkcs11.NewAttribute(pkcs11.CKA_LABEL, prefix+"*"))
	}

	if err := c.ctx.FindObjectsInit(c.session, template); err != nil {
		return nil, fmt.Errorf("failed to initialize object search: %w", err)
	}
	defer func() {
		if finalErr := c.ctx.FindObjectsFinal(c.session); finalErr != nil {
			c.logger.V(1).Info("Failed to finalize object search", "error", finalErr)
		}
	}()

	// Get all matching objects
	objs, _, err := c.ctx.FindObjects(c.session, 1000) // Max 1000 objects
	if err != nil {
		return nil, fmt.Errorf("failed to find objects: %w", err)
	}

	// Extract unique paths from object labels
	pathsMap := make(map[string]bool)
	for _, obj := range objs {
		// Get the label
		labelAttr, err := c.ctx.GetAttributeValue(c.session, obj, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_LABEL, nil),
		})
		if err != nil {
			c.logger.V(1).Info("Failed to get object label", "error", err)
			continue
		}

		if len(labelAttr) == 0 || len(labelAttr[0].Value) == 0 {
			continue
		}

		label := string(labelAttr[0].Value)

		// Extract the base path (remove key suffix)
		path := label
		if strings.Contains(label, "/") {
			parts := strings.Split(label, "/")
			if len(parts) > 1 {
				// Check if the last part looks like a key name
				lastPart := parts[len(parts)-1]
				// If it's a common key name, use the parent path
				if lastPart == defaultKeyName || lastPart == "username" || lastPart == "password" ||
					lastPart == "api-key" || lastPart == "api-secret" || lastPart == "token" {
					path = strings.Join(parts[:len(parts)-1], "/")
				}
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
