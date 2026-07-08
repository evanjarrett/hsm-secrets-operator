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

package manager

import (
	"bytes"
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/agent"
	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/pki"
)

func rotationTestConfig() pki.Config {
	return pki.Config{
		Namespace:        "test-ns",
		CASecretName:     "hsm-mtls-ca",
		ServerSecretName: "hsm-agent-server-tls",
		ClientSecretName: "hsm-manager-client-tls",
		LeafValidity:     pki.DefaultLeafValidity,
		RenewBefore:      pki.DefaultRenewBefore,
	}
}

// currentClientLeafDER returns the DER of the leaf the holder presents on a
// handshake, exercising the same GetClientCertificate path the gRPC dial uses.
func currentClientLeafDER(t *testing.T, ct *agent.ClientTLS) []byte {
	t.Helper()
	leaf, err := ct.Config().GetClientCertificate(nil)
	require.NoError(t, err)
	require.NotEmpty(t, leaf.Certificate)
	return leaf.Certificate[0]
}

func TestPKIRunnableRotateOnceSwapsRotatedLeaf(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	cfg := rotationTestConfig()
	// Force rotation: a fresh 90d leaf has ~90d remaining, so a RenewBefore larger
	// than the validity makes every EnsurePKI re-issue.
	cfg.RenewBefore = pki.DefaultLeafValidity * 2
	ctx := context.Background()

	bundle, err := pki.EnsurePKI(ctx, c, cfg)
	require.NoError(t, err)
	ct := agent.NewClientTLS(bundle)
	before := currentClientLeafDER(t, ct)

	r := &pkiRunnable{client: c, pkiCfg: cfg, clientTLS: ct, logger: logr.Discard()}
	require.NoError(t, r.rotateOnce(ctx))

	after := currentClientLeafDER(t, ct)
	require.False(t, bytes.Equal(before, after),
		"rotateOnce should re-issue the expiring leaf and swap it into the holder")
}

func TestPKIRunnableRotateOnceIdempotentWhenFresh(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	cfg := rotationTestConfig() // default 30d renewBefore, fresh 90d leaf => no rotation
	ctx := context.Background()

	bundle, err := pki.EnsurePKI(ctx, c, cfg)
	require.NoError(t, err)
	ct := agent.NewClientTLS(bundle)
	before := currentClientLeafDER(t, ct)

	r := &pkiRunnable{client: c, pkiCfg: cfg, clientTLS: ct, logger: logr.Discard()}
	require.NoError(t, r.rotateOnce(ctx))

	after := currentClientLeafDER(t, ct)
	require.True(t, bytes.Equal(before, after),
		"rotateOnce must not re-issue a leaf that is nowhere near expiry")
}
