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
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"

	"google.golang.org/grpc/credentials"
)

const (
	// Certificate validity period
	CertValidityPeriod = 365 * 24 * time.Hour // 1 year
	// Key size for RSA keys
	RSAKeySize = 2048
)

// TLSConfig represents TLS configuration for internal communication
type TLSConfig struct {
	CertPEM []byte
	KeyPEM  []byte
	CAPEM   []byte
}

// CertificateManager manages internal TLS certificates for gRPC communication
type CertificateManager struct {
	caCert *x509.Certificate
	caKey  *rsa.PrivateKey
}

// NewCertificateManager creates a new certificate manager with a self-signed CA
func NewCertificateManager() (*CertificateManager, error) {
	// Generate CA private key
	caKey, err := rsa.GenerateKey(rand.Reader, RSAKeySize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CA private key: %w", err)
	}

	// Create CA certificate template
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{"HSM Secrets Operator"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{""},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
			CommonName:    "HSM Secrets Operator CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(CertValidityPeriod),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	// Create self-signed CA certificate
	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	return &CertificateManager{
		caCert: caCert,
		caKey:  caKey,
	}, nil
}

// GenerateServerCert generates a server certificate for the HSM agent
func (cm *CertificateManager) GenerateServerCert(serviceName, namespace string, dnsNames []string, ips []net.IP) (*TLSConfig, error) {
	// Generate server private key
	serverKey, err := rsa.GenerateKey(rand.Reader, RSAKeySize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate server private key: %w", err)
	}

	// Prepare DNS names for the certificate
	serverDNSNames := []string{
		serviceName,
		fmt.Sprintf("%s.%s", serviceName, namespace),
		fmt.Sprintf("%s.%s.svc", serviceName, namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace),
	}
	serverDNSNames = append(serverDNSNames, dnsNames...)

	// Prepare IP addresses
	serverIPs := []net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("::1"),
	}
	serverIPs = append(serverIPs, ips...)

	// Create server certificate template
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().Unix()),
		Subject: pkix.Name{
			Organization: []string{"HSM Secrets Operator"},
			CommonName:   fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace),
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(CertValidityPeriod),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		DNSNames:     serverDNSNames,
		IPAddresses:  serverIPs,
	}

	// Create server certificate signed by CA
	serverCertDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, cm.caCert, &serverKey.PublicKey, cm.caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create server certificate: %w", err)
	}

	// Encode certificates and key to PEM format
	serverCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverCertDER,
	})

	serverKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(serverKey),
	})

	caCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cm.caCert.Raw,
	})

	return &TLSConfig{
		CertPEM: serverCertPEM,
		KeyPEM:  serverKeyPEM,
		CAPEM:   caCertPEM,
	}, nil
}

// GenerateClientCert generates a client certificate for the manager
func (cm *CertificateManager) GenerateClientCert(clientName string) (*TLSConfig, error) {
	// Generate client private key
	clientKey, err := rsa.GenerateKey(rand.Reader, RSAKeySize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate client private key: %w", err)
	}

	// Create client certificate template
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().Unix() + 1),
		Subject: pkix.Name{
			Organization: []string{"HSM Secrets Operator"},
			CommonName:   clientName,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(CertValidityPeriod),
		SubjectKeyId:          []byte{1, 2, 3, 4, 5},
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	// Create client certificate signed by CA
	clientCertDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, cm.caCert, &clientKey.PublicKey, cm.caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create client certificate: %w", err)
	}

	// Encode certificates and key to PEM format
	clientCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: clientCertDER,
	})

	clientKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(clientKey),
	})

	caCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cm.caCert.Raw,
	})

	return &TLSConfig{
		CertPEM: clientCertPEM,
		KeyPEM:  clientKeyPEM,
		CAPEM:   caCertPEM,
	}, nil
}

// GetServerCredentials returns gRPC server credentials from TLS config
func (config *TLSConfig) GetServerCredentials() (credentials.TransportCredentials, error) {
	cert, err := tls.X509KeyPair(config.CertPEM, config.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(config.CAPEM) {
		return nil, fmt.Errorf("failed to add CA certificate to pool")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS12,
	}

	return credentials.NewTLS(tlsConfig), nil
}

// GetClientCredentials returns gRPC client credentials from TLS config
func (config *TLSConfig) GetClientCredentials(serverName string) (credentials.TransportCredentials, error) {
	cert, err := tls.X509KeyPair(config.CertPEM, config.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(config.CAPEM) {
		return nil, fmt.Errorf("failed to add CA certificate to pool")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS12,
	}

	return credentials.NewTLS(tlsConfig), nil
}

// GetCACertPEM returns the CA certificate in PEM format
func (cm *CertificateManager) GetCACertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cm.caCert.Raw,
	})
}
