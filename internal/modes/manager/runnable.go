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
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/api"
	"github.com/evanjarrett/hsm-secrets-operator/internal/mirror"
)

// APIServerRunnable starts the REST API server
type APIServerRunnable struct {
	k8sClient         client.Client
	agentManager      *agent.Manager
	operatorNamespace string
	k8sInterface      *kubernetes.Clientset
	apiPort           int
	logger            logr.Logger
}

// NewAPIServerRunnable creates a new API server runnable
func NewAPIServerRunnable(k8sClient client.Client, agentManager *agent.Manager, operatorNamespace string, k8sInterface *kubernetes.Clientset, apiPort int, logger logr.Logger) *APIServerRunnable {
	return &APIServerRunnable{
		k8sClient:         k8sClient,
		agentManager:      agentManager,
		operatorNamespace: operatorNamespace,
		k8sInterface:      k8sInterface,
		apiPort:           apiPort,
		logger:            logger.WithName("api-server-runnable"),
	}
}

// Start starts the API server - implements manager.Runnable
func (asr *APIServerRunnable) Start(ctx context.Context) error {
	asr.logger.Info("Starting API server", "port", asr.apiPort)

	// Start the API server
	apiServer := api.NewServer(asr.k8sClient, asr.agentManager, asr.operatorNamespace, asr.k8sInterface, asr.apiPort, asr.logger)
	return apiServer.Start(ctx)
}

// MirrorManagerRunnable starts the HSM mirroring service
type MirrorManagerRunnable struct {
	k8sClient         client.Client
	agentManager      *agent.Manager
	operatorNamespace string
	logger            logr.Logger
}

// NewMirrorManagerRunnable creates a new mirror manager runnable
func NewMirrorManagerRunnable(k8sClient client.Client, agentManager *agent.Manager, operatorNamespace string, logger logr.Logger) *MirrorManagerRunnable {
	return &MirrorManagerRunnable{
		k8sClient:         k8sClient,
		agentManager:      agentManager,
		operatorNamespace: operatorNamespace,
		logger:            logger.WithName("mirror-manager-runnable"),
	}
}

// Start starts the mirroring service - implements manager.Runnable
func (mmr *MirrorManagerRunnable) Start(ctx context.Context) error {
	mmr.logger.Info("Starting HSM mirroring service")

	// Create mirror manager
	mirrorManager := mirror.NewMirrorManager(mmr.k8sClient, mmr.agentManager, mmr.logger, mmr.operatorNamespace)

	// Start mirroring cycle
	mirrorTicker := time.NewTicker(30 * time.Second) // Mirror every 30 seconds
	defer mirrorTicker.Stop()

	mmr.logger.Info("starting device-scoped HSM mirroring", "interval", "30s")

	// Wait for agents to be ready before starting mirroring
	mmr.logger.Info("waiting for HSM agents to be ready before starting mirroring")
	ready, err := mirrorManager.WaitForAgentsReady(ctx, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("failed to wait for agents to be ready: %w", err)
	}
	if !ready {
		mmr.logger.Info("no agents became ready within timeout, disabling mirroring")
		// Don't return error, just wait for context cancellation
		<-ctx.Done()
		return nil
	}
	mmr.logger.Info("HSM agents are ready, starting mirroring cycle")

	for {
		select {
		case <-mirrorTicker.C:
			mmr.logger.Info("starting device-scoped mirroring cycle")
			mirrorCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			result, err := mirrorManager.MirrorAllSecrets(mirrorCtx)
			cancel()

			if err != nil {
				mmr.logger.Error(err, "device-scoped mirroring failed")
			} else {
				mmr.logger.Info("device-scoped mirroring completed",
					"secretsProcessed", result.SecretsProcessed,
					"secretsUpdated", result.SecretsUpdated,
					"secretsCreated", result.SecretsCreated,
					"metadataRestored", result.MetadataRestored,
					"errors", len(result.Errors),
					"success", result.Success)
				if len(result.Errors) > 0 {
					mmr.logger.Info("mirroring errors details", "errors", result.Errors)
				}
			}
		case <-ctx.Done():
			mmr.logger.Info("Mirror manager context cancelled, stopping mirroring")
			return nil
		}
	}
}
