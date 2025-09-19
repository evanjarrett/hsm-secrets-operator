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
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/evanjarrett/hsm-secrets-operator/kubectl-hsm/pkg/util"
)

// NewAuthCmd creates a new auth command
func NewAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication for HSM API",
		Long: `Manage authentication for the HSM Secrets Operator API.

This command provides authentication management including:
- Token validation
- Cache management
- Authentication status

The plugin automatically handles JWT token generation and caching
using Kubernetes service account tokens.`,
	}

	cmd.AddCommand(NewAuthStatusCmd())
	cmd.AddCommand(NewAuthClearCmd())
	cmd.AddCommand(NewAuthConfigCmd())

	return cmd
}

// NewAuthStatusCmd creates a new auth status command
func NewAuthStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show authentication status",
		Long:  "Display the current authentication status and token information.",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := util.CreateClient()
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			fmt.Println("HSM API Authentication Status")
			fmt.Println("============================")

			// Try to make an authenticated request to check status
			health, err := client.GetHealth(ctx)
			if err != nil {
				fmt.Printf("❌ Authentication: FAILED\n")
				fmt.Printf("   Error: %v\n", err)
				fmt.Println("\nTo troubleshoot:")
				fmt.Println("1. Ensure you have a valid service account with HSM permissions")
				fmt.Println("2. Check that the HSM API server is running")
				fmt.Println("3. Verify your kubeconfig is correctly configured")
				fmt.Println("4. Run 'kubectl hsm auth clear' to clear cached credentials")
				return nil
			}

			fmt.Printf("✅ Authentication: SUCCESS\n")
			fmt.Printf("   API Status: %s\n", health.Status)
			fmt.Printf("   HSM Connected: %t\n", health.HSMConnected)
			fmt.Printf("   Active Nodes: %d\n", health.ActiveNodes)
			fmt.Printf("   Replication: %t\n", health.ReplicationEnabled)

			return nil
		},
	}

	return cmd
}

// NewAuthClearCmd creates a new auth clear command
func NewAuthClearCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear cached authentication tokens",
		Long: `Clear cached JWT tokens from the local cache.

This forces the plugin to re-authenticate on the next API request.
Useful for troubleshooting authentication issues or switching contexts.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := util.CreateClient()
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			if err := client.ClearAuthCache(); err != nil {
				fmt.Printf("Failed to clear authentication cache: %v\n", err)
				return err
			}

			fmt.Println("✅ Authentication cache cleared successfully")
			fmt.Println("Next API request will re-authenticate automatically")
			return nil
		},
	}

	return cmd
}

// NewAuthConfigCmd creates a new auth config command
func NewAuthConfigCmd() *cobra.Command {
	var serviceAccount string
	var namespace string

	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configure authentication settings",
		Long: `Configure authentication settings for the HSM API client.

This command allows you to specify which service account and namespace
to use for authentication. The settings persist for the current session.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := util.CreateClient()
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			if serviceAccount != "" {
				client.SetServiceAccount(serviceAccount)
				fmt.Printf("✅ Service account set to: %s\n", serviceAccount)
			}

			if namespace != "" {
				client.SetNamespace(namespace)
				fmt.Printf("✅ Namespace set to: %s\n", namespace)
			}

			if serviceAccount == "" && namespace == "" {
				fmt.Println("No configuration changes made.")
				fmt.Println("\nUsage:")
				fmt.Println("  kubectl hsm auth config --service-account=my-sa --namespace=my-ns")
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&serviceAccount, "service-account", "", "Service account name to use for authentication")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Namespace for the service account")

	return cmd
}