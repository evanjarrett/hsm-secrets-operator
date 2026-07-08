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

// Package pki provides a self-managed certificate authority and leaf-certificate
// issuance for securing the manager <-> agent gRPC channel with mutual TLS.
//
// The manager dials ephemeral agent pod IPs rather than a stable Service DNS
// name, so leaf SANs cannot encode a routable identity. Instead the client pins
// tls.Config.ServerName to ServerCertDNSName and the server leaf is issued with
// that name in its DNSNames, allowing a single shared server certificate to
// authenticate every agent regardless of pod IP.
//
// The package is pure standard library (crypto/ecdsa, crypto/x509, crypto/tls)
// and holds no external dependencies.
package pki

import "time"

const (
	// ServerCertDNSName is the SAN pinned on the agent server certificate and
	// the ServerName the manager verifies against. It is a logical identity, not
	// a routable hostname.
	ServerCertDNSName = "hsm-agent"

	// ClientCertDNSName is the identity encoded in the manager client
	// certificate's subject/SAN.
	ClientCertDNSName = "hsm-manager"

	// DefaultLeafValidity is how long an issued leaf certificate is valid.
	DefaultLeafValidity = 90 * 24 * time.Hour

	// DefaultRenewBefore is how much validity must remain before a leaf is
	// considered due for rotation.
	DefaultRenewBefore = 30 * 24 * time.Hour

	// CAValidity is the lifetime of the self-managed certificate authority.
	CAValidity = 10 * 365 * 24 * time.Hour

	// pemTypeCertificate is the PEM block type for X.509 certificates.
	pemTypeCertificate = "CERTIFICATE"
)

// clockNow is the time source used when issuing and evaluating certificates.
// It is a package variable so tests can substitute a deterministic clock.
var clockNow = time.Now
