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

  # Update an existing secret (adds/modifies keys, preserves others)
  kubectl hsm update api-config --from-literal timeout=30

  # List all secrets
  kubectl hsm list

  # Get a specific secret
  kubectl hsm get database-creds

  # Delete a secret (with confirmation)
  kubectl hsm delete old-credentials

  # Check operator health
  kubectl hsm health

  # Trigger manual mirror synchronization
  kubectl hsm mirror sync

  # Force immediate mirror sync
  kubectl hsm mirror sync --force

For more information about the HSM Secrets Operator, visit:
https://github.com/evanjarrett/hsm-secrets-operator`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Add version command
	cmd.AddCommand(newVersionCmd())

	// Add core secret management commands
	cmd.AddCommand(commands.NewCreateCmd()) // includes "update" alias
	cmd.AddCommand(commands.NewRenameKeyCmd())
	cmd.AddCommand(commands.NewGetCmd())
	cmd.AddCommand(commands.NewListCmd())
	cmd.AddCommand(commands.NewDeleteCmd())

	// Add PIN management commands
	cmd.AddCommand(commands.NewRotatePinCmd())

	// Add operational commands
	cmd.AddCommand(commands.NewHealthCmd())
	cmd.AddCommand(commands.NewDevicesCmd())
	cmd.AddCommand(commands.NewMirrorCmd())

	// Add authentication command
	cmd.AddCommand(commands.NewAuthCmd())

	// Add completion command
	cmd.AddCommand(newCompletionCmd())

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

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate completion script",
		Long: `Generate the autocompletion script for kubectl-hsm for the specified shell.
See each sub-command's help for details on how to use the generated script.`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return fmt.Errorf("unsupported shell type: %s", args[0])
			}
		},
	}

	// Add subcommands for each shell
	cmd.AddCommand(&cobra.Command{
		Use:   "bash",
		Short: "Generate the autocompletion script for bash",
		Long: `Generate the autocompletion script for the bash shell.

To load completions in your current shell session:

	source <(kubectl hsm completion bash)

To load completions for every new session, execute once:

### Linux:
	kubectl hsm completion bash > /etc/bash_completion.d/kubectl-hsm

### macOS:
	kubectl hsm completion bash > $(brew --prefix)/etc/bash_completion.d/kubectl-hsm

You will need to start a new shell for this setup to take effect.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Root().GenBashCompletion(os.Stdout)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "zsh",
		Short: "Generate the autocompletion script for zsh",
		Long: `Generate the autocompletion script for the zsh shell.

To load completions in your current shell session:

	source <(kubectl hsm completion zsh)

To load completions for every new session, execute once:

### zsh completion system:
	kubectl hsm completion zsh > "${fpath[1]}/_kubectl-hsm"

### Using oh-my-zsh:
	mkdir -p ~/.oh-my-zsh/completions
	kubectl hsm completion zsh > ~/.oh-my-zsh/completions/_kubectl-hsm

You will need to start a new shell for this setup to take effect.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Root().GenZshCompletion(os.Stdout)
		},
	})

	return cmd
}
