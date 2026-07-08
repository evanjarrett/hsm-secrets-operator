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

package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"time"
)

// Leaf is an issued leaf certificate together with its private key, in PEM form.
type Leaf struct {
	CertPEM []byte
	KeyPEM  []byte
}

// IssueServerCert issues a serverAuth leaf for the agent gRPC server. The leaf
// carries dnsName as its single SAN; the manager pins this name via
// tls.Config.ServerName, so one certificate authenticates every agent.
func (ca *CA) IssueServerCert(dnsName string, validity time.Duration) (*Leaf, error) {
	return ca.issueLeaf(dnsName, dnsName, validity, x509.ExtKeyUsageServerAuth)
}

// IssueClientCert issues a clientAuth leaf for the manager identity.
func (ca *CA) IssueClientCert(name string, validity time.Duration) (*Leaf, error) {
	return ca.issueLeaf(name, name, validity, x509.ExtKeyUsageClientAuth)
}

// issueLeaf signs a new ECDSA P-256 leaf certificate with the given SAN and EKU.
func (ca *CA) issueLeaf(commonName, dnsName string, validity time.Duration, eku x509.ExtKeyUsage) (*Leaf, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate leaf key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	notBefore := clockNow().Add(-1 * time.Minute)
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"hsm-secrets-operator"},
		},
		NotBefore:             notBefore,
		NotAfter:              clockNow().Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{eku},
		BasicConstraintsValid: true,
		DNSNames:              []string{dnsName},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create leaf certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: pemTypeCertificate, Bytes: der})
	keyPEM, err := marshalECKey(key)
	if err != nil {
		return nil, err
	}

	return &Leaf{CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

// NeedsRotation reports whether the leaf in certPEM is missing, unparseable, or
// within renewBefore of its expiry and therefore due for re-issuance.
func NeedsRotation(certPEM []byte, renewBefore time.Duration) bool {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != pemTypeCertificate {
		return true
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true
	}
	return clockNow().After(cert.NotAfter.Add(-renewBefore))
}
