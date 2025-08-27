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
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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

// readFromFile reads content from a file for --from-file flags
func readFromFile(key, filename string) (map[string]any, error) {
	// Handle both "key=file" and "file" formats
	if filename == "" {
		filename = key
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

// formatDuration formats a time duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}