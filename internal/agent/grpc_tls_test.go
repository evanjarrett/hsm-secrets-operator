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
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	hsmv1 "tangled.org/evan.jarrett.net/hsm-secrets-operator/api/proto/hsm/v1"
	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/hsm"
	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/pki"
)

// freePort returns an available localhost TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := lis.Addr().(*net.TCPAddr).Port
	require.NoError(t, lis.Close())
	return port
}

// caPoolFor builds a trust pool from a CA's certificate.
func caPoolFor(t *testing.T, ca *pki.CA) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(ca.CertPEM()))
	return pool
}

// startTLSAgent stands up a real mTLS GRPCServer serving the given CA's server
// leaf on a fresh localhost port and returns its endpoint.
func startTLSAgent(t *testing.T, ca *pki.CA) string {
	t.Helper()

	serverLeaf, err := ca.IssueServerCert(pki.ServerCertDNSName, pki.DefaultLeafValidity)
	require.NoError(t, err)

	dir := t.TempDir()
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")
	caFile := filepath.Join(dir, "ca.crt")
	require.NoError(t, os.WriteFile(certFile, serverLeaf.CertPEM, 0o600))
	require.NoError(t, os.WriteFile(keyFile, serverLeaf.KeyPEM, 0o600))
	require.NoError(t, os.WriteFile(caFile, ca.CertPEM(), 0o600))

	mockHSM := hsm.NewMockClient()
	require.NoError(t, mockHSM.Initialize(context.Background(), hsm.Config{}))

	port := freePort(t)
	server := NewGRPCServer(mockHSM, port, freePort(t), logr.Discard())
	server.SetTLSFiles(certFile, keyFile, caFile)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		if err := server.Start(ctx); err != nil {
			t.Logf("tls agent server stopped: %v", err)
		}
	}()

	endpoint := "127.0.0.1:" + strconv.Itoa(port)
	waitForListener(t, endpoint)
	return endpoint
}

// waitForListener polls until endpoint accepts TCP connections, so tests do not
// depend on a fixed sleep to race the server goroutine coming up.
func waitForListener(t *testing.T, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", endpoint, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tls agent server did not start listening on %s within timeout", endpoint)
}

// dialAndPing dials endpoint with the given transport creds and issues one RPC,
// returning any error (handshake failures surface on the first RPC).
func dialAndPing(t *testing.T, endpoint string, creds credentials.TransportCredentials) error {
	t.Helper()
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = hsmv1.NewHSMAgentClient(conn).GetInfo(ctx, &hsmv1.GetInfoRequest{})
	return err
}

func TestAgentMTLS_ValidClientSucceeds(t *testing.T) {
	ca, err := pki.GenerateCA()
	require.NoError(t, err)
	endpoint := startTLSAgent(t, ca)

	clientLeaf, err := ca.IssueClientCert(pki.ClientCertDNSName, pki.DefaultLeafValidity)
	require.NoError(t, err)
	pair, err := tls.X509KeyPair(clientLeaf.CertPEM, clientLeaf.KeyPEM)
	require.NoError(t, err)

	bundle := &pki.Bundle{CAPool: caPoolFor(t, ca), CACertPEM: ca.CertPEM(), ClientCert: &pair}
	clientTLS := NewClientTLS(bundle)

	client, err := NewGRPCClient(endpoint, logr.Discard(), clientTLS)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	info, err := client.GetInfo(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Mock HSM Token", info.Label)
}

func TestAgentMTLS_ClientFromDifferentCARejected(t *testing.T) {
	ca, err := pki.GenerateCA()
	require.NoError(t, err)
	endpoint := startTLSAgent(t, ca)

	// Client leaf issued by an untrusted CA; trust the real CA for the server so
	// only the client-auth step fails.
	otherCA, err := pki.GenerateCA()
	require.NoError(t, err)
	rogueLeaf, err := otherCA.IssueClientCert(pki.ClientCertDNSName, pki.DefaultLeafValidity)
	require.NoError(t, err)
	pair, err := tls.X509KeyPair(rogueLeaf.CertPEM, rogueLeaf.KeyPEM)
	require.NoError(t, err)

	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{pair},
		RootCAs:      caPoolFor(t, ca),
		ServerName:   pki.ServerCertDNSName,
		MinVersion:   tls.VersionTLS13,
	})
	require.Error(t, dialAndPing(t, endpoint, creds))
}

func TestAgentMTLS_NoClientCertRejected(t *testing.T) {
	ca, err := pki.GenerateCA()
	require.NoError(t, err)
	endpoint := startTLSAgent(t, ca)

	// Trust the server but present no client certificate.
	creds := credentials.NewTLS(&tls.Config{
		RootCAs:    caPoolFor(t, ca),
		ServerName: pki.ServerCertDNSName,
		MinVersion: tls.VersionTLS13,
	})
	require.Error(t, dialAndPing(t, endpoint, creds))
}

func TestAgentMTLS_WrongServerNameRejected(t *testing.T) {
	ca, err := pki.GenerateCA()
	require.NoError(t, err)
	endpoint := startTLSAgent(t, ca)

	clientLeaf, err := ca.IssueClientCert(pki.ClientCertDNSName, pki.DefaultLeafValidity)
	require.NoError(t, err)
	pair, err := tls.X509KeyPair(clientLeaf.CertPEM, clientLeaf.KeyPEM)
	require.NoError(t, err)

	// Valid client cert, but the pinned ServerName does not match the server
	// leaf's SAN, so server-cert verification fails on the client side.
	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{pair},
		RootCAs:      caPoolFor(t, ca),
		ServerName:   "wrong-name",
		MinVersion:   tls.VersionTLS13,
	})
	require.Error(t, dialAndPing(t, endpoint, creds))
}
