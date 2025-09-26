//go:build !cgo

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
)

// Stub types for non-CGO builds
type Session struct{}
type ObjectHandle struct{}

// initializePKCS11 returns an error for non-CGO builds
func initializePKCS11(config Config, pin string) (*Session, uint, error) {
	return nil, 0, fmt.Errorf("PKCS#11 support requires CGO (set CGO_ENABLED=1 and rebuild)")
}

// closePKCS11 is a no-op for non-CGO builds
func closePKCS11(session *Session) error {
	return nil
}

// getTokenInfoPKCS11 returns an error for non-CGO builds
func getTokenInfoPKCS11(session *Session, slot uint) (*tokenInfo, error) {
	return nil, fmt.Errorf("PKCS#11 support requires CGO (set CGO_ENABLED=1 and rebuild)")
}

// findObjectsPKCS11 returns an error for non-CGO builds
func findObjectsPKCS11(session *Session, path string) ([]pkcs11Object, error) {
	return nil, fmt.Errorf("PKCS#11 support requires CGO (set CGO_ENABLED=1 and rebuild)")
}

// createObjectPKCS11 returns an error for non-CGO builds
func createObjectPKCS11(session *Session, label string, value []byte) (ObjectHandle, error) {
	return ObjectHandle{}, fmt.Errorf("PKCS#11 support requires CGO (set CGO_ENABLED=1 and rebuild)")
}

// deleteSecretObjectsPKCS11 returns an error for non-CGO builds
func deleteSecretObjectsPKCS11(session *Session, path string) error {
	return fmt.Errorf("PKCS#11 support requires CGO (set CGO_ENABLED=1 and rebuild)")
}

// changePINPKCS11 returns an error for non-CGO builds
func changePINPKCS11(session *Session, oldPIN, newPIN string) error {
	return fmt.Errorf("PKCS#11 support requires CGO (set CGO_ENABLED=1 and rebuild)")
}

// init logs a warning about CGO requirement
func init() {
	fmt.Println("WARNING: PKCS#11 client running in fallback mode (CGO disabled). HSM operations will fail.")
}
