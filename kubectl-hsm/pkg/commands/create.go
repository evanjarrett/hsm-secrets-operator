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
	"strings"

	"github.com/spf13/cobra"
)

// CreateOptions holds options for the create command
type CreateOptions struct {
	CommonOptions
	FromLiteral  []string
	FromFile     []string
	Interactive  bool
}

// NewCreateCmd creates the create command
func NewCreateCmd() *cobra.Command {
	opts := &CreateOptions{}

	cmd := &cobra.Command{
		Use:   "create SECRET_NAME [flags]",
		Short: "Create a new HSM secret",
		Long: `Create a new secret in the HSM.

The secret data can be provided in several ways:
- --from-literal key=value: Specify key-value pairs directly
- --from-file key=path: Load values from files
- --interactive: Prompt for values interactively (recommended for passwords)

Examples:
  # Create secret with literal values
  kubectl hsm create database-creds --from-literal username=admin --from-literal password=secret123

  # Load values from files
  kubectl hsm create tls-cert --from-file cert=server.crt --from-file key=server.key

  # Interactive creation (prompts for input, hides passwords)
  kubectl hsm create api-keys --interactive`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context(), args[0])
		},
	}

	cmd.Flags().StringArrayVar(&opts.FromLiteral, "from-literal", nil, "Specify a key and literal value to insert in secret (i.e. --from-literal key=value)")
	cmd.Flags().StringArrayVar(&opts.FromFile, "from-file", nil, "Key files can be specified using their file path, in which case a default name will be given to them, or optionally with a name and file path, in which case the given name will be used")
	cmd.Flags().BoolVar(&opts.Interactive, "interactive", false, "Prompt for secret values interactively")
	cmd.Flags().StringVarP(&opts.Namespace, "namespace", "n", "", "Override the default namespace")
	cmd.Flags().StringVarP(&opts.Output, "output", "o", "text", "Output format (text, json, yaml)")
	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Show verbose output including port forward details")

	return cmd
}

// Run executes the create command
func (opts *CreateOptions) Run(ctx context.Context, secretName string) error {
	// Validate secret name
	if secretName == "" {
		return fmt.Errorf("secret name is required")
	}

	// Validate options
	methods := 0
	if len(opts.FromLiteral) > 0 {
		methods++
	}
	if len(opts.FromFile) > 0 {
		methods++
	}
	if opts.Interactive {
		methods++
	}

	if methods == 0 {
		return fmt.Errorf("must specify one of --from-literal, --from-file, or --interactive")
	}
	if methods > 1 {
		return fmt.Errorf("cannot specify multiple input methods (--from-literal, --from-file, --interactive)")
	}

	// Collect secret data
	var secretData map[string]any
	var err error

	if len(opts.FromLiteral) > 0 {
		secretData, err = parseFromLiteral(opts.FromLiteral)
		if err != nil {
			return err
		}
	} else if len(opts.FromFile) > 0 {
		secretData = make(map[string]any)
		for _, fileSpec := range opts.FromFile {
			parts := strings.SplitN(fileSpec, "=", 2)
			var key, filename string
			if len(parts) == 2 {
				key = parts[0]
				filename = parts[1]
			} else {
				filename = parts[0]
			}

			fileData, err := readFromFile(key, filename)
			if err != nil {
				return err
			}

			// Merge file data
			for k, v := range fileData {
				secretData[k] = v
			}
		}
	} else if opts.Interactive {
		secretData, err = promptForInteractiveInput()
		if err != nil {
			return err
		}
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

	// Create the secret
	fmt.Printf("Creating secret '%s' in namespace '%s'...\n", secretName, cm.GetCurrentNamespace())
	if err := hsmClient.CreateSecret(ctx, secretName, secretData); err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	fmt.Printf("Secret '%s' created successfully.\n", secretName)

	// Show how to retrieve the secret
	fmt.Printf("\nTo view the secret:\n")
	fmt.Printf("  kubectl hsm get %s\n", secretName)
	if opts.Namespace != "" {
		fmt.Printf("  kubectl hsm get %s -n %s\n", secretName, opts.Namespace)
	}

	return nil
}