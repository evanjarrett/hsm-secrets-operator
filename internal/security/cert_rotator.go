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
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Default rotation interval (30 days)
	DefaultRotationInterval = 30 * 24 * time.Hour
	// Renewal threshold (renew when 1/3 of lifetime remains)
	DefaultRenewalThreshold = CertValidityPeriod / 3
	// Minimum rotation interval (prevent too frequent rotation)
	MinRotationInterval = 1 * time.Hour
)

// CertificateRotator manages automatic certificate rotation
type CertificateRotator struct {
	certManager      *CertificateManager
	k8sClient        client.Client
	rotationInterval time.Duration
	renewalThreshold time.Duration
	logger           logr.Logger
	stopCh           chan struct{}
	mu               sync.RWMutex
	currentTLSConfig *TLSConfig
	updateCallbacks  []TLSUpdateCallback
	namespace        string
	secretName       string
}

// TLSUpdateCallback is called when certificates are rotated
type TLSUpdateCallback func(newTLSConfig *TLSConfig) error

// RotatorConfig configures the certificate rotator
type RotatorConfig struct {
	RotationInterval time.Duration
	RenewalThreshold time.Duration
	Namespace        string
	SecretName       string
	ServiceName      string
	DNSNames         []string
	IPs              []net.IP
}

// DefaultRotatorConfig returns default configuration
func DefaultRotatorConfig(namespace, secretName, serviceName string) RotatorConfig {
	return RotatorConfig{
		RotationInterval: DefaultRotationInterval,
		RenewalThreshold: DefaultRenewalThreshold,
		Namespace:        namespace,
		SecretName:       secretName,
		ServiceName:      serviceName,
		DNSNames:         []string{},
		IPs:              []net.IP{},
	}
}

// NewCertificateRotator creates a new certificate rotator
func NewCertificateRotator(k8sClient client.Client, config RotatorConfig, logger logr.Logger) (*CertificateRotator, error) {
	// Allow short intervals for testing
	if config.RotationInterval < MinRotationInterval && config.RotationInterval >= 100*time.Millisecond {
		logger.Info("Using short rotation interval for testing", "interval", config.RotationInterval)
	} else if config.RotationInterval < MinRotationInterval {
		return nil, fmt.Errorf("rotation interval %v is too short, minimum is %v", config.RotationInterval, MinRotationInterval)
	}

	certManager, err := NewCertificateManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate manager: %w", err)
	}

	return &CertificateRotator{
		certManager:      certManager,
		k8sClient:        k8sClient,
		rotationInterval: config.RotationInterval,
		renewalThreshold: config.RenewalThreshold,
		logger:           logger.WithName("cert-rotator"),
		stopCh:           make(chan struct{}),
		updateCallbacks:  make([]TLSUpdateCallback, 0),
		namespace:        config.Namespace,
		secretName:       config.SecretName,
	}, nil
}

// AddUpdateCallback registers a callback for certificate updates
func (cr *CertificateRotator) AddUpdateCallback(callback TLSUpdateCallback) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.updateCallbacks = append(cr.updateCallbacks, callback)
}

// GetCurrentTLSConfig returns the current TLS configuration
func (cr *CertificateRotator) GetCurrentTLSConfig() *TLSConfig {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	return cr.currentTLSConfig
}

// Start begins the certificate rotation process
func (cr *CertificateRotator) Start(ctx context.Context, serviceName string, dnsNames []string, ips []net.IP) error {
	cr.logger.Info("Starting certificate rotator",
		"rotation-interval", cr.rotationInterval,
		"renewal-threshold", cr.renewalThreshold,
		"service", serviceName,
		"secret", fmt.Sprintf("%s/%s", cr.namespace, cr.secretName))

	// Try to load existing certificates from Kubernetes secret
	existingTLS, err := cr.loadCertificatesFromSecret(ctx)
	if err != nil {
		cr.logger.Info("No existing certificates found, generating new ones", "error", err)
		existingTLS = nil
	}

	// Check if we need to generate new certificates
	if existingTLS == nil || cr.needsRenewal(existingTLS) {
		cr.logger.Info("Generating new certificates")
		newTLS, err := cr.generateAndStoreCertificates(ctx, serviceName, dnsNames, ips)
		if err != nil {
			return fmt.Errorf("failed to generate initial certificates: %w", err)
		}
		cr.setCurrentTLSConfig(newTLS)
	} else {
		cr.logger.Info("Using existing valid certificates")
		cr.setCurrentTLSConfig(existingTLS)
	}

	// Start rotation loop
	go cr.rotationLoop(ctx, serviceName, dnsNames, ips)

	return nil
}

// Stop stops the certificate rotator
func (cr *CertificateRotator) Stop() {
	close(cr.stopCh)
}

// rotationLoop runs the periodic certificate rotation
func (cr *CertificateRotator) rotationLoop(ctx context.Context, serviceName string, dnsNames []string, ips []net.IP) {
	ticker := time.NewTicker(cr.rotationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cr.logger.Info("Checking certificate renewal")
			if cr.needsRenewal(cr.GetCurrentTLSConfig()) {
				if err := cr.rotateCertificates(ctx, serviceName, dnsNames, ips); err != nil {
					cr.logger.Error(err, "Failed to rotate certificates")
				}
			} else {
				cr.logger.Info("Certificates still valid, no rotation needed")
			}
		case <-cr.stopCh:
			cr.logger.Info("Certificate rotator stopping")
			return
		case <-ctx.Done():
			cr.logger.Info("Certificate rotator context cancelled")
			return
		}
	}
}

// rotateCertificates generates new certificates and updates all components
func (cr *CertificateRotator) rotateCertificates(ctx context.Context, serviceName string, dnsNames []string, ips []net.IP) error {
	cr.logger.Info("Rotating certificates", "service", serviceName)

	newTLS, err := cr.generateAndStoreCertificates(ctx, serviceName, dnsNames, ips)
	if err != nil {
		return fmt.Errorf("failed to generate new certificates: %w", err)
	}

	// Update current config
	cr.setCurrentTLSConfig(newTLS)

	// Notify all callbacks
	cr.mu.RLock()
	callbacks := make([]TLSUpdateCallback, len(cr.updateCallbacks))
	copy(callbacks, cr.updateCallbacks)
	cr.mu.RUnlock()

	for i, callback := range callbacks {
		if err := callback(newTLS); err != nil {
			cr.logger.Error(err, "Certificate update callback failed", "callback-index", i)
		}
	}

	cr.logger.Info("Certificate rotation completed successfully")
	return nil
}

// generateAndStoreCertificates creates new certificates and stores them in Kubernetes
func (cr *CertificateRotator) generateAndStoreCertificates(ctx context.Context, serviceName string, dnsNames []string, ips []net.IP) (*TLSConfig, error) {
	// Generate server certificate
	serverTLS, err := cr.certManager.GenerateServerCert(serviceName, cr.namespace, dnsNames, ips)
	if err != nil {
		return nil, fmt.Errorf("failed to generate server certificate: %w", err)
	}

	// Store certificates in Kubernetes secret
	if err := cr.storeCertificatesInSecret(ctx, serverTLS); err != nil {
		return nil, fmt.Errorf("failed to store certificates: %w", err)
	}

	return serverTLS, nil
}

// loadCertificatesFromSecret loads existing certificates from Kubernetes secret
func (cr *CertificateRotator) loadCertificatesFromSecret(ctx context.Context) (*TLSConfig, error) {
	secret := &corev1.Secret{}
	err := cr.k8sClient.Get(ctx, types.NamespacedName{
		Name:      cr.secretName,
		Namespace: cr.namespace,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", cr.namespace, cr.secretName, err)
	}

	certPEM, ok := secret.Data["tls.crt"]
	if !ok {
		return nil, fmt.Errorf("certificate not found in secret")
	}

	keyPEM, ok := secret.Data["tls.key"]
	if !ok {
		return nil, fmt.Errorf("private key not found in secret")
	}

	caPEM, ok := secret.Data["ca.crt"]
	if !ok {
		return nil, fmt.Errorf("CA certificate not found in secret")
	}

	return &TLSConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
		CAPEM:   caPEM,
	}, nil
}

// storeCertificatesInSecret stores certificates in Kubernetes secret
func (cr *CertificateRotator) storeCertificatesInSecret(ctx context.Context, tlsConfig *TLSConfig) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.secretName,
			Namespace: cr.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "hsm-secrets-operator",
				"app.kubernetes.io/component":  "tls",
				"app.kubernetes.io/managed-by": "hsm-certificate-rotator",
			},
			Annotations: map[string]string{
				"hsm.j5t.io/rotation-time": time.Now().Format(time.RFC3339),
				"hsm.j5t.io/cert-expiry":   cr.getCertExpiry(tlsConfig.CertPEM).Format(time.RFC3339),
			},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": tlsConfig.CertPEM,
			"tls.key": tlsConfig.KeyPEM,
			"ca.crt":  tlsConfig.CAPEM,
		},
	}

	// Try to update existing secret first
	existing := &corev1.Secret{}
	err := cr.k8sClient.Get(ctx, types.NamespacedName{
		Name:      cr.secretName,
		Namespace: cr.namespace,
	}, existing)

	if err == nil {
		// Update existing secret
		existing.Data = secret.Data
		existing.Annotations = secret.Annotations
		return cr.k8sClient.Update(ctx, existing)
	} else {
		// Create new secret
		return cr.k8sClient.Create(ctx, secret)
	}
}

// needsRenewal checks if certificates need renewal
func (cr *CertificateRotator) needsRenewal(tlsConfig *TLSConfig) bool {
	if tlsConfig == nil {
		return true
	}

	expiry := cr.getCertExpiry(tlsConfig.CertPEM)
	timeUntilExpiry := time.Until(expiry)

	// Renew if less than renewal threshold remains
	needsRenewal := timeUntilExpiry < cr.renewalThreshold

	cr.logger.Info("Certificate renewal check",
		"expiry", expiry.Format(time.RFC3339),
		"time-until-expiry", timeUntilExpiry,
		"renewal-threshold", cr.renewalThreshold,
		"needs-renewal", needsRenewal)

	return needsRenewal
}

// getCertExpiry extracts expiry time from certificate PEM
func (cr *CertificateRotator) getCertExpiry(certPEM []byte) time.Time {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		cr.logger.Error(fmt.Errorf("failed to decode certificate PEM"), "Invalid PEM data")
		return time.Now() // Force renewal on error
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		cr.logger.Error(err, "Failed to parse certificate")
		return time.Now() // Force renewal on error
	}

	return cert.NotAfter
}

// setCurrentTLSConfig updates the current TLS configuration thread-safely
func (cr *CertificateRotator) setCurrentTLSConfig(tlsConfig *TLSConfig) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.currentTLSConfig = tlsConfig
}

// GetCertificateInfo returns information about the current certificate
func (cr *CertificateRotator) GetCertificateInfo() map[string]interface{} {
	tlsConfig := cr.GetCurrentTLSConfig()
	if tlsConfig == nil {
		return map[string]interface{}{
			"status": "no-certificate",
		}
	}

	expiry := cr.getCertExpiry(tlsConfig.CertPEM)
	timeUntilExpiry := time.Until(expiry)

	return map[string]interface{}{
		"status":            "active",
		"expiry":            expiry.Format(time.RFC3339),
		"time_until_expiry": timeUntilExpiry.String(),
		"needs_renewal":     cr.needsRenewal(tlsConfig),
		"rotation_interval": cr.rotationInterval.String(),
		"renewal_threshold": cr.renewalThreshold.String(),
	}
}
