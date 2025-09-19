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
	"testing"
	"time"
)

func TestMockClient_ChangePIN(t *testing.T) {
	tests := []struct {
		name    string
		oldPIN  string
		newPIN  string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "successful PIN change",
			oldPIN:  "123456",
			newPIN:  "654321",
			wantErr: false,
		},
		{
			name:    "incorrect old PIN",
			oldPIN:  "wrong",
			newPIN:  "654321",
			wantErr: true,
			errMsg:  "old PIN is incorrect",
		},
		{
			name:    "empty old PIN",
			oldPIN:  "",
			newPIN:  "654321",
			wantErr: true,
			errMsg:  "old PIN cannot be empty",
		},
		{
			name:    "empty new PIN",
			oldPIN:  "123456",
			newPIN:  "",
			wantErr: true,
			errMsg:  "new PIN cannot be empty",
		},
		{
			name:    "same old and new PIN",
			oldPIN:  "123456",
			newPIN:  "123456",
			wantErr: true,
			errMsg:  "new PIN must be different from old PIN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client := NewMockClient()

			// Initialize the client
			config := DefaultConfig()
			config.PINProvider = NewStaticPINProvider("123456")
			err := client.Initialize(ctx, config)
			if err != nil {
				t.Fatalf("Failed to initialize mock client: %v", err)
			}

			// Test ChangePIN
			err = client.ChangePIN(ctx, tt.oldPIN, tt.newPIN)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("Expected error message '%s', got '%s'", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestMockClient_ChangePIN_NotConnected(t *testing.T) {
	ctx := context.Background()
	client := NewMockClient()

	// Don't initialize the client (not connected)
	err := client.ChangePIN(ctx, "123456", "654321")

	if err == nil {
		t.Error("Expected error for disconnected client, but got none")
	}

	expectedErr := "HSM not connected"
	if err.Error() != expectedErr {
		t.Errorf("Expected error '%s', got '%s'", expectedErr, err.Error())
	}
}

func TestKubernetesPINProvider_InvalidateCache(t *testing.T) {
	// Create a mock PIN provider
	provider := &KubernetesPINProvider{
		cachedPIN:   "test-pin",
		cacheExpiry: testTimeNow().Add(10 * testMinute),
		cacheTTL:    10 * testMinute,
	}

	// Verify cache is populated
	if provider.cachedPIN == "" {
		t.Error("Expected cached PIN to be populated")
	}

	// Invalidate cache
	provider.InvalidateCache()

	// Verify cache is cleared
	if provider.cachedPIN != "" {
		t.Error("Expected cached PIN to be cleared after invalidation")
	}

	if !provider.cacheExpiry.IsZero() {
		t.Error("Expected cache expiry to be zero after invalidation")
	}
}

func TestKubernetesPINProvider_InvalidateCacheAfterPINChange(t *testing.T) {
	// Create a mock PIN provider
	provider := &KubernetesPINProvider{
		cachedPIN:   "old-pin",
		cacheExpiry: testTimeNow().Add(10 * testMinute),
		cacheTTL:    10 * testMinute,
	}

	// Verify cache is populated
	if provider.cachedPIN == "" {
		t.Error("Expected cached PIN to be populated")
	}

	// Call InvalidateCacheAfterPINChange
	provider.InvalidateCacheAfterPINChange()

	// Verify cache is cleared
	if provider.cachedPIN != "" {
		t.Error("Expected cached PIN to be cleared after PIN change")
	}

	if !provider.cacheExpiry.IsZero() {
		t.Error("Expected cache expiry to be zero after PIN change")
	}
}

// Test helpers
var (
	testTimeNow = time.Now
	testMinute  = time.Minute
)
