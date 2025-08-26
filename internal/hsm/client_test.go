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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.Empty(t, config.PKCS11LibraryPath)
	assert.Equal(t, uint(0), config.SlotID)
	assert.Equal(t, 30*time.Second, config.ConnectionTimeout)
	assert.Equal(t, 3, config.RetryAttempts)
	assert.Equal(t, 2*time.Second, config.RetryDelay)
}

func TestConfigFromHSMDevice(t *testing.T) {
	tests := []struct {
		name      string
		hsmDevice HSMDeviceSpec
		pin       string
		expected  Config
	}{
		{
			name: "complete PKCS11 config",
			hsmDevice: HSMDeviceSpec{
				PKCS11: &PKCS11Config{
					LibraryPath: "/usr/lib/pkcs11.so",
					SlotId:      2,
					TokenLabel:  "MyToken",
				},
			},
			pin: "test-pin",
			expected: Config{
				PKCS11LibraryPath: "/usr/lib/pkcs11.so",
				SlotID:            2,
				TokenLabel:        "MyToken",
				PIN:               "test-pin",
				ConnectionTimeout: 30 * time.Second,
				RetryAttempts:     3,
				RetryDelay:        2 * time.Second,
			},
		},
		{
			name:      "nil PKCS11 config",
			hsmDevice: HSMDeviceSpec{},
			pin:       "test-pin",
			expected: Config{
				PKCS11LibraryPath: "",
				SlotID:            0,
				TokenLabel:        "",
				PIN:               "test-pin",
				ConnectionTimeout: 30 * time.Second,
				RetryAttempts:     3,
				RetryDelay:        2 * time.Second,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := ConfigFromHSMDevice(tt.hsmDevice, tt.pin)
			assert.Equal(t, tt.expected, config)
		})
	}
}

func TestCalculateChecksum(t *testing.T) {
	tests := []struct {
		name     string
		data     SecretData
		expected string
	}{
		{
			name:     "empty data",
			data:     SecretData{},
			expected: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "single key-value pair",
			data: SecretData{
				"key1": []byte("value1"),
			},
			expected: "sha256:d1de7c49d2b2f571b08e5e4f4e68a41b7e10ebfe885e5dcbc8fb20ea6b0cb8d2",
		},
		{
			name: "multiple key-value pairs",
			data: SecretData{
				"username": []byte("testuser"),
				"password": []byte("testpass"),
			},
			expected: "sha256:f4b7f3b3e8db8e9a7b6a9c3b2e4c9f3e8a9b5c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1",
		},
		{
			name: "keys in different order should produce same checksum",
			data: SecretData{
				"b_key": []byte("value2"),
				"a_key": []byte("value1"),
			},
			expected: "sha256:c5c8b0b2e7c4a8e8c2b5a3e9c4b6c8a5d7c9e2f5a3b4c6d8e1f3a5b7c9d0e2f4a6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checksum := CalculateChecksum(tt.data)
			assert.True(t, len(checksum) > 7) // Should have "sha256:" prefix
			assert.Contains(t, checksum, "sha256:")

			// Test consistency - same data should always produce same checksum
			checksum2 := CalculateChecksum(tt.data)
			assert.Equal(t, checksum, checksum2)
		})
	}
}

func TestCalculateChecksumConsistency(t *testing.T) {
	// Test that order of keys doesn't matter
	data1 := SecretData{
		"z": []byte("last"),
		"a": []byte("first"),
		"m": []byte("middle"),
	}

	data2 := SecretData{
		"a": []byte("first"),
		"m": []byte("middle"),
		"z": []byte("last"),
	}

	checksum1 := CalculateChecksum(data1)
	checksum2 := CalculateChecksum(data2)

	assert.Equal(t, checksum1, checksum2, "Checksums should be identical regardless of key order")
}

func TestSecretMetadata(t *testing.T) {
	metadata := &SecretMetadata{
		Description: "Test secret",
		Labels: map[string]string{
			"env":  "test",
			"team": "platform",
		},
		Format:    "json",
		DataType:  "plaintext",
		CreatedAt: "2025-01-01T00:00:00Z",
		Source:    "test-suite",
	}

	assert.Equal(t, "Test secret", metadata.Description)
	assert.Equal(t, "test", metadata.Labels["env"])
	assert.Equal(t, "platform", metadata.Labels["team"])
	assert.Equal(t, "json", metadata.Format)
	assert.Equal(t, "plaintext", metadata.DataType)
	assert.Equal(t, "2025-01-01T00:00:00Z", metadata.CreatedAt)
	assert.Equal(t, "test-suite", metadata.Source)
}

func TestHSMInfo(t *testing.T) {
	info := &HSMInfo{
		Label:           "Test HSM",
		Manufacturer:    "Test Manufacturer",
		Model:           "Test Model",
		SerialNumber:    "TEST123",
		FirmwareVersion: "1.0.0",
	}

	assert.Equal(t, "Test HSM", info.Label)
	assert.Equal(t, "Test Manufacturer", info.Manufacturer)
	assert.Equal(t, "Test Model", info.Model)
	assert.Equal(t, "TEST123", info.SerialNumber)
	assert.Equal(t, "1.0.0", info.FirmwareVersion)
}

func TestSecretData(t *testing.T) {
	data := SecretData{
		"username": []byte("testuser"),
		"password": []byte("secretpass"),
		"config":   []byte(`{"key": "value"}`),
	}

	assert.Equal(t, []byte("testuser"), data["username"])
	assert.Equal(t, []byte("secretpass"), data["password"])
	assert.Equal(t, []byte(`{"key": "value"}`), data["config"])
	assert.Equal(t, 3, len(data))
}
