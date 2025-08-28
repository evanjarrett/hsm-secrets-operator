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
	FromJsonFile string
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
	cmd.Flags().StringArrayVar(&opts.FromFile, "from-file", nil, "Load secret data from files. Use 'key=file' or just 'file' (uses filename without extension as key)")
	cmd.Flags().StringVar(&opts.FromJsonFile, "from-json-file", "", "Load secret data from a JSON file with structure {\"name\":\"secret-name\",\"secrets\":[{\"key\":\"k\",\"value\":\"v\"}]}")
	cmd.Flags().BoolVar(&opts.Interactive, "interactive", false, "Prompt for secret values interactively")
	cmd.Flags().StringVarP(&opts.Namespace, "namespace", "n", "", "Override the default namespace")
	cmd.Flags().StringVarP(&opts.Output, "output", "o", "text", "Output format (text, json, yaml)")
	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Show verbose output including port forward details")

	// Add completion for output flag
	cmd.RegisterFlagCompletionFunc("output", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json", "yaml"}, cobra.ShellCompDirectiveNoFileComp
	})

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
	if opts.FromJsonFile != "" {
		methods++
	}
	if opts.Interactive {
		methods++
	}

	if methods == 0 {
		return fmt.Errorf("must specify one of --from-literal, --from-file, --from-json-file, or --interactive")
	}
	if methods > 1 {
		return fmt.Errorf("cannot specify multiple input methods (--from-literal, --from-file, --from-json-file, --interactive)")
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

			fileData, err := readFromFileImproved(key, filename)
			if err != nil {
				return err
			}

			// Merge file data with collision detection
			for k, v := range fileData {
				if existingValue, exists := secretData[k]; exists {
					fmt.Printf("Warning: Key '%s' already exists (from previous file), overwriting with value from '%s'\n", k, filename)
					fmt.Printf("  Previous value: %s...\n", truncateString(existingValue.(string), 50))
					fmt.Printf("  New value: %s...\n", truncateString(v.(string), 50))
				}
				secretData[k] = v
			}
		}
	} else if opts.FromJsonFile != "" {
		secretData, err = readFromJsonFile(opts.FromJsonFile)
		if err != nil {
			return err
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