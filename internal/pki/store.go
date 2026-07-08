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
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"maps"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ManagedByAnnotation marks a PKI Secret's provenance. When absent or equal to
// SelfManagedValue the operator self-manages the Secret; any other value means
// an external controller (e.g. cert-manager) owns it and the operator must not
// generate or rotate its contents.
const (
	ManagedByAnnotation = "hsm.j5t.io/mtls-managed-by"
	SelfManagedValue    = "hsm-secrets-operator"
)

// Config parameterizes PKI Secret management.
type Config struct {
	Namespace        string
	CASecretName     string
	ServerSecretName string
	ClientSecretName string
	LeafValidity     time.Duration
	RenewBefore      time.Duration
	// CertManagerEnabled signals that an external controller populates and
	// rotates the PKI Secrets. When true, EnsurePKI reads existing material and
	// never generates or overwrites Secrets.
	CertManagerEnabled bool
}

// Bundle is the in-memory PKI material the manager needs at runtime: the trust
// pool for verifying agent server certs and the manager's own client leaf.
type Bundle struct {
	CAPool           *x509.CertPool
	CACertPEM        []byte
	ClientCert       *tls.Certificate
	ServerSecretName string
}

// EnsurePKI is idempotent: it get-or-creates the CA, server, and client Secrets,
// re-issuing any leaf that is missing or within RenewBefore of expiry, and
// returns the runtime Bundle. Secrets are left un-owned so they survive pod
// restarts. When cfg.CertManagerEnabled is set, it only reads existing Secrets.
func EnsurePKI(ctx context.Context, c client.Client, cfg Config) (*Bundle, error) {
	if cfg.LeafValidity == 0 {
		cfg.LeafValidity = DefaultLeafValidity
	}
	if cfg.RenewBefore == 0 {
		cfg.RenewBefore = DefaultRenewBefore
	}

	if cfg.CertManagerEnabled {
		return readExternalBundle(ctx, c, cfg)
	}

	ca, err := ensureCA(ctx, c, cfg)
	if err != nil {
		return nil, err
	}

	if err := ensureLeafSecret(ctx, c, cfg, cfg.ServerSecretName, ca, func() (*Leaf, error) {
		return ca.IssueServerCert(ServerCertDNSName, cfg.LeafValidity)
	}); err != nil {
		return nil, fmt.Errorf("ensuring server Secret: %w", err)
	}

	clientLeaf, err := ensureClientLeafSecret(ctx, c, cfg, ca)
	if err != nil {
		return nil, fmt.Errorf("ensuring client Secret: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.CertPEM()) {
		return nil, fmt.Errorf("failed to build CA trust pool")
	}

	return &Bundle{
		CAPool:           pool,
		CACertPEM:        ca.CertPEM(),
		ClientCert:       clientLeaf,
		ServerSecretName: cfg.ServerSecretName,
	}, nil
}

// ensureCA loads the CA Secret, generating and persisting a new CA if absent.
func ensureCA(ctx context.Context, c client.Client, cfg Config) (*CA, error) {
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.CASecretName}
	err := c.Get(ctx, key, &secret)
	if err == nil {
		return LoadCA(secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey])
	}
	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("getting CA Secret: %w", err)
	}

	ca, err := GenerateCA()
	if err != nil {
		return nil, err
	}
	caSecret := newPKISecret(cfg.Namespace, cfg.CASecretName, map[string][]byte{
		corev1.TLSCertKey:       ca.CertPEM(),
		corev1.TLSPrivateKeyKey: ca.KeyPEM(),
	})
	if err := c.Create(ctx, caSecret); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Lost a create race; load the winner.
			if getErr := c.Get(ctx, key, &secret); getErr == nil {
				return LoadCA(secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey])
			}
		}
		return nil, fmt.Errorf("creating CA Secret: %w", err)
	}
	return ca, nil
}

// ensureLeafSecret get-or-creates a leaf Secret (ca.crt/tls.crt/tls.key), issuing
// via issue when the Secret is missing or its leaf needs rotation.
func ensureLeafSecret(ctx context.Context, c client.Client, cfg Config, name string, ca *CA, issue func() (*Leaf, error)) error {
	_, _, err := getOrIssueLeaf(ctx, c, cfg, name, ca, issue)
	return err
}

// ensureClientLeafSecret behaves like ensureLeafSecret but returns the parsed
// client leaf for in-memory use by the manager.
func ensureClientLeafSecret(ctx context.Context, c client.Client, cfg Config, ca *CA) (*tls.Certificate, error) {
	certPEM, keyPEM, err := getOrIssueLeaf(ctx, c, cfg, cfg.ClientSecretName, ca, func() (*Leaf, error) {
		return ca.IssueClientCert(ClientCertDNSName, cfg.LeafValidity)
	})
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parsing client leaf: %w", err)
	}
	return &pair, nil
}

// getOrIssueLeaf returns the current leaf cert/key PEM for the named Secret,
// creating or updating it as needed. It returns the effective cert/key PEM.
func getOrIssueLeaf(ctx context.Context, c client.Client, cfg Config, name string, ca *CA, issue func() (*Leaf, error)) (certPEM, keyPEM []byte, err error) {
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: cfg.Namespace, Name: name}
	getErr := c.Get(ctx, key, &secret)
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return nil, nil, fmt.Errorf("getting Secret %s: %w", name, getErr)
	}

	exists := getErr == nil

	// Respect externally-managed Secrets: trust their contents as-is.
	if exists && isExternallyManaged(&secret) {
		return secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey], nil
	}

	if exists && !NeedsRotation(secret.Data[corev1.TLSCertKey], cfg.RenewBefore) {
		return secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey], nil
	}

	leaf, err := issue()
	if err != nil {
		return nil, nil, err
	}
	data := map[string][]byte{
		corev1.ServiceAccountRootCAKey: ca.CertPEM(),
		corev1.TLSCertKey:              leaf.CertPEM,
		corev1.TLSPrivateKeyKey:        leaf.KeyPEM,
	}

	if !exists {
		newSecret := newPKISecret(cfg.Namespace, name, data)
		if createErr := c.Create(ctx, newSecret); createErr != nil {
			return nil, nil, fmt.Errorf("creating Secret %s: %w", name, createErr)
		}
		return leaf.CertPEM, leaf.KeyPEM, nil
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	maps.Copy(secret.Data, data)
	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[ManagedByAnnotation] = SelfManagedValue
	if updateErr := c.Update(ctx, &secret); updateErr != nil {
		return nil, nil, fmt.Errorf("updating Secret %s: %w", name, updateErr)
	}
	return leaf.CertPEM, leaf.KeyPEM, nil
}

// readExternalBundle builds a Bundle from Secrets populated by an external
// controller (cert-manager). It does not generate or modify any Secret.
func readExternalBundle(ctx context.Context, c client.Client, cfg Config) (*Bundle, error) {
	var clientSecret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.ClientSecretName}, &clientSecret); err != nil {
		return nil, fmt.Errorf("getting externally-managed client Secret %s: %w", cfg.ClientSecretName, err)
	}
	caPEM := clientSecret.Data[corev1.ServiceAccountRootCAKey]
	if len(caPEM) == 0 {
		return nil, fmt.Errorf("externally-managed client Secret %s missing %s", cfg.ClientSecretName, corev1.ServiceAccountRootCAKey)
	}
	pair, err := tls.X509KeyPair(clientSecret.Data[corev1.TLSCertKey], clientSecret.Data[corev1.TLSPrivateKeyKey])
	if err != nil {
		return nil, fmt.Errorf("parsing externally-managed client leaf: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to build CA trust pool from externally-managed Secret")
	}
	return &Bundle{
		CAPool:           pool,
		CACertPEM:        caPEM,
		ClientCert:       &pair,
		ServerSecretName: cfg.ServerSecretName,
	}, nil
}

// isExternallyManaged reports whether a Secret is owned by an external
// controller rather than self-managed by the operator.
func isExternallyManaged(secret *corev1.Secret) bool {
	v, ok := secret.Annotations[ManagedByAnnotation]
	return ok && v != SelfManagedValue
}

// newPKISecret builds a TLS Secret carrying the self-managed provenance
// annotation. Secrets are intentionally left without owner references.
func newPKISecret(namespace, name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": SelfManagedValue,
				"app.kubernetes.io/component":  "mtls",
			},
			Annotations: map[string]string{
				ManagedByAnnotation: SelfManagedValue,
			},
		},
		Type: corev1.SecretTypeTLS,
		Data: data,
	}
}
