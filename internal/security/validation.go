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

package security

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// Maximum path length for HSM secret paths
	MaxSecretPathLength = 256
	// Maximum secret data size (1MB)
	MaxSecretDataSize = 1024 * 1024
	// Maximum metadata field length
	MaxMetadataFieldLength = 1024
)

var (
	// Valid secret path pattern: alphanumeric, hyphens, underscores, forward slashes
	validPathPattern = regexp.MustCompile(`^[a-zA-Z0-9/_-]+$`)
	// Forbidden path patterns (prevent directory traversal, etc.)
	forbiddenPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\.\.`),       // Directory traversal
		regexp.MustCompile(`//`),         // Double slashes
		regexp.MustCompile(`^/`),         // Leading slash
		regexp.MustCompile(`/$`),         // Trailing slash
		regexp.MustCompile(`_metadata$`), // Reserved metadata suffix
	}
)

// InputValidator validates and sanitizes input for HSM operations
type InputValidator struct{}

// NewInputValidator creates a new input validator
func NewInputValidator() *InputValidator {
	return &InputValidator{}
}

// ValidateSecretPath validates and sanitizes secret paths
func (v *InputValidator) ValidateSecretPath(path string) error {
	if path == "" {
		return fmt.Errorf("secret path cannot be empty")
	}

	if len(path) > MaxSecretPathLength {
		return fmt.Errorf("secret path too long: %d > %d", len(path), MaxSecretPathLength)
	}

	// Check valid pattern
	if !validPathPattern.MatchString(path) {
		return fmt.Errorf("secret path contains invalid characters: %s", path)
	}

	// Check forbidden patterns
	for _, pattern := range forbiddenPatterns {
		if pattern.MatchString(path) {
			return fmt.Errorf("secret path contains forbidden pattern: %s", path)
		}
	}

	return nil
}

// ValidateSecretData validates secret data size and content
func (v *InputValidator) ValidateSecretData(data map[string][]byte) error {
	if data == nil {
		return fmt.Errorf("secret data cannot be nil")
	}

	if len(data) == 0 {
		return fmt.Errorf("secret data cannot be empty")
	}

	totalSize := 0
	for key, value := range data {
		if key == "" {
			return fmt.Errorf("secret data key cannot be empty")
		}

		if len(key) > MaxMetadataFieldLength {
			return fmt.Errorf("secret data key too long: %d > %d", len(key), MaxMetadataFieldLength)
		}

		// Check for metadata key suffix (reserved)
		if strings.HasSuffix(key, "_metadata") {
			return fmt.Errorf("secret data key cannot end with '_metadata': %s", key)
		}

		// Validate key pattern
		if !validPathPattern.MatchString(key) {
			return fmt.Errorf("secret data key contains invalid characters: %s", key)
		}

		totalSize += len(value)
		if totalSize > MaxSecretDataSize {
			return fmt.Errorf("secret data too large: %d > %d", totalSize, MaxSecretDataSize)
		}
	}

	return nil
}

// ValidateMetadata validates secret metadata
func (v *InputValidator) ValidateMetadata(secretMetadata map[string]string) error {
	if secretMetadata == nil {
		return nil // Metadata is optional
	}

	for key, value := range secretMetadata {
		if len(key) > MaxMetadataFieldLength {
			return fmt.Errorf("metadata key too long: %d > %d", len(key), MaxMetadataFieldLength)
		}

		if len(value) > MaxMetadataFieldLength {
			return fmt.Errorf("metadata value too long: %d > %d", len(value), MaxMetadataFieldLength)
		}

		// Sanitize metadata fields
		if strings.ContainsAny(key, "\x00\n\r") {
			return fmt.Errorf("metadata key contains invalid characters: %s", key)
		}

		if strings.ContainsAny(value, "\x00") {
			return fmt.Errorf("metadata value contains null bytes: %s", value)
		}
	}

	return nil
}

// ValidationInterceptor returns a gRPC unary interceptor for input validation
func ValidationInterceptor(validator *InputValidator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Type-specific validation based on request type
		switch r := req.(type) {
		case interface{ GetPath() string }:
			if err := validator.ValidateSecretPath(r.GetPath()); err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid path: %v", err)
			}
		}

		// Additional validation for write requests
		switch r := req.(type) {
		case interface {
			GetSecretData() interface{ GetData() map[string][]byte }
		}:
			if secretData := r.GetSecretData(); secretData != nil {
				if err := validator.ValidateSecretData(secretData.GetData()); err != nil {
					return nil, status.Errorf(codes.InvalidArgument, "invalid secret data: %v", err)
				}
			}
		case interface {
			GetMetadata() interface{ GetLabels() map[string]string }
		}:
			if metadata := r.GetMetadata(); metadata != nil {
				if err := validator.ValidateMetadata(metadata.GetLabels()); err != nil {
					return nil, status.Errorf(codes.InvalidArgument, "invalid metadata: %v", err)
				}
			}
		}

		return handler(ctx, req)
	}
}
