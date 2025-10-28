package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/evanjarrett/hsm-secrets-operator/internal/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
	ctrl "sigs.k8s.io/controller-runtime"
)

func main() {
	if len(os.Args) < 3 {
		printUsage()
		os.Exit(1)
	}

	operation := os.Args[1]
	pin := os.Args[2]

	// Set up logger
	logger := ctrl.Log.WithName("test-hsm")

	// Start pcscd daemon
	logger.Info("Starting pcscd daemon")
	pcscdMgr := agent.NewPCSCDManager(logger, true) // true = debug output enabled
	if err := pcscdMgr.Start(); err != nil {
		log.Fatalf("Failed to start pcscd: %v", err)
	}
	defer func() {
		if err := pcscdMgr.Stop(); err != nil {
			logger.Error(err, "Failed to stop pcscd")
		}
	}()
	logger.Info("pcscd daemon started successfully")

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Info("Received shutdown signal, stopping pcscd")
		if err := pcscdMgr.Stop(); err != nil {
			logger.Error(err, "Failed to stop pcscd during shutdown")
		}
		os.Exit(0)
	}()

	// Get library path from environment or use default
	libraryPath := os.Getenv("PKCS11_LIBRARY")
	if libraryPath == "" {
		// Try common locations (Debian/Ubuntu path first for trixie-slim)
		libraryPaths := []string{
			"/usr/lib/x86_64-linux-gnu/opensc-pkcs11.so",        // Debian/Ubuntu (trixie-slim)
			"/usr/lib/x86_64-linux-gnu/pkcs11/opensc-pkcs11.so", // Debian/Ubuntu (older)
			"/usr/lib64/pkcs11/opensc-pkcs11.so",                // Fedora/RHEL
			"/usr/lib/pkcs11/opensc-pkcs11.so",                  // Generic fallback
		}
		found := false
		for _, path := range libraryPaths {
			if _, err := os.Stat(path); err == nil {
				libraryPath = path
				found = true
				break
			}
		}
		if !found {
			log.Fatal("Could not find PKCS#11 library. Set PKCS11_LIBRARY environment variable.")
		}
	}

	// Create HSM client
	client := hsm.NewPKCS11Client()

	// Configure with static PIN provider
	config := hsm.Config{
		PKCS11LibraryPath: libraryPath,
		SlotID:            0,
		UseSlotID:         true,
		PINProvider:       hsm.NewStaticPINProvider(pin),
	}

	ctx := context.Background()
	if err := client.Initialize(ctx, config); err != nil {
		log.Fatalf("Failed to initialize: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Fatalf("Failed to close client")
		}
	}()

	// Execute operation
	switch operation {
	case "create", "write":
		if len(os.Args) < 4 {
			fmt.Println("Usage: test create <pin> <secret-name> [key1=value1] [key2=value2] ...")
			os.Exit(1)
		}
		secretName := os.Args[3]
		data := make(map[string][]byte)

		// Parse key=value pairs
		if len(os.Args) > 4 {
			for _, arg := range os.Args[4:] {
				key, value := "", ""
				for i, c := range arg {
					if c == '=' {
						key = arg[:i]
						value = arg[i+1:]
						break
					}
				}
				if key == "" {
					fmt.Printf("Invalid argument: %s (expected key=value)\n", arg)
					os.Exit(1)
				}
				data[key] = []byte(value)
			}
		} else {
			// Default empty secret
			data["data"] = []byte("")
		}

		fmt.Printf("Creating secret '%s' with %d keys...\n", secretName, len(data))
		// Convert to SecretData type and write with nil metadata
		secretData := hsm.SecretData(data)
		if err := client.WriteSecret(ctx, secretName, secretData, nil); err != nil {
			log.Fatalf("Failed to write secret: %v", err)
		}
		fmt.Println("✅ Successfully created secret!")

	case "read", "get":
		if len(os.Args) < 4 {
			fmt.Println("Usage: test read <pin> <secret-name>")
			os.Exit(1)
		}
		secretName := os.Args[3]

		fmt.Printf("Reading secret '%s'...\n", secretName)
		data, err := client.ReadSecret(ctx, secretName)
		if err != nil {
			log.Fatalf("Failed to read secret: %v", err)
		}

		fmt.Printf("✅ Secret '%s' has %d keys:\n", secretName, len(data))
		for key, value := range data {
			fmt.Printf("  %s: %s\n", key, string(value))
		}

	case "delete", "remove":
		if len(os.Args) < 4 {
			fmt.Println("Usage: test delete <pin> <secret-name>")
			os.Exit(1)
		}
		secretName := os.Args[3]

		fmt.Printf("Deleting secret '%s'...\n", secretName)
		if err := client.DeleteSecret(ctx, secretName); err != nil {
			log.Fatalf("Failed to delete secret: %v", err)
		}
		fmt.Println("✅ Successfully deleted secret!")

	case "list":
		fmt.Println("Listing all secrets...")
		secrets, err := client.ListSecrets(ctx, "") // Empty prefix = list all
		if err != nil {
			log.Fatalf("Failed to list secrets: %v", err)
		}

		if len(secrets) == 0 {
			fmt.Println("No secrets found")
		} else {
			fmt.Printf("Found %d secrets:\n", len(secrets))
			for _, secret := range secrets {
				fmt.Printf("  - %s\n", secret)
			}
		}

	default:
		fmt.Printf("Unknown operation: %s\n", operation)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("HSM Test Utility")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  test create <pin> <secret-name> [key1=value1] [key2=value2] ...")
	fmt.Println("  test read <pin> <secret-name>")
	fmt.Println("  test delete <pin> <secret-name>")
	fmt.Println("  test list <pin>")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  test create 648219 my-secret username=admin password=secret123")
	fmt.Println("  test read 648219 my-secret")
	fmt.Println("  test delete 648219 my-secret")
	fmt.Println("  test list 648219")
}
