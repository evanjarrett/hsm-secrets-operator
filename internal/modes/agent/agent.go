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

package agent

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	agentconfig "github.com/evanjarrett/hsm-secrets-operator/internal/config"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

var (
	setupLog = ctrl.Log.WithName("agent")
	scheme   = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hsmv1alpha1.AddToScheme(scheme))
}

// Run starts the agent mode
func Run(args []string) error {
	// Create a new flag set for agent-specific flags
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)

	var deviceName string
	var port int
	var healthPort int
	var pkcs11LibraryPath string
	var slotID int
	var tokenLabel string
	var pin string
	fs.StringVar(&deviceName, "device-name", "", "Name of the HSM device this agent serves")
	fs.IntVar(&port, "port", 9090, "Port for the HSM agent gRPC API")
	fs.IntVar(&healthPort, "health-port", 8093, "Port for health checks")
	fs.StringVar(&pkcs11LibraryPath, "pkcs11-library", "", "Path to PKCS#11 library")
	fs.IntVar(&slotID, "slot-id", 0, "PKCS#11 slot ID")
	fs.StringVar(&tokenLabel, "token-label", "", "PKCS#11 token label")
	fs.StringVar(&pin, "pin", "", "PKCS#11 PIN (use environment variable HSM_PIN for security)")

	// Parse agent-specific flags from the remaining unparsed arguments
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Validate required parameters - all must be provided via CLI args now
	if deviceName == "" {
		return fmt.Errorf("device name is required via --device-name")
	}
	if pkcs11LibraryPath == "" {
		return fmt.Errorf("PKCS11 library path is required via --pkcs11-library")
	}
	if tokenLabel == "" {
		return fmt.Errorf("token label is required via --token-label")
	}

	setupLog.Info("Starting HSM agent",
		"device", deviceName,
		"port", port,
		"health-port", healthPort,
		"protocol", "gRPC",
		"pkcs11-library", pkcs11LibraryPath,
		"slot-id", slotID,
		"token-label", tokenLabel,
	)

	// Get Kubernetes clients for certificate management and PIN access
	var k8sClient client.Client
	var k8sTypedClient kubernetes.Interface
	var enableTLS bool
	if kubeConfig, err := config.GetConfig(); err == nil {
		if k8sClient, err = client.New(kubeConfig, client.Options{Scheme: scheme}); err != nil {
			setupLog.Error(err, "Failed to create Kubernetes client, TLS disabled")
		} else {
			enableTLS = true
			setupLog.Info("Kubernetes client created, TLS enabled")
		}
		if k8sTypedClient, err = kubernetes.NewForConfig(kubeConfig); err != nil {
			setupLog.Error(err, "Failed to create typed Kubernetes client")
			return err
		}
	} else {
		setupLog.Error(err, "Failed to get Kubernetes config")
		return err
	}

	// Create configuration from environment variables (downward API only)
	agentConfig, err := agentconfig.NewAgentConfigFromEnv()
	if err != nil {
		return fmt.Errorf("failed to create agent config: %w", err)
	}

	// Set CLI args into config
	agentConfig.DeviceName = deviceName
	agentConfig.PKCS11LibraryPath = pkcs11LibraryPath
	agentConfig.TokenLabel = tokenLabel

	// Validate complete configuration
	if err := agentConfig.Validate(); err != nil {
		return fmt.Errorf("invalid agent configuration: %w", err)
	}

	// Create HSM client
	var hsmClient hsm.Client

	if agentConfig.PKCS11LibraryPath != "" {
		// Create PIN provider for Kubernetes Secret access
		pinProvider := hsm.NewKubernetesPINProvider(k8sClient, k8sTypedClient, agentConfig.DeviceName, agentConfig.PodNamespace)

		// Create PKCS#11 client for production use
		hsmConfig := hsm.Config{
			PKCS11LibraryPath: agentConfig.PKCS11LibraryPath,
			SlotID:            uint(slotID),
			TokenLabel:        agentConfig.TokenLabel,
			ConnectionTimeout: 30 * time.Second,
			RetryAttempts:     3,
			RetryDelay:        2 * time.Second,
			PINProvider:       pinProvider,
		}

		hsmClient = hsm.NewPKCS11Client()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := hsmClient.Initialize(ctx, hsmConfig); err != nil {
			setupLog.Error(err, "Failed to initialize PKCS#11 client")
			return err
		}
	} else {
		// Use mock client for testing
		setupLog.Info("No PKCS#11 library specified, using mock client")
		hsmClient = hsm.NewMockClient()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// For mock client, use a static PIN provider
		mockConfig := hsm.DefaultConfig()
		mockConfig.PINProvider = hsm.NewStaticPINProvider("123456") // Mock PIN
		if err := hsmClient.Initialize(ctx, mockConfig); err != nil {
			setupLog.Error(err, "Failed to initialize mock client")
			return err
		}
	}

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		setupLog.Info("Received signal, shutting down", "signal", sig)
		cancel()
	}()

	// Configure gRPC server
	serverConfig := agent.GRPCServerConfig{
		ServiceName: fmt.Sprintf("hsm-agent-%s", agentConfig.DeviceName),
		Namespace:   agentConfig.PodNamespace,
		K8sClient:   k8sClient,
		EnableTLS:   enableTLS,
		DNSNames: []string{
			fmt.Sprintf("hsm-agent-%s", agentConfig.DeviceName),
		},
		IPs: []net.IP{
			net.ParseIP("127.0.0.1"),
		},
	}

	// Start gRPC server
	setupLog.Info("HSM agent ready", "device", agentConfig.DeviceName, "tls_enabled", enableTLS)

	grpcServer := agent.NewGRPCServer(hsmClient, port, healthPort, serverConfig, setupLog)
	if err := grpcServer.Start(ctx); err != nil {
		setupLog.Error(err, "gRPC server failed")
		return err
	}

	// Cleanup
	setupLog.Info("Closing HSM client")
	if err := hsmClient.Close(); err != nil {
		setupLog.Error(err, "Failed to close HSM client")
	}

	setupLog.Info("HSM agent shutdown complete")
	return nil
}
