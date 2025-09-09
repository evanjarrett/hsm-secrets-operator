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
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/evanjarrett/hsm-secrets-operator/kubectl-hsm/pkg/client"
)

// HealthOptions holds options for the health command
type HealthOptions struct {
	CommonOptions
}

// NewHealthCmd creates the health command
func NewHealthCmd() *cobra.Command {
	opts := &HealthOptions{}

	cmd := &cobra.Command{
		Use:   "health [flags]",
		Short: "Check HSM operator health",
		Long: `Check the health status of the HSM operator and connected devices.

This command verifies:
- Connection to the HSM operator API
- HSM device connectivity
- Replication status
- Active agent nodes

Examples:
  # Check health status
  kubectl hsm health

  # Check health with JSON output
  kubectl hsm health -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	cmd.Flags().StringVarP(&opts.Namespace, "namespace", "n", "", "Override the default namespace")
	cmd.Flags().StringVarP(&opts.Output, "output", "o", "text", "Output format (text, json, yaml)")
	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Show verbose output including port forward details")

	return cmd
}

// Run executes the health command
func (opts *HealthOptions) Run(ctx context.Context) error {
	// Create client manager
	cm, err := NewClientManager(opts.Namespace, opts.Verbose)
	if err != nil {
		return err
	}
	defer cm.Close()

	// Get HSM client
	hsmClient, err := cm.GetClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to HSM operator: %w", err)
	}

	// Get health status, device status, and device info in parallel
	health, err := hsmClient.GetHealth(ctx)
	if err != nil {
		return fmt.Errorf("failed to get health status: %w", err)
	}

	var deviceStatus *client.DeviceStatusResponse
	var deviceInfo *client.DeviceInfoResponse

	// Get device information for enhanced health display
	if deviceStatus, err = hsmClient.GetDeviceStatus(ctx); err != nil {
		// Don't fail the health check if device status is unavailable
		if opts.Verbose {
			fmt.Fprintf(os.Stderr, "Warning: failed to get device status: %v\n", err)
		}
	}

	if deviceInfo, err = hsmClient.GetDeviceInfo(ctx); err != nil {
		// Don't fail the health check if device info is unavailable
		if opts.Verbose {
			fmt.Fprintf(os.Stderr, "Warning: failed to get device info: %v\n", err)
		}
	}

	// Handle output formatting
	switch opts.Output {
	case "json":
		combinedOutput := map[string]interface{}{
			"health": health,
		}
		if deviceStatus != nil {
			combinedOutput["deviceStatus"] = deviceStatus
		}
		if deviceInfo != nil {
			combinedOutput["deviceInfo"] = deviceInfo
		}
		jsonBytes, err := json.MarshalIndent(combinedOutput, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal health status to JSON: %w", err)
		}
		fmt.Println(string(jsonBytes))
	case "yaml":
		combinedOutput := map[string]interface{}{
			"health": health,
		}
		if deviceStatus != nil {
			combinedOutput["deviceStatus"] = deviceStatus
		}
		if deviceInfo != nil {
			combinedOutput["deviceInfo"] = deviceInfo
		}
		yamlBytes, err := yaml.Marshal(combinedOutput)
		if err != nil {
			return fmt.Errorf("failed to marshal health status to YAML: %w", err)
		}
		fmt.Print(string(yamlBytes))
	default:
		return opts.displayHealthText(health, deviceStatus, deviceInfo, cm.GetCurrentNamespace())
	}

	return nil
}

// displayHealthText displays the health status in a human-readable format
func (opts *HealthOptions) displayHealthText(health *client.HealthStatus, deviceStatus *client.DeviceStatusResponse, deviceInfo *client.DeviceInfoResponse, namespace string) error {
	fmt.Printf("HSM Operator Health Status\n")
	fmt.Printf("==========================\n\n")

	// Overall status with emoji
	statusEmoji := "✅"
	if health.Status == "degraded" {
		statusEmoji = "⚠️"
	} else if health.Status == "unhealthy" {
		statusEmoji = "❌"
	}

	fmt.Printf("Overall Status:    %s %s\n", statusEmoji, health.Status)
	fmt.Printf("Namespace:         %s\n", namespace)
	fmt.Printf("Check Time:        %s\n", health.Timestamp.Format("2006-01-02 15:04:05 UTC"))
	fmt.Printf("\n")

	// Device Status Section
	if deviceStatus != nil && deviceStatus.TotalDevices > 0 {
		fmt.Printf("Device Status:\n")

		// Sort device names for consistent output
		var deviceNames []string
		for deviceName := range deviceStatus.Devices {
			deviceNames = append(deviceNames, deviceName)
		}
		sort.Strings(deviceNames)

		connectedCount := 0
		for _, deviceName := range deviceNames {
			connected := deviceStatus.Devices[deviceName]
			statusIcon := "🔴"
			statusText := "Disconnected"
			if connected {
				statusIcon = "🟢"
				statusText = "Connected"
				connectedCount++
			}

			deviceInfoText := ""
			if deviceInfo != nil && deviceInfo.DeviceInfos[deviceName] != nil {
				info := deviceInfo.DeviceInfos[deviceName]
				if info.Manufacturer != "" && info.Model != "" {
					deviceInfoText = fmt.Sprintf("    (%s %s)", info.Manufacturer, info.Model)
				}
			}

			fmt.Printf("  %s %-15s %s%s\n", statusIcon, deviceName, statusText, deviceInfoText)
		}

		fmt.Printf("\n")
		fmt.Printf("Device Summary:    %d/%d connected\n", connectedCount, deviceStatus.TotalDevices)
	} else {
		// Fallback to basic HSM connectivity info
		hsmEmoji := "✅"
		if !health.HSMConnected {
			hsmEmoji = "❌"
		}
		fmt.Printf("HSM Connected:     %s %t\n", hsmEmoji, health.HSMConnected)
	}

	// Replication status
	replicationEmoji := "✅"
	if !health.ReplicationEnabled {
		replicationEmoji = "⚠️"
	}
	fmt.Printf("Replication:       %s %t (%d nodes)\n", replicationEmoji, health.ReplicationEnabled, health.ActiveNodes)
	fmt.Printf("\n")

	// Enhanced recommendations based on device status
	if deviceStatus != nil {
		connectedCount := 0
		for _, connected := range deviceStatus.Devices {
			if connected {
				connectedCount++
			}
		}

		if connectedCount == 0 {
			fmt.Printf("⚠️  Recommendations:\n")
			fmt.Printf("   • All devices are disconnected - check device connections\n")
			fmt.Printf("   • Verify HSM agent pods are running: kubectl get pods -l app.kubernetes.io/name=hsm-agent\n")
			fmt.Printf("   • Check agent logs for connection errors\n")
		} else if connectedCount < deviceStatus.TotalDevices {
			fmt.Printf("⚠️  Recommendations:\n")
			fmt.Printf("   • Some devices are disconnected - check device connections\n")
			fmt.Printf("   • Verify all agent pods are healthy\n")
		} else if connectedCount == 1 {
			fmt.Printf("💡 Recommendations:\n")
			fmt.Printf("   • Consider adding more HSM devices for high availability\n")
			fmt.Printf("   • Multiple devices enable automatic replication and failover\n")
		}
	} else if !health.HSMConnected {
		fmt.Printf("⚠️  Recommendations:\n")
		fmt.Printf("   • Check if HSM devices are connected and accessible\n")
		fmt.Printf("   • Verify HSM agent pods are running: kubectl get pods -l app.kubernetes.io/component=agent\n")
		fmt.Printf("   • Check agent logs for connection errors\n")
	}

	// Overall assessment
	if health.Status == "healthy" {
		if deviceStatus != nil {
			connectedCount := 0
			for _, connected := range deviceStatus.Devices {
				if connected {
					connectedCount++
				}
			}
			if connectedCount > 1 {
				fmt.Printf("🎉 All systems operational with %d devices providing high availability!\n", connectedCount)
			} else {
				fmt.Printf("🎉 All systems operational!\n")
			}
		} else {
			fmt.Printf("🎉 All systems operational!\n")
		}
	}

	return nil
}
