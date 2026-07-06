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
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateSOPIN(t *testing.T) {
	pin, err := GenerateSOPIN()
	if err != nil {
		t.Fatalf("GenerateSOPIN returned error: %v", err)
	}

	// SC-HSM SO-PIN is 8 bytes = 16 hex characters.
	if len(pin) != 16 {
		t.Errorf("expected 16-character SO-PIN, got %d (%q)", len(pin), pin)
	}

	// Must be uppercase hex.
	if pin != strings.ToUpper(pin) {
		t.Errorf("SO-PIN is not uppercase: %q", pin)
	}
	if _, err := hex.DecodeString(pin); err != nil {
		t.Errorf("SO-PIN is not valid hex: %q (%v)", pin, err)
	}
}

func TestGenerateSOPIN_Unique(t *testing.T) {
	seen := make(map[string]struct{})
	for range 100 {
		pin, err := GenerateSOPIN()
		if err != nil {
			t.Fatalf("GenerateSOPIN returned error: %v", err)
		}
		if _, dup := seen[pin]; dup {
			t.Fatalf("GenerateSOPIN produced a duplicate value %q within 100 draws", pin)
		}
		seen[pin] = struct{}{}
	}
}

func TestRedactPINs(t *testing.T) {
	out := "sc-hsm-tool: using SO-PIN ABCD1234 and PIN 648219 to initialize"
	got := redactPINs(out, "ABCD1234", "648219")
	if strings.Contains(got, "ABCD1234") || strings.Contains(got, "648219") {
		t.Errorf("redactPINs left a secret in output: %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Errorf("redactPINs did not insert a redaction marker: %q", got)
	}

	// Empty secrets must be ignored (never redact the whole string to ***).
	unchanged := redactPINs("hello world", "")
	if unchanged != "hello world" {
		t.Errorf("redactPINs mangled output with empty secret: %q", unchanged)
	}
}
