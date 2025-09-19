package config

import (
	"fmt"
	"os"
)

// ManagerConfig holds configuration for manager mode
type ManagerConfig struct {
	Hostname       string
	DiscoveryImage string
	AgentImage     string
}

// NewManagerConfigFromEnv creates ManagerConfig from environment variables and system calls
func NewManagerConfigFromEnv() (*ManagerConfig, error) {
	// Get hostname from system
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("failed to get hostname: %w", err)
	}

	cfg := &ManagerConfig{
		Hostname: hostname,
	}

	return cfg, nil
}

// NewManagerConfigWithImages creates ManagerConfig with specified images
func NewManagerConfigWithImages(agentImage, discoveryImage string) (*ManagerConfig, error) {
	cfg, err := NewManagerConfigFromEnv()
	if err != nil {
		return nil, err
	}

	cfg.AgentImage = agentImage
	cfg.DiscoveryImage = discoveryImage

	return cfg, nil
}
