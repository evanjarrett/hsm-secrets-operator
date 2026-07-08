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
	"crypto/tls"
	"crypto/x509"
	"sync/atomic"

	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/pki"
)

// ClientTLS holds the manager's mutual-TLS material for dialing agents. The
// client leaf is stored behind an atomic pointer so rotation can swap it without
// disturbing existing pooled connections: new handshakes pick up the new leaf,
// established connections keep working.
//
// Because the manager dials ephemeral agent pod IPs, verification pins
// ServerName to pki.ServerCertDNSName rather than the dialed address, letting a
// single shared server certificate authenticate every agent.
type ClientTLS struct {
	leaf       atomic.Pointer[tls.Certificate]
	rootCAs    *x509.CertPool
	serverName string
}

// NewClientTLS builds a ClientTLS holder from an issued PKI bundle.
func NewClientTLS(bundle *pki.Bundle) *ClientTLS {
	ct := &ClientTLS{
		rootCAs:    bundle.CAPool,
		serverName: pki.ServerCertDNSName,
	}
	ct.leaf.Store(bundle.ClientCert)
	return ct
}

// SwapLeaf atomically replaces the client leaf presented on new handshakes.
func (c *ClientTLS) SwapLeaf(cert *tls.Certificate) {
	c.leaf.Store(cert)
}

// Config returns a *tls.Config suitable for grpc credentials.NewTLS. The
// GetClientCertificate callback reads the current leaf on each handshake so
// rotation takes effect without rebuilding the config.
func (c *ClientTLS) Config() *tls.Config {
	return &tls.Config{
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return c.leaf.Load(), nil
		},
		RootCAs:    c.rootCAs,
		ServerName: c.serverName,
		MinVersion: tls.VersionTLS13,
	}
}
