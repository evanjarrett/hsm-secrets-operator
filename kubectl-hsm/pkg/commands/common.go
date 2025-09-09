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
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/evanjarrett/hsm-secrets-operator/kubectl-hsm/pkg/client"
	"github.com/evanjarrett/hsm-secrets-operator/kubectl-hsm/pkg/util"
)

// CommonOptions holds common options for all commands
type CommonOptions struct {
	Namespace string
	Output    string
	Verbose   bool
}

// ClientManager manages the connection to the HSM operator API
type ClientManager struct {
	kubectl     *util.KubectlUtil
	portForward *util.PortForward
	hsmClient   *client.Client
	verbose     bool
}

// NewClientManager creates a new client manager
func NewClientManager(namespace string, verbose bool) (*ClientManager, error) {
	kubectl, err := util.NewKubectlUtil(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize kubectl utilities: %w", err)
	}

	return &ClientManager{
		kubectl: kubectl,
		verbose: verbose,
	}, nil
}

// GetClient returns an HSM API client, setting up port forwarding if necessary
func (cm *ClientManager) GetClient(ctx context.Context) (*client.Client, error) {
	if cm.hsmClient != nil {
		return cm.hsmClient, nil
	}

	// Set up port forwarding to the operator
	localPort := 8090 // Default port, could be made configurable
	pf, err := cm.kubectl.CreatePortForward(ctx, localPort, cm.verbose)
	if err != nil {
		return nil, err
	}

	cm.portForward = pf

	// Create HSM client pointing to the forwarded port
	baseURL := fmt.Sprintf("http://localhost:%d", pf.GetLocalPort())
	cm.hsmClient = client.NewClient(baseURL)

	return cm.hsmClient, nil
}

// Close cleans up the client manager resources
func (cm *ClientManager) Close() {
	if cm.portForward != nil {
		cm.portForward.Stop()
	}
}

// GetCurrentNamespace returns the current namespace
func (cm *ClientManager) GetCurrentNamespace() string {
	return cm.kubectl.GetCurrentNamespace()
}

// readSecretValue reads a secret value, optionally hiding input for passwords
func readSecretValue(prompt string, hidden bool) (string, error) {
	fmt.Print(prompt)

	if hidden {
		// Read password without echoing
		byteValue, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println() // Add newline after hidden input
		if err != nil {
			return "", fmt.Errorf("failed to read hidden input: %w", err)
		}
		return string(byteValue), nil
	}

	// Read normal input
	var value string
	if _, err := fmt.Scanln(&value); err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}
	return value, nil
}

// parseFromLiteral parses key=value pairs from --from-literal flags
func parseFromLiteral(literals []string) (map[string]any, error) {
	data := make(map[string]any)

	for _, literal := range literals {
		parts := strings.SplitN(literal, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --from-literal format: %s (expected key=value)", literal)
		}
		data[parts[0]] = parts[1]
	}

	return data, nil
}

// promptForInteractiveInput prompts the user for secret values interactively
func promptForInteractiveInput() (map[string]any, error) {
	data := make(map[string]any)

	fmt.Println("Enter secret data (press Enter with empty key to finish):")

	for {
		key, err := readSecretValue("Key: ", false)
		if err != nil {
			return nil, err
		}

		if key == "" {
			break
		}

		// Determine if this looks like a password field
		isPassword := strings.Contains(strings.ToLower(key), "password") ||
			strings.Contains(strings.ToLower(key), "secret") ||
			strings.Contains(strings.ToLower(key), "token") ||
			strings.Contains(strings.ToLower(key), "key")

		value, err := readSecretValue(fmt.Sprintf("Value for '%s': ", key), isPassword)
		if err != nil {
			return nil, err
		}

		data[key] = value
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("no secret data provided")
	}

	return data, nil
}

// JsonSecretImport represents the structure of a JSON secret import file
type JsonSecretImport struct {
	Name    string             `json:"name"`
	Secrets []JsonSecretKVPair `json:"secrets"`
}

// JsonSecretKVPair represents a key-value pair in the JSON import
type JsonSecretKVPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// readFromJsonFile reads secret data from a JSON file
func readFromJsonFile(filename string) (map[string]any, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read JSON file %s: %w", filename, err)
	}

	var importData JsonSecretImport
	if err := json.Unmarshal(content, &importData); err != nil {
		return nil, fmt.Errorf("failed to parse JSON file %s: %w", filename, err)
	}

	data := make(map[string]any)
	for _, kv := range importData.Secrets {
		if kv.Key == "" {
			return nil, fmt.Errorf("empty key found in JSON file %s", filename)
		}
		data[kv.Key] = kv.Value
	}

	return data, nil
}

// readFromFileImproved reads from file with improved key derivation logic
func readFromFileImproved(key, filename string) (map[string]any, error) {
	// Handle both "key=file" and "file" formats
	if key == "" {
		// Only filename provided (no explicit key)
		key = filepath.Base(filename)

		// Remove file extension for the key
		if ext := filepath.Ext(key); ext != "" {
			key = strings.TrimSuffix(key, ext)
		}
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filename, err)
	}

	data := map[string]any{
		key: string(content),
	}

	return data, nil
}

// truncateString truncates a string to the specified length with "..." suffix
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// CompletionSecretNames provides bash completion for secret names
func CompletionSecretNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// If we already have a secret name, don't complete more
	if len(args) >= 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Get namespace from flag or use default
	namespace, _ := cmd.Flags().GetString("namespace")
	verbose, _ := cmd.Flags().GetBool("verbose")

	// Create client manager
	cm, err := NewClientManager(namespace, verbose)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	defer cm.Close()

	// Get HSM client
	ctx := cmd.Context()
	hsmClient, err := cm.GetClient(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	// List secrets
	secretList, err := hsmClient.ListSecrets(ctx, 0, 0)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return secretList.Secrets, cobra.ShellCompDirectiveNoFileComp
}
