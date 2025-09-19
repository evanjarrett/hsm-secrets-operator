package config

import (
	"fmt"
	"os"
)

// DiscoveryConfig holds configuration for discovery mode
type DiscoveryConfig struct {
	NodeName     string
	PodName      string
	PodNamespace string
}

// NewDiscoveryConfigFromEnv creates DiscoveryConfig from environment variables (downward API only)
func NewDiscoveryConfigFromEnv() (*DiscoveryConfig, error) {
	// NODE_NAME must come from environment (downward API)
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return nil, fmt.Errorf("NODE_NAME environment variable is required")
	}

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

	cfg := &DiscoveryConfig{
		NodeName:     nodeName,
		PodName:      podName,
		PodNamespace: namespace,
	}

	return cfg, nil
}

// Validate checks that all required configuration is present
func (c *DiscoveryConfig) Validate() error {
	if c.NodeName == "" {
		return fmt.Errorf("node name is required")
	}
	if c.PodName == "" {
		return fmt.Errorf("pod name is required")
	}
	if c.PodNamespace == "" {
		return fmt.Errorf("pod namespace is required")
	}
	return nil
}
