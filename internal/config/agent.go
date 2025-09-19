package config

import (
	"fmt"
	"os"
)

// AgentConfig holds configuration for agent mode
type AgentConfig struct {
	DeviceName        string
	PKCS11LibraryPath string
	TokenLabel        string
	PodName           string
	PodNamespace      string
}

// NewAgentConfigFromEnv creates AgentConfig from system information
func NewAgentConfigFromEnv() (*AgentConfig, error) {
	// Get namespace using config function
	namespace, err := GetCurrentNamespace()
	if err != nil {
		return nil, fmt.Errorf("failed to get current namespace: %w", err)
	}

	// Get pod name from hostname
	podName, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("failed to get hostname: %w", err)
	}

	cfg := &AgentConfig{
		PodName:      podName,
		PodNamespace: namespace,
	}

	return cfg, nil
}

// Validate checks that all required configuration is present
func (c *AgentConfig) Validate() error {
	if c.DeviceName == "" {
		return fmt.Errorf("device name is required")
	}
	if c.PKCS11LibraryPath == "" {
		return fmt.Errorf("PKCS11 library path is required")
	}
	if c.PodNamespace == "" {
		return fmt.Errorf("pod namespace is required")
	}
	return nil
}
