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
	"text/tabwriter"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/evanjarrett/hsm-secrets-operator/kubectl-hsm/pkg/client"
)

// DevicesOptions holds options for the devices command
type DevicesOptions struct {
	CommonOptions
	Detailed bool
}

// NewDevicesCmd creates the devices command
func NewDevicesCmd() *cobra.Command {
	opts := &DevicesOptions{}

	cmd := &cobra.Command{
		Use:   "devices [flags]",
		Short: "List HSM devices",
		Long: `List all HSM devices and their connectivity status.

This command shows:
- Device connectivity status (connected/disconnected)  
- Device names and identifiers
- Basic device information (manufacturer, model, serial)
- Detailed device specifications (with --detailed flag)

Examples:
  # List all HSM devices
  kubectl hsm devices

  # Show detailed device information
  kubectl hsm devices --detailed

  # Output in JSON format
  kubectl hsm devices -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	cmd.Flags().BoolVar(&opts.Detailed, "detailed", false, "Show detailed device information")
	cmd.Flags().StringVarP(&opts.Namespace, "namespace", "n", "", "Override the default namespace")
	cmd.Flags().StringVarP(&opts.Output, "output", "o", "text", "Output format (text, json, yaml)")
	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Show verbose output including port forward details")

	// Add completion for output flag
	cmd.RegisterFlagCompletionFunc("output", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json", "yaml"}, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// Run executes the devices command
func (opts *DevicesOptions) Run(ctx context.Context) error {
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

	// Get device status and info in parallel
	statusResponse, err := hsmClient.GetDeviceStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to get device status: %w", err)
	}

	var infoResponse *client.DeviceInfoResponse
	if opts.Detailed || opts.Output != "text" {
		infoResponse, err = hsmClient.GetDeviceInfo(ctx)
		if err != nil {
			return fmt.Errorf("failed to get device info: %w", err)
		}
	}

	// Handle output formatting
	switch opts.Output {
	case "json":
		combinedOutput := map[string]interface{}{
			"devices":      statusResponse.Devices,
			"totalDevices": statusResponse.TotalDevices,
		}
		if infoResponse != nil {
			combinedOutput["deviceInfos"] = infoResponse.DeviceInfos
		}
		jsonBytes, err := json.MarshalIndent(combinedOutput, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal device data to JSON: %w", err)
		}
		fmt.Println(string(jsonBytes))
	case "yaml":
		combinedOutput := map[string]interface{}{
			"devices":      statusResponse.Devices,
			"totalDevices": statusResponse.TotalDevices,
		}
		if infoResponse != nil {
			combinedOutput["deviceInfos"] = infoResponse.DeviceInfos
		}
		yamlBytes, err := yaml.Marshal(combinedOutput)
		if err != nil {
			return fmt.Errorf("failed to marshal device data to YAML: %w", err)
		}
		fmt.Print(string(yamlBytes))
	default:
		return opts.displayDevicesText(statusResponse, infoResponse, cm.GetCurrentNamespace())
	}

	return nil
}

// displayDevicesText displays the devices in a human-readable format
func (opts *DevicesOptions) displayDevicesText(statusResponse *client.DeviceStatusResponse, infoResponse *client.DeviceInfoResponse, namespace string) error {
	fmt.Printf("HSM Devices\n")
	fmt.Printf("===========\n\n")
	fmt.Printf("Namespace:     %s\n", namespace)
	fmt.Printf("Total Devices: %d\n\n", statusResponse.TotalDevices)

	if statusResponse.TotalDevices == 0 {
		fmt.Println("No HSM devices found.")
		fmt.Println("\n💡 Recommendations:")
		fmt.Println("   • Check if HSM devices are connected and accessible")
		fmt.Println("   • Verify discovery pods are running: kubectl get pods -l app.kubernetes.io/component=discovery")
		fmt.Println("   • Check HSMPool status: kubectl get hsmpool")
		return nil
	}

	// Sort device names for consistent output
	var deviceNames []string
	for deviceName := range statusResponse.Devices {
		deviceNames = append(deviceNames, deviceName)
	}
	sort.Strings(deviceNames)

	if opts.Detailed {
		// Detailed view - show each device with full information
		for i, deviceName := range deviceNames {
			if i > 0 {
				fmt.Println()
			}
			opts.displayDetailedDevice(deviceName, statusResponse.Devices[deviceName], infoResponse)
		}
	} else {
		// Table view - compact format
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATUS\tMANUFACTURER\tMODEL\tSERIAL")

		for _, deviceName := range deviceNames {
			connected := statusResponse.Devices[deviceName]
			statusIcon := "🔴"
			statusText := "Disconnected"
			if connected {
				statusIcon = "🟢"
				statusText = "Connected"
			}

			manufacturer := "Unknown"
			model := "Unknown"
			serial := "Unknown"

			if infoResponse != nil && infoResponse.DeviceInfos[deviceName] != nil {
				info := infoResponse.DeviceInfos[deviceName]
				if info.Manufacturer != "" {
					manufacturer = info.Manufacturer
				}
				if info.Model != "" {
					model = info.Model
				}
				if info.SerialNumber != "" {
					serial = info.SerialNumber
				}
			}

			fmt.Fprintf(w, "%s\t%s %s\t%s\t%s\t%s\n",
				deviceName, statusIcon, statusText, manufacturer, model, serial)
		}

		w.Flush()
	}

	// Summary and recommendations
	connectedCount := 0
	for _, connected := range statusResponse.Devices {
		if connected {
			connectedCount++
		}
	}

	fmt.Printf("\nSummary: %d/%d devices connected\n", connectedCount, statusResponse.TotalDevices)

	if connectedCount == 0 {
		fmt.Printf("\n⚠️  All devices are disconnected!\n")
		fmt.Printf("   • Check HSM device connections\n")
		fmt.Printf("   • Verify agent pods are running: kubectl get pods -l app.kubernetes.io/name=hsm-agent\n")
		fmt.Printf("   • Check agent logs for connection errors\n")
	} else if connectedCount < statusResponse.TotalDevices {
		fmt.Printf("\n⚠️  Some devices are disconnected\n")
		fmt.Printf("   • Check disconnected device connections\n")
		fmt.Printf("   • Verify agent pods are healthy\n")
	} else if connectedCount > 1 {
		fmt.Printf("\n🎉 All devices connected - replication enabled!\n")
	} else {
		fmt.Printf("\n💡 Consider adding more devices for high availability\n")
	}

	return nil
}

// displayDetailedDevice displays detailed information for a single device
func (opts *DevicesOptions) displayDetailedDevice(deviceName string, connected bool, infoResponse *client.DeviceInfoResponse) {
	statusIcon := "🔴"
	statusText := "Disconnected"
	if connected {
		statusIcon = "🟢"
		statusText = "Connected"
	}

	fmt.Printf("Device: %s %s %s\n", statusIcon, deviceName, statusText)
	fmt.Printf("Status: %s\n", statusText)

	if infoResponse != nil && infoResponse.DeviceInfos[deviceName] != nil {
		info := infoResponse.DeviceInfos[deviceName]

		if info.Manufacturer != "" {
			fmt.Printf("Manufacturer: %s\n", info.Manufacturer)
		}
		if info.Model != "" {
			fmt.Printf("Model: %s\n", info.Model)
		}
		if info.SerialNumber != "" {
			fmt.Printf("Serial Number: %s\n", info.SerialNumber)
		}
		if info.FirmwareInfo != "" {
			fmt.Printf("Firmware: %s\n", info.FirmwareInfo)
		}
		if info.LibraryInfo != "" {
			fmt.Printf("Library: %s\n", info.LibraryInfo)
		}
		fmt.Printf("Slot ID: %d\n", info.SlotID)
		fmt.Printf("Token Present: %t\n", info.TokenPresent)
		if info.TokenLabel != "" {
			fmt.Printf("Token Label: %s\n", info.TokenLabel)
		}
	} else {
		fmt.Printf("Device information: Not available\n")
	}
}
