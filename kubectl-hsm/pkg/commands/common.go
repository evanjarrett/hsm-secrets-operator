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

// readFromFile reads from file with smart format detection
// Supports:
// - .env files: KEY=VALUE format
// - .json files: {"key":"value"} format
// - Other files: content as single key-value (key derived from filename)
func readFromFile(key, filename string) (map[string]any, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filename, err)
	}

	ext := strings.ToLower(filepath.Ext(filename))
	
	// Auto-detect format based on file extension
	switch ext {
	case ".env":
		return readFromEnvContent(filename, content)
	case ".json":
		return readFromJsonContent(filename, content)
	default:
		// Try to auto-detect format by content
		if data, err := tryParseAsJson(content); err == nil {
			return data, nil
		}
		if data, err := tryParseAsEnv(filename, content); err == nil {
			return data, nil
		}
		
		// Fall back to single key-value format
		return readAsSingleKeyValue(key, filename, content)
	}
}

// tryParseAsJson attempts to parse content as JSON
func tryParseAsJson(content []byte) (map[string]any, error) {
	var data map[string]any
	if err := json.Unmarshal(content, &data); err != nil {
		return nil, err
	}
	
	// Check if this looks like secret data (not nested structures)
	for key, value := range data {
		if key == "" {
			return nil, fmt.Errorf("empty key found")
		}
		
		// Convert all values to strings, reject complex nested structures
		switch v := value.(type) {
		case string:
			// Good - string value
		case nil:
			data[key] = ""
		case map[string]any, []any:
			// Reject nested objects/arrays - not simple key-value format
			return nil, fmt.Errorf("nested structures not supported, expected simple key-value format")
		default:
			// Convert other types (numbers, booleans, etc.) to string
			data[key] = fmt.Sprintf("%v", v)
		}
	}
	
	return data, nil
}

// tryParseAsEnv attempts to parse content as .env format
func tryParseAsEnv(filename string, content []byte) (map[string]any, error) {
	lines := strings.Split(string(content), "\n")
	hasKeyValuePairs := false
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "=") {
			hasKeyValuePairs = true
			break
		}
	}
	
	if !hasKeyValuePairs {
		return nil, fmt.Errorf("no KEY=VALUE pairs found")
	}
	
	return readFromEnvContent(filename, content)
}

// readFromEnvContent parses env content (extracted from readFromEnvFile)
func readFromEnvContent(filename string, content []byte) (map[string]any, error) {
	data := make(map[string]any)
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE format
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid format at line %d in %s: expected KEY=VALUE, got: %s", lineNum, filename, line)
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove quotes if present
		if len(value) >= 2 {
			if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
				(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
				value = value[1 : len(value)-1]
			}
		}

		if key == "" {
			return nil, fmt.Errorf("empty key at line %d in %s", lineNum, filename)
		}

		// Check for duplicate keys
		if _, exists := data[key]; exists {
			return nil, fmt.Errorf("duplicate key '%s' found at line %d in %s", key, lineNum, filename)
		}

		data[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading env content from %s: %w", filename, err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("no valid key-value pairs found in %s", filename)
	}

	return data, nil
}

// readFromJsonContent parses JSON content directly
func readFromJsonContent(filename string, content []byte) (map[string]any, error) {
	var data map[string]any
	if err := json.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("failed to parse JSON file %s: expected simple key-value format {\"key\":\"value\"}: %w", filename, err)
	}

	// Validate that all values can be converted to strings
	for key, value := range data {
		if key == "" {
			return nil, fmt.Errorf("empty key found in JSON file %s", filename)
		}
		
		// Convert non-string values to strings
		switch v := value.(type) {
		case string:
			// Already a string, keep as-is
		case nil:
			data[key] = ""
		default:
			// Convert other types (numbers, booleans, etc.) to strings
			data[key] = fmt.Sprintf("%v", v)
		}
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("no key-value pairs found in JSON file %s", filename)
	}

	return data, nil
}

// readAsSingleKeyValue treats file content as a single value
func readAsSingleKeyValue(key, filename string, content []byte) (map[string]any, error) {
	// Handle both "key=file" and "file" formats
	if key == "" {
		// Only filename provided (no explicit key)
		key = filepath.Base(filename)

		// Remove file extension for the key
		if ext := filepath.Ext(key); ext != "" {
			key = strings.TrimSuffix(key, ext)
		}
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

// showSecretDiff displays the differences between existing and new secret data
func showSecretDiff(existing, new map[string]any, secretName string) {
	fmt.Printf("\nDifferences for secret '%s':\n", secretName)
	fmt.Println(strings.Repeat("=", 50))

	// Track all keys from both maps
	allKeys := make(map[string]bool)
	for k := range existing {
		allKeys[k] = true
	}
	for k := range new {
		allKeys[k] = true
	}

	// Show changes for each key
	hasChanges := false
	for key := range allKeys {
		existingValue, existsInExisting := existing[key]
		newValue, existsInNew := new[key]

		if !existsInExisting && existsInNew {
			// New key being added
			fmt.Printf("+ %s: %s\n", key, truncateString(fmt.Sprintf("%v", newValue), 100))
			hasChanges = true
		} else if existsInExisting && !existsInNew {
			// Key being removed
			fmt.Printf("- %s: %s\n", key, truncateString(fmt.Sprintf("%v", existingValue), 100))
			hasChanges = true
		} else if fmt.Sprintf("%v", existingValue) != fmt.Sprintf("%v", newValue) {
			// Key value being changed
			fmt.Printf("~ %s:\n", key)
			fmt.Printf("  - %s\n", truncateString(fmt.Sprintf("%v", existingValue), 100))
			fmt.Printf("  + %s\n", truncateString(fmt.Sprintf("%v", newValue), 100))
			hasChanges = true
		}
	}

	if !hasChanges {
		fmt.Println("No changes detected.")
	}
	fmt.Println()
}

// showDryRunSummary displays what would be changed without executing
func showDryRunSummary(existing, final map[string]any, secretName string, isReplace bool) {
	fmt.Printf("\nDry run summary for secret '%s':\n", secretName)
	fmt.Println(strings.Repeat("=", 50))

	if existing == nil {
		fmt.Printf("Operation: Create new secret\n")
		fmt.Printf("Keys to be added: %d\n", len(final))
		for key, value := range final {
			fmt.Printf("  + %s: %s\n", key, truncateString(fmt.Sprintf("%v", value), 100))
		}
	} else {
		if isReplace {
			fmt.Printf("Operation: Replace entire secret\n")
		} else {
			fmt.Printf("Operation: Update existing secret (merge)\n")
		}

		addedKeys := 0
		modifiedKeys := 0
		removedKeys := 0

		// Count changes
		for key, newValue := range final {
			if existingValue, exists := existing[key]; !exists {
				addedKeys++
			} else if fmt.Sprintf("%v", existingValue) != fmt.Sprintf("%v", newValue) {
				modifiedKeys++
			}
		}

		if isReplace {
			for key := range existing {
				if _, exists := final[key]; !exists {
					removedKeys++
				}
			}
		}

		fmt.Printf("Keys to be added: %d\n", addedKeys)
		fmt.Printf("Keys to be modified: %d\n", modifiedKeys)
		if isReplace {
			fmt.Printf("Keys to be removed: %d\n", removedKeys)
		}

		showSecretDiff(existing, final, secretName)
	}

	fmt.Println("Note: This is a dry run. No changes were made to the actual secret.")
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
