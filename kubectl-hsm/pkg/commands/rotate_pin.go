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

package commands

import (
	"context"
	"encoding/base64"
	"fmt"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// NewRotatePinCmd creates the rotate-pin command
func NewRotatePinCmd() *cobra.Command {
	var (
		oldPin    string
		newPin    string
		dryRun    bool
		namespace string
		verbose   bool
	)

	cmd := &cobra.Command{
		Use:   "rotate-pin",
		Short: "Rotate HSM PIN on all connected devices",
		Long: `Rotate the PIN for all connected HSM devices in the cluster.

This command will:
1. Validate the old PIN against the current Kubernetes Secret
2. Change the PIN on all HSM devices atomically
3. Provide a kubectl patch command to update the Kubernetes Secret

Examples:
  # Interactive PIN rotation (recommended for security)
  kubectl hsm rotate-pin

  # Specify PINs via flags (less secure due to shell history)
  kubectl hsm rotate-pin --old-pin=123456 --new-pin=654321

  # Dry run to see what would happen
  kubectl hsm rotate-pin --dry-run

  # Rotate PIN in specific namespace
  kubectl hsm rotate-pin --namespace=production`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRotatePin(cmd.Context(), oldPin, newPin, dryRun, namespace, verbose)
		},
	}

	cmd.Flags().StringVar(&oldPin, "old-pin", "", "Current PIN (will prompt if not provided)")
	cmd.Flags().StringVar(&newPin, "new-pin", "", "New PIN (will prompt if not provided)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be done without making changes")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Namespace to use (default: current context namespace)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")

	return cmd
}

func runRotatePin(ctx context.Context, oldPin, newPin string, dryRun bool, namespace string, verbose bool) error {
	// Create client manager
	cm, err := NewClientManager(namespace, verbose)
	if err != nil {
		return fmt.Errorf("failed to initialize client manager: %w", err)
	}
	defer cm.Close()

	// Get current namespace for display
	currentNamespace := cm.GetCurrentNamespace()
	if namespace == "" {
		namespace = currentNamespace
	}

	// Get HSM client
	hsmClient, err := cm.GetClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to HSM operator: %w", err)
	}

	// Get device status to show what devices will be affected
	deviceStatus, err := hsmClient.GetDeviceStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to get device status: %w", err)
	}

	if len(deviceStatus.Devices) == 0 {
		return fmt.Errorf("no HSM devices found - ensure HSM agents are running and devices are connected")
	}

	// Show device information
	fmt.Printf("Found %d HSM device(s) in namespace '%s':\n", len(deviceStatus.Devices), namespace)
	connectedCount := 0
	for deviceName, connected := range deviceStatus.Devices {
		status := "disconnected"
		if connected {
			status = "connected"
			connectedCount++
		}
		fmt.Printf("  - %s: %s\n", deviceName, status)
	}

	if connectedCount == 0 {
		return fmt.Errorf("no HSM devices are currently connected")
	}

	fmt.Printf("\nPIN rotation will affect %d connected device(s).\n\n", connectedCount)

	// Get PINs interactively if not provided
	if oldPin == "" {
		oldPin, err = readPIN("Enter current PIN: ")
		if err != nil {
			return fmt.Errorf("failed to read current PIN: %w", err)
		}
	}

	if newPin == "" {
		newPin, err = readPIN("Enter new PIN: ")
		if err != nil {
			return fmt.Errorf("failed to read new PIN: %w", err)
		}

		// Confirm new PIN
		confirmPin, err := readPIN("Confirm new PIN: ")
		if err != nil {
			return fmt.Errorf("failed to read PIN confirmation: %w", err)
		}

		if newPin != confirmPin {
			return fmt.Errorf("new PIN and confirmation do not match")
		}
	}

	// Validate PINs
	if oldPin == "" {
		return fmt.Errorf("old PIN cannot be empty")
	}
	if newPin == "" {
		return fmt.Errorf("new PIN cannot be empty")
	}
	if oldPin == newPin {
		return fmt.Errorf("new PIN must be different from old PIN")
	}

	// TODO: Validate old PIN against Kubernetes Secret
	// This would require reading the HSM PIN secret and comparing

	if dryRun {
		fmt.Println("DRY RUN: PIN rotation plan")
		fmt.Println("================================")
		fmt.Printf("Current PIN: %s (masked)\n", maskPIN(oldPin))
		fmt.Printf("New PIN: %s (masked)\n", maskPIN(newPin))
		fmt.Printf("Devices to update: %d\n", connectedCount)
		for deviceName, connected := range deviceStatus.Devices {
			if connected {
				fmt.Printf("  - %s\n", deviceName)
			}
		}
		fmt.Println("\nKubectl command to update PIN secret after rotation:")
		fmt.Printf("kubectl patch secret hsm-pin -n %s --type='json' -p='[{\"op\":\"replace\",\"path\":\"/data/pin\",\"value\":\"%s\"}]'\n",
			namespace, encodePINForSecret(newPin))
		fmt.Println("\nNo changes made (dry run).")
		return nil
	}

	// Confirm operation
	fmt.Printf("About to rotate PIN on %d HSM device(s). This operation cannot be undone.\n", connectedCount)
	if !confirmOperation("Continue with PIN rotation? (y/N): ") {
		fmt.Println("PIN rotation cancelled.")
		return nil
	}

	// Perform PIN rotation
	fmt.Println("Rotating PIN on HSM devices...")

	response, err := hsmClient.ChangePIN(ctx, oldPin, newPin)
	if err != nil {
		return fmt.Errorf("PIN rotation failed: %w", err)
	}

	// Check if operation was completely successful
	if len(response.Errors) > 0 {
		fmt.Printf("⚠ PIN rotation completed with warnings:\n")
		fmt.Printf("  Successful devices: %d/%d\n", response.SuccessCount, response.TotalCount)
		fmt.Printf("  Errors:\n")
		for _, errMsg := range response.Errors {
			fmt.Printf("    - %s\n", errMsg)
		}
		fmt.Printf("  Message: %s\n", response.Message)
	} else {
		fmt.Printf("✓ PIN rotation completed successfully on all %d device(s)!\n", response.SuccessCount)
	}

	fmt.Println()
	fmt.Println("IMPORTANT: Update the Kubernetes Secret with the new PIN:")
	fmt.Printf("kubectl patch secret hsm-pin -n %s --type='json' -p='[{\"op\":\"replace\",\"path\":\"/data/pin\",\"value\":\"%s\"}]'\n",
		namespace, encodePINForSecret(newPin))
	fmt.Println()
	fmt.Println("After updating the secret, HSM agents will automatically use the new PIN.")

	return nil
}

// readPIN securely reads a PIN from user input
func readPIN(prompt string) (string, error) {
	fmt.Print(prompt)

	// Read password without echoing
	bytePin, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println() // Add newline after hidden input

	if err != nil {
		return "", fmt.Errorf("failed to read PIN: %w", err)
	}

	return string(bytePin), nil
}

// maskPIN returns a masked version of the PIN for display
func maskPIN(pin string) string {
	if len(pin) <= 2 {
		return "***"
	}
	return pin[:1] + "***" + pin[len(pin)-1:]
}

// encodePINForSecret base64 encodes the PIN for Kubernetes Secret
func encodePINForSecret(pin string) string {
	// kubectl patch expects base64 encoded values for secret data
	return base64.StdEncoding.EncodeToString([]byte(pin))
}

// confirmOperation prompts user for confirmation
func confirmOperation(prompt string) bool {
	fmt.Print(prompt)
	var response string
	fmt.Scanln(&response)
	return response == "y" || response == "Y" || response == "yes" || response == "Yes"
}
