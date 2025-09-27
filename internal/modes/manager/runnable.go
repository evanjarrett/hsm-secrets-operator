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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
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

// MirrorTriggerEvent represents an event that should trigger mirroring
type MirrorTriggerEvent struct {
	Reason string // "agent_ready", "agent_changed", "manual", "periodic"
	Force  bool   // Skip optimization checks
	Source string // Additional context (e.g., agent pod name)
}

// MirrorManagerRunnable starts the HSM mirroring service
type MirrorManagerRunnable struct {
	k8sClient         client.Client
	k8sInterface      *kubernetes.Clientset
	agentManager      *agent.Manager
	operatorNamespace string
	logger            logr.Logger

	// Event-driven mirroring
	mirrorTrigger    chan MirrorTriggerEvent
	periodicInterval time.Duration // Safety net interval (default 5 minutes)
	debounceWindow   time.Duration // Wait for multiple changes (default 5 seconds)
}

// NewMirrorManagerRunnable creates a new mirror manager runnable
func NewMirrorManagerRunnable(k8sClient client.Client, k8sInterface *kubernetes.Clientset, agentManager *agent.Manager, operatorNamespace string, logger logr.Logger, periodicInterval, debounceWindow time.Duration) *MirrorManagerRunnable {
	return &MirrorManagerRunnable{
		k8sClient:         k8sClient,
		k8sInterface:      k8sInterface,
		agentManager:      agentManager,
		operatorNamespace: operatorNamespace,
		logger:            logger,
		mirrorTrigger:     make(chan MirrorTriggerEvent, 10), // Buffered channel
		periodicInterval:  periodicInterval,
		debounceWindow:    debounceWindow,
	}
}

// setupAgentPodWatcher sets up a pod informer to watch for agent pod changes
func (mmr *MirrorManagerRunnable) setupAgentPodWatcher(ctx context.Context) {
	// Create a pod watcher for agent pods in the operator namespace
	watchlist := cache.NewListWatchFromClient(
		mmr.k8sInterface.CoreV1().RESTClient(),
		"pods",
		mmr.operatorNamespace,
		fields.Everything(),
	)

	// Create the informer using the new API
	_, controller := cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: watchlist,
		ObjectType:    &corev1.Pod{},
		ResyncPeriod:  5 * time.Minute, // Resync interval
		Handler: cache.ResourceEventHandlerFuncs{
			UpdateFunc: func(oldObj, newObj any) {
				pod, ok := newObj.(*corev1.Pod)
				if !ok {
					return
				}

				// Only watch agent pods
				if !mmr.isAgentPod(pod) {
					return
				}

				// Check if pod became ready
				if mmr.isPodReady(pod) && !mmr.wasPodReady(oldObj.(*corev1.Pod)) {
					mmr.logger.Info("Agent pod became ready, triggering mirror sync",
						"pod", pod.Name,
						"node", pod.Spec.NodeName)

					mmr.TriggerMirror("agent_ready", pod.Name, false)
				}
			},
		},
	})

	// Start the controller in a goroutine
	go controller.Run(ctx.Done())

	mmr.logger.Info("Agent pod watcher started")
}

// isAgentPod checks if a pod is an HSM agent pod
func (mmr *MirrorManagerRunnable) isAgentPod(pod *corev1.Pod) bool {
	if pod.Labels == nil {
		return false
	}
	component, exists := pod.Labels["app.kubernetes.io/component"]
	return exists && component == "hsm-agent"
}

// isPodReady checks if a pod is in ready state
func (mmr *MirrorManagerRunnable) isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// wasPodReady checks if the old pod was ready (for detecting transitions)
func (mmr *MirrorManagerRunnable) wasPodReady(pod *corev1.Pod) bool {
	return mmr.isPodReady(pod)
}

// TriggerMirror sends a trigger event to the mirror manager
func (mmr *MirrorManagerRunnable) TriggerMirror(reason, source string, force bool) {
	select {
	case mmr.mirrorTrigger <- MirrorTriggerEvent{
		Reason: reason,
		Force:  force,
		Source: source,
	}:
		mmr.logger.V(1).Info("Mirror trigger sent", "reason", reason, "source", source, "force", force)
	default:
		mmr.logger.V(1).Info("Mirror trigger channel full, dropping event", "reason", reason, "source", source)
	}
}

// Start starts the mirroring service - implements manager.Runnable
func (mmr *MirrorManagerRunnable) Start(ctx context.Context) error {
	mmr.logger.Info("Starting event-driven HSM mirroring service",
		"periodicInterval", mmr.periodicInterval,
		"debounceWindow", mmr.debounceWindow)

	// Set global reference for API access
	api.SetMirrorTrigger(mmr)

	// Create mirror manager
	mirrorManager := mirror.NewMirrorManager(mmr.k8sClient, mmr.agentManager, mmr.logger, mmr.operatorNamespace)

	// Set up agent pod watcher for event-driven mirroring
	mmr.setupAgentPodWatcher(ctx)

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
	mmr.logger.Info("HSM agents are ready, starting event-driven mirroring")

	// Set up periodic safety net ticker (much less frequent than before)
	periodicTicker := time.NewTicker(mmr.periodicInterval)
	defer periodicTicker.Stop()

	// Track last sync to avoid unnecessary operations
	var lastSyncTime time.Time

	// Debounce timer for batching multiple rapid events
	var debounceTimer *time.Timer
	var pendingEvents []MirrorTriggerEvent

	for {
		select {
		case event := <-mmr.mirrorTrigger:
			mmr.logger.V(1).Info("Received mirror trigger event",
				"reason", event.Reason,
				"source", event.Source,
				"force", event.Force)

			// Add to pending events for debouncing
			pendingEvents = append(pendingEvents, event)

			// Reset or start debounce timer
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(mmr.debounceWindow, func() {
				// Process batched events
				mmr.processPendingEvents(ctx, mirrorManager, pendingEvents, &lastSyncTime)
				pendingEvents = nil
			})

		case <-periodicTicker.C:
			// Periodic safety net - only sync if it's been a while
			timeSinceLastSync := time.Since(lastSyncTime)
			if timeSinceLastSync > mmr.periodicInterval {
				mmr.logger.Info("Performing periodic safety mirror sync",
					"timeSinceLastSync", timeSinceLastSync)

				mmr.performMirrorSync(ctx, mirrorManager, "periodic", "", &lastSyncTime)
			} else {
				mmr.logger.V(1).Info("Skipping periodic sync, recent sync performed",
					"timeSinceLastSync", timeSinceLastSync)
			}

		case <-ctx.Done():
			mmr.logger.Info("Mirror manager context cancelled, stopping mirroring")
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return nil
		}
	}
}

// processPendingEvents handles batched mirror trigger events
func (mmr *MirrorManagerRunnable) processPendingEvents(ctx context.Context, mirrorManager *mirror.MirrorManager, events []MirrorTriggerEvent, lastSyncTime *time.Time) {
	if len(events) == 0 {
		return
	}

	// Determine if any event forces sync
	force := false
	reasons := make([]string, 0, len(events))
	sources := make([]string, 0, len(events))

	for _, event := range events {
		if event.Force {
			force = true
		}
		reasons = append(reasons, event.Reason)
		sources = append(sources, event.Source)
	}

	// Check if we should skip sync (optimization)
	if !force && time.Since(*lastSyncTime) < 30*time.Second {
		mmr.logger.V(1).Info("Skipping mirror sync due to recent sync",
			"timeSinceLastSync", time.Since(*lastSyncTime),
			"events", len(events))
		return
	}

	combinedReason := fmt.Sprintf("batched(%s)", reasons[0])
	if len(reasons) > 1 {
		combinedReason = fmt.Sprintf("batched(%d events)", len(events))
	}

	mmr.logger.Info("Processing batched mirror events",
		"eventCount", len(events),
		"reasons", reasons,
		"sources", sources)

	mmr.performMirrorSync(ctx, mirrorManager, combinedReason, fmt.Sprintf("%v", sources), lastSyncTime)
}

// performMirrorSync executes the actual mirroring operation
func (mmr *MirrorManagerRunnable) performMirrorSync(ctx context.Context, mirrorManager *mirror.MirrorManager, reason, source string, lastSyncTime *time.Time) {
	mmr.logger.Info("Starting mirror sync",
		"reason", reason,
		"source", source)

	mirrorCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	result, err := mirrorManager.MirrorAllSecrets(mirrorCtx)

	if err != nil {
		mmr.logger.Error(err, "Mirror sync failed",
			"reason", reason,
			"source", source)
	} else {
		*lastSyncTime = time.Now()
		mmr.logger.Info("Mirror sync completed",
			"reason", reason,
			"source", source,
			"secretsProcessed", result.SecretsProcessed,
			"secretsUpdated", result.SecretsUpdated,
			"secretsCreated", result.SecretsCreated,
			"metadataRestored", result.MetadataRestored,
			"errors", len(result.Errors),
			"success", result.Success)

		if len(result.Errors) > 0 {
			mmr.logger.V(1).Info("Mirror sync error details", "errors", result.Errors)
		}
	}
}
