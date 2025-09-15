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

	"github.com/spf13/cobra"
)

// RenameKeyOptions holds options for the rename-key command
type RenameKeyOptions struct {
	CommonOptions
}

// NewRenameKeyCmd creates the rename-key command
func NewRenameKeyCmd() *cobra.Command {
	opts := &RenameKeyOptions{}

	cmd := &cobra.Command{
		Use:   "rename-key SECRET_NAME OLD_KEY NEW_KEY",
		Short: "Rename a key within an existing HSM secret",
		Long: `Atomically rename a key within an existing secret.

This command reads the current value of the old key, creates the new key with the same value,
and removes the old key. The operation is atomic - if any step fails, the secret remains unchanged.

Examples:
  # Rename a key in an existing secret
  kubectl hsm rename-key database-creds username user_name

  # Rename with namespace
  kubectl hsm rename-key api-config old_api_key api_key -n production`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context(), args[0], args[1], args[2])
		},
	}

	cmd.Flags().StringVarP(&opts.Namespace, "namespace", "n", "", "Override the default namespace")
	cmd.Flags().StringVarP(&opts.Output, "output", "o", "text", "Output format (text, json, yaml)")
	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Show verbose output including port forward details")

	// Add completion for output flag
	cmd.RegisterFlagCompletionFunc("output", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json", "yaml"}, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// Run executes the rename-key command
func (opts *RenameKeyOptions) Run(ctx context.Context, secretName, oldKey, newKey string) error {
	// Validate inputs
	if secretName == "" {
		return fmt.Errorf("secret name is required")
	}
	if oldKey == "" {
		return fmt.Errorf("old key name is required")
	}
	if newKey == "" {
		return fmt.Errorf("new key name is required")
	}
	if oldKey == newKey {
		return fmt.Errorf("old key and new key must be different")
	}

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

	// Get the current secret
	fmt.Printf("Reading secret '%s' from namespace '%s'...\n", secretName, cm.GetCurrentNamespace())
	existing, err := hsmClient.GetSecret(ctx, secretName)
	if err != nil {
		return fmt.Errorf("failed to read secret '%s': %w", secretName, err)
	}

	// Check if old key exists
	oldValue, exists := existing.Data[oldKey]
	if !exists {
		return fmt.Errorf("key '%s' does not exist in secret '%s'", oldKey, secretName)
	}

	// Check if new key already exists
	if _, exists := existing.Data[newKey]; exists {
		return fmt.Errorf("key '%s' already exists in secret '%s'. Cannot rename to existing key", newKey, secretName)
	}

	fmt.Printf("Renaming key '%s' to '%s' in secret '%s'...\n", oldKey, newKey, secretName)

	// Create new data map with the renamed key
	newData := make(map[string]any)
	for k, v := range existing.Data {
		if k == oldKey {
			// Replace old key with new key
			newData[newKey] = v
		} else {
			// Keep all other keys unchanged
			newData[k] = v
		}
	}

	// Replace the entire secret with the modified data
	// This ensures atomicity - either all changes are applied or none
	if err := hsmClient.CreateSecretWithOptions(ctx, secretName, newData, true); err != nil {
		return fmt.Errorf("failed to rename key in secret: %w", err)
	}

	fmt.Printf("Key renamed successfully.\n")
	fmt.Printf("  Secret: %s\n", secretName)
	fmt.Printf("  Old key: %s\n", oldKey)
	fmt.Printf("  New key: %s\n", newKey)
	fmt.Printf("  Value: %s...\n", truncateString(fmt.Sprintf("%v", oldValue), 50))

	// Show how to retrieve the secret
	fmt.Printf("\nTo view the updated secret:\n")
	fmt.Printf("  kubectl hsm get %s\n", secretName)
	if opts.Namespace != "" {
		fmt.Printf("  kubectl hsm get %s -n %s\n", secretName, opts.Namespace)
	}

	return nil
}