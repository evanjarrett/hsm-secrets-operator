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
	"time"

	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// SecretFormat defines the format of the secret data
type SecretFormat string

const (
	// SecretFormatJSON stores data as JSON key-value pairs
	SecretFormatJSON SecretFormat = "json"
	// SecretFormatBinary stores raw binary data
	SecretFormatBinary SecretFormat = "binary"
	// SecretFormatText stores plain text data
	SecretFormatText SecretFormat = "text"
)

// CreateSecretRequest represents a request to create a new secret
type CreateSecretRequest struct {
	// Label is the human-readable identifier for the secret
	Label string `json:"label" validate:"required,min=1,max=255"`

	// ID is the unique numeric identifier for the secret on the HSM
	ID uint32 `json:"id" validate:"required,min=1"`

	// Format specifies how the data should be stored
	Format SecretFormat `json:"format" validate:"required,oneof=json binary text"`

	// Data contains the actual secret data
	Data map[string]any `json:"data" validate:"required"`

	// Description is an optional description of the secret
	Description string `json:"description,omitempty" validate:"max=1000"`

	// Tags are optional metadata tags
	Tags map[string]string `json:"tags,omitempty"`
}

// UpdateSecretRequest represents a request to update an existing secret
type UpdateSecretRequest struct {
	// Data contains the updated secret data
	Data map[string]any `json:"data" validate:"required"`

	// Description is an optional updated description
	Description string `json:"description,omitempty" validate:"max=1000"`

	// Tags are optional updated metadata tags
	Tags map[string]string `json:"tags,omitempty"`
}

// ImportSecretRequest represents a request to import a secret from external source
type ImportSecretRequest struct {
	// Source specifies where to import from (kubernetes, vault, etc.)
	Source string `json:"source" validate:"required,oneof=kubernetes vault file"`

	// SecretName is the name of the source secret
	SecretName string `json:"secret_name" validate:"required"`

	// SecretNamespace is the namespace for Kubernetes secrets
	SecretNamespace string `json:"secret_namespace,omitempty"`

	// TargetLabel is the label for the imported secret on HSM
	TargetLabel string `json:"target_label" validate:"required,min=1,max=255"`

	// TargetID is the ID for the imported secret on HSM
	TargetID uint32 `json:"target_id" validate:"required,min=1"`

	// Format specifies how the imported data should be stored
	Format SecretFormat `json:"format" validate:"required,oneof=json binary text"`

	// KeyMapping maps source keys to target keys (optional)
	KeyMapping map[string]string `json:"key_mapping,omitempty"`
}

// SecretInfo represents information about a secret
type SecretInfo struct {
	// Label is the human-readable identifier
	Label string `json:"label"`

	// ID is the unique numeric identifier on the HSM
	ID uint32 `json:"id"`

	// Format specifies the data format
	Format SecretFormat `json:"format"`

	// Description is the secret description
	Description string `json:"description,omitempty"`

	// Tags are metadata tags
	Tags map[string]string `json:"tags,omitempty"`

	// CreatedAt is when the secret was created
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the secret was last updated
	UpdatedAt time.Time `json:"updated_at"`

	// Size is the size of the secret data in bytes
	Size int64 `json:"size"`

	// Checksum is the SHA256 checksum of the data
	Checksum string `json:"checksum"`

	// IsReplicated indicates if the secret is replicated across nodes
	IsReplicated bool `json:"is_replicated"`
}

// SecretData represents the actual secret data
type SecretData struct {
	// Data contains the secret key-value pairs
	Data map[string]any `json:"data"`

	// Metadata contains additional information about the secret
	Metadata SecretInfo `json:"metadata"`
}

// SecretList represents a list of secrets
type SecretList struct {
	// Secrets is the list of secret information
	Secrets []SecretInfo `json:"secrets"`

	// Total is the total number of secrets
	Total int `json:"total"`

	// Page is the current page number (for pagination)
	Page int `json:"page,omitempty"`

	// PageSize is the number of items per page
	PageSize int `json:"page_size,omitempty"`
}

// APIResponse represents a standard API response
type APIResponse struct {
	// Success indicates if the operation was successful
	Success bool `json:"success"`

	// Message provides additional information about the result
	Message string `json:"message,omitempty"`

	// Data contains the response data
	Data any `json:"data,omitempty"`

	// Error contains error details if the operation failed
	Error *APIError `json:"error,omitempty"`
}

// APIError represents an API error
type APIError struct {
	// Code is the error code
	Code string `json:"code"`

	// Message is the human-readable error message
	Message string `json:"message"`

	// Details contains additional error details
	Details map[string]any `json:"details,omitempty"`
}

// HealthStatus represents the health status of the API server
type HealthStatus struct {
	// Status is the overall health status
	Status string `json:"status"`

	// HSMConnected indicates if HSM is connected
	HSMConnected bool `json:"hsm_connected"`

	// ReplicationEnabled indicates if replication is enabled
	ReplicationEnabled bool `json:"replication_enabled"`

	// ActiveNodes is the number of active HSM nodes
	ActiveNodes int `json:"active_nodes"`

	// Timestamp is when the health check was performed
	Timestamp time.Time `json:"timestamp"`
}

// ListSecretsResponse represents the response for listing secrets
type ListSecretsResponse struct {
	Secrets []string `json:"secrets"`
	Count   int      `json:"count"`
	Prefix  string   `json:"prefix,omitempty"`
}

// ReadSecretResponse represents the response for reading a secret
type ReadSecretResponse struct {
	Path        string            `json:"path"`
	Data        map[string][]byte `json:"data"`
	Checksum    string            `json:"checksum,omitempty"`
	DeviceCount int               `json:"deviceCount,omitempty"`
}

// WriteSecretResponse represents the response for writing a secret
type WriteSecretResponse struct {
	Path string `json:"path"`
	Keys int    `json:"keys"`
}

// DeleteSecretResponse represents the response for deleting a secret
type DeleteSecretResponse struct {
	Path          string         `json:"path"`
	Devices       int            `json:"devices"`
	DeviceResults map[string]any `json:"deviceResults"`
	Warnings      []string       `json:"warnings,omitempty"`
}

// ReadMetadataResponse represents the response for reading metadata
type ReadMetadataResponse struct {
	Path     string              `json:"path"`
	Metadata *hsm.SecretMetadata `json:"metadata"`
}

// GetChecksumResponse represents the response for getting a checksum
type GetChecksumResponse struct {
	Path     string `json:"path"`
	Checksum string `json:"checksum"`
}

type IsConnectedResponse struct {
	Devices      map[string]bool `json:"devices"`
	TotalDevices int             `json:"totalDevices"`
}

// GetInfoResponse represents the response for getting HSM info
type GetInfoResponse struct {
	DeviceInfos map[string]*hsm.HSMInfo `json:"deviceInfos"` // deviceName -> HSMInfo
}
