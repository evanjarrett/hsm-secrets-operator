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

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// Token cache file
	TokenCacheDir  = ".kube"
	TokenCacheFile = "hsm-cache"
	// Token refresh threshold (5 minutes before expiry)
	TokenRefreshThreshold = 5 * time.Minute
)

// TokenManager handles JWT token caching and automatic refresh
type TokenManager struct {
	baseURL       string
	k8sClient     kubernetes.Interface
	serviceAccount string
	namespace     string
	httpClient    *http.Client
	cachedToken   *CachedToken
}

// CachedToken represents a cached JWT token
type CachedToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	TokenType string    `json:"token_type"`
}

// TokenRequest represents a token generation request
type TokenRequest struct {
	K8sToken string `json:"k8s_token"`
}

// TokenResponse represents a token generation response
type TokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	TokenType string    `json:"token_type"`
}

// NewTokenManager creates a new token manager
func NewTokenManager(baseURL string) (*TokenManager, error) {
	// Load Kubernetes configuration
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	k8sClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Get current context to determine service account
	rawConfig, err := kubeConfig.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get raw kubeconfig: %w", err)
	}

	// Use current context namespace, default to "default"
	namespace := rawConfig.Contexts[rawConfig.CurrentContext].Namespace
	if namespace == "" {
		namespace = "default"
	}

	tm := &TokenManager{
		baseURL:        baseURL,
		k8sClient:      k8sClient,
		serviceAccount: "kubectl-hsm", // Default service account name
		namespace:      namespace,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Try to load cached token
	tm.loadCachedToken()

	return tm, nil
}

// GetValidToken returns a valid JWT token, refreshing if necessary
func (tm *TokenManager) GetValidToken(ctx context.Context) (string, error) {
	// Check if cached token is still valid
	if tm.cachedToken != nil && tm.isTokenValid() {
		return tm.cachedToken.Token, nil
	}

	// Generate new token
	token, err := tm.generateNewToken(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to generate new token: %w", err)
	}

	return token, nil
}

// isTokenValid checks if the cached token is still valid
func (tm *TokenManager) isTokenValid() bool {
	if tm.cachedToken == nil {
		return false
	}

	// Check if token expires within the refresh threshold
	return time.Now().Add(TokenRefreshThreshold).Before(tm.cachedToken.ExpiresAt)
}

// generateNewToken generates a new JWT token
func (tm *TokenManager) generateNewToken(ctx context.Context) (string, error) {
	// Get Kubernetes service account token
	k8sToken, err := tm.getK8sServiceAccountToken(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get Kubernetes token: %w", err)
	}

	// Exchange for HSM JWT token
	hsmToken, err := tm.exchangeForHSMToken(ctx, k8sToken)
	if err != nil {
		return "", fmt.Errorf("failed to exchange for HSM token: %w", err)
	}

	// Cache the token
	tm.cachedToken = &CachedToken{
		Token:     hsmToken.Token,
		ExpiresAt: hsmToken.ExpiresAt,
		TokenType: hsmToken.TokenType,
	}

	// Save to cache file
	tm.saveCachedToken()

	return hsmToken.Token, nil
}

// getK8sServiceAccountToken gets a Kubernetes service account token
func (tm *TokenManager) getK8sServiceAccountToken(ctx context.Context) (string, error) {
	// Create token request for the service account
	tokenRequest := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: &[]int64{3600}[0], // 1 hour
		},
	}

	// Try to get token for the specified service account
	result, err := tm.k8sClient.CoreV1().ServiceAccounts(tm.namespace).CreateToken(
		ctx, tm.serviceAccount, tokenRequest, metav1.CreateOptions{})
	if err != nil {
		// If service account doesn't exist or we don't have permission,
		// try to use the default service account
		if tm.serviceAccount != "default" {
			tm.serviceAccount = "default"
			result, err = tm.k8sClient.CoreV1().ServiceAccounts(tm.namespace).CreateToken(
				ctx, tm.serviceAccount, tokenRequest, metav1.CreateOptions{})
		}
		if err != nil {
			return "", fmt.Errorf("failed to create token for service account %s/%s: %w",
				tm.namespace, tm.serviceAccount, err)
		}
	}

	return result.Status.Token, nil
}

// exchangeForHSMToken exchanges a Kubernetes token for an HSM JWT token
func (tm *TokenManager) exchangeForHSMToken(ctx context.Context, k8sToken string) (*TokenResponse, error) {
	// Prepare request
	tokenReq := TokenRequest{
		K8sToken: k8sToken,
	}

	jsonData, err := json.Marshal(tokenReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal token request: %w", err)
	}

	// Make request to HSM API
	url := tm.baseURL + "/api/v1/auth/token"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := tm.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokenResp, nil
}

// loadCachedToken loads token from cache file
func (tm *TokenManager) loadCachedToken() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}

	cacheFile := filepath.Join(homeDir, TokenCacheDir, TokenCacheFile)
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return
	}

	var cached CachedToken
	if err := json.Unmarshal(data, &cached); err != nil {
		return
	}

	tm.cachedToken = &cached
}

// saveCachedToken saves token to cache file
func (tm *TokenManager) saveCachedToken() {
	if tm.cachedToken == nil {
		return
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}

	cacheDir := filepath.Join(homeDir, TokenCacheDir)
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return
	}

	data, err := json.Marshal(tm.cachedToken)
	if err != nil {
		return
	}

	cacheFile := filepath.Join(cacheDir, TokenCacheFile)
	os.WriteFile(cacheFile, data, 0600)
}

// SetServiceAccount sets the service account name to use for token generation
func (tm *TokenManager) SetServiceAccount(serviceAccount string) {
	tm.serviceAccount = serviceAccount
}

// SetNamespace sets the namespace for the service account
func (tm *TokenManager) SetNamespace(namespace string) {
	tm.namespace = namespace
}

// ClearCache clears the cached token
func (tm *TokenManager) ClearCache() error {
	tm.cachedToken = nil

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	cacheFile := filepath.Join(homeDir, TokenCacheDir, TokenCacheFile)
	return os.Remove(cacheFile)
}