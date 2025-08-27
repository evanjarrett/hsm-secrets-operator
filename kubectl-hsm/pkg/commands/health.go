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

	// Get health status
	health, err := hsmClient.GetHealth(ctx)
	if err != nil {
		return fmt.Errorf("failed to get health status: %w", err)
	}

	// Handle output formatting
	switch opts.Output {
	case "json":
		jsonBytes, err := json.MarshalIndent(health, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal health status to JSON: %w", err)
		}
		fmt.Println(string(jsonBytes))
	case "yaml":
		yamlBytes, err := yaml.Marshal(health)
		if err != nil {
			return fmt.Errorf("failed to marshal health status to YAML: %w", err)
		}
		fmt.Print(string(yamlBytes))
	default:
		return opts.displayHealthText(health, cm.GetCurrentNamespace())
	}

	return nil
}

// displayHealthText displays the health status in a human-readable format
func (opts *HealthOptions) displayHealthText(health *client.HealthStatus, namespace string) error {
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

	// HSM connectivity
	hsmEmoji := "✅"
	if !health.HSMConnected {
		hsmEmoji = "❌"
	}
	fmt.Printf("HSM Connected:     %s %t\n", hsmEmoji, health.HSMConnected)
	
	// Replication status
	replicationEmoji := "✅"
	if !health.ReplicationEnabled {
		replicationEmoji = "⚠️"
	}
	fmt.Printf("Replication:       %s %t\n", replicationEmoji, health.ReplicationEnabled)
	fmt.Printf("Active Nodes:      %d\n", health.ActiveNodes)
	fmt.Printf("\n")

	// Recommendations
	if !health.HSMConnected {
		fmt.Printf("⚠️  Recommendations:\n")
		fmt.Printf("   • Check if HSM devices are connected and accessible\n")
		fmt.Printf("   • Verify HSM agent pods are running: kubectl get pods -l app.kubernetes.io/component=agent\n")
		fmt.Printf("   • Check agent logs for connection errors\n")
	}

	if !health.ReplicationEnabled && health.ActiveNodes <= 1 {
		fmt.Printf("💡 Recommendations:\n")
		fmt.Printf("   • Consider adding more HSM devices for high availability\n")
		fmt.Printf("   • Multiple devices enable automatic replication and failover\n")
	}

	// Overall assessment
	if health.Status == "healthy" {
		fmt.Printf("🎉 All systems operational!\n")
	}

	return nil
}