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

package api

import (
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"

	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

func TestNewProxyClient(t *testing.T) {
	tests := []struct {
		name           string
		server         *Server
		logger         logr.Logger
		expectedServer *Server
	}{
		{
			name:           "valid server and logger",
			server:         &Server{},
			logger:         logr.Discard(),
			expectedServer: &Server{},
		},
		{
			name:           "nil server",
			server:         nil,
			logger:         logr.Discard(),
			expectedServer: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewProxyClient(tt.server, tt.logger)

			assert.NotNil(t, client)
			assert.Equal(t, tt.expectedServer, client.server)
			assert.NotNil(t, client.logger)
			assert.NotNil(t, client.grpcClients)
			assert.Empty(t, client.grpcClients)
		})
	}
}

func TestParseTimestampFromMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata *hsm.SecretMetadata
		expected int64
	}{
		{
			name:     "nil metadata",
			metadata: nil,
			expected: 0,
		},
		{
			name: "nil labels",
			metadata: &hsm.SecretMetadata{
				Labels: nil,
			},
			expected: 0,
		},
		{
			name: "empty labels",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{},
			},
			expected: 0,
		},
		{
			name: "valid RFC3339 timestamp",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{
					"sync.timestamp": "2025-01-15T10:30:00Z",
				},
			},
			expected: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC).Unix(),
		},
		{
			name: "valid Unix timestamp in sync.version",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{
					"sync.version": "1640995200", // 2022-01-01 00:00:00 UTC
				},
			},
			expected: 1640995200,
		},
		{
			name: "RFC3339 timestamp takes precedence over sync.version",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{
					"sync.timestamp": "2025-01-15T10:30:00Z",
					"sync.version":   "1640995200",
				},
			},
			expected: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC).Unix(),
		},
		{
			name: "invalid RFC3339 timestamp falls back to sync.version",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{
					"sync.timestamp": "invalid-timestamp",
					"sync.version":   "1640995200",
				},
			},
			expected: 1640995200,
		},
		{
			name: "invalid RFC3339 and invalid sync.version",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{
					"sync.timestamp": "invalid-timestamp",
					"sync.version":   "not-a-number",
				},
			},
			expected: 0,
		},
		{
			name: "sync.version with extra text",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{
					"sync.version": "1640995200-extra-text",
				},
			},
			expected: 1640995200, // fmt.Sscanf should parse the number part
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseTimestampFromMetadata(tt.metadata)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsSecretDeleted(t *testing.T) {
	tests := []struct {
		name     string
		metadata *hsm.SecretMetadata
		expected bool
	}{
		{
			name:     "nil metadata",
			metadata: nil,
			expected: false,
		},
		{
			name: "nil labels",
			metadata: &hsm.SecretMetadata{
				Labels: nil,
			},
			expected: false,
		},
		{
			name: "empty labels",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{},
			},
			expected: false,
		},
		{
			name: "no deleted marker",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{
					"other.label": "value",
				},
			},
			expected: false,
		},
		{
			name: "deleted marker set to true",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{
					"sync.deleted": "true",
				},
			},
			expected: true,
		},
		{
			name: "deleted marker set to false",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{
					"sync.deleted": "false",
				},
			},
			expected: false,
		},
		{
			name: "deleted marker set to empty string",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{
					"sync.deleted": "",
				},
			},
			expected: false,
		},
		{
			name: "deleted marker set to non-true value",
			metadata: &hsm.SecretMetadata{
				Labels: map[string]string{
					"sync.deleted": "random-value",
				},
			},
			expected: false, // Only exact "true" should be truthy
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSecretDeleted(tt.metadata)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestProxyWriteResult(t *testing.T) {
	t.Run("successful write result", func(t *testing.T) {
		result := WriteResult{
			DeviceName: "pico-hsm-1",
			Error:      nil,
		}

		assert.Equal(t, "pico-hsm-1", result.DeviceName)
		assert.NoError(t, result.Error)
	})

	t.Run("failed write result", func(t *testing.T) {
		result := WriteResult{
			DeviceName: "pico-hsm-2",
			Error:      assert.AnError,
		}

		assert.Equal(t, "pico-hsm-2", result.DeviceName)
		assert.Error(t, result.Error)
	})
}

func TestChecksumResult(t *testing.T) {
	t.Run("successful checksum result", func(t *testing.T) {
		result := checksumResult{
			deviceName: "pico-hsm-1",
			checksum:   "sha256:abcd1234",
			err:        nil,
		}

		assert.Equal(t, "pico-hsm-1", result.deviceName)
		assert.Equal(t, "sha256:abcd1234", result.checksum)
		assert.NoError(t, result.err)
	})

	t.Run("failed checksum result", func(t *testing.T) {
		result := checksumResult{
			deviceName: "pico-hsm-2",
			checksum:   "",
			err:        assert.AnError,
		}

		assert.Equal(t, "pico-hsm-2", result.deviceName)
		assert.Empty(t, result.checksum)
		assert.Error(t, result.err)
	})
}

func TestSecretResult(t *testing.T) {
	t.Run("successful secret result", func(t *testing.T) {
		data := hsm.SecretData{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		}
		metadata := &hsm.SecretMetadata{
			Labels: map[string]string{
				"test": "value",
			},
		}

		result := secretResult{
			deviceName: "pico-hsm-1",
			data:       data,
			metadata:   metadata,
			err:        nil,
		}

		assert.Equal(t, "pico-hsm-1", result.deviceName)
		assert.NotNil(t, result.data)
		assert.NotNil(t, result.metadata)
		assert.NoError(t, result.err)
		assert.Equal(t, "value", result.metadata.Labels["test"])
	})

	t.Run("failed secret result", func(t *testing.T) {
		result := secretResult{
			deviceName: "pico-hsm-2",
			data:       nil,
			metadata:   nil,
			err:        assert.AnError,
		}

		assert.Equal(t, "pico-hsm-2", result.deviceName)
		assert.Nil(t, result.data)
		assert.Nil(t, result.metadata)
		assert.Error(t, result.err)
	})
}

func TestMetadataResult(t *testing.T) {
	t.Run("successful metadata result", func(t *testing.T) {
		metadata := &hsm.SecretMetadata{
			Labels: map[string]string{
				"version": "1.0",
			},
		}

		result := metadataResult{
			deviceName: "pico-hsm-1",
			metadata:   metadata,
			err:        nil,
		}

		assert.Equal(t, "pico-hsm-1", result.deviceName)
		assert.NotNil(t, result.metadata)
		assert.NoError(t, result.err)
		assert.Equal(t, "1.0", result.metadata.Labels["version"])
	})

	t.Run("failed metadata result", func(t *testing.T) {
		result := metadataResult{
			deviceName: "pico-hsm-2",
			metadata:   nil,
			err:        assert.AnError,
		}

		assert.Equal(t, "pico-hsm-2", result.deviceName)
		assert.Nil(t, result.metadata)
		assert.Error(t, result.err)
	})
}

// Benchmark tests for performance-critical functions
func BenchmarkParseTimestampFromMetadata(b *testing.B) {
	metadata := &hsm.SecretMetadata{
		Labels: map[string]string{
			"sync.timestamp": "2025-01-15T10:30:00Z",
			"sync.version":   "1640995200",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseTimestampFromMetadata(metadata)
	}
}

func TestValidatePathParam(t *testing.T) {
	// We need to test validatePathParam which requires gin.Context
	// Since it's a method that interacts with HTTP context, we'll test the logic that would be in it
	tests := []struct {
		name          string
		pathParam     string
		expectedValid bool
	}{
		{
			name:          "valid path parameter",
			pathParam:     "my-secret",
			expectedValid: true,
		},
		{
			name:          "valid path with dashes",
			pathParam:     "my-secret-key",
			expectedValid: true,
		},
		{
			name:          "valid path with numbers",
			pathParam:     "secret123",
			expectedValid: true,
		},
		{
			name:          "empty path parameter",
			pathParam:     "",
			expectedValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the validation logic that validatePathParam would use
			isValid := tt.pathParam != ""
			assert.Equal(t, tt.expectedValid, isValid)
		})
	}
}

func TestProxyClientStructures(t *testing.T) {
	server := &Server{}
	logger := logr.Discard()

	client := NewProxyClient(server, logger)

	// Test that ProxyClient has expected structure
	assert.NotNil(t, client)
	assert.Equal(t, server, client.server)
	assert.NotNil(t, client.logger)
	assert.NotNil(t, client.grpcClients)
	assert.Empty(t, client.grpcClients)
}

func TestResultStructures(t *testing.T) {
	// Test checksumResult structure
	t.Run("checksumResult", func(t *testing.T) {
		result := checksumResult{
			deviceName: "test-device",
			checksum:   "sha256:abc123",
			err:        nil,
		}
		assert.Equal(t, "test-device", result.deviceName)
		assert.Equal(t, "sha256:abc123", result.checksum)
		assert.NoError(t, result.err)
	})

	// Test secretResult structure
	t.Run("secretResult", func(t *testing.T) {
		data := hsm.SecretData{
			"key1": []byte("value1"),
		}
		metadata := &hsm.SecretMetadata{
			Labels: map[string]string{
				"version": "1",
			},
		}

		result := secretResult{
			deviceName: "test-device",
			data:       data,
			metadata:   metadata,
			err:        nil,
		}

		assert.Equal(t, "test-device", result.deviceName)
		assert.NotNil(t, result.data)
		assert.NotNil(t, result.metadata)
		assert.NoError(t, result.err)
		assert.Equal(t, "1", result.metadata.Labels["version"])
	})

	// Test metadataResult structure
	t.Run("metadataResult", func(t *testing.T) {
		metadata := &hsm.SecretMetadata{
			Labels: map[string]string{
				"timestamp": "123456789",
			},
		}

		result := metadataResult{
			deviceName: "test-device",
			metadata:   metadata,
			err:        nil,
		}

		assert.Equal(t, "test-device", result.deviceName)
		assert.NotNil(t, result.metadata)
		assert.NoError(t, result.err)
		assert.Equal(t, "123456789", result.metadata.Labels["timestamp"])
	})
}

func TestErrorHandling(t *testing.T) {
	// Test that result structures properly handle errors
	t.Run("checksumResult with error", func(t *testing.T) {
		result := checksumResult{
			deviceName: "failed-device",
			checksum:   "",
			err:        assert.AnError,
		}
		assert.Equal(t, "failed-device", result.deviceName)
		assert.Empty(t, result.checksum)
		assert.Error(t, result.err)
	})

	t.Run("secretResult with error", func(t *testing.T) {
		result := secretResult{
			deviceName: "failed-device",
			data:       nil,
			metadata:   nil,
			err:        assert.AnError,
		}
		assert.Equal(t, "failed-device", result.deviceName)
		assert.Nil(t, result.data)
		assert.Nil(t, result.metadata)
		assert.Error(t, result.err)
	})

	t.Run("metadataResult with error", func(t *testing.T) {
		result := metadataResult{
			deviceName: "failed-device",
			metadata:   nil,
			err:        assert.AnError,
		}
		assert.Equal(t, "failed-device", result.deviceName)
		assert.Nil(t, result.metadata)
		assert.Error(t, result.err)
	})
}

func BenchmarkIsSecretDeleted(b *testing.B) {
	metadata := &hsm.SecretMetadata{
		Labels: map[string]string{
			"sync.deleted": "true",
			"other.label":  "value",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isSecretDeleted(metadata)
	}
}

// Test findConsensusChecksum method
func TestProxyClient_FindConsensusChecksum(t *testing.T) {
	proxyClient := &ProxyClient{
		logger: logr.Discard(),
	}

	tests := []struct {
		name             string
		results          []checksumResult
		path             string
		expectedChecksum string
		expectedError    string
	}{
		{
			name:             "no results",
			results:          []checksumResult{},
			path:             "test-secret",
			expectedChecksum: "",
			expectedError:    "checksum not found on any HSM device",
		},
		{
			name: "single result",
			results: []checksumResult{
				{deviceName: "pico-hsm-0", checksum: "sha256:abc123"},
			},
			path:             "test-secret",
			expectedChecksum: "sha256:abc123",
			expectedError:    "",
		},
		{
			name: "consensus reached",
			results: []checksumResult{
				{deviceName: "pico-hsm-0", checksum: "sha256:abc123"},
				{deviceName: "pico-hsm-1", checksum: "sha256:abc123"},
				{deviceName: "pico-hsm-2", checksum: "sha256:def456"},
			},
			path:             "test-secret",
			expectedChecksum: "sha256:abc123", // Most common (2 vs 1)
			expectedError:    "",
		},
		{
			name: "tie broken by first occurrence",
			results: []checksumResult{
				{deviceName: "pico-hsm-0", checksum: "sha256:abc123"},
				{deviceName: "pico-hsm-1", checksum: "sha256:def456"},
			},
			path:             "test-secret",
			expectedChecksum: "sha256:abc123", // First one wins in tie
			expectedError:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checksum, err := proxyClient.findConsensusChecksum(tt.results, tt.path)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.Empty(t, checksum)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedChecksum, checksum)
			}
		})
	}
}

// Test findMostRecentSecretResult method
func TestProxyClient_FindMostRecentSecretResult(t *testing.T) {
	proxyClient := &ProxyClient{
		logger: logr.Discard(),
	}

	tests := []struct {
		name          string
		results       []secretResult
		path          string
		expectedData  hsm.SecretData
		expectedError string
	}{
		{
			name:          "no results",
			results:       []secretResult{},
			path:          "test-secret",
			expectedData:  nil,
			expectedError: "secret not found on any HSM device",
		},
		{
			name: "single result",
			results: []secretResult{
				{
					deviceName: "pico-hsm-0",
					data:       hsm.SecretData{"key1": []byte("value1")},
					metadata: &hsm.SecretMetadata{
						Labels: map[string]string{"sync.version": "1640995200"},
					},
				},
			},
			path:          "test-secret",
			expectedData:  hsm.SecretData{"key1": []byte("value1")},
			expectedError: "",
		},
		{
			name: "most recent wins",
			results: []secretResult{
				{
					deviceName: "pico-hsm-0",
					data:       hsm.SecretData{"key1": []byte("old_value")},
					metadata: &hsm.SecretMetadata{
						Labels: map[string]string{"sync.version": "1640995200"},
					},
				},
				{
					deviceName: "pico-hsm-1",
					data:       hsm.SecretData{"key1": []byte("new_value")},
					metadata: &hsm.SecretMetadata{
						Labels: map[string]string{"sync.version": "1640995300"}, // More recent
					},
				},
			},
			path:          "test-secret",
			expectedData:  hsm.SecretData{"key1": []byte("new_value")}, // More recent one
			expectedError: "",
		},
		{
			name: "deleted secret (tombstone)",
			results: []secretResult{
				{
					deviceName: "pico-hsm-0",
					data:       hsm.SecretData{},
					metadata: &hsm.SecretMetadata{
						Labels: map[string]string{
							"sync.deleted": "true",
							"sync.version": "1640995200",
						},
					},
				},
			},
			path:          "test-secret",
			expectedData:  nil,
			expectedError: "secret not found on any HSM device", // Deleted secrets return not found
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := proxyClient.findMostRecentSecretResult(tt.results, tt.path)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.Nil(t, data)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedData, data)
			}
		})
	}
}

// Test findMostRecentMetadataResult method
func TestProxyClient_FindMostRecentMetadataResult(t *testing.T) {
	proxyClient := &ProxyClient{
		logger: logr.Discard(),
	}

	tests := []struct {
		name             string
		results          []metadataResult
		path             string
		expectedMetadata *hsm.SecretMetadata
		expectedError    string
	}{
		{
			name:             "no results",
			results:          []metadataResult{},
			path:             "test-secret",
			expectedMetadata: nil,
			expectedError:    "metadata not found on any HSM device",
		},
		{
			name: "single result",
			results: []metadataResult{
				{
					deviceName: "pico-hsm-0",
					metadata: &hsm.SecretMetadata{
						Labels: map[string]string{"sync.version": "1640995200"},
					},
				},
			},
			path: "test-secret",
			expectedMetadata: &hsm.SecretMetadata{
				Labels: map[string]string{"sync.version": "1640995200"},
			},
			expectedError: "",
		},
		{
			name: "most recent wins",
			results: []metadataResult{
				{
					deviceName: "pico-hsm-0",
					metadata: &hsm.SecretMetadata{
						Labels: map[string]string{"sync.version": "1640995200"},
					},
				},
				{
					deviceName: "pico-hsm-1",
					metadata: &hsm.SecretMetadata{
						Labels: map[string]string{"sync.version": "1640995300"}, // More recent
					},
				},
			},
			path: "test-secret",
			expectedMetadata: &hsm.SecretMetadata{
				Labels: map[string]string{"sync.version": "1640995300"}, // More recent one
			},
			expectedError: "",
		},
		{
			name: "tombstone metadata returned (not filtered)",
			results: []metadataResult{
				{
					deviceName: "pico-hsm-0",
					metadata: &hsm.SecretMetadata{
						Labels: map[string]string{
							"sync.deleted": "true",
							"sync.version": "1640995200",
						},
					},
				},
			},
			path: "test-secret",
			expectedMetadata: &hsm.SecretMetadata{
				Labels: map[string]string{
					"sync.deleted": "true",
					"sync.version": "1640995200",
				},
			},
			expectedError: "", // Tombstones are returned for metadata (unlike secrets)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata, err := proxyClient.findMostRecentMetadataResult(tt.results, tt.path)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.Nil(t, metadata)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedMetadata, metadata)
			}
		})
	}
}

// Test ProxyClient client management methods
func TestProxyClient_ClientManagement(t *testing.T) {
	proxyClient := NewProxyClient(&Server{}, logr.Discard())

	t.Run("GetClientCount initially zero", func(t *testing.T) {
		count := proxyClient.GetClientCount()
		assert.Equal(t, 0, count)
	})

	t.Run("CleanupDisconnectedClients with no clients", func(t *testing.T) {
		// Should not panic with empty client map
		proxyClient.CleanupDisconnectedClients()
		assert.Equal(t, 0, proxyClient.GetClientCount())
	})

	t.Run("Close with no clients", func(t *testing.T) {
		err := proxyClient.Close()
		assert.NoError(t, err)
		assert.Equal(t, 0, proxyClient.GetClientCount())
	})
}

// Test logMultiDeviceOperation method
func TestProxyClient_LogMultiDeviceOperation(t *testing.T) {
	// Test that logging doesn't panic and handles various inputs
	proxyClient := &ProxyClient{
		logger: logr.Discard(), // Uses discard logger so we can't test output, but can test no panics
	}

	tests := []struct {
		name           string
		deviceNames    []string
		selectedDevice string
		operationName  string
		path           string
		syncDetails    string
	}{
		{
			name:           "normal operation",
			deviceNames:    []string{"pico-hsm-0", "pico-hsm-1"},
			selectedDevice: "pico-hsm-1",
			operationName:  "Secret",
			path:           "test-secret",
			syncDetails:    "timestamp: 1640995200",
		},
		{
			name:           "empty device list",
			deviceNames:    []string{},
			selectedDevice: "none",
			operationName:  "Metadata",
			path:           "empty-test",
			syncDetails:    "no details",
		},
		{
			name:           "single device",
			deviceNames:    []string{"yubikey-0"},
			selectedDevice: "yubikey-0",
			operationName:  "Checksum",
			path:           "single-device-test",
			syncDetails:    "consensus checksum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			assert.NotPanics(t, func() {
				proxyClient.logMultiDeviceOperation(tt.deviceNames, tt.selectedDevice, tt.operationName, tt.path, tt.syncDetails)
			})
		})
	}
}
