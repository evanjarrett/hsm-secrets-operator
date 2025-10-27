//go:build cgo

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
	"fmt"
	"strings"

	"github.com/miekg/pkcs11"
)

// CGO-specific types
type Session struct {
	ctx     *pkcs11.Ctx
	session pkcs11.SessionHandle
	slot    uint
}

type ObjectHandle = pkcs11.ObjectHandle

// initializePKCS11 establishes connection to the HSM via PKCS#11
func initializePKCS11(config Config, pin string) (*Session, uint, error) {
	// Initialize PKCS#11 context
	ctx := pkcs11.New(config.PKCS11LibraryPath)
	if ctx == nil {
		return nil, 0, fmt.Errorf("failed to create PKCS#11 context for library: %s", config.PKCS11LibraryPath)
	}

	// Initialize the library
	if err := ctx.Initialize(); err != nil {
		return nil, 0, fmt.Errorf("failed to initialize PKCS#11 library: %w", err)
	}

	// Find the slot
	slots, err := ctx.GetSlotList(true) // true = only slots with tokens
	if err != nil {
		if finErr := ctx.Finalize(); finErr != nil {
			// Ignore finalize error, return original error
			_ = finErr
		}
		ctx.Destroy()
		return nil, 0, fmt.Errorf("failed to get slot list: %w", err)
	}

	if len(slots) == 0 {
		if finErr := ctx.Finalize(); finErr != nil {
			// Ignore finalize error, return original error
			_ = finErr
		}
		ctx.Destroy()
		return nil, 0, fmt.Errorf("no slots with tokens found")
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
			if finErr := ctx.Finalize(); finErr != nil {
				// Ignore finalize error, return original error
				_ = finErr
			}
			ctx.Destroy()
			return nil, 0, fmt.Errorf("specified slot ID %d not found", config.SlotID)
		}
	} else if config.TokenLabel != "" {
		// Find slot by token label
		for _, slot := range slots {
			tokenInfo, err := ctx.GetTokenInfo(slot)
			if err != nil {
				continue
			}
			if strings.TrimSpace(tokenInfo.Label) == config.TokenLabel {
				targetSlot = slot
				found = true
				break
			}
		}
		if !found {
			if finErr := ctx.Finalize(); finErr != nil {
				// Ignore finalize error, return original error
				_ = finErr
			}
			ctx.Destroy()
			return nil, 0, fmt.Errorf("token with label '%s' not found", config.TokenLabel)
		}
	} else {
		// Use first available slot
		targetSlot = slots[0]
	}

	// Open session
	session, err := ctx.OpenSession(targetSlot, pkcs11.CKF_SERIAL_SESSION|pkcs11.CKF_RW_SESSION)
	if err != nil {
		if finErr := ctx.Finalize(); finErr != nil {
			// Ignore finalize error, return original error
			_ = finErr
		}
		ctx.Destroy()
		return nil, 0, fmt.Errorf("failed to open session: %w", err)
	}

	// Login with PIN
	if err := ctx.Login(session, pkcs11.CKU_USER, pin); err != nil {
		if closeErr := ctx.CloseSession(session); closeErr != nil {
			// Ignore close error, return original error
			_ = closeErr
		}
		if finErr := ctx.Finalize(); finErr != nil {
			// Ignore finalize error, return original error
			_ = finErr
		}
		ctx.Destroy()
		return nil, 0, fmt.Errorf("failed to login with PIN: %w", err)
	}

	return &Session{
		ctx:     ctx,
		session: session,
		slot:    targetSlot,
	}, targetSlot, nil
}

// closePKCS11 terminates the HSM connection
func closePKCS11(session *Session) error {
	if session == nil {
		return nil
	}

	// Logout and close session
	if session.ctx != nil && session.session != 0 {
		if logoutErr := session.ctx.Logout(session.session); logoutErr != nil {
			// Ignore logout error but continue
			_ = logoutErr
		}
		if closeErr := session.ctx.CloseSession(session.session); closeErr != nil {
			// Ignore close error but continue
			_ = closeErr
		}
	}

	// Finalize and destroy context
	if session.ctx != nil {
		if finErr := session.ctx.Finalize(); finErr != nil {
			// Ignore finalize error but continue
			_ = finErr
		}
		session.ctx.Destroy()
	}

	return nil
}

// getTokenInfoPKCS11 returns information about the HSM token
func getTokenInfoPKCS11(session *Session, slot uint) (*tokenInfo, error) {
	if session == nil {
		return nil, fmt.Errorf("session is nil")
	}

	// Get token information from PKCS#11
	pkcs11TokenInfo, err := session.ctx.GetTokenInfo(session.slot)
	if err != nil {
		return nil, fmt.Errorf("failed to get token info: %w", err)
	}

	// Get slot information
	slotInfo, slotErr := session.ctx.GetSlotInfo(session.slot)

	info := &tokenInfo{
		Label:           strings.TrimSpace(pkcs11TokenInfo.Label),
		ManufacturerID:  strings.TrimSpace(pkcs11TokenInfo.ManufacturerID),
		Model:           strings.TrimSpace(pkcs11TokenInfo.Model),
		SerialNumber:    strings.TrimSpace(pkcs11TokenInfo.SerialNumber),
		FirmwareVersion: fmt.Sprintf("%d.%d", pkcs11TokenInfo.FirmwareVersion.Major, pkcs11TokenInfo.FirmwareVersion.Minor),
	}

	// Add slot info if available
	if slotErr == nil {
		if info.ManufacturerID == "" {
			info.ManufacturerID = strings.TrimSpace(slotInfo.ManufacturerID)
		}
		if info.Model == "" {
			info.Model = strings.TrimSpace(slotInfo.SlotDescription)
		}
	}

	return info, nil
}

// findObjectsPKCS11 finds PKCS#11 data objects matching the given path
func findObjectsPKCS11(session *Session, path string) ([]pkcs11Object, error) {
	if session == nil {
		return nil, fmt.Errorf("session is nil")
	}

	// Find all data objects (we'll filter by label after)
	template := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_DATA),
	}

	if err := session.ctx.FindObjectsInit(session.session, template); err != nil {
		return nil, fmt.Errorf("failed to initialize object search: %w", err)
	}

	// Get all matching objects
	objs, _, err := session.ctx.FindObjects(session.session, 100) // Max 100 objects (reduced from 1000 to be less aggressive)
	if err != nil {
		// Always finalize before returning error
		_ = session.ctx.FindObjectsFinal(session.session)
		return nil, fmt.Errorf("failed to find objects: %w", err)
	}

	// CRITICAL: Must call FindObjectsFinal to release search operation
	// If this fails, the session remains in search mode and CreateObject will fail
	if err := session.ctx.FindObjectsFinal(session.session); err != nil {
		return nil, fmt.Errorf("failed to finalize object search (session may be in invalid state): %w", err)
	}

	// Pre-allocate slice for better performance
	objects := make([]pkcs11Object, 0, len(objs))

	// Read each data object
	for _, obj := range objs {
		// Get the label and value
		attrs, err := session.ctx.GetAttributeValue(session.session, obj, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_LABEL, nil),
			pkcs11.NewAttribute(pkcs11.CKA_VALUE, nil),
		})
		if err != nil {
			continue // Skip objects we can't read
		}

		if len(attrs) < 2 || len(attrs[0].Value) == 0 {
			continue // Skip objects without proper attributes
		}

		label := string(attrs[0].Value)
		value := attrs[1].Value

		// If path is specified, filter by it
		if path != "" && !strings.HasPrefix(label, path) {
			continue
		}

		objects = append(objects, pkcs11Object{
			Label:  label,
			Value:  value,
			Handle: obj,
		})
	}

	return objects, nil
}

// createObjectPKCS11 creates a new PKCS#11 data object
func createObjectPKCS11(session *Session, label string, value []byte) (ObjectHandle, error) {
	if session == nil {
		return 0, fmt.Errorf("session is nil")
	}

	// Infer data type from content
	dataType := InferDataType(value)

	// Get OID for data type
	oid, err := GetOIDForDataType(dataType)
	if err != nil {
		oid = OIDPlaintext // Default fallback
	}

	// Encode OID as DER
	derOID, err := EncodeDER(oid)
	if err != nil {
		derOID = nil // Will skip CKA_OBJECT_ID if encoding fails
	}

	// Build template with proper attributes
	template := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_DATA),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, label),
		pkcs11.NewAttribute(pkcs11.CKA_APPLICATION, applicationName), // Proper application name
		pkcs11.NewAttribute(pkcs11.CKA_VALUE, value),
		pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),      // Store persistently
		pkcs11.NewAttribute(pkcs11.CKA_PRIVATE, true),    // Require authentication
		pkcs11.NewAttribute(pkcs11.CKA_MODIFIABLE, true), // Allow updates
	}

	// Add OID if we successfully encoded it
	if derOID != nil {
		template = append(template, pkcs11.NewAttribute(pkcs11.CKA_OBJECT_ID, derOID))
	}

	obj, err := session.ctx.CreateObject(session.session, template)
	if err != nil {
		return 0, fmt.Errorf("failed to create data object: %w", err)
	}

	return obj, nil
}

// deleteSecretObjectsPKCS11 removes all data objects matching the given path prefix
func deleteSecretObjectsPKCS11(session *Session, path string) error {
	if session == nil {
		return fmt.Errorf("session is nil")
	}

	// Find all data objects (we'll filter by label after)
	template := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_DATA),
	}

	if err := session.ctx.FindObjectsInit(session.session, template); err != nil {
		return fmt.Errorf("failed to initialize object search: %w", err)
	}

	// Get all matching objects
	objs, _, err := session.ctx.FindObjects(session.session, 100) // Max 100 objects
	if err != nil {
		// Always finalize before returning error
		_ = session.ctx.FindObjectsFinal(session.session)
		return fmt.Errorf("failed to find objects: %w", err)
	}

	// Delete each object that matches our path
	deletedCount := 0
	for _, obj := range objs {
		// Get the label to check if this object matches our path
		labelAttr, err := session.ctx.GetAttributeValue(session.session, obj, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_LABEL, nil),
		})
		if err != nil {
			continue
		}

		if len(labelAttr) == 0 || len(labelAttr[0].Value) == 0 {
			continue
		}

		label := string(labelAttr[0].Value)
		// Only delete objects that match our path
		if !strings.HasPrefix(label, path) {
			continue
		}

		if err := session.ctx.DestroyObject(session.session, obj); err != nil {
			// Log error but continue with other objects
			continue
		}
		deletedCount++
	}

	// CRITICAL: Must call FindObjectsFinal to release search operation
	// If this fails, the session remains in search mode and CreateObject will fail
	if err := session.ctx.FindObjectsFinal(session.session); err != nil {
		return fmt.Errorf("failed to finalize object search after deleting %d objects (session may be in invalid state): %w", deletedCount, err)
	}

	return nil
}

// changePINPKCS11 changes the HSM PIN
func changePINPKCS11(session *Session, oldPIN, newPIN string) error {
	if session == nil {
		return fmt.Errorf("session is nil")
	}

	// Use PKCS#11 SetPIN function to change the PIN
	// Note: This changes the user PIN (not SO PIN)
	if err := session.ctx.SetPIN(session.session, oldPIN, newPIN); err != nil {
		return fmt.Errorf("failed to change HSM PIN: %w", err)
	}

	return nil
}
