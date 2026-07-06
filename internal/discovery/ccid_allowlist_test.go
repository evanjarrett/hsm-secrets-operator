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

package discovery

import (
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
)

func TestLoadCCIDAllowlistFromFile(t *testing.T) {
	allowlist, err := loadCCIDAllowlistFromFile(filepath.Join("testdata", "Info.plist"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := allowlist.Len(); got != 3 {
		t.Fatalf("expected 3 pairs, got %d", got)
	}

	cases := []struct {
		name         string
		vendor, prod string
		want         bool
	}{
		{"pico masquerade (lowercase)", "20a0", "4230", true},
		{"pico new firmware (uppercase, no prefix)", "2E8A", "10FD", true},
		{"pico new firmware (0x prefixed)", "0x2e8a", "0x10fd", true},
		{"smartcard-hsm", "04e6", "5816", true},
		{"unknown pair", "1050", "0407", false},
		{"vendor only, wrong product", "20a0", "9999", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := allowlist.Contains(tc.vendor, tc.prod); got != tc.want {
				t.Errorf("Contains(%q,%q) = %v, want %v", tc.vendor, tc.prod, got, tc.want)
			}
		})
	}
}

func TestLoadCCIDAllowlistFromFile_Missing(t *testing.T) {
	// A nonexistent path is an error at the file layer...
	if _, err := loadCCIDAllowlistFromFile(filepath.Join("testdata", "does-not-exist.plist")); err == nil {
		t.Fatal("expected error for missing file")
	}

	// ...but LoadCCIDAllowlist swallows it into an empty, non-nil allowlist.
	orig := ccidPlistGlobs
	t.Cleanup(func() { ccidPlistGlobs = orig })
	ccidPlistGlobs = []string{filepath.Join("testdata", "no-such-dir", "*", "Info.plist")}

	allowlist := LoadCCIDAllowlist(logr.Discard())
	if allowlist == nil {
		t.Fatal("expected non-nil allowlist")
	}
	if allowlist.Len() != 0 {
		t.Fatalf("expected empty allowlist, got %d pairs", allowlist.Len())
	}
	if allowlist.Contains("20a0", "4230") {
		t.Error("empty allowlist should not contain anything")
	}
}

func TestNormalizeID(t *testing.T) {
	cases := map[string]string{
		"0x20A0": "20a0",
		"0X20a0": "20a0",
		" 20A0 ": "20a0",
		"2e8a":   "2e8a",
		"":       "",
	}
	for in, want := range cases {
		if got := normalizeID(in); got != want {
			t.Errorf("normalizeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadCCIDAllowlistFromGlob(t *testing.T) {
	orig := ccidPlistGlobs
	t.Cleanup(func() { ccidPlistGlobs = orig })
	ccidPlistGlobs = []string{filepath.Join("testdata", "Info.plist")}

	allowlist := LoadCCIDAllowlist(logr.Discard())
	if !allowlist.Contains("2e8a", "10fd") {
		t.Error("expected glob-loaded allowlist to contain 2e8a:10fd")
	}
}
