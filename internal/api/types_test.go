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

	"github.com/stretchr/testify/assert"
)

func TestSecretFormat(t *testing.T) {
	tests := []struct {
		name     string
		format   SecretFormat
		expected string
	}{
		{"JSON format", SecretFormatJSON, "json"},
		{"Binary format", SecretFormatBinary, "binary"},
		{"Text format", SecretFormatText, "text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.format))
		})
	}
}

func TestCreateSecretRequest(t *testing.T) {
	req := CreateSecretRequest{
		Label:  "test-secret",
		ID:     123,
		Format: SecretFormatJSON,
		Data: map[string]any{
			"username": "testuser",
			"password": "testpass",
		},
		Description: "A test secret",
		Tags: map[string]string{
			"env":  "test",
			"team": "platform",
		},
	}

	assert.Equal(t, "test-secret", req.Label)
	assert.Equal(t, uint32(123), req.ID)
	assert.Equal(t, SecretFormatJSON, req.Format)
	assert.Equal(t, "testuser", req.Data["username"])
	assert.Equal(t, "testpass", req.Data["password"])
	assert.Equal(t, "A test secret", req.Description)
	assert.Equal(t, "test", req.Tags["env"])
	assert.Equal(t, "platform", req.Tags["team"])
}

func TestUpdateSecretRequest(t *testing.T) {
	req := UpdateSecretRequest{
		Data: map[string]any{
			"username": "updateduser",
			"password": "updatedpass",
		},
		Description: "Updated description",
		Tags: map[string]string{
			"env":     "production",
			"updated": "true",
		},
	}

	assert.Equal(t, "updateduser", req.Data["username"])
	assert.Equal(t, "updatedpass", req.Data["password"])
	assert.Equal(t, "Updated description", req.Description)
	assert.Equal(t, "production", req.Tags["env"])
	assert.Equal(t, "true", req.Tags["updated"])
}

func TestImportSecretRequest(t *testing.T) {
	req := ImportSecretRequest{
		Source:          "kubernetes",
		SecretName:      "source-secret",
		SecretNamespace: "default",
		TargetLabel:     "target-secret",
		TargetID:        456,
		Format:          SecretFormatText,
		KeyMapping: map[string]string{
			"src_key": "dst_key",
		},
	}

	assert.Equal(t, "kubernetes", req.Source)
	assert.Equal(t, "source-secret", req.SecretName)
	assert.Equal(t, "default", req.SecretNamespace)
	assert.Equal(t, "target-secret", req.TargetLabel)
	assert.Equal(t, uint32(456), req.TargetID)
	assert.Equal(t, SecretFormatText, req.Format)
	assert.Equal(t, "dst_key", req.KeyMapping["src_key"])
}

func TestSecretInfo(t *testing.T) {
	now := time.Now()
	info := SecretInfo{
		Label:        "test-secret",
		ID:           789,
		Format:       SecretFormatBinary,
		Description:  "Test secret info",
		Tags:         map[string]string{"type": "test"},
		CreatedAt:    now,
		UpdatedAt:    now,
		Size:         1024,
		Checksum:     "sha256:abcd1234",
		IsReplicated: true,
	}

	assert.Equal(t, "test-secret", info.Label)
	assert.Equal(t, uint32(789), info.ID)
	assert.Equal(t, SecretFormatBinary, info.Format)
	assert.Equal(t, "Test secret info", info.Description)
	assert.Equal(t, "test", info.Tags["type"])
	assert.Equal(t, now, info.CreatedAt)
	assert.Equal(t, now, info.UpdatedAt)
	assert.Equal(t, int64(1024), info.Size)
	assert.Equal(t, "sha256:abcd1234", info.Checksum)
	assert.True(t, info.IsReplicated)
}

func TestSecretData(t *testing.T) {
	now := time.Now()
	info := SecretInfo{
		Label:     "metadata-secret",
		ID:        101,
		CreatedAt: now,
	}

	data := SecretData{
		Data: map[string]any{
			"key1": "value1",
			"key2": 42,
		},
		Metadata: info,
	}

	assert.Equal(t, "value1", data.Data["key1"])
	assert.Equal(t, 42, data.Data["key2"])
	assert.Equal(t, "metadata-secret", data.Metadata.Label)
	assert.Equal(t, uint32(101), data.Metadata.ID)
	assert.Equal(t, now, data.Metadata.CreatedAt)
}

func TestSecretList(t *testing.T) {
	secrets := []SecretInfo{
		{Label: "secret1", ID: 1},
		{Label: "secret2", ID: 2},
		{Label: "secret3", ID: 3},
	}

	list := SecretList{
		Secrets:  secrets,
		Total:    3,
		Page:     1,
		PageSize: 10,
	}

	assert.Len(t, list.Secrets, 3)
	assert.Equal(t, 3, list.Total)
	assert.Equal(t, 1, list.Page)
	assert.Equal(t, 10, list.PageSize)

	// Check first secret
	assert.Equal(t, "secret1", list.Secrets[0].Label)
	assert.Equal(t, uint32(1), list.Secrets[0].ID)
}

func TestAPIResponse(t *testing.T) {
	t.Run("successful response", func(t *testing.T) {
		resp := APIResponse{
			Success: true,
			Message: "Operation completed successfully",
			Data: map[string]string{
				"result": "success",
			},
		}

		assert.True(t, resp.Success)
		assert.Equal(t, "Operation completed successfully", resp.Message)
		assert.NotNil(t, resp.Data)
		assert.Nil(t, resp.Error)

		data, ok := resp.Data.(map[string]string)
		assert.True(t, ok)
		assert.Equal(t, "success", data["result"])
	})

	t.Run("error response", func(t *testing.T) {
		apiError := &APIError{
			Code:    "VALIDATION_ERROR",
			Message: "Invalid input data",
			Details: map[string]any{
				"field": "label",
				"issue": "required",
			},
		}

		resp := APIResponse{
			Success: false,
			Message: "Request failed",
			Error:   apiError,
		}

		assert.False(t, resp.Success)
		assert.Equal(t, "Request failed", resp.Message)
		assert.Nil(t, resp.Data)
		assert.NotNil(t, resp.Error)

		assert.Equal(t, "VALIDATION_ERROR", resp.Error.Code)
		assert.Equal(t, "Invalid input data", resp.Error.Message)
		assert.Equal(t, "label", resp.Error.Details["field"])
		assert.Equal(t, "required", resp.Error.Details["issue"])
	})
}

func TestAPIError(t *testing.T) {
	apiError := APIError{
		Code:    "HSM_CONNECTION_ERROR",
		Message: "Failed to connect to HSM device",
		Details: map[string]any{
			"device_path": "/dev/ttyUSB0",
			"error_type":  "connection_timeout",
			"retry_count": 3,
		},
	}

	assert.Equal(t, "HSM_CONNECTION_ERROR", apiError.Code)
	assert.Equal(t, "Failed to connect to HSM device", apiError.Message)
	assert.Equal(t, "/dev/ttyUSB0", apiError.Details["device_path"])
	assert.Equal(t, "connection_timeout", apiError.Details["error_type"])
	assert.Equal(t, 3, apiError.Details["retry_count"])
}

func TestHealthStatus(t *testing.T) {
	now := time.Now()
	health := HealthStatus{
		Status:             "healthy",
		HSMConnected:       true,
		ReplicationEnabled: true,
		ActiveNodes:        3,
		Timestamp:          now,
	}

	assert.Equal(t, "healthy", health.Status)
	assert.True(t, health.HSMConnected)
	assert.True(t, health.ReplicationEnabled)
	assert.Equal(t, 3, health.ActiveNodes)
	assert.Equal(t, now, health.Timestamp)
}

func TestHealthStatusUnhealthy(t *testing.T) {
	now := time.Now()
	health := HealthStatus{
		Status:             "degraded",
		HSMConnected:       false,
		ReplicationEnabled: false,
		ActiveNodes:        0,
		Timestamp:          now,
	}

	assert.Equal(t, "degraded", health.Status)
	assert.False(t, health.HSMConnected)
	assert.False(t, health.ReplicationEnabled)
	assert.Equal(t, 0, health.ActiveNodes)
	assert.Equal(t, now, health.Timestamp)
}

func TestWriteResult(t *testing.T) {
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

// Test JSON serialization/deserialization
func TestCreateSecretRequestSerialization(t *testing.T) {
	req := CreateSecretRequest{
		Label:  "serialize-test",
		ID:     999,
		Format: SecretFormatJSON,
		Data: map[string]any{
			"key1":   "value1",
			"key2":   123,
			"nested": map[string]any{"inner": "value"},
		},
		Description: "Serialization test",
		Tags: map[string]string{
			"test": "serialization",
		},
	}

	// Test that all fields are properly accessible
	assert.Equal(t, "serialize-test", req.Label)
	assert.Equal(t, uint32(999), req.ID)
	assert.Equal(t, SecretFormatJSON, req.Format)
	assert.Contains(t, req.Data, "key1")
	assert.Contains(t, req.Data, "key2")
	assert.Contains(t, req.Data, "nested")

	nested, ok := req.Data["nested"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "value", nested["inner"])
}

func TestEmptyStructures(t *testing.T) {
	t.Run("empty CreateSecretRequest", func(t *testing.T) {
		req := CreateSecretRequest{}
		assert.Empty(t, req.Label)
		assert.Equal(t, uint32(0), req.ID)
		assert.Empty(t, req.Format)
		assert.Nil(t, req.Data)
		assert.Empty(t, req.Description)
		assert.Nil(t, req.Tags)
	})

	t.Run("empty SecretList", func(t *testing.T) {
		list := SecretList{}
		assert.Nil(t, list.Secrets)
		assert.Equal(t, 0, list.Total)
		assert.Equal(t, 0, list.Page)
		assert.Equal(t, 0, list.PageSize)
	})

	t.Run("empty HealthStatus", func(t *testing.T) {
		health := HealthStatus{}
		assert.Empty(t, health.Status)
		assert.False(t, health.HSMConnected)
		assert.False(t, health.ReplicationEnabled)
		assert.Equal(t, 0, health.ActiveNodes)
		assert.True(t, health.Timestamp.IsZero())
	})
}

// Benchmark tests
func BenchmarkCreateSecretRequest(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := CreateSecretRequest{
			Label:  "benchmark-secret",
			ID:     uint32(i),
			Format: SecretFormatJSON,
			Data: map[string]any{
				"username": "user",
				"password": "pass",
			},
			Description: "Benchmark test",
			Tags:        map[string]string{"bench": "true"},
		}
		_ = req // Avoid unused variable
	}
}

func BenchmarkSecretList(b *testing.B) {
	secrets := make([]SecretInfo, 1000)
	for i := 0; i < 1000; i++ {
		secrets[i] = SecretInfo{
			Label: "secret-" + string(rune(i)),
			ID:    uint32(i),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		list := SecretList{
			Secrets:  secrets,
			Total:    1000,
			Page:     1,
			PageSize: 100,
		}
		_ = list // Avoid unused variable
	}
}
