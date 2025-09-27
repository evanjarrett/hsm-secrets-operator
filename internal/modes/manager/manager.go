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
	"crypto/tls"
	"flag"
	"os"
	"path/filepath"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/config"
	"github.com/evanjarrett/hsm-secrets-operator/internal/controller"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("manager")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hsmv1alpha1.AddToScheme(scheme))
}

// managerConfig holds all the configuration for the manager
type managerConfig struct {
	metricsAddr          string
	metricsCertPath      string
	metricsCertName      string
	metricsCertKey       string
	webhookCertPath      string
	webhookCertName      string
	webhookCertKey       string
	enableLeaderElection bool
	probeAddr            string
	secureMetrics        bool
	enableHTTP2          bool
	enableAPI            bool
	apiPort              int
	agentImage           string
	discoveryImage       string

	// Mirror configuration
	mirrorPeriodicInterval string // Duration string for periodic sync interval
	mirrorDebounceWindow   string // Duration string for event debounce window
}

// parseFlags parses command line arguments and returns the configuration
func parseFlags(args []string) (*managerConfig, error) {
	fs := flag.NewFlagSet("manager", flag.ContinueOnError)
	cfg := &managerConfig{}

	fs.StringVar(&cfg.metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	fs.StringVar(&cfg.probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&cfg.enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	fs.BoolVar(&cfg.secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	fs.StringVar(&cfg.webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	fs.StringVar(&cfg.webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	fs.StringVar(&cfg.webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	fs.StringVar(&cfg.metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	fs.StringVar(&cfg.metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	fs.StringVar(&cfg.metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	fs.BoolVar(&cfg.enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	fs.BoolVar(&cfg.enableAPI, "enable-api", true,
		"Enable the REST API server for HSM secret management")
	fs.IntVar(&cfg.apiPort, "api-port", 8090,
		"Port for the REST API server")
	fs.StringVar(&cfg.agentImage, "agent-image", "",
		"Container image for HSM agent pods")
	fs.StringVar(&cfg.discoveryImage, "discovery-image", "",
		"Container image for HSM discovery DaemonSet")

	// Mirror configuration flags
	fs.StringVar(&cfg.mirrorPeriodicInterval, "mirror-periodic-interval", "5m",
		"Interval for periodic mirror safety sync (e.g., 5m, 10m, 1h)")
	fs.StringVar(&cfg.mirrorDebounceWindow, "mirror-debounce-window", "5s",
		"Debounce window for batching mirror events (e.g., 5s, 10s, 30s)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return cfg, nil
}

// setupManager creates and configures the controller-runtime manager
func setupManager(cfg *managerConfig) (ctrl.Manager, *certwatcher.CertWatcher, *certwatcher.CertWatcher, error) {
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

	if !cfg.enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Create watchers for metrics and webhooks certificates
	var metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts

	if len(cfg.webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", cfg.webhookCertPath, "webhook-cert-name", cfg.webhookCertName, "webhook-cert-key", cfg.webhookCertKey)

		var err error
		webhookCertWatcher, err = certwatcher.New(
			filepath.Join(cfg.webhookCertPath, cfg.webhookCertName),
			filepath.Join(cfg.webhookCertPath, cfg.webhookCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to initialize webhook certificate watcher")
			return nil, nil, nil, err
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
	metricsServerOptions := server.Options{
		BindAddress:   cfg.metricsAddr,
		SecureServing: cfg.secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if cfg.secureMetrics {
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
	if len(cfg.metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", cfg.metricsCertPath, "metrics-cert-name", cfg.metricsCertName, "metrics-cert-key", cfg.metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(cfg.metricsCertPath, cfg.metricsCertName),
			filepath.Join(cfg.metricsCertPath, cfg.metricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "to initialize metrics certificate watcher", "error", err)
			return nil, nil, nil, err
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: cfg.probeAddr,
		LeaderElection:         cfg.enableLeaderElection,
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
		return nil, nil, nil, err
	}

	return mgr, metricsCertWatcher, webhookCertWatcher, nil
}

// setupOperatorComponents sets up operator namespace, name, TLS manager, and agent manager
func setupOperatorComponents() (string, string, error) {
	// Get current operator namespace and name
	operatorNamespace, err := config.GetCurrentNamespace()
	if err != nil {
		setupLog.Error(err, "unable to get the current namespace")
		return "", "", err
	}
	operatorName, _ := os.Hostname()
	setupLog.Info("Detected operator details", "namespace", operatorNamespace, "name", operatorName)

	return operatorNamespace, operatorName, nil
}

// setupAllControllers sets up all controllers (base and agent-dependent)
func setupAllControllers(mgr ctrl.Manager, cfg *managerConfig, serviceAccountName string, agentManager *agent.Manager, operatorNamespace, operatorName string) error {
	// Create image resolver
	imageResolver := config.NewImageResolver(mgr.GetClient())

	// Set up HSMPool controller to aggregate discovery reports from pod annotations
	if err := (&controller.HSMPoolReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HSMPool")
		return err
	}

	// Set up discovery DaemonSet controller (manager-owned)
	if err := (&controller.DiscoveryDaemonSetReconciler{
		Client:             mgr.GetClient(),
		Scheme:             mgr.GetScheme(),
		ImageResolver:      imageResolver,
		DiscoveryImage:     cfg.discoveryImage,
		ServiceAccountName: serviceAccountName,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DiscoveryDaemonSet")
		return err
	}

	// Set up HSMPool agent controller to deploy agents when pools are ready
	if err := (&controller.HSMPoolAgentReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		AgentManager:         agentManager,
		ImageResolver:        imageResolver,
		DeviceAbsenceTimeout: 10 * time.Minute, // Default: cleanup agents after 10 minutes of device absence
		ServiceAccountName:   serviceAccountName,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HSMPoolAgent")
		return err
	}

	// Set up HSMSecret controller
	if err := (&controller.HSMSecretReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		AgentManager:      agentManager,
		OperatorNamespace: operatorNamespace,
		OperatorName:      operatorName,
		StartupTime:       time.Now(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HSMSecret")
		return err
	}

	setupLog.Info("All controllers set up successfully")
	return nil
}

// startServices starts the API server, mirroring service, and manager
func startServices(mgr ctrl.Manager, agentManager *agent.Manager, operatorNamespace string, cfg *managerConfig) error {
	// Start API server if enabled
	if cfg.enableAPI {
		// Create Kubernetes clientset for JWT authentication
		k8sInterface, err := kubernetes.NewForConfig(mgr.GetConfig())
		if err != nil {
			setupLog.Error(err, "unable to create Kubernetes clientset for API authentication")
			return err
		}

		// Create API server runnable
		apiServerRunnable := NewAPIServerRunnable(mgr.GetClient(), agentManager, operatorNamespace, k8sInterface, cfg.apiPort, ctrl.Log.WithName("api"))

		// Add API server as a Runnable to ensure it starts after the cache is ready
		if err := mgr.Add(apiServerRunnable); err != nil {
			setupLog.Error(err, "unable to add API server to manager")
			return err
		}
		setupLog.Info("API server will start", "port", cfg.apiPort)
	}

	// Create Kubernetes clientset for pod watching
	k8sInterface, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create Kubernetes clientset for mirror manager")
		return err
	}

	// Parse mirror configuration durations
	periodicInterval, err := time.ParseDuration(cfg.mirrorPeriodicInterval)
	if err != nil {
		setupLog.Error(err, "invalid mirror periodic interval", "value", cfg.mirrorPeriodicInterval)
		return err
	}

	debounceWindow, err := time.ParseDuration(cfg.mirrorDebounceWindow)
	if err != nil {
		setupLog.Error(err, "invalid mirror debounce window", "value", cfg.mirrorDebounceWindow)
		return err
	}

	// Start device-scoped HSM mirroring in background
	mirrorManagerRunnable := NewMirrorManagerRunnable(mgr.GetClient(), k8sInterface, agentManager, operatorNamespace, ctrl.Log.WithName("agent-mirror"), periodicInterval, debounceWindow)
	if err := mgr.Add(mirrorManagerRunnable); err != nil {
		setupLog.Error(err, "unable to add mirror manager to manager")
		return err
	}
	setupLog.Info("Mirror manager will start")

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		return err
	}

	return nil
}

// Run starts the manager mode
func Run(args []string) error {
	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}

	mgr, metricsCertWatcher, webhookCertWatcher, err := setupManager(cfg)
	if err != nil {
		return err
	}

	operatorNamespace, operatorName, err := setupOperatorComponents()
	if err != nil {
		return err
	}

	// Get the service account name from environment variable (injected via downward API)
	serviceAccountName := os.Getenv("SERVICE_ACCOUNT_NAME")
	if serviceAccountName == "" {
		serviceAccountName = "hsm-secrets-operator-controller-manager" // fallback default
	}
	setupLog.Info("Using service account", "serviceAccount", serviceAccountName)

	// Create agent manager directly (no runnable needed)
	setupLog.Info("Creating agent manager")
	imageResolver := config.NewImageResolver(mgr.GetClient())
	agentManager := agent.NewManager(mgr.GetClient(), operatorNamespace, cfg.agentImage, imageResolver)

	// Setup all controllers directly
	if err := setupAllControllers(mgr, cfg, serviceAccountName, agentManager, operatorNamespace, operatorName); err != nil {
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

	return startServices(mgr, agentManager, operatorNamespace, cfg)
}
