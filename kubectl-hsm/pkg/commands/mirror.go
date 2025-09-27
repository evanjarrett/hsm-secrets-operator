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

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/evanjarrett/hsm-secrets-operator/kubectl-hsm/pkg/client"
)

// MirrorOptions holds options for the mirror command
type MirrorOptions struct {
	CommonOptions
	Force bool // Force sync even if recent sync occurred
}

// Complete fills in default values for the mirror options
func (o *MirrorOptions) Complete() error {
	return nil
}

// Validate checks that the mirror options are valid
func (o *MirrorOptions) Validate() error {
	return nil
}

// AddCommonFlags adds common flags to the command
func (o *MirrorOptions) AddCommonFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&o.Namespace, "namespace", "n", "", "namespace to use (defaults to current kubectl namespace)")
	cmd.Flags().StringVarP(&o.Output, "output", "o", "", "output format (table|json|yaml)")
	cmd.Flags().BoolVarP(&o.Verbose, "verbose", "v", false, "verbose output")
}

// NewMirrorCmd creates the mirror command
func NewMirrorCmd() *cobra.Command {
	opts := &MirrorOptions{}

	cmd := &cobra.Command{
		Use:   "mirror",
		Short: "Mirror management operations",
		Long: `Commands for managing HSM secret mirroring between devices.

The mirror commands allow you to manually trigger synchronization
of secrets between HSM devices, useful for ensuring consistency
after direct HSM modifications or troubleshooting.`,
	}

	// Add subcommands
	cmd.AddCommand(newMirrorSyncCmd(opts))

	return cmd
}

// newMirrorSyncCmd creates the mirror sync subcommand
func newMirrorSyncCmd(opts *MirrorOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync [flags]",
		Short: "Trigger manual mirror synchronization",
		Long: `Manually trigger synchronization of secrets between HSM devices.

This command is useful when:
- You've made direct changes to HSM devices outside the operator
- You want to ensure immediate consistency across all devices
- You're troubleshooting mirror synchronization issues

Examples:
  # Trigger normal mirror sync
  kubectl hsm mirror sync

  # Force immediate sync (ignores recent sync optimization)
  kubectl hsm mirror sync --force

  # Trigger sync with specific output format
  kubectl hsm mirror sync -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Complete(); err != nil {
				return fmt.Errorf("failed to complete options: %w", err)
			}

			if err := opts.Validate(); err != nil {
				return fmt.Errorf("invalid options: %w", err)
			}

			return runMirrorSync(opts)
		},
	}

	// Add flags
	cmd.Flags().BoolVar(&opts.Force, "force", false, "Force sync even if recent sync occurred")
	opts.AddCommonFlags(cmd)

	return cmd
}

// runMirrorSync executes the mirror sync operation
func runMirrorSync(opts *MirrorOptions) error {
	ctx := context.Background()

	// Create client manager
	cm, err := NewClientManager(opts.Namespace, opts.Verbose)
	if err != nil {
		return fmt.Errorf("failed to initialize client manager: %w", err)
	}
	defer cm.Close()

	// Get HSM client
	hsmClient, err := cm.GetClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to get HSM client: %w", err)
	}

	// Trigger mirror sync
	result, err := hsmClient.TriggerMirrorSync(ctx, opts.Force)
	if err != nil {
		return fmt.Errorf("failed to trigger mirror sync: %w", err)
	}

	// Output result
	return outputMirrorSyncResult(result, opts.Output)
}

// outputMirrorSyncResult formats and outputs the mirror sync result
func outputMirrorSyncResult(result *client.MirrorSyncResult, outputFormat string) error {
	switch outputFormat {
	case "json":
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)

	case "yaml":
		yamlBytes, err := yaml.Marshal(result)
		if err != nil {
			return fmt.Errorf("failed to marshal result to YAML: %w", err)
		}
		fmt.Print(string(yamlBytes))
		return nil

	case "table", "":
		// Table format (default)
		fmt.Printf("Mirror Sync Triggered Successfully\n\n")
		fmt.Printf("Reason:    %s\n", result.Reason)
		fmt.Printf("Forced:    %t\n", result.Force)
		fmt.Printf("Status:    %s\n", result.Message)
		fmt.Printf("Triggered: %t\n", result.Triggered)
		fmt.Printf("\n")

		if result.Force {
			fmt.Printf("ℹ️  Force flag used - sync will run immediately regardless of recent syncs\n")
		} else {
			fmt.Printf("ℹ️  Normal sync triggered - may be debounced with other recent events\n")
		}

		return nil

	default:
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}
}