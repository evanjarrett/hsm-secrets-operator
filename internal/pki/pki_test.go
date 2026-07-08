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
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
)

func parseLeaf(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("failed to decode leaf PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse leaf: %v", err)
	}
	return cert
}

func caPool(t *testing.T, ca *CA) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.CertPEM()) {
		t.Fatal("failed to append CA cert to pool")
	}
	return pool
}

func TestGenerateCA(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	if !ca.Certificate().IsCA {
		t.Error("CA cert IsCA = false, want true")
	}
	if ca.Certificate().KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("CA cert missing KeyUsageCertSign")
	}
}

func TestLoadCARoundTrip(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	loaded, err := LoadCA(ca.CertPEM(), ca.KeyPEM())
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	// A leaf issued by the reloaded CA must still verify against the original.
	leaf, err := loaded.IssueServerCert(ServerCertDNSName, DefaultLeafValidity)
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}
	verifyLeaf(t, ca, leaf.CertPEM, ServerCertDNSName, x509.ExtKeyUsageServerAuth)
}

func TestServerCertVerifies(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	leaf, err := ca.IssueServerCert(ServerCertDNSName, DefaultLeafValidity)
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}

	cert := parseLeaf(t, leaf.CertPEM)
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("server leaf EKU = %v, want serverAuth", cert.ExtKeyUsage)
	}

	// Correct ServerName verifies.
	verifyLeaf(t, ca, leaf.CertPEM, ServerCertDNSName, x509.ExtKeyUsageServerAuth)

	// Wrong ServerName fails.
	if _, err := cert.Verify(x509.VerifyOptions{
		DNSName:   "wrong-name",
		Roots:     caPool(t, ca),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err == nil {
		t.Error("expected verification failure for wrong ServerName, got nil")
	}
}

func TestClientCertVerifies(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	leaf, err := ca.IssueClientCert(ClientCertDNSName, DefaultLeafValidity)
	if err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	cert := parseLeaf(t, leaf.CertPEM)
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Errorf("client leaf EKU = %v, want clientAuth", cert.ExtKeyUsage)
	}
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     caPool(t, ca),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Errorf("client leaf failed to verify: %v", err)
	}
}

func TestLeafFromDifferentCADoesNotVerify(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	other, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA(other): %v", err)
	}
	leaf, err := other.IssueServerCert(ServerCertDNSName, DefaultLeafValidity)
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}
	cert := parseLeaf(t, leaf.CertPEM)
	if _, err := cert.Verify(x509.VerifyOptions{
		DNSName:   ServerCertDNSName,
		Roots:     caPool(t, ca),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err == nil {
		t.Error("expected verification failure for leaf from a different CA, got nil")
	}
}

func TestNeedsRotation(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	fresh, err := ca.IssueServerCert(ServerCertDNSName, DefaultLeafValidity)
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}
	if NeedsRotation(fresh.CertPEM, DefaultRenewBefore) {
		t.Error("fresh 90d leaf reported as needing rotation")
	}

	shortLived, err := ca.IssueServerCert(ServerCertDNSName, time.Second)
	if err != nil {
		t.Fatalf("IssueServerCert(short): %v", err)
	}
	// A 1s leaf is already within any positive renewBefore window.
	if !NeedsRotation(shortLived.CertPEM, DefaultRenewBefore) {
		t.Error("1s leaf reported as not needing rotation")
	}

	// Garbage / missing PEM always needs rotation.
	if !NeedsRotation([]byte("not a cert"), DefaultRenewBefore) {
		t.Error("invalid PEM reported as not needing rotation")
	}
	if !NeedsRotation(nil, DefaultRenewBefore) {
		t.Error("nil PEM reported as not needing rotation")
	}
}

// verifyLeaf asserts that certPEM chains to ca, matches dnsName, and satisfies eku.
func verifyLeaf(t *testing.T, ca *CA, certPEM []byte, dnsName string, eku x509.ExtKeyUsage) {
	t.Helper()
	cert := parseLeaf(t, certPEM)
	if _, err := cert.Verify(x509.VerifyOptions{
		DNSName:   dnsName,
		Roots:     caPool(t, ca),
		KeyUsages: []x509.ExtKeyUsage{eku},
	}); err != nil {
		t.Fatalf("leaf verification failed: %v", err)
	}
}
