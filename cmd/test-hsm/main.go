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
	"log"
	"os"
	"time"

	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

func main() {
	var (
		libraryPath = flag.String("library", "", "Path to PKCS#11 library (required)")
		slotID      = flag.Uint("slot", 0, "PKCS#11 slot ID")
		useSlotID   = flag.Bool("use-slot-id", false, "Use specific slot ID instead of auto-discovery")
		pin         = flag.String("pin", "", "HSM PIN (required)")
		tokenLabel  = flag.String("token", "", "Token label for auto-discovery")
		testPath    = flag.String("path", "test/hsm-operator", "Secret path to test")
		operation   = flag.String("op", "test", "Operation: test, list, info, write, read, delete")
	)
	flag.Parse()

	if *libraryPath == "" {
		fmt.Println("ERROR: -library is required")
		fmt.Println("\nCommon PKCS#11 libraries:")
		fmt.Println("  OpenSC: /usr/lib/pkcs11/opensc-pkcs11.so")
		fmt.Println("  SoftHSM: /usr/lib/softhsm/libsofthsm2.so") 
		fmt.Println("  Pico HSM: /usr/local/lib/libsc-hsm-pkcs11.so")
		os.Exit(1)
	}

	if *pin == "" {
		fmt.Println("ERROR: -pin is required")
		os.Exit(1)
	}

	// Create PKCS#11 client
	client := hsm.NewPKCS11Client()
	defer client.Close()

	// Configure HSM
	config := hsm.Config{
		PKCS11LibraryPath: *libraryPath,
		SlotID:            *slotID,
		UseSlotID:         *useSlotID,
		PIN:               *pin,
		TokenLabel:        *tokenLabel,
		ConnectionTimeout: 30 * time.Second,
		RetryAttempts:     3,
		RetryDelay:        2 * time.Second,
	}

	ctx := context.Background()

	fmt.Printf("🔐 Testing HSM with library: %s\n", *libraryPath)
	if *useSlotID {
		fmt.Printf("📍 Using slot ID: %d\n", *slotID)
	} else if *tokenLabel != "" {
		fmt.Printf("🏷️  Looking for token: %s\n", *tokenLabel)
	} else {
		fmt.Printf("🔍 Auto-discovering first available slot\n")
	}

	// Initialize connection
	fmt.Print("🔌 Connecting to HSM... ")
	if err := client.Initialize(ctx, config); err != nil {
		fmt.Printf("❌ FAILED\n")
		log.Fatalf("Failed to initialize HSM: %v", err)
	}
	fmt.Printf("✅ SUCCESS\n")

	// Get HSM info
	fmt.Print("ℹ️  Getting HSM info... ")
	info, err := client.GetInfo(ctx)
	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
	} else {
		fmt.Printf("✅ SUCCESS\n")
		fmt.Printf("   Label: %s\n", info.Label)
		fmt.Printf("   Manufacturer: %s\n", info.Manufacturer)
		fmt.Printf("   Model: %s\n", info.Model)
		fmt.Printf("   Serial: %s\n", info.SerialNumber)
		fmt.Printf("   Firmware: %s\n", info.FirmwareVersion)
	}

	switch *operation {
	case "info":
		// Already displayed above
		return

	case "list":
		fmt.Print("📋 Listing secrets... ")
		paths, err := client.ListSecrets(ctx, "")
		if err != nil {
			fmt.Printf("❌ FAILED: %v\n", err)
			return
		}
		fmt.Printf("✅ Found %d secrets\n", len(paths))
		for i, path := range paths {
			fmt.Printf("   %d. %s\n", i+1, path)
		}

	case "write":
		testData := hsm.SecretData{
			"username": []byte("test-user"),
			"password": []byte("test-password-123"),
			"api-key":  []byte("sk-test123456789"),
		}

		fmt.Printf("✍️  Writing test secret to '%s'... ", *testPath)
		if err := client.WriteSecret(ctx, *testPath, testData); err != nil {
			fmt.Printf("❌ FAILED: %v\n", err)
			return
		}
		fmt.Printf("✅ SUCCESS\n")
		fmt.Printf("   Written %d keys: username, password, api-key\n", len(testData))

	case "read":
		fmt.Printf("📖 Reading secret from '%s'... ", *testPath)
		data, err := client.ReadSecret(ctx, *testPath)
		if err != nil {
			fmt.Printf("❌ FAILED: %v\n", err)
			return
		}
		fmt.Printf("✅ SUCCESS\n")
		fmt.Printf("   Found %d keys:\n", len(data))
		for key, value := range data {
			fmt.Printf("   %s: %s\n", key, string(value))
		}

	case "delete":
		fmt.Printf("🗑️  Deleting secret '%s'... ", *testPath)
		if err := client.DeleteSecret(ctx, *testPath); err != nil {
			fmt.Printf("❌ FAILED: %v\n", err)
			return
		}
		fmt.Printf("✅ SUCCESS\n")

	case "test":
		// Full test cycle
		testData := hsm.SecretData{
			"test-key": []byte("test-value-" + time.Now().Format("15:04:05")),
		}

		fmt.Printf("\n🧪 Running full test cycle with path: %s\n", *testPath)

		// Test write
		fmt.Print("1️⃣  Writing test data... ")
		if err := client.WriteSecret(ctx, *testPath, testData); err != nil {
			fmt.Printf("❌ FAILED: %v\n", err)
			return
		}
		fmt.Printf("✅ SUCCESS\n")

		// Test read
		fmt.Print("2️⃣  Reading test data... ")
		readData, err := client.ReadSecret(ctx, *testPath)
		if err != nil {
			fmt.Printf("❌ FAILED: %v\n", err)
			return
		}
		fmt.Printf("✅ SUCCESS\n")

		// Verify data
		fmt.Print("3️⃣  Verifying data integrity... ")
		if len(readData) != len(testData) {
			fmt.Printf("❌ FAILED: key count mismatch\n")
			return
		}
		for key, expectedValue := range testData {
			if actualValue, exists := readData[key]; !exists {
				fmt.Printf("❌ FAILED: missing key '%s'\n", key)
				return
			} else if string(actualValue) != string(expectedValue) {
				fmt.Printf("❌ FAILED: value mismatch for key '%s'\n", key)
				return
			}
		}
		fmt.Printf("✅ SUCCESS\n")

		// Test checksum
		fmt.Print("4️⃣  Testing checksum... ")
		checksum, err := client.GetChecksum(ctx, *testPath)
		if err != nil {
			fmt.Printf("❌ FAILED: %v\n", err)
			return
		}
		fmt.Printf("✅ SUCCESS: %s\n", checksum[:16]+"...")

		// Test list
		fmt.Print("5️⃣  Testing list operation... ")
		paths, err := client.ListSecrets(ctx, "test")
		if err != nil {
			fmt.Printf("❌ FAILED: %v\n", err)
			return
		}
		found := false
		for _, path := range paths {
			if path == *testPath {
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("❌ FAILED: test path not found in list\n")
			return
		}
		fmt.Printf("✅ SUCCESS\n")

		// Test delete
		fmt.Print("6️⃣  Cleaning up (delete)... ")
		if err := client.DeleteSecret(ctx, *testPath); err != nil {
			fmt.Printf("❌ FAILED: %v\n", err)
			return
		}
		fmt.Printf("✅ SUCCESS\n")

		// Verify deletion
		fmt.Print("7️⃣  Verifying deletion... ")
		_, err = client.ReadSecret(ctx, *testPath)
		if err == nil {
			fmt.Printf("❌ FAILED: secret still exists after deletion\n")
			return
		}
		fmt.Printf("✅ SUCCESS\n")

		fmt.Printf("\n🎉 All tests passed! HSM PKCS#11 implementation is working correctly.\n")

	default:
		fmt.Printf("❌ Unknown operation: %s\n", *operation)
		fmt.Println("Available operations: info, list, write, read, delete, test")
		os.Exit(1)
	}
}