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

package client

import (
	"time"
)

// APIResponse represents a standard API response from HSM operator
type APIResponse struct {
	Success bool     `json:"success"`
	Message string   `json:"message,omitempty"`
	Data    any      `json:"data,omitempty"`
	Error   *APIError `json:"error,omitempty"`
}

// APIError represents an API error response
type APIError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// SecretData represents the actual secret data
type SecretData struct {
	Data     map[string]any `json:"data"`
	Metadata *SecretInfo    `json:"metadata,omitempty"`
}

// SecretInfo represents information about a secret
type SecretInfo struct {
	Label        string            `json:"label"`
	ID           uint32            `json:"id"`
	Format       string            `json:"format"`
	Description  string            `json:"description,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Size         int64             `json:"size"`
	Checksum     string            `json:"checksum"`
	IsReplicated bool              `json:"is_replicated"`
}

// SecretList represents a list of secrets
type SecretList struct {
	Secrets  []string     `json:"secrets,omitempty"`
	Paths    []string     `json:"paths,omitempty"`
	Count    int          `json:"count"`
	Total    int          `json:"total"`
	Page     int          `json:"page,omitempty"`
	PageSize int          `json:"page_size,omitempty"`
	Prefix   string       `json:"prefix,omitempty"`
}

// CreateSecretRequest represents a request to create a secret
type CreateSecretRequest struct {
	Data        map[string]any    `json:"data"`
	Description string            `json:"description,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}

// HealthStatus represents the health status of the HSM operator
type HealthStatus struct {
	Status             string    `json:"status"`
	HSMConnected       bool      `json:"hsm_connected"`
	ReplicationEnabled bool      `json:"replication_enabled"`
	ActiveNodes        int       `json:"active_nodes"`
	Timestamp          time.Time `json:"timestamp"`
}