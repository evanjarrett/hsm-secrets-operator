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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

var (
	setupLog = ctrl.Log.WithName("agent")
)

func main() {
	var deviceName string
	var port int
	var healthPort int
	var pkcs11LibraryPath string
	var slotID int
	var tokenLabel string
	var pin string

	flag.StringVar(&deviceName, "device-name", "", "Name of the HSM device this agent serves")
	flag.IntVar(&port, "port", 8092, "Port for the HSM agent API")
	flag.IntVar(&healthPort, "health-port", 8093, "Port for health checks")
	flag.StringVar(&pkcs11LibraryPath, "pkcs11-library", "", "Path to PKCS#11 library")
	flag.IntVar(&slotID, "slot-id", 0, "PKCS#11 slot ID")
	flag.StringVar(&tokenLabel, "token-label", "", "PKCS#11 token label")
	flag.StringVar(&pin, "pin", "", "PKCS#11 PIN (use environment variable HSM_PIN for security)")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Validate required parameters
	if deviceName == "" {
		deviceName = os.Getenv("HSM_DEVICE_NAME")
		if deviceName == "" {
			setupLog.Error(fmt.Errorf("device name required"), "Device name must be provided via --device-name or HSM_DEVICE_NAME environment variable")
			os.Exit(1)
		}
	}

	// Get configuration from environment variables if not provided via flags
	if pkcs11LibraryPath == "" {
		pkcs11LibraryPath = os.Getenv("PKCS11_LIBRARY_PATH")
	}
	if tokenLabel == "" {
		tokenLabel = os.Getenv("PKCS11_TOKEN_LABEL")
	}
	if pin == "" {
		pin = os.Getenv("PKCS11_PIN")
	}

	setupLog.Info("Starting HSM agent",
		"device", deviceName,
		"port", port,
		"health-port", healthPort,
		"pkcs11-library", pkcs11LibraryPath,
		"slot-id", slotID,
		"token-label", tokenLabel,
	)

	// Create HSM client
	var hsmClient hsm.Client

	if pkcs11LibraryPath != "" {
		// Create PKCS#11 client for production use
		config := hsm.Config{
			PKCS11LibraryPath: pkcs11LibraryPath,
			SlotID:            uint(slotID),
			PIN:               pin,
			TokenLabel:        tokenLabel,
			ConnectionTimeout: 30 * time.Second,
			RetryAttempts:     3,
			RetryDelay:        2 * time.Second,
		}

		hsmClient = hsm.NewPKCS11Client()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := hsmClient.Initialize(ctx, config); err != nil {
			setupLog.Error(err, "Failed to initialize PKCS#11 client")
			os.Exit(1)
		}
	} else {
		// Use mock client for testing
		setupLog.Info("No PKCS#11 library specified, using mock client")
		hsmClient = hsm.NewMockClient()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := hsmClient.Initialize(ctx, hsm.DefaultConfig()); err != nil {
			setupLog.Error(err, "Failed to initialize mock client")
			os.Exit(1)
		}
	}

	// Create agent server
	server := agent.NewServer(hsmClient, deviceName, port, healthPort, setupLog)

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

	// Start server
	setupLog.Info("HSM agent ready", "device", deviceName)

	if err := server.Start(ctx); err != nil {
		setupLog.Error(err, "Server failed")
		os.Exit(1)
	}

	// Cleanup
	setupLog.Info("Closing HSM client")
	if err := hsmClient.Close(); err != nil {
		setupLog.Error(err, "Failed to close HSM client")
	}

	setupLog.Info("HSM agent shutdown complete")
}
