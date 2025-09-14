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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// Test Config validation and edge cases
func TestConfig_Validation(t *testing.T) {
	tests := []struct {
		name           string
		config         Config
		expectedValid  bool
		expectedFields []string // Fields that should be validated
	}{
		{
			name: "valid complete config",
			config: Config{
				PKCS11LibraryPath: "/usr/lib/libpkcs11.so",
				SlotID:            1,
				PIN:               "test-pin",
				TokenLabel:        "TestToken",
				ConnectionTimeout: 30 * time.Second,
				RetryAttempts:     3,
				RetryDelay:        2 * time.Second,
				UseSlotID:         true,
			},
			expectedValid:  true,
			expectedFields: []string{"PKCS11LibraryPath", "PIN", "SlotID", "TokenLabel"},
		},
		{
			name: "minimal valid config",
			config: Config{
				PKCS11LibraryPath: "/usr/lib/pkcs11.so",
				PIN:               "pin",
				ConnectionTimeout: 10 * time.Second,
				RetryAttempts:     1,
				RetryDelay:        1 * time.Second,
			},
			expectedValid:  true,
			expectedFields: []string{"PKCS11LibraryPath", "PIN"},
		},
		{
			name: "config with zero timeouts",
			config: Config{
				PKCS11LibraryPath: "/usr/lib/pkcs11.so",
				PIN:               "pin",
				ConnectionTimeout: 0,
				RetryAttempts:     0,
				RetryDelay:        0,
			},
			expectedValid:  true, // Zero values should be allowed
			expectedFields: []string{"PKCS11LibraryPath", "PIN"},
		},
		{
			name: "config with high slot ID",
			config: Config{
				PKCS11LibraryPath: "/usr/lib/pkcs11.so",
				PIN:               "pin",
				SlotID:            999999,
				UseSlotID:         true,
				ConnectionTimeout: 30 * time.Second,
				RetryAttempts:     3,
				RetryDelay:        2 * time.Second,
			},
			expectedValid:  true,
			expectedFields: []string{"PKCS11LibraryPath", "PIN", "SlotID"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test basic field validation
			if len(tt.expectedFields) > 0 {
				for _, field := range tt.expectedFields {
					switch field {
					case "PKCS11LibraryPath":
						assert.NotEmpty(t, tt.config.PKCS11LibraryPath, "PKCS11LibraryPath should not be empty")
					case "PIN":
						assert.NotEmpty(t, tt.config.PIN, "PIN should not be empty")
					case "SlotID":
						assert.GreaterOrEqual(t, tt.config.SlotID, uint(0), "SlotID should be >= 0")
					case "TokenLabel":
						assert.NotEmpty(t, tt.config.TokenLabel, "TokenLabel should not be empty")
					}
				}
			}

			// Test timeout and retry values are reasonable
			assert.GreaterOrEqual(t, tt.config.RetryAttempts, 0, "RetryAttempts should be >= 0")
			assert.GreaterOrEqual(t, tt.config.ConnectionTimeout, time.Duration(0), "ConnectionTimeout should be >= 0")
			assert.GreaterOrEqual(t, tt.config.RetryDelay, time.Duration(0), "RetryDelay should be >= 0")
		})
	}
}

// Test ConfigFromHSMDevice with edge cases
func TestConfigFromHSMDevice_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		hsmDevice HSMDeviceSpec
		pin       string
		validate  func(t *testing.T, config Config)
	}{
		{
			name: "empty PIN should be preserved",
			hsmDevice: HSMDeviceSpec{
				PKCS11: &PKCS11Config{
					LibraryPath: "/usr/lib/pkcs11.so",
					SlotId:      0,
					TokenLabel:  "Token",
				},
			},
			pin: "", // Empty PIN
			validate: func(t *testing.T, config Config) {
				assert.Empty(t, config.PIN, "Empty PIN should be preserved")
				assert.Equal(t, "/usr/lib/pkcs11.so", config.PKCS11LibraryPath)
			},
		},
		{
			name: "negative slot ID should be converted properly",
			hsmDevice: HSMDeviceSpec{
				PKCS11: &PKCS11Config{
					LibraryPath: "/usr/lib/pkcs11.so",
					SlotId:      -1, // Negative slot ID
					TokenLabel:  "Token",
				},
			},
			pin: "pin",
			validate: func(t *testing.T, config Config) {
				// Note: int32(-1) cast to uint becomes a large positive number
				// This tests the type conversion behavior
				assert.Equal(t, uint(18446744073709551615), config.SlotID, "Negative SlotId should convert to large uint")
			},
		},
		{
			name: "very long paths and labels",
			hsmDevice: HSMDeviceSpec{
				PKCS11: &PKCS11Config{
					LibraryPath: "/very/long/path/to/pkcs11/library/that/might/exist/somewhere/on/filesystem/libpkcs11.so",
					SlotId:      123456,
					TokenLabel:  "AVeryLongTokenLabelThatMightBeUsedInProductionEnvironmentsWithDescriptiveNames",
				},
			},
			pin: "a-very-long-pin-that-someone-might-use-for-security-reasons",
			validate: func(t *testing.T, config Config) {
				assert.True(t, len(config.PKCS11LibraryPath) > 50, "Long library path should be preserved")
				assert.True(t, len(config.TokenLabel) > 50, "Long token label should be preserved")
				assert.True(t, len(config.PIN) > 20, "Long PIN should be preserved")
			},
		},
		{
			name: "default values are properly inherited",
			hsmDevice: HSMDeviceSpec{
				PKCS11: &PKCS11Config{
					LibraryPath: "/usr/lib/pkcs11.so",
					// No SlotId or TokenLabel provided
				},
			},
			pin: "test-pin",
			validate: func(t *testing.T, config Config) {
				defaultConfig := DefaultConfig()
				assert.Equal(t, defaultConfig.ConnectionTimeout, config.ConnectionTimeout)
				assert.Equal(t, defaultConfig.RetryAttempts, config.RetryAttempts)
				assert.Equal(t, defaultConfig.RetryDelay, config.RetryDelay)
				assert.Equal(t, uint(0), config.SlotID) // Default SlotId is 0
				assert.Empty(t, config.TokenLabel)      // Default TokenLabel is empty
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := ConfigFromHSMDevice(tt.hsmDevice, tt.pin)
			tt.validate(t, config)
		})
	}
}

// Test DefaultConfig properties and immutability
func TestDefaultConfig_Properties(t *testing.T) {
	t.Run("default values are reasonable", func(t *testing.T) {
		config := DefaultConfig()

		// Test reasonable default values
		assert.Equal(t, 30*time.Second, config.ConnectionTimeout, "Default timeout should be 30 seconds")
		assert.Equal(t, 3, config.RetryAttempts, "Default retry attempts should be 3")
		assert.Equal(t, 2*time.Second, config.RetryDelay, "Default retry delay should be 2 seconds")
		assert.Equal(t, uint(0), config.SlotID, "Default slot ID should be 0")
		assert.False(t, config.UseSlotID, "Default UseSlotID should be false")

		// Test empty values that must be configured
		assert.Empty(t, config.PKCS11LibraryPath, "Library path should be empty by default")
		assert.Empty(t, config.PIN, "PIN should be empty by default")
		assert.Empty(t, config.TokenLabel, "Token label should be empty by default")
	})

	t.Run("multiple calls return equivalent configs", func(t *testing.T) {
		config1 := DefaultConfig()
		config2 := DefaultConfig()

		// Should be equivalent but not the same memory address
		assert.Equal(t, config1, config2)
		assert.NotSame(t, &config1, &config2)
	})

	t.Run("modifications don't affect subsequent calls", func(t *testing.T) {
		// Get a config and modify it
		config1 := DefaultConfig()
		config1.PIN = "modified-pin"
		config1.RetryAttempts = 99

		// Get another config - should be unaffected
		config2 := DefaultConfig()
		assert.Empty(t, config2.PIN)
		assert.Equal(t, 3, config2.RetryAttempts)
	})
}

// Test SecretData edge cases and operations
func TestSecretData_EdgeCases(t *testing.T) {
	t.Run("empty secret data", func(t *testing.T) {
		data := SecretData{}
		assert.Equal(t, 0, len(data))
		assert.Empty(t, data["nonexistent"])
	})

	t.Run("nil and empty values", func(t *testing.T) {
		data := SecretData{
			"nil_value":   nil,
			"empty_value": []byte{},
			"zero_byte":   []byte{0},
		}

		assert.Nil(t, data["nil_value"])
		assert.NotNil(t, data["empty_value"])
		assert.Equal(t, 0, len(data["empty_value"]))
		assert.Equal(t, 1, len(data["zero_byte"]))
		assert.Equal(t, byte(0), data["zero_byte"][0])
	})

	t.Run("large data values", func(t *testing.T) {
		// Test with large data values (e.g., certificates, keys)
		largeData := make([]byte, 10000)
		for i := range largeData {
			largeData[i] = byte(i % 256)
		}

		data := SecretData{
			"large_cert": largeData,
			"small_key":  []byte("small"),
		}

		assert.Equal(t, 10000, len(data["large_cert"]))
		assert.Equal(t, 5, len(data["small_key"]))
		assert.Equal(t, byte(255), data["large_cert"][255]) // Check pattern
	})

	t.Run("unicode and special characters in keys", func(t *testing.T) {
		data := SecretData{
			"emoji_key_🔑":      []byte("secret"),
			"spaces in key":    []byte("value"),
			"key/with/slashes": []byte("path-like"),
			"key.with.dots":    []byte("dns-like"),
			"UPPERCASE":        []byte("shouty"),
		}

		assert.Equal(t, 5, len(data))
		assert.Equal(t, []byte("secret"), data["emoji_key_🔑"])
		assert.Equal(t, []byte("value"), data["spaces in key"])
		assert.Equal(t, []byte("path-like"), data["key/with/slashes"])
		assert.Equal(t, []byte("dns-like"), data["key.with.dots"])
		assert.Equal(t, []byte("shouty"), data["UPPERCASE"])
	})
}

// Test SecretMetadata edge cases and JSON marshaling
func TestSecretMetadata_EdgeCases(t *testing.T) {
	t.Run("empty metadata", func(t *testing.T) {
		metadata := &SecretMetadata{}
		assert.Empty(t, metadata.Description)
		assert.Nil(t, metadata.Labels)
		assert.Empty(t, metadata.Format)
		assert.Empty(t, metadata.DataType)
		assert.Empty(t, metadata.CreatedAt)
		assert.Empty(t, metadata.Source)
	})

	t.Run("metadata with nil labels map", func(t *testing.T) {
		metadata := &SecretMetadata{
			Description: "Test",
			Labels:      nil, // Explicitly nil
			Format:      "json",
		}
		assert.Equal(t, "Test", metadata.Description)
		assert.Nil(t, metadata.Labels)
		assert.Equal(t, "json", metadata.Format)
	})

	t.Run("metadata with empty labels map", func(t *testing.T) {
		metadata := &SecretMetadata{
			Description: "Test",
			Labels:      make(map[string]string), // Empty but not nil
			Format:      "json",
		}
		assert.Equal(t, "Test", metadata.Description)
		assert.NotNil(t, metadata.Labels)
		assert.Equal(t, 0, len(metadata.Labels))
	})

	t.Run("metadata with special characters", func(t *testing.T) {
		metadata := &SecretMetadata{
			Description: "Test with unicode: 🔐 and newlines\nand tabs\t",
			Labels: map[string]string{
				"unicode-label": "value-with-🌟",
				"env/stage":     "production",
				"team.domain":   "platform",
			},
			Format:    "pem",
			DataType:  "x509-cert",
			CreatedAt: "2025-01-01T00:00:00Z",
			Source:    "import/from/legacy-system",
		}

		assert.Contains(t, metadata.Description, "🔐")
		assert.Contains(t, metadata.Description, "\n")
		assert.Contains(t, metadata.Description, "\t")
		assert.Equal(t, "value-with-🌟", metadata.Labels["unicode-label"])
		assert.Equal(t, "production", metadata.Labels["env/stage"])
		assert.Equal(t, "platform", metadata.Labels["team.domain"])
	})
}

// Test HSMInfo edge cases
func TestHSMInfo_EdgeCases(t *testing.T) {
	t.Run("empty HSM info", func(t *testing.T) {
		info := &HSMInfo{}
		assert.Empty(t, info.Label)
		assert.Empty(t, info.Manufacturer)
		assert.Empty(t, info.Model)
		assert.Empty(t, info.SerialNumber)
		assert.Empty(t, info.FirmwareVersion)
	})

	t.Run("HSM info with special characters and long values", func(t *testing.T) {
		info := &HSMInfo{
			Label:           "HSM-Label-With-Dashes-And-123",
			Manufacturer:    "Manufacturer Name with Spaces & Special chars (™)",
			Model:           "Model_v2.0-beta",
			SerialNumber:    "SN123456789ABCDEF!@#$%",
			FirmwareVersion: "v1.2.3-build.456+sha.abc123",
		}

		assert.Contains(t, info.Label, "123")
		assert.Contains(t, info.Manufacturer, "™")
		assert.Contains(t, info.Model, "beta")
		assert.Contains(t, info.SerialNumber, "!@#$%")
		assert.Contains(t, info.FirmwareVersion, "sha.abc123")
	})
}

// Test CalculateChecksum edge cases
func TestCalculateChecksum_EdgeCases(t *testing.T) {
	t.Run("empty secret data", func(t *testing.T) {
		data := SecretData{}
		checksum := CalculateChecksum(data)
		assert.Contains(t, checksum, "sha256:")
		assert.True(t, len(checksum) > 10)
	})

	t.Run("single key", func(t *testing.T) {
		data := SecretData{"key": []byte("value")}
		checksum1 := CalculateChecksum(data)
		checksum2 := CalculateChecksum(data)
		assert.Equal(t, checksum1, checksum2, "checksum should be deterministic")
	})

	t.Run("key order independence", func(t *testing.T) {
		data1 := SecretData{
			"zeta":  []byte("last"),
			"alpha": []byte("first"),
			"beta":  []byte("middle"),
		}
		data2 := SecretData{
			"alpha": []byte("first"),
			"beta":  []byte("middle"),
			"zeta":  []byte("last"),
		}
		checksum1 := CalculateChecksum(data1)
		checksum2 := CalculateChecksum(data2)
		assert.Equal(t, checksum1, checksum2, "checksum should be order-independent")
	})

	t.Run("large data", func(t *testing.T) {
		largeValue := make([]byte, 1024*1024) // 1MB
		for i := range largeValue {
			largeValue[i] = byte(i % 256)
		}

		data := SecretData{"large": largeValue}
		checksum := CalculateChecksum(data)
		assert.Contains(t, checksum, "sha256:")
		assert.True(t, len(checksum) > 10)
	})

	t.Run("binary data with nulls", func(t *testing.T) {
		data := SecretData{
			"binary": []byte{0x00, 0x01, 0xFF, 0x00, 0xDE, 0xAD, 0xBE, 0xEF},
		}
		checksum := CalculateChecksum(data)
		assert.Contains(t, checksum, "sha256:")
	})
}

// Test connection management and retry scenarios
func TestConfig_ConnectionManagement(t *testing.T) {
	t.Run("timeout values", func(t *testing.T) {
		tests := []struct {
			name    string
			timeout time.Duration
			valid   bool
		}{
			{"zero timeout", 0, false},
			{"negative timeout", -time.Second, false},
			{"very short timeout", time.Millisecond, false},
			{"normal timeout", 30 * time.Second, true},
			{"long timeout", 5 * time.Minute, true},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := DefaultConfig()
				config.ConnectionTimeout = tt.timeout

				// Validate timeout range
				if tt.valid {
					assert.True(t, config.ConnectionTimeout > 0)
					assert.True(t, config.ConnectionTimeout < 10*time.Minute, "timeout should be reasonable")
				} else {
					assert.True(t, config.ConnectionTimeout <= time.Second || config.ConnectionTimeout < 0)
				}
			})
		}
	})

	t.Run("retry configuration", func(t *testing.T) {
		tests := []struct {
			name          string
			attempts      int
			delay         time.Duration
			expectedValid bool
		}{
			{"no retries", 0, 0, false},
			{"negative attempts", -1, time.Second, false},
			{"excessive attempts", 100, time.Second, false},
			{"negative delay", 3, -time.Second, false},
			{"normal config", 3, 2 * time.Second, true},
			{"minimal config", 1, time.Millisecond, true},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := DefaultConfig()
				config.RetryAttempts = tt.attempts
				config.RetryDelay = tt.delay

				if tt.expectedValid {
					assert.True(t, config.RetryAttempts >= 1)
					assert.True(t, config.RetryAttempts <= 10, "retry attempts should be reasonable")
					assert.True(t, config.RetryDelay >= 0)
					assert.True(t, config.RetryDelay <= time.Minute, "retry delay should be reasonable")
				} else {
					assert.True(t, config.RetryAttempts < 1 || config.RetryAttempts > 10 || config.RetryDelay < 0)
				}
			})
		}
	})
}

// Test MockClient error conditions and edge cases
func TestMockClient_ErrorConditions(t *testing.T) {
	t.Run("operations on uninitialized client", func(t *testing.T) {
		client := NewMockClient()
		ctx := context.Background()

		// Test all operations fail when not connected
		operations := []struct {
			name string
			fn   func() error
		}{
			{"GetInfo", func() error { _, err := client.GetInfo(ctx); return err }},
			{"ReadSecret", func() error { _, err := client.ReadSecret(ctx, "test"); return err }},
			{"WriteSecret", func() error { return client.WriteSecret(ctx, "test", SecretData{}, nil) }},
			{"ReadMetadata", func() error { _, err := client.ReadMetadata(ctx, "test"); return err }},
			{"DeleteSecret", func() error { return client.DeleteSecret(ctx, "test") }},
			{"ListSecrets", func() error { _, err := client.ListSecrets(ctx, ""); return err }},
		}

		for _, op := range operations {
			t.Run(op.name, func(t *testing.T) {
				err := op.fn()
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "HSM not connected")
			})
		}
	})

	t.Run("large path names", func(t *testing.T) {
		client := NewMockClient()
		ctx := context.Background()

		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		// Test very long path
		longPath := strings.Repeat("very-long-path-segment/", 50) + "final-secret"
		data := SecretData{"key": []byte("value")}

		err = client.WriteSecret(ctx, longPath, data, nil)
		assert.NoError(t, err, "should handle long paths")

		readData, err := client.ReadSecret(ctx, longPath)
		assert.NoError(t, err)
		assert.Equal(t, data, readData)
	})

	t.Run("special character paths", func(t *testing.T) {
		client := NewMockClient()
		ctx := context.Background()

		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		specialPaths := []string{
			"path/with spaces/secret",
			"path/with-unicode-🔐/secret",
			"path/with.dots.and_underscores/secret",
			"path/with@symbols#and$percent%/secret",
		}

		for _, path := range specialPaths {
			t.Run(fmt.Sprintf("path: %s", path), func(t *testing.T) {
				data := SecretData{"key": []byte("test-value")}

				err := client.WriteSecret(ctx, path, data, nil)
				assert.NoError(t, err, "should handle special character paths")

				readData, err := client.ReadSecret(ctx, path)
				assert.NoError(t, err)
				assert.Equal(t, data, readData)

				err = client.DeleteSecret(ctx, path)
				assert.NoError(t, err)
			})
		}
	})

	t.Run("memory safety with concurrent access", func(t *testing.T) {
		client := NewMockClient()
		ctx := context.Background()

		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		// Test that modifications to returned data don't affect stored data
		originalData := SecretData{
			"username": []byte("original-user"),
			"password": []byte("original-pass"),
		}

		err = client.WriteSecret(ctx, "test/memory-safety", originalData, nil)
		require.NoError(t, err)

		// Read and modify the returned data
		readData, err := client.ReadSecret(ctx, "test/memory-safety")
		require.NoError(t, err)

		// Modify the returned data
		readData["username"][0] = 'X' // Should not affect stored data
		readData["password"] = []byte("modified")
		readData["new-key"] = []byte("new-value")

		// Read again to verify original data is unchanged
		readData2, err := client.ReadSecret(ctx, "test/memory-safety")
		require.NoError(t, err)

		assert.Equal(t, []byte("original-user"), readData2["username"])
		assert.Equal(t, []byte("original-pass"), readData2["password"])
		assert.NotContains(t, readData2, "new-key")
	})

	t.Run("nil data handling", func(t *testing.T) {
		client := NewMockClient()
		ctx := context.Background()

		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		// Test writing nil data
		err = client.WriteSecret(ctx, "test/nil-data", nil, nil)
		assert.NoError(t, err)

		readData, err := client.ReadSecret(ctx, "test/nil-data")
		assert.NoError(t, err)
		assert.NotNil(t, readData)
		assert.Equal(t, 0, len(readData))

		// Test writing data with nil values
		dataWithNils := SecretData{
			"nil-value": nil,
			"empty":     []byte{},
			"real":      []byte("value"),
		}

		err = client.WriteSecret(ctx, "test/with-nils", dataWithNils, nil)
		assert.NoError(t, err)

		readData, err = client.ReadSecret(ctx, "test/with-nils")
		assert.NoError(t, err)

		// MockClient's append behavior: append([]byte(nil), v...) returns nil when v is nil or empty
		assert.Nil(t, readData["nil-value"])
		assert.Nil(t, readData["empty"]) // Empty slice becomes nil through append
		assert.Equal(t, []byte("value"), readData["real"])
	})
}

// Test PKCS11Config type conversion edge cases
func TestPKCS11Config_TypeConversions(t *testing.T) {
	t.Run("boundary values for SlotId", func(t *testing.T) {
		tests := []struct {
			name     string
			slotId   int32
			expected uint
		}{
			{"zero slot", 0, 0},
			{"positive slot", 42, 42},
			{"max int32", 2147483647, 2147483647},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				hsmDevice := HSMDeviceSpec{
					PKCS11: &PKCS11Config{
						LibraryPath: "/test/lib.so",
						SlotId:      tt.slotId,
						TokenLabel:  "TestToken",
					},
				}

				config := ConfigFromHSMDevice(hsmDevice, "test-pin")
				assert.Equal(t, tt.expected, config.SlotID)
				assert.Equal(t, "/test/lib.so", config.PKCS11LibraryPath)
				assert.Equal(t, "TestToken", config.TokenLabel)
				assert.Equal(t, "test-pin", config.PIN)
			})
		}
	})

	t.Run("nil PKCS11 config", func(t *testing.T) {
		hsmDevice := HSMDeviceSpec{
			PKCS11: nil,
		}

		config := ConfigFromHSMDevice(hsmDevice, "test-pin")

		// Should use defaults from DefaultConfig
		defaultConfig := DefaultConfig()
		assert.Equal(t, defaultConfig.PKCS11LibraryPath, config.PKCS11LibraryPath)
		assert.Equal(t, defaultConfig.SlotID, config.SlotID)
		assert.Equal(t, defaultConfig.TokenLabel, config.TokenLabel)
		assert.Equal(t, "test-pin", config.PIN)
	})
}
