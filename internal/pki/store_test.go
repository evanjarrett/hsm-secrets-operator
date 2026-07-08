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
	"bytes"
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testConfig() Config {
	return Config{
		Namespace:        "test-ns",
		CASecretName:     "hsm-mtls-ca",
		ServerSecretName: "hsm-agent-server-tls",
		ClientSecretName: "hsm-manager-client-tls",
		LeafValidity:     DefaultLeafValidity,
		RenewBefore:      DefaultRenewBefore,
	}
}

func TestEnsurePKICreatesSecrets(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	cfg := testConfig()

	bundle, err := EnsurePKI(context.Background(), c, cfg)
	if err != nil {
		t.Fatalf("EnsurePKI: %v", err)
	}
	if bundle.ClientCert == nil || bundle.CAPool == nil {
		t.Fatal("bundle missing client cert or CA pool")
	}

	// All three Secrets exist with the expected keys.
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{cfg.CASecretName, []string{corev1.TLSCertKey, corev1.TLSPrivateKeyKey}},
		{cfg.ServerSecretName, []string{corev1.ServiceAccountRootCAKey, corev1.TLSCertKey, corev1.TLSPrivateKeyKey}},
		{cfg.ClientSecretName, []string{corev1.ServiceAccountRootCAKey, corev1.TLSCertKey, corev1.TLSPrivateKeyKey}},
	} {
		var s corev1.Secret
		if err := c.Get(context.Background(), types.NamespacedName{Namespace: cfg.Namespace, Name: tc.name}, &s); err != nil {
			t.Fatalf("get Secret %s: %v", tc.name, err)
		}
		for _, k := range tc.keys {
			if len(s.Data[k]) == 0 {
				t.Errorf("Secret %s missing key %s", tc.name, k)
			}
		}
	}
}

func TestEnsurePKIIsIdempotent(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	cfg := testConfig()
	ctx := context.Background()

	if _, err := EnsurePKI(ctx, c, cfg); err != nil {
		t.Fatalf("EnsurePKI (1): %v", err)
	}

	var serverBefore corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.ServerSecretName}, &serverBefore); err != nil {
		t.Fatalf("get server Secret: %v", err)
	}

	// A second run with fresh leaves must not re-issue (no rotation due).
	if _, err := EnsurePKI(ctx, c, cfg); err != nil {
		t.Fatalf("EnsurePKI (2): %v", err)
	}

	var serverAfter corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.ServerSecretName}, &serverAfter); err != nil {
		t.Fatalf("get server Secret after: %v", err)
	}
	if !bytes.Equal(serverBefore.Data[corev1.TLSCertKey], serverAfter.Data[corev1.TLSCertKey]) {
		t.Error("server leaf was re-issued on an idempotent run (should be stable)")
	}
}

func TestEnsurePKIRotatesExpiringLeaf(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	cfg := testConfig()
	// Force rotation: any leaf with less than this remaining is renewed, and a
	// freshly issued 90d leaf has ~90d remaining, so a huge RenewBefore triggers it.
	cfg.RenewBefore = DefaultLeafValidity * 2
	ctx := context.Background()

	if _, err := EnsurePKI(ctx, c, cfg); err != nil {
		t.Fatalf("EnsurePKI (1): %v", err)
	}
	var before corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.ClientSecretName}, &before); err != nil {
		t.Fatalf("get client Secret: %v", err)
	}

	if _, err := EnsurePKI(ctx, c, cfg); err != nil {
		t.Fatalf("EnsurePKI (2): %v", err)
	}
	var after corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.ClientSecretName}, &after); err != nil {
		t.Fatalf("get client Secret after: %v", err)
	}
	if bytes.Equal(before.Data[corev1.TLSCertKey], after.Data[corev1.TLSCertKey]) {
		t.Error("expiring client leaf was not rotated")
	}
}

func TestEnsurePKIRotatesAfterClockAdvance(t *testing.T) {
	// Restore the real clock after the test.
	realClock := clockNow
	t.Cleanup(func() { clockNow = realClock })

	base := time.Now()
	clockNow = func() time.Time { return base }

	c := fake.NewClientBuilder().Build()
	cfg := testConfig() // 90d validity, 30d renewBefore
	ctx := context.Background()

	if _, err := EnsurePKI(ctx, c, cfg); err != nil {
		t.Fatalf("EnsurePKI (initial): %v", err)
	}
	var before corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.ServerSecretName}, &before); err != nil {
		t.Fatalf("get server Secret: %v", err)
	}

	// Advance the clock past the renewBefore threshold (90d - 30d = 60d in).
	clockNow = func() time.Time { return base.Add(61 * 24 * time.Hour) }

	if _, err := EnsurePKI(ctx, c, cfg); err != nil {
		t.Fatalf("EnsurePKI (after advance): %v", err)
	}
	var after corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.ServerSecretName}, &after); err != nil {
		t.Fatalf("get server Secret after: %v", err)
	}
	if bytes.Equal(before.Data[corev1.TLSCertKey], after.Data[corev1.TLSCertKey]) {
		t.Error("leaf was not rotated after the clock advanced into the renew window")
	}
}

// externalTLSSecret builds a cert-manager-style Secret (ca.crt/tls.crt/tls.key)
// carrying the externally-managed provenance annotation.
func externalTLSSecret(t *testing.T, name string, ca *CA, leaf *Leaf) *corev1.Secret {
	t.Helper()
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "test-ns",
			Annotations: map[string]string{ManagedByAnnotation: "cert-manager"},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.ServiceAccountRootCAKey: ca.CertPEM(),
			corev1.TLSCertKey:              leaf.CertPEM,
			corev1.TLSPrivateKeyKey:        leaf.KeyPEM,
		},
	}
}

func TestEnsurePKICertManagerReadsExternalBundle(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	clientLeaf, err := ca.IssueClientCert(ClientCertDNSName, DefaultLeafValidity)
	if err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	cfg := testConfig()
	cfg.CertManagerEnabled = true

	// Only the externally-managed client Secret exists; EnsurePKI must not create
	// a CA or server Secret in cert-manager mode.
	c := fake.NewClientBuilder().
		WithObjects(externalTLSSecret(t, cfg.ClientSecretName, ca, clientLeaf)).
		Build()

	bundle, err := EnsurePKI(context.Background(), c, cfg)
	if err != nil {
		t.Fatalf("EnsurePKI (cert-manager): %v", err)
	}
	if bundle.ClientCert == nil || bundle.CAPool == nil {
		t.Fatal("bundle missing client cert or CA pool")
	}
	if !bytes.Equal(bundle.CACertPEM, ca.CertPEM()) {
		t.Error("bundle CA does not match the externally-provided CA")
	}

	// No self-managed CA Secret should have been created.
	var caSecret corev1.Secret
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.CASecretName}, &caSecret); err == nil {
		t.Error("EnsurePKI created a CA Secret in cert-manager mode (should read-only)")
	}
}

func TestEnsurePKICertManagerMissingClientSecret(t *testing.T) {
	cfg := testConfig()
	cfg.CertManagerEnabled = true
	c := fake.NewClientBuilder().Build()

	if _, err := EnsurePKI(context.Background(), c, cfg); err == nil {
		t.Error("expected error when externally-managed client Secret is absent")
	}
}

func TestEnsurePKICertManagerMissingCAKey(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	clientLeaf, err := ca.IssueClientCert(ClientCertDNSName, DefaultLeafValidity)
	if err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}
	cfg := testConfig()
	cfg.CertManagerEnabled = true

	secret := externalTLSSecret(t, cfg.ClientSecretName, ca, clientLeaf)
	delete(secret.Data, corev1.ServiceAccountRootCAKey) // drop ca.crt
	c := fake.NewClientBuilder().WithObjects(secret).Build()

	if _, err := EnsurePKI(context.Background(), c, cfg); err == nil {
		t.Error("expected error when externally-managed client Secret is missing ca.crt")
	}
}
