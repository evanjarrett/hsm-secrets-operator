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
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/evanjarrett/hsm-secrets-operator/kubectl-hsm/pkg/commands"
)

var version = "dev" // Set during build

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kubectl-hsm",
		Short: "Kubernetes plugin for HSM secret management",
		Long: `kubectl-hsm is a kubectl plugin that provides a Kubernetes-native
command-line interface for Hardware Security Module (HSM) secret management.

This plugin integrates with the HSM Secrets Operator to provide secure
secret storage using hardware-based security modules while maintaining
seamless integration with Kubernetes workflows.

Examples:
  # Create a new secret interactively (recommended for sensitive data)
  kubectl hsm create database-creds --interactive

  # Create a secret with literal values
  kubectl hsm create api-config --from-literal api_key=abc123 --from-literal endpoint=https://api.example.com

  # List all secrets
  kubectl hsm list

  # Get a specific secret
  kubectl hsm get database-creds

  # Delete a secret (with confirmation)
  kubectl hsm delete old-credentials

  # Check operator health
  kubectl hsm health

For more information about the HSM Secrets Operator, visit:
https://github.com/evanjarrett/hsm-secrets-operator`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Add version command
	cmd.AddCommand(newVersionCmd())

	// Add core secret management commands
	cmd.AddCommand(commands.NewCreateCmd())
	cmd.AddCommand(commands.NewGetCmd())
	cmd.AddCommand(commands.NewListCmd())
	cmd.AddCommand(commands.NewDeleteCmd())

	// Add operational commands
	cmd.AddCommand(commands.NewHealthCmd())

	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Long:  "Display the version of kubectl-hsm plugin and related information.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("kubectl-hsm version: %s\n", version)
			fmt.Printf("Compatible with: HSM Secrets Operator v1alpha1\n")
			fmt.Printf("Plugin type: kubectl plugin\n")
		},
	}
}