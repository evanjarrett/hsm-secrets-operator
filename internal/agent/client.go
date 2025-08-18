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

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// Client implements the HSM client interface by communicating with HSM agents
type Client struct {
	httpClient    *http.Client
	baseURL       string
	logger        logr.Logger
	deviceName    string
	timeout       time.Duration
	retryAttempts int
	retryDelay    time.Duration
}

// NewClient creates a new agent client
func NewClient(baseURL, deviceName string, logger logr.Logger) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL:       strings.TrimSuffix(baseURL, "/"),
		logger:        logger.WithName("agent-client"),
		deviceName:    deviceName,
		timeout:       30 * time.Second,
		retryAttempts: 3,
		retryDelay:    2 * time.Second,
	}
}

// Initialize establishes connection to the HSM agent
func (c *Client) Initialize(ctx context.Context, config hsm.Config) error {
	// The agent handles HSM initialization, we just need to verify connectivity
	info, err := c.GetInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize agent client: %w", err)
	}

	c.logger.Info("Agent client initialized", "device", c.deviceName, "hsm_label", info.Label)
	return nil
}

// Close terminates the HSM connection
func (c *Client) Close() error {
	// HTTP client doesn't need explicit closing, agent handles HSM cleanup
	return nil
}

// GetInfo returns information about the HSM device
func (c *Client) GetInfo(ctx context.Context) (*hsm.HSMInfo, error) {
	var response AgentResponse
	if err := c.doRequest(ctx, "GET", "/api/v1/hsm/info", nil, &response); err != nil {
		return nil, fmt.Errorf("failed to get HSM info: %w", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("agent error: %s", response.Error.Message)
	}

	// Convert response data to HSMInfo
	data, ok := response.Data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response data format")
	}

	info := &hsm.HSMInfo{}
	if label, ok := data["label"].(string); ok {
		info.Label = label
	}
	if manufacturer, ok := data["manufacturer"].(string); ok {
		info.Manufacturer = manufacturer
	}
	if model, ok := data["model"].(string); ok {
		info.Model = model
	}
	if serialNumber, ok := data["serialNumber"].(string); ok {
		info.SerialNumber = serialNumber
	}
	if firmwareVersion, ok := data["firmwareVersion"].(string); ok {
		info.FirmwareVersion = firmwareVersion
	}

	return info, nil
}

// ReadSecret reads secret data from the specified HSM path
func (c *Client) ReadSecret(ctx context.Context, path string) (hsm.SecretData, error) {
	escapedPath := c.escapePath(path)
	endpoint := fmt.Sprintf("/api/v1/hsm/secrets/%s", escapedPath)

	var response AgentResponse
	if err := c.doRequest(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("failed to read secret: %w", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("agent error: %s", response.Error.Message)
	}

	// Convert response data to SecretData
	responseData, ok := response.Data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response data format")
	}

	secretDataRaw, ok := responseData["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid secret data format")
	}

	secretData := make(hsm.SecretData)
	for key, value := range secretDataRaw {
		switch v := value.(type) {
		case string:
			secretData[key] = []byte(v)
		case []byte:
			secretData[key] = v
		case []interface{}:
			// Handle JSON array (byte array)
			bytes := make([]byte, len(v))
			for i, b := range v {
				if byteVal, ok := b.(float64); ok {
					bytes[i] = byte(byteVal)
				}
			}
			secretData[key] = bytes
		default:
			// Convert to string as fallback
			secretData[key] = []byte(fmt.Sprintf("%v", v))
		}
	}

	return secretData, nil
}

// WriteSecret writes secret data to the specified HSM path
func (c *Client) WriteSecret(ctx context.Context, path string, data hsm.SecretData) error {
	escapedPath := c.escapePath(path)
	endpoint := fmt.Sprintf("/api/v1/hsm/secrets/%s", escapedPath)

	// Convert SecretData to request format
	requestData := make(map[string]interface{})
	for key, value := range data {
		requestData[key] = string(value)
	}

	request := AgentRequest{
		Path: path,
		Data: requestData,
	}

	var response AgentResponse
	if err := c.doRequest(ctx, "POST", endpoint, &request, &response); err != nil {
		return fmt.Errorf("failed to write secret: %w", err)
	}

	if !response.Success {
		return fmt.Errorf("agent error: %s", response.Error.Message)
	}

	return nil
}

// DeleteSecret removes secret data from the specified HSM path
func (c *Client) DeleteSecret(ctx context.Context, path string) error {
	escapedPath := c.escapePath(path)
	endpoint := fmt.Sprintf("/api/v1/hsm/secrets/%s", escapedPath)

	var response AgentResponse
	if err := c.doRequest(ctx, "DELETE", endpoint, nil, &response); err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}

	if !response.Success {
		return fmt.Errorf("agent error: %s", response.Error.Message)
	}

	return nil
}

// ListSecrets returns a list of secret paths
func (c *Client) ListSecrets(ctx context.Context, prefix string) ([]string, error) {
	endpoint := "/api/v1/hsm/secrets"
	if prefix != "" {
		endpoint += "?prefix=" + prefix
	}

	var response AgentResponse
	if err := c.doRequest(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("agent error: %s", response.Error.Message)
	}

	// Convert response data to string slice
	responseData, ok := response.Data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response data format")
	}

	pathsRaw, ok := responseData["paths"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid paths data format")
	}

	paths := make([]string, len(pathsRaw))
	for i, pathRaw := range pathsRaw {
		if path, ok := pathRaw.(string); ok {
			paths[i] = path
		}
	}

	return paths, nil
}

// GetChecksum returns the SHA256 checksum of the secret data at the given path
func (c *Client) GetChecksum(ctx context.Context, path string) (string, error) {
	escapedPath := c.escapePath(path)
	endpoint := fmt.Sprintf("/api/v1/hsm/checksum/%s", escapedPath)

	var response AgentResponse
	if err := c.doRequest(ctx, "GET", endpoint, nil, &response); err != nil {
		return "", fmt.Errorf("failed to get checksum: %w", err)
	}

	if !response.Success {
		return "", fmt.Errorf("agent error: %s", response.Error.Message)
	}

	// Extract checksum from response
	responseData, ok := response.Data.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid response data format")
	}

	checksum, ok := responseData["checksum"].(string)
	if !ok {
		return "", fmt.Errorf("invalid checksum format")
	}

	return checksum, nil
}

// IsConnected returns true if the HSM agent is connected and responsive
func (c *Client) IsConnected() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.GetInfo(ctx)
	return err == nil
}

// doRequest performs an HTTP request with retry logic
func (c *Client) doRequest(ctx context.Context, method, endpoint string, requestBody interface{}, responseBody interface{}) error {
	url := c.baseURL + endpoint

	var reqBodyReader io.Reader
	if requestBody != nil {
		jsonBody, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBodyReader = bytes.NewReader(jsonBody)
	}

	var lastErr error
	for attempt := 0; attempt <= c.retryAttempts; attempt++ {
		if attempt > 0 {
			c.logger.V(1).Info("Retrying request", "attempt", attempt, "url", url, "method", method)

			// Reset the request body reader for retry
			if requestBody != nil {
				jsonBody, _ := json.Marshal(requestBody)
				reqBodyReader = bytes.NewReader(jsonBody)
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.retryDelay):
				// Continue with retry
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, url, reqBodyReader)
		if err != nil {
			lastErr = fmt.Errorf("failed to create request: %w", err)
			continue
		}

		if requestBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}

		defer resp.Body.Close()

		// Read response body
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			lastErr = fmt.Errorf("failed to read response body: %w", err)
			continue
		}

		// Check for HTTP errors
		if resp.StatusCode >= 400 {
			// Try to parse error response
			var errorResp AgentResponse
			if json.Unmarshal(bodyBytes, &errorResp) == nil && errorResp.Error != nil {
				lastErr = fmt.Errorf("agent error (status %d): %s", resp.StatusCode, errorResp.Error.Message)
			} else {
				lastErr = fmt.Errorf("HTTP error (status %d): %s", resp.StatusCode, string(bodyBytes))
			}

			// Don't retry client errors (4xx)
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				break
			}
			continue
		}

		// Parse successful response
		if responseBody != nil {
			if err := json.Unmarshal(bodyBytes, responseBody); err != nil {
				lastErr = fmt.Errorf("failed to unmarshal response: %w", err)
				continue
			}
		}

		// Success
		return nil
	}

	return fmt.Errorf("request failed after %d attempts: %w", c.retryAttempts+1, lastErr)
}

// escapePath escapes path components for URL usage
func (c *Client) escapePath(path string) string {
	// Simple path escaping - in production might want more sophisticated handling
	path = strings.ReplaceAll(path, "/", "%2F")
	path = strings.ReplaceAll(path, " ", "%20")
	return path
}

// SetRetryPolicy configures retry behavior
func (c *Client) SetRetryPolicy(attempts int, delay time.Duration) {
	c.retryAttempts = attempts
	c.retryDelay = delay
}

// SetTimeout configures request timeout
func (c *Client) SetTimeout(timeout time.Duration) {
	c.timeout = timeout
	c.httpClient.Timeout = timeout
}
