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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/evanjarrett/hsm-secrets-operator/kubectl-hsm/pkg/auth"
)

// Client provides methods for interacting with the HSM operator API
type Client struct {
	baseURL      string
	httpClient   *http.Client
	tokenManager *auth.TokenManager
}

// NewClient creates a new HSM API client
func NewClient(baseURL string) *Client {
	client := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Initialize JWT token manager (best effort)
	if tokenManager, err := auth.NewTokenManager(baseURL); err == nil {
		client.tokenManager = tokenManager
	}
	// If token manager fails to initialize, continue without authentication
	// This provides backwards compatibility for development environments

	return client
}

// SetServiceAccount sets the service account to use for authentication
func (c *Client) SetServiceAccount(serviceAccount string) {
	if c.tokenManager != nil {
		c.tokenManager.SetServiceAccount(serviceAccount)
	}
}

// SetNamespace sets the namespace for authentication
func (c *Client) SetNamespace(namespace string) {
	if c.tokenManager != nil {
		c.tokenManager.SetNamespace(namespace)
	}
}

// ClearAuthCache clears the cached authentication token
func (c *Client) ClearAuthCache() error {
	if c.tokenManager != nil {
		return c.tokenManager.ClearCache()
	}
	return nil
}

// decodeBase64Data converts base64-encoded string values back to plain text
// This is needed because the server returns []byte which gets JSON-marshaled as base64
func decodeBase64Data(data map[string]any) map[string]any {
	decoded := make(map[string]any)
	for key, value := range data {
		if strValue, ok := value.(string); ok {
			// Try to decode as base64 - if it fails, keep original value
			if decodedBytes, err := base64.StdEncoding.DecodeString(strValue); err == nil {
				decoded[key] = string(decodedBytes)
			} else {
				decoded[key] = strValue
			}
		} else {
			decoded[key] = value
		}
	}
	return decoded
}

// CreateSecret creates a new secret in the HSM, merging with existing data if present
func (c *Client) CreateSecret(ctx context.Context, name string, data map[string]any) error {
	return c.CreateSecretWithOptions(ctx, name, data, false)
}

// CreateSecretWithOptions creates a new secret in the HSM with replace option
func (c *Client) CreateSecretWithOptions(ctx context.Context, name string, data map[string]any, replace bool) error {
	// Only merge if replace is false
	if !replace {
		// Try to read existing secret first for merge behavior
		existing, err := c.GetSecret(ctx, name)
		if err == nil && existing != nil {
			// Decode existing base64-encoded data first to prevent double-encoding
			decodedExisting := decodeBase64Data(existing.Data)

			// Merge decoded existing data with new data (new data takes precedence)
			mergedData := make(map[string]any)

			// Start with decoded existing data
			maps.Copy(mergedData, decodedExisting)

			// Override/add with new data
			for k, v := range data {
				mergedData[k] = v
			}

			data = mergedData
		}
		// If error reading existing secret, continue with original data (new secret)
	}

	req := CreateSecretRequest{
		Data: data,
	}

	return c.doRequest(ctx, "POST", fmt.Sprintf("/api/v1/hsm/secrets/%s", name), req, nil)
}

// GetSecret retrieves a secret from the HSM and decodes base64-encoded data
func (c *Client) GetSecret(ctx context.Context, name string) (*SecretData, error) {
	var result SecretData
	err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/v1/hsm/secrets/%s", name), nil, &result)
	if err != nil {
		return nil, err
	}

	// Decode base64-encoded data to plain text for consistent handling
	result.Data = decodeBase64Data(result.Data)

	return &result, nil
}

// ListSecrets lists all secrets in the HSM
func (c *Client) ListSecrets(ctx context.Context, page, pageSize int) (*SecretList, error) {
	path := "/api/v1/hsm/secrets"

	// Add pagination parameters if specified
	if page > 0 || pageSize > 0 {
		params := url.Values{}
		if page > 0 {
			params.Add("page", strconv.Itoa(page))
		}
		if pageSize > 0 {
			params.Add("page_size", strconv.Itoa(pageSize))
		}
		if len(params) > 0 {
			path += "?" + params.Encode()
		}
	}

	var result SecretList
	err := c.doRequest(ctx, "GET", path, nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteSecret deletes a secret from the HSM
func (c *Client) DeleteSecret(ctx context.Context, name string) error {
	return c.doRequest(ctx, "DELETE", fmt.Sprintf("/api/v1/hsm/secrets/%s", name), nil, nil)
}

// GetHealth checks the health status of the HSM operator
func (c *Client) GetHealth(ctx context.Context) (*HealthStatus, error) {
	var result HealthStatus
	err := c.doRequest(ctx, "GET", "/api/v1/health", nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// GetDeviceStatus retrieves the connectivity status of all HSM devices
func (c *Client) GetDeviceStatus(ctx context.Context) (*DeviceStatusResponse, error) {
	var result DeviceStatusResponse
	err := c.doRequest(ctx, "GET", "/api/v1/hsm/status", nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// GetDeviceInfo retrieves detailed information about all HSM devices
func (c *Client) GetDeviceInfo(ctx context.Context) (*DeviceInfoResponse, error) {
	var result DeviceInfoResponse
	err := c.doRequest(ctx, "GET", "/api/v1/hsm/info", nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// ChangePIN changes the HSM PIN from old PIN to new PIN
func (c *Client) ChangePIN(ctx context.Context, oldPIN, newPIN string) (*ChangePINResponse, error) {
	req := ChangePINRequest{
		OldPIN: oldPIN,
		NewPIN: newPIN,
	}

	var result ChangePINResponse
	err := c.doRequest(ctx, "POST", "/api/v1/hsm/change-pin", req, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// doRequest performs an HTTP request and handles the standard API response format
func (c *Client) doRequest(ctx context.Context, method, path string, requestBody any, responseData any) error {
	url := c.baseURL + path

	var body io.Reader
	if requestBody != nil {
		jsonData, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		body = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Add JWT authentication if available
	if c.tokenManager != nil && !strings.HasSuffix(path, "/health") && !strings.HasSuffix(path, "/healthz") {
		token, err := c.tokenManager.GetValidToken(ctx)
		if err != nil {
			return fmt.Errorf("failed to get authentication token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("failed to parse API response: %w", err)
	}

	// Check if the API reported an error
	if !apiResp.Success {
		if apiResp.Error != nil {
			return fmt.Errorf("API error (%s): %s", apiResp.Error.Code, apiResp.Error.Message)
		}
		return fmt.Errorf("API request failed: %s", apiResp.Message)
	}

	// If we need to extract specific data from the response
	if responseData != nil && apiResp.Data != nil {
		dataBytes, err := json.Marshal(apiResp.Data)
		if err != nil {
			return fmt.Errorf("failed to marshal response data: %w", err)
		}

		if err := json.Unmarshal(dataBytes, responseData); err != nil {
			return fmt.Errorf("failed to unmarshal response data: %w", err)
		}
	}

	return nil
}
