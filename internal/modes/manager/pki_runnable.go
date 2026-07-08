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
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/agent"
	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/pki"
)

// pkiRotationInterval is how often the rotation runnable re-checks leaf expiry.
const pkiRotationInterval = 12 * time.Hour

// pkiConfig builds the pki.Config from manager flags, parsing duration strings.
func pkiConfig(cfg *managerConfig, namespace string) (pki.Config, error) {
	leafValidity, err := time.ParseDuration(cfg.agentTLSLeafValidity)
	if err != nil {
		return pki.Config{}, fmt.Errorf("invalid agent-tls-leaf-validity %q: %w", cfg.agentTLSLeafValidity, err)
	}
	renewBefore, err := time.ParseDuration(cfg.agentTLSRenewBefore)
	if err != nil {
		return pki.Config{}, fmt.Errorf("invalid agent-tls-renew-before %q: %w", cfg.agentTLSRenewBefore, err)
	}
	return pki.Config{
		Namespace:          namespace,
		CASecretName:       cfg.agentTLSCASecret,
		ServerSecretName:   cfg.agentTLSServerSecret,
		ClientSecretName:   cfg.agentTLSClientSecret,
		LeafValidity:       leafValidity,
		RenewBefore:        renewBefore,
		CertManagerEnabled: cfg.agentTLSCertManager,
	}, nil
}

// bootstrapAgentPKI synchronously ensures the mTLS PKI Secrets exist and returns
// the manager's client-TLS holder. It uses an uncached client because it runs
// before mgr.Start (the cached client is not usable until the cache is running).
func bootstrapAgentPKI(ctx context.Context, restConfig *rest.Config, pkiCfg pki.Config) (*agent.ClientTLS, error) {
	c, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating uncached client for PKI bootstrap: %w", err)
	}
	bundle, err := pki.EnsurePKI(ctx, c, pkiCfg)
	if err != nil {
		return nil, fmt.Errorf("ensuring agent mTLS PKI: %w", err)
	}
	return agent.NewClientTLS(bundle), nil
}

// pkiRunnable periodically re-runs EnsurePKI to rotate leaves before expiry and
// swaps the manager's in-memory client leaf when it changes. It runs only on the
// leader (rotation writes Secrets and only the leader dials agents).
type pkiRunnable struct {
	client    client.Client
	pkiCfg    pki.Config
	clientTLS *agent.ClientTLS
	logger    logr.Logger
}

// NeedLeaderElection ensures rotation runs on a single replica.
func (r *pkiRunnable) NeedLeaderElection() bool { return true }

// Start runs the rotation loop until the context is cancelled.
func (r *pkiRunnable) Start(ctx context.Context) error {
	ticker := time.NewTicker(pkiRotationInterval)
	defer ticker.Stop()

	r.logger.Info("Starting agent mTLS rotation runnable", "interval", pkiRotationInterval.String())
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.rotateOnce(ctx); err != nil {
				r.logger.Error(err, "agent mTLS rotation check failed")
			}
		}
	}
}

// rotateOnce re-runs EnsurePKI (re-issuing any leaf due for rotation) and swaps
// the manager's in-memory client leaf. It is idempotent when nothing is due.
func (r *pkiRunnable) rotateOnce(ctx context.Context) error {
	bundle, err := pki.EnsurePKI(ctx, r.client, r.pkiCfg)
	if err != nil {
		return err
	}
	r.clientTLS.SwapLeaf(bundle.ClientCert)
	return nil
}
