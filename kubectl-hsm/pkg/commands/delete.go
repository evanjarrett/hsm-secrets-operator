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
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// DeleteOptions holds options for the delete command
type DeleteOptions struct {
	CommonOptions
	Key   string
	Force bool
}

// NewDeleteCmd creates the delete command
func NewDeleteCmd() *cobra.Command {
	opts := &DeleteOptions{}

	cmd := &cobra.Command{
		Use:   "delete SECRET_NAME [flags]",
		Short: "Delete an HSM secret",
		Long: `Delete an HSM secret or a specific key within a secret.

By default, deletes the entire secret. Use --key to delete only a specific key.

Examples:
  # Delete an entire secret (with confirmation)
  kubectl hsm delete database-creds

  # Delete a specific key from a secret
  kubectl hsm delete database-creds --key password

  # Force delete without confirmation
  kubectl hsm delete api-keys --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context(), args[0])
		},
	}

	cmd.Flags().StringVar(&opts.Key, "key", "", "Delete only the specified key from the secret")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "Skip confirmation prompt")
	cmd.Flags().StringVarP(&opts.Namespace, "namespace", "n", "", "Override the default namespace")
	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Show verbose output including port forward details")

	return cmd
}

// Run executes the delete command
func (opts *DeleteOptions) Run(ctx context.Context, secretName string) error {
	// Validate secret name
	if secretName == "" {
		return fmt.Errorf("secret name is required")
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

	if opts.Key != "" {
		return fmt.Errorf("deleting individual keys is not yet supported - please delete the entire secret and recreate it")
	}

	// Confirm deletion unless force is specified
	if !opts.Force {
		if err := opts.confirmDeletion(secretName); err != nil {
			return err
		}
	}

	// Delete the secret
	fmt.Printf("Deleting secret '%s' from namespace '%s'...\n", secretName, cm.GetCurrentNamespace())
	if err := hsmClient.DeleteSecret(ctx, secretName); err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}

	fmt.Printf("Secret '%s' deleted successfully.\n", secretName)
	return nil
}

// confirmDeletion prompts the user to confirm the deletion
func (opts *DeleteOptions) confirmDeletion(secretName string) error {
	var target string
	if opts.Key != "" {
		target = fmt.Sprintf("key '%s' from secret '%s'", opts.Key, secretName)
	} else {
		target = fmt.Sprintf("secret '%s'", secretName)
	}

	fmt.Printf("⚠️  You are about to delete %s.\n", target)
	fmt.Printf("This action cannot be undone.\n\n")
	fmt.Printf("Type the secret name '%s' to confirm: ", secretName)

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read confirmation: %w", err)
	}

	input = strings.TrimSpace(input)
	if input != secretName {
		return fmt.Errorf("confirmation failed - deletion cancelled")
	}

	return nil
}