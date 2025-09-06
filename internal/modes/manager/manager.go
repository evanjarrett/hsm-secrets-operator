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
	"crypto/tls"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/api"
	"github.com/evanjarrett/hsm-secrets-operator/internal/controller"
	"github.com/evanjarrett/hsm-secrets-operator/internal/mirror"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("manager")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hsmv1alpha1.AddToScheme(scheme))
}

// getCurrentNamespace returns the namespace the operator is running in
func getCurrentNamespace() string {
	// Try to read namespace from service account mount
	if ns, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return strings.TrimSpace(string(ns))
	}

	// Fallback to default namespace if we can't determine it
	setupLog.Info("Could not determine current namespace, using 'default'")
	return "default"
}

// getOperatorName returns the operator deployment name
// This can be overridden via environment variable or falls back to default
func getOperatorName() string {
	// Check if operator name is provided via environment variable
	if name := os.Getenv("OPERATOR_NAME"); name != "" {
		return name
	}

	// Check if deployment name is provided via downward API
	if hostname := os.Getenv("HOSTNAME"); hostname != "" {
		// Kubernetes deployment pods have hostname like: deployment-name-replicaset-hash-pod-hash
		// Extract the deployment name by removing the last two parts (replicaset-hash and pod-hash)
		parts := strings.Split(hostname, "-")
		if len(parts) >= 3 {
			// Remove last two parts (replicaset hash and pod hash) to get deployment name
			deploymentParts := parts[:len(parts)-2]
			return strings.Join(deploymentParts, "-")
		}
		return hostname
	}

	// Fallback to default deployment name
	setupLog.Info("Could not determine operator name, using 'controller-manager'")
	return "controller-manager"
}

// Run starts the manager mode
func Run(args []string) error {
	// Create a new flag set for manager-specific flags
	fs := flag.NewFlagSet("manager", flag.ContinueOnError)

	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var enableAPI bool
	var apiPort int

	fs.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	fs.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	fs.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	fs.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	fs.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	fs.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	fs.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	fs.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	fs.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	fs.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	fs.BoolVar(&enableAPI, "enable-api", true,
		"Enable the REST API server for HSM secret management")
	fs.IntVar(&apiPort, "api-port", 8090,
		"Port for the REST API server")

	// Parse manager-specific flags from the remaining unparsed arguments
	if err := fs.Parse(args); err != nil {
		return err
	}

	var tlsOpts []func(*tls.Config)

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Create watchers for metrics and webhooks certificates
	var metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		var err error
		webhookCertWatcher, err = certwatcher.New(
			filepath.Join(webhookCertPath, webhookCertName),
			filepath.Join(webhookCertPath, webhookCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to initialize webhook certificate watcher")
			return err
		}

		webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
			config.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: webhookTLSOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "to initialize metrics certificate watcher", "error", err)
			return err
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "64b68d60.j5t.io",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		return err
	}

	// HSM mirroring is now handled by the sync package and HSMSyncReconciler
	// Device discovery is handled by separate discovery daemon

	// Get current operator namespace and name
	operatorNamespace := getCurrentNamespace()
	operatorName := getOperatorName()
	setupLog.Info("Detected operator details", "namespace", operatorNamespace, "name", operatorName)

	// Agent manager will detect the current namespace automatically
	imageResolver := controller.NewImageResolver(mgr.GetClient())
	agentManager := agent.NewManager(mgr.GetClient(), "", imageResolver)

	// Set up HSMPool controller to aggregate discovery reports from pod annotations
	if err := (&controller.HSMPoolReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HSMPool")
		return err
	}

	// Set up HSMPool agent controller to deploy agents when pools are ready
	if err := (&controller.HSMPoolAgentReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		AgentManager:         agentManager,
		DeviceAbsenceTimeout: 10 * time.Minute, // Default: cleanup agents after 10 minutes of device absence
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HSMPoolAgent")
		return err
	}

	if err := (&controller.HSMSecretReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		AgentManager:      agentManager,
		OperatorNamespace: operatorNamespace,
		OperatorName:      operatorName,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HSMSecret")
		return err
	}

	// Set up discovery DaemonSet controller (manager-owned)
	if err := (&controller.DiscoveryDaemonSetReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		ImageResolver: imageResolver,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DiscoveryDaemonSet")
		return err
	}

	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "unable to add metrics certificate watcher to manager")
			return err
		}
	}

	if webhookCertWatcher != nil {
		setupLog.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			setupLog.Error(err, "unable to add webhook certificate watcher to manager")
			return err
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		return err
	}

	// Start API server if enabled
	if enableAPI {
		apiServer := api.NewServer(mgr.GetClient(), agentManager, ctrl.Log.WithName("api"))

		// Start API server in a separate goroutine
		go func() {
			setupLog.Info("starting API server", "port", apiPort)
			if err := apiServer.Start(apiPort); err != nil {
				setupLog.Error(err, "problem running API server")
			}
		}()
	}

	// Start device-scoped HSM mirroring in background
	mirrorManager := mirror.NewMirrorManager(mgr.GetClient(), agentManager, ctrl.Log.WithName("device-mirror"), operatorNamespace)
	go func() {
		mirrorTicker := time.NewTicker(30 * time.Second) // Mirror every 30 seconds
		defer mirrorTicker.Stop()

		setupLog.Info("starting device-scoped HSM mirroring", "interval", "30s")

		// Wait for agents to be ready before starting mirroring
		ctx := context.Background()
		ready, err := mirrorManager.WaitForAgentsReady(ctx, 5*time.Minute)
		if err != nil {
			setupLog.Error(err, "failed to wait for agents to be ready")
			return
		}
		if !ready {
			setupLog.Info("no agents became ready within timeout, disabling mirroring")
			return
		}

		for range mirrorTicker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			result, err := mirrorManager.MirrorAllSecrets(ctx)
			cancel()

			if err != nil {
				setupLog.Error(err, "device-scoped mirroring failed")
			} else if result.SecretsProcessed > 0 {
				setupLog.Info("device-scoped mirroring completed",
					"secretsProcessed", result.SecretsProcessed,
					"secretsUpdated", result.SecretsUpdated,
					"secretsCreated", result.SecretsCreated,
					"errors", len(result.Errors))
			}
		}
	}()

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		return err
	}

	return nil
}
