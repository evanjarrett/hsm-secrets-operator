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
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/api"
	"github.com/evanjarrett/hsm-secrets-operator/internal/config"
	"github.com/evanjarrett/hsm-secrets-operator/internal/controller"
	"github.com/evanjarrett/hsm-secrets-operator/internal/mirror"
)

// AgentManagerRunnable wraps an agent manager to create it after TLS is ready
type AgentManagerRunnable struct {
	k8sClient          client.Client
	agentImage         string
	operatorNS         string
	serviceAccountName string
	logger             logr.Logger
	agentManager       *agent.Manager
	mu                 sync.RWMutex
	ready              bool
	readyCh            chan struct{}
	readyOnce          sync.Once
}

// NewAgentManagerRunnable creates a new agent manager runnable
func NewAgentManagerRunnable(k8sClient client.Client, agentImage string, operatorNS, serviceAccountName string, logger logr.Logger) *AgentManagerRunnable {
	return &AgentManagerRunnable{
		k8sClient:          k8sClient,
		agentImage:         agentImage,
		operatorNS:         operatorNS,
		serviceAccountName: serviceAccountName,
		logger:             logger.WithName("agent-manager-runnable"),
		readyCh:            make(chan struct{}),
	}
}

// Start creates the agent manager after TLS is ready - implements manager.Runnable
func (amr *AgentManagerRunnable) Start(ctx context.Context) error {
	amr.logger.Info("Starting agent manager (after TLS is ready)")

	// Create image resolver
	imageResolver := config.NewImageResolver(amr.k8sClient)

	// Create agent manager with TLS config
	agentManager := agent.NewManager(amr.k8sClient, "", amr.agentImage, imageResolver)

	// Store the agent manager and mark as ready
	amr.mu.Lock()
	amr.agentManager = agentManager
	amr.ready = true
	amr.mu.Unlock()

	// Signal that agent manager is ready
	amr.readyOnce.Do(func() {
		close(amr.readyCh)
	})

	amr.logger.Info("Agent manager created successfully with TLS configuration")

	// Wait for context cancellation (the agent manager itself doesn't need a shutdown sequence)
	<-ctx.Done()

	amr.logger.Info("Agent manager context cancelled")
	return nil
}

// GetAgentManager returns the agent manager if ready, nil otherwise
func (amr *AgentManagerRunnable) GetAgentManager() *agent.Manager {
	amr.mu.RLock()
	defer amr.mu.RUnlock()

	if !amr.ready {
		return nil
	}
	return amr.agentManager
}

// WaitForReady waits for the agent manager to be ready
func (amr *AgentManagerRunnable) WaitForReady(ctx context.Context, timeout time.Duration) (*agent.Manager, error) {
	// If already ready, return immediately
	amr.mu.RLock()
	if amr.ready {
		agentManager := amr.agentManager
		amr.mu.RUnlock()
		return agentManager, nil
	}
	amr.mu.RUnlock()

	// Wait for ready signal or timeout
	select {
	case <-amr.readyCh:
		return amr.GetAgentManager(), nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for agent manager to be ready after %v", timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// IsReady returns true if the agent manager is ready
func (amr *AgentManagerRunnable) IsReady() bool {
	amr.mu.RLock()
	defer amr.mu.RUnlock()
	return amr.ready
}

// APIServerRunnable starts the REST API server after agent manager is ready
type APIServerRunnable struct {
	k8sClient            client.Client
	agentManagerRunnable *AgentManagerRunnable
	operatorNamespace    string
	k8sInterface         *kubernetes.Clientset
	apiPort              int
	logger               logr.Logger
}

// NewAPIServerRunnable creates a new API server runnable
func NewAPIServerRunnable(k8sClient client.Client, agentManagerRunnable *AgentManagerRunnable, operatorNamespace string, k8sInterface *kubernetes.Clientset, apiPort int, logger logr.Logger) *APIServerRunnable {
	return &APIServerRunnable{
		k8sClient:            k8sClient,
		agentManagerRunnable: agentManagerRunnable,
		operatorNamespace:    operatorNamespace,
		k8sInterface:         k8sInterface,
		apiPort:              apiPort,
		logger:               logger.WithName("api-server-runnable"),
	}
}

// Start starts the API server after agent manager is ready - implements manager.Runnable
func (asr *APIServerRunnable) Start(ctx context.Context) error {
	asr.logger.Info("Waiting for agent manager to be ready before starting API server")

	// Wait for agent manager to be ready
	agentManager, err := asr.agentManagerRunnable.WaitForReady(ctx, 60*time.Second)
	if err != nil {
		return fmt.Errorf("timeout waiting for agent manager to be ready: %w", err)
	}

	asr.logger.Info("Agent manager is ready, starting API server", "port", asr.apiPort)

	// Start the API server
	apiServer := api.NewServer(asr.k8sClient, agentManager, asr.operatorNamespace, asr.k8sInterface, asr.apiPort, asr.logger)
	return apiServer.Start(ctx)
}

// MirrorManagerRunnable starts the HSM mirroring service after agent manager is ready
type MirrorManagerRunnable struct {
	k8sClient            client.Client
	agentManagerRunnable *AgentManagerRunnable
	operatorNamespace    string
	logger               logr.Logger
}

// NewMirrorManagerRunnable creates a new mirror manager runnable
func NewMirrorManagerRunnable(k8sClient client.Client, agentManagerRunnable *AgentManagerRunnable, operatorNamespace string, logger logr.Logger) *MirrorManagerRunnable {
	return &MirrorManagerRunnable{
		k8sClient:            k8sClient,
		agentManagerRunnable: agentManagerRunnable,
		operatorNamespace:    operatorNamespace,
		logger:               logger.WithName("mirror-manager-runnable"),
	}
}

// Start starts the mirroring service after agent manager is ready - implements manager.Runnable
func (mmr *MirrorManagerRunnable) Start(ctx context.Context) error {
	mmr.logger.Info("Waiting for agent manager to be ready before starting HSM mirroring")

	// Wait for agent manager to be ready
	agentManager, err := mmr.agentManagerRunnable.WaitForReady(ctx, 60*time.Second)
	if err != nil {
		return fmt.Errorf("timeout waiting for agent manager to be ready: %w", err)
	}

	mmr.logger.Info("Agent manager is ready, starting HSM mirroring service")

	// Create mirror manager
	mirrorManager := mirror.NewMirrorManager(mmr.k8sClient, agentManager, mmr.logger, mmr.operatorNamespace)

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

// AgentControllerSetupRunnable sets up controllers that depend on the agent manager after it's ready
type AgentControllerSetupRunnable struct {
	agentManagerRunnable *AgentManagerRunnable
	mgr                  manager.Manager
	operatorNamespace    string
	operatorName         string
	serviceAccountName   string
	logger               logr.Logger
}

// NewAgentControllerSetupRunnable creates a new agent controller setup runnable
func NewAgentControllerSetupRunnable(agentManagerRunnable *AgentManagerRunnable, mgr manager.Manager, operatorNamespace, operatorName, serviceAccountName string, logger logr.Logger) *AgentControllerSetupRunnable {
	return &AgentControllerSetupRunnable{
		agentManagerRunnable: agentManagerRunnable,
		mgr:                  mgr,
		operatorNamespace:    operatorNamespace,
		operatorName:         operatorName,
		serviceAccountName:   serviceAccountName,
		logger:               logger.WithName("agent-controller-setup"),
	}
}

// Start sets up agent-dependent controllers after agent manager is ready - implements manager.Runnable
func (acsr *AgentControllerSetupRunnable) Start(ctx context.Context) error {
	acsr.logger.Info("Waiting for agent manager to be ready before setting up controllers")

	// Wait for agent manager to be ready
	agentManager, err := acsr.agentManagerRunnable.WaitForReady(ctx, 60*time.Second)
	if err != nil {
		return fmt.Errorf("timeout waiting for agent manager to be ready: %w", err)
	}

	acsr.logger.Info("Agent manager is ready, setting up dependent controllers")

	// Create image resolver
	imageResolver := config.NewImageResolver(acsr.mgr.GetClient())

	// Set up HSMPool agent controller to deploy agents when pools are ready
	if err := (&controller.HSMPoolAgentReconciler{
		Client:               acsr.mgr.GetClient(),
		Scheme:               acsr.mgr.GetScheme(),
		AgentManager:         agentManager,
		ImageResolver:        imageResolver,
		DeviceAbsenceTimeout: 10 * time.Minute, // Default: cleanup agents after 10 minutes of device absence
		ServiceAccountName:   acsr.serviceAccountName,
	}).SetupWithManager(acsr.mgr); err != nil {
		return fmt.Errorf("unable to create controller HSMPoolAgent: %w", err)
	}

	// Set up HSMSecret controller
	if err := (&controller.HSMSecretReconciler{
		Client:            acsr.mgr.GetClient(),
		Scheme:            acsr.mgr.GetScheme(),
		AgentManager:      agentManager,
		OperatorNamespace: acsr.operatorNamespace,
		OperatorName:      acsr.operatorName,
		StartupTime:       time.Now(),
	}).SetupWithManager(acsr.mgr); err != nil {
		return fmt.Errorf("unable to create controller HSMSecret: %w", err)
	}

	acsr.logger.Info("Agent-dependent controllers set up successfully")

	// Wait for context cancellation
	<-ctx.Done()

	acsr.logger.Info("Agent controller setup context cancelled")
	return nil
}
