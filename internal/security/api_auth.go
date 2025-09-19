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
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/golang-jwt/jwt/v5"
	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// Token validity period
	TokenValidityPeriod = 30 * time.Minute
	// JWT secret size
	JWTSecretSize = 32
	// API token header
	TokenHeader = "Authorization"
	// Bearer prefix
	BearerPrefix = "Bearer "
)

// Claims represents JWT claims for API authentication
type Claims struct {
	ServiceAccount string `json:"sub"`
	Namespace      string `json:"namespace"`
	jwt.RegisteredClaims
}

// APIAuthenticator handles API authentication using Kubernetes service accounts
type APIAuthenticator struct {
	k8sClient kubernetes.Interface
	jwtSecret []byte
	logger    logr.Logger
}

// NewAPIAuthenticator creates a new API authenticator
func NewAPIAuthenticator(k8sClient kubernetes.Interface, logger logr.Logger) (*APIAuthenticator, error) {
	// Generate random JWT secret
	secret := make([]byte, JWTSecretSize)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("failed to generate JWT secret: %w", err)
	}

	return &APIAuthenticator{
		k8sClient: k8sClient,
		jwtSecret: secret,
		logger:    logger.WithName("api-auth"),
	}, nil
}

// GenerateToken generates a JWT token for a validated service account
func (a *APIAuthenticator) GenerateToken(ctx context.Context, k8sToken string) (string, error) {
	// Validate the Kubernetes service account token
	saInfo, err := a.validateServiceAccountToken(ctx, k8sToken)
	if err != nil {
		return "", fmt.Errorf("invalid service account token: %w", err)
	}

	// Check if service account has HSM access permissions
	if err := a.validateHSMPermissions(ctx, saInfo.ServiceAccount, saInfo.Namespace); err != nil {
		return "", fmt.Errorf("service account lacks HSM permissions: %w", err)
	}

	// Create JWT claims
	claims := &Claims{
		ServiceAccount: saInfo.ServiceAccount,
		Namespace:      saInfo.Namespace,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(TokenValidityPeriod)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "hsm-secrets-operator",
			Subject:   fmt.Sprintf("%s.%s", saInfo.ServiceAccount, saInfo.Namespace),
		},
	}

	// Create and sign token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(a.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	a.logger.Info("Generated API token", "service_account", saInfo.ServiceAccount, "namespace", saInfo.Namespace)
	return tokenString, nil
}

// ValidateToken validates a JWT token and returns the claims
func (a *APIAuthenticator) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.jwtSecret, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token claims")
}

// validateServiceAccountToken validates a Kubernetes service account token
func (a *APIAuthenticator) validateServiceAccountToken(ctx context.Context, token string) (*ServiceAccountInfo, error) {
	// Use TokenReview to validate the token
	tokenReview := &authv1.TokenReview{
		Spec: authv1.TokenReviewSpec{
			Token: token,
		},
	}

	result, err := a.k8sClient.AuthenticationV1().TokenReviews().Create(ctx, tokenReview, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to review token: %w", err)
	}

	if !result.Status.Authenticated {
		return nil, fmt.Errorf("token authentication failed: %s", result.Status.Error)
	}

	// Extract service account information
	userInfo := result.Status.User
	parts := strings.Split(userInfo.Username, ":")
	if len(parts) != 4 || parts[0] != "system" || parts[1] != "serviceaccount" {
		return nil, fmt.Errorf("token is not for a service account: %s", userInfo.Username)
	}

	return &ServiceAccountInfo{
		ServiceAccount: parts[3],
		Namespace:      parts[2],
		Groups:         userInfo.Groups,
		UID:            userInfo.UID,
	}, nil
}

// validateHSMPermissions checks if the service account has necessary HSM permissions
func (a *APIAuthenticator) validateHSMPermissions(ctx context.Context, serviceAccount, namespace string) error {
	// For now, we'll implement basic validation
	// In a full implementation, you would use SubjectAccessReview to check specific permissions

	// Check if service account exists
	_, err := a.k8sClient.CoreV1().ServiceAccounts(namespace).Get(ctx, serviceAccount, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("service account not found: %w", err)
	}

	// TODO: Add SubjectAccessReview to check specific HSM permissions
	// For now, any valid service account is allowed
	return nil
}

// ServiceAccountInfo contains service account information
type ServiceAccountInfo struct {
	ServiceAccount string
	Namespace      string
	Groups         []string
	UID            string
}

// AuthMiddleware returns a Gin middleware for API authentication
func (a *APIAuthenticator) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// Skip authentication for health checks
		if strings.HasSuffix(path, "/health") || strings.HasSuffix(path, "/healthz") {
			c.Next()
			return
		}

		// Skip authentication for token generation endpoint
		if strings.HasSuffix(path, "/auth/token") {
			c.Next()
			return
		}

		// Skip authentication for web UI static files and root redirect
		if path == "/" || strings.HasPrefix(path, "/web/") {
			c.Next()
			return
		}

		// Extract token from header
		authHeader := c.GetHeader(TokenHeader)
		if authHeader == "" {
			a.logger.Info("Missing authorization header", "path", c.Request.URL.Path, "client_ip", c.ClientIP())
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			c.Abort()
			return
		}

		if !strings.HasPrefix(authHeader, BearerPrefix) {
			a.logger.Info("Invalid authorization header format", "path", c.Request.URL.Path, "client_ip", c.ClientIP())
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header format"})
			c.Abort()
			return
		}

		tokenString := strings.TrimPrefix(authHeader, BearerPrefix)

		// Validate token
		claims, err := a.ValidateToken(tokenString)
		if err != nil {
			a.logger.Info("Token validation failed", "error", err, "path", c.Request.URL.Path, "client_ip", c.ClientIP())
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			c.Abort()
			return
		}

		// Store claims in context for later use
		c.Set("claims", claims)
		c.Set("service_account", claims.ServiceAccount)
		c.Set("namespace", claims.Namespace)

		a.logger.V(1).Info("Request authenticated",
			"service_account", claims.ServiceAccount,
			"namespace", claims.Namespace,
			"path", c.Request.URL.Path,
			"client_ip", c.ClientIP(),
		)

		c.Next()
	}
}

// GetClaimsFromContext extracts claims from Gin context
func GetClaimsFromContext(c *gin.Context) (*Claims, bool) {
	claims, exists := c.Get("claims")
	if !exists {
		return nil, false
	}

	claimsTyped, ok := claims.(*Claims)
	return claimsTyped, ok
}

// TokenRequest represents a token generation request
type TokenRequest struct {
	K8sToken string `json:"k8s_token" binding:"required"`
}

// TokenResponse represents a token generation response
type TokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	TokenType string    `json:"token_type"`
}

// HandleTokenGeneration handles POST /auth/token requests
func (a *APIAuthenticator) HandleTokenGeneration() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req TokenRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "details": err.Error()})
			return
		}

		// Generate token
		token, err := a.GenerateToken(c.Request.Context(), req.K8sToken)
		if err != nil {
			a.logger.Info("Token generation failed", "error", err, "client_ip", c.ClientIP())
			c.JSON(http.StatusUnauthorized, gin.H{"error": "failed to generate token", "details": err.Error()})
			return
		}

		// Return token
		response := TokenResponse{
			Token:     token,
			ExpiresAt: time.Now().Add(TokenValidityPeriod),
			TokenType: "Bearer",
		}

		c.JSON(http.StatusOK, response)
	}
}
