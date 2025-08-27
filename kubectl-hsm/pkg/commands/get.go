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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
	
	"github.com/evanjarrett/hsm-secrets-operator/kubectl-hsm/pkg/client"
)

// GetOptions holds options for the get command
type GetOptions struct {
	CommonOptions
	Key string
}

// NewGetCmd creates the get command
func NewGetCmd() *cobra.Command {
	opts := &GetOptions{}

	cmd := &cobra.Command{
		Use:   "get SECRET_NAME [flags]",
		Short: "Get an HSM secret",
		Long: `Retrieve and display an HSM secret.

By default, displays all keys in the secret. Use --key to display only a specific key.

Examples:
  # Get all keys in a secret
  kubectl hsm get database-creds

  # Get a specific key from a secret
  kubectl hsm get database-creds --key password

  # Output in different formats
  kubectl hsm get api-keys -o json
  kubectl hsm get api-keys -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context(), args[0])
		},
	}

	cmd.Flags().StringVar(&opts.Key, "key", "", "Show only the specified key")
	cmd.Flags().StringVarP(&opts.Namespace, "namespace", "n", "", "Override the default namespace")
	cmd.Flags().StringVarP(&opts.Output, "output", "o", "text", "Output format (text, json, yaml)")
	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Show verbose output including port forward details")

	return cmd
}

// Run executes the get command
func (opts *GetOptions) Run(ctx context.Context, secretName string) error {
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

	// Retrieve the secret
	secretData, err := hsmClient.GetSecret(ctx, secretName)
	if err != nil {
		return fmt.Errorf("failed to get secret: %w", err)
	}

	// Handle specific key request
	if opts.Key != "" {
		value, exists := secretData.Data[opts.Key]
		if !exists {
			return fmt.Errorf("key '%s' not found in secret '%s'", opts.Key, secretName)
		}

		switch opts.Output {
		case "json":
			keyData := map[string]any{opts.Key: value}
			jsonBytes, err := json.MarshalIndent(keyData, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal key data to JSON: %w", err)
			}
			fmt.Println(string(jsonBytes))
		case "yaml":
			keyData := map[string]any{opts.Key: value}
			yamlBytes, err := yaml.Marshal(keyData)
			if err != nil {
				return fmt.Errorf("failed to marshal key data to YAML: %w", err)
			}
			fmt.Print(string(yamlBytes))
		default:
			fmt.Printf("%s\n", value)
		}
		return nil
	}

	// Display full secret
	switch opts.Output {
	case "json":
		jsonBytes, err := json.MarshalIndent(secretData, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal secret data to JSON: %w", err)
		}
		fmt.Println(string(jsonBytes))
	case "yaml":
		yamlBytes, err := yaml.Marshal(secretData)
		if err != nil {
			return fmt.Errorf("failed to marshal secret data to YAML: %w", err)
		}
		fmt.Print(string(yamlBytes))
	default:
		return opts.displaySecretText(secretName, secretData, cm.GetCurrentNamespace())
	}

	return nil
}

// displaySecretText displays the secret in a human-readable text format
func (opts *GetOptions) displaySecretText(secretName string, secretData *client.SecretData, namespace string) error {
	fmt.Printf("Name:         %s\n", secretName)
	fmt.Printf("Namespace:    %s\n", namespace)


	// Parse metadata from _metadata key if present
	if metadataValue, hasMetadata := secretData.Data["_metadata"]; hasMetadata {
		if err := opts.parseAndDisplayMetadata(metadataValue); err != nil {
			fmt.Printf("Metadata:     <parse error: %v>\n", err)
		}
	}

	// Display data keys (but not values for security, and exclude _metadata)
	var keys []string
	for k := range secretData.Data {
		if k != "_metadata" {
			keys = append(keys, k)
		}
	}
	
	if len(keys) > 0 {
		sort.Strings(keys)
		fmt.Printf("Keys:         %s\n", strings.Join(keys, ", "))
	} else {
		fmt.Printf("Keys:         <none>\n")
	}

	return nil
}

// parseAndDisplayMetadata parses and displays metadata from the _metadata key
func (opts *GetOptions) parseAndDisplayMetadata(metadataValue any) error {
	var metadataMap map[string]any
	
	switch v := metadataValue.(type) {
	case string:
		// First try to decode as base64, then parse JSON
		decoded, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			// If base64 decode fails, try direct JSON parsing
			if jsonErr := json.Unmarshal([]byte(v), &metadataMap); jsonErr != nil {
				return fmt.Errorf("failed to parse metadata (not base64: %v, not JSON: %v)", err, jsonErr)
			}
		} else {
			// Parse the decoded base64 as JSON
			if err := json.Unmarshal(decoded, &metadataMap); err != nil {
				return fmt.Errorf("failed to parse base64-decoded metadata JSON: %w", err)
			}
		}
	case map[string]any:
		metadataMap = v
	default:
		return fmt.Errorf("unexpected metadata type: %T", v)
	}
	
	// Display all metadata fields with proper formatting
	opts.displayMetadataFields(metadataMap, "")
	
	return nil
}

// displayMetadataFields displays metadata fields with proper key formatting
func (opts *GetOptions) displayMetadataFields(data map[string]any, indent string) {
	// Get all keys and sort them for consistent output
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	
	for _, key := range keys {
		value := data[key]
		
		// Handle nested objects (like labels)
		if nested, ok := value.(map[string]any); ok {
			// Display the parent key with capitalization
			displayKey := opts.formatMetadataKey(key)
			fmt.Printf("%s%-13s\n", indent, displayKey+":")
			
			// Display nested items with indentation, keeping original keys
			nestedKeys := make([]string, 0, len(nested))
			for k := range nested {
				nestedKeys = append(nestedKeys, k)
			}
			sort.Strings(nestedKeys)
			
			for _, nestedKey := range nestedKeys {
				fmt.Printf("%s  %-11s %v\n", indent, nestedKey+":", nested[nestedKey])
			}
			continue
		}
		
		// Format the display key (capitalize first letter, replace underscores with spaces)
		displayKey := opts.formatMetadataKey(key)
		
		// Display the key-value pair
		fmt.Printf("%s%-13s %v\n", indent, displayKey+":", value)
	}
}

// formatMetadataKey formats a metadata key for display
func (opts *GetOptions) formatMetadataKey(key string) string {
	// Replace underscores with spaces
	formatted := strings.ReplaceAll(key, "_", " ")
	
	// Split into words and capitalize each word
	words := strings.Fields(formatted)
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	
	return strings.Join(words, " ")
}