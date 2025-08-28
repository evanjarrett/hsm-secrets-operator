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

// ListOptions holds options for the list command
type ListOptions struct {
	CommonOptions
	AllNamespaces bool
}

// NewListCmd creates the list command
func NewListCmd() *cobra.Command {
	opts := &ListOptions{}

	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: "List HSM secrets",
		Long: `List all secrets stored in the HSM.

Examples:
  # List secrets in current namespace
  kubectl hsm list

  # List secrets in specific namespace  
  kubectl hsm list -n hsm-secrets-operator-system

  # List secrets in all namespaces (Note: HSM secrets are global, namespace is for display only)
  kubectl hsm list --all-namespaces

  # Output in different formats
  kubectl hsm list -o json
  kubectl hsm list -o yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	cmd.Flags().BoolVar(&opts.AllNamespaces, "all-namespaces", false, "List secrets from all namespaces")
	cmd.Flags().StringVarP(&opts.Namespace, "namespace", "n", "", "Override the default namespace")
	cmd.Flags().StringVarP(&opts.Output, "output", "o", "text", "Output format (text, json, yaml)")
	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Show verbose output including port forward details")

	// Add completion for output flag
	cmd.RegisterFlagCompletionFunc("output", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json", "yaml"}, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// Run executes the list command
func (opts *ListOptions) Run(ctx context.Context) error {
	// Create client manager
	cm, err := NewClientManager(opts.Namespace, opts.Verbose)
	if err != nil {
		return err
	}
	defer cm.Close()

	// Get HSM client
	hsmClient, err := cm.GetClient(ctx)
	if err != nil {
		return err
	}

	// List secrets (HSM secrets are global, but we display namespace context)
	secretList, err := hsmClient.ListSecrets(ctx, 0, 0) // No pagination for now
	if err != nil {
		return fmt.Errorf("failed to list secrets: %w", err)
	}

	// Handle output formatting
	switch opts.Output {
	case "json":
		// Create clean output without pagination fields
		cleanOutput := map[string]interface{}{
			"count":   secretList.Count,
			"secrets": secretList.Secrets,
		}
		jsonBytes, err := json.MarshalIndent(cleanOutput, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal secrets to JSON: %w", err)
		}
		fmt.Println(string(jsonBytes))
	case "yaml":
		// Create clean output without pagination fields  
		cleanOutput := map[string]interface{}{
			"count":   secretList.Count,
			"secrets": secretList.Secrets,
		}
		yamlBytes, err := yaml.Marshal(cleanOutput)
		if err != nil {
			return fmt.Errorf("failed to marshal secrets to YAML: %w", err)
		}
		fmt.Print(string(yamlBytes))
	default:
		return opts.displaySecretsText(secretList, cm.GetCurrentNamespace())
	}

	return nil
}

// displaySecretsText displays the secrets in a human-readable table format
func (opts *ListOptions) displaySecretsText(secretList *client.SecretList, currentNamespace string) error {
	if secretList == nil {
		fmt.Println("No secrets found")
		return nil
	}

	// The API returns secret names as strings, not full SecretInfo objects
	if len(secretList.Secrets) == 0 {
		fmt.Println("No secrets found")
		return nil
	}

	// Sort secret names for consistent output
	secrets := make([]string, len(secretList.Secrets))
	copy(secrets, secretList.Secrets)
	sort.Strings(secrets)

	// Create table writer
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Print header
	if opts.AllNamespaces {
		fmt.Fprintln(w, "NAMESPACE\tNAME")
	} else {
		fmt.Fprintln(w, "NAME")
	}

	// Print each secret name
	for _, secret := range secrets {
		if opts.AllNamespaces {
			fmt.Fprintf(w, "%s\t%s\n", currentNamespace, secret)
		} else {
			fmt.Fprintf(w, "%s\n", secret)
		}
	}

	w.Flush()

	// Show summary
	fmt.Printf("\nTotal: %d secrets\n", secretList.Count)

	return nil
}

// displaySecretPaths displays just the secret paths when detailed info isn't available
func (opts *ListOptions) displaySecretPaths(secretList *client.SecretList, currentNamespace string) error {
	if len(secretList.Paths) == 0 {
		fmt.Println("No secrets found")
		return nil
	}

	// Sort paths for consistent output
	paths := make([]string, len(secretList.Paths))
	copy(paths, secretList.Paths)
	sort.Strings(paths)

	// Create table writer
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Print header
	if opts.AllNamespaces {
		fmt.Fprintln(w, "NAMESPACE\tNAME")
	} else {
		fmt.Fprintln(w, "NAME")
	}

	// Print each path
	for _, path := range paths {
		if opts.AllNamespaces {
			fmt.Fprintf(w, "%s\t%s\n", currentNamespace, path)
		} else {
			fmt.Fprintf(w, "%s\n", path)
		}
	}

	w.Flush()

	// Show summary
	fmt.Printf("\nTotal: %d secrets\n", secretList.Count)

	return nil
}

// formatBytes formats a byte count in human-readable format
func formatBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	
	units := []string{"B", "KB", "MB", "GB"}
	size := float64(bytes)
	unitIndex := 0
	
	for unitIndex < len(units)-1 && size >= 1024 {
		size /= 1024
		unitIndex++
	}
	
	if size == float64(int64(size)) {
		return fmt.Sprintf("%.0f%s", size, units[unitIndex])
	}
	return fmt.Sprintf("%.1f%s", size, units[unitIndex])
}