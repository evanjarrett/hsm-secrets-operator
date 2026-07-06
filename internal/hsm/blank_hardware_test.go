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
	"context"
	"os"
	"testing"
)

// TestIsTokenBlank_Hardware exercises blank-token detection against a real reader.
// It is skipped unless HSM_BLANK_TEST_LIB points at a PKCS#11 library. Optionally set
// HSM_BLANK_TEST_EXPECT=true|false to assert the expected result. This test is read-only
// (no login, no init) and safe to run against any attached token.
func TestIsTokenBlank_Hardware(t *testing.T) {
	lib := os.Getenv("HSM_BLANK_TEST_LIB")
	if lib == "" {
		t.Skip("set HSM_BLANK_TEST_LIB to a PKCS#11 library path to run the hardware blank-detection test")
	}

	blank, err := IsTokenBlank(context.Background(), Config{PKCS11LibraryPath: lib})
	if err != nil {
		t.Fatalf("IsTokenBlank error: %v", err)
	}
	t.Logf("IsTokenBlank(%s) => %v", lib, blank)

	if want := os.Getenv("HSM_BLANK_TEST_EXPECT"); want != "" {
		wantBlank := want == "true"
		if blank != wantBlank {
			t.Errorf("IsTokenBlank = %v, want %v", blank, wantBlank)
		}
	}
}
