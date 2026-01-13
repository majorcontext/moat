package config

import (
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// GlobalConfig holds global AgentOps settings from ~/.agentops/config.yaml.
type GlobalConfig struct {
	Proxy ProxyConfig `yaml:"proxy"`
}

// ProxyConfig holds reverse proxy settings.
type ProxyConfig struct {
	Port int `yaml:"port"`
}

// DefaultGlobalConfig returns the default global configuration.
func DefaultGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		Proxy: ProxyConfig{
			Port: 8080,
		},
	}
}

// LoadGlobal reads ~/.agentops/config.yaml and applies environment overrides.
func LoadGlobal() (*GlobalConfig, error) {
	cfg := DefaultGlobalConfig()

	// Try to load from file
	homeDir, err := os.UserHomeDir()
	if err == nil {
		configPath := filepath.Join(homeDir, ".agentops", "config.yaml")
		if data, err := os.ReadFile(configPath); err == nil {
			_ = yaml.Unmarshal(data, cfg) // Ignore unmarshal errors, use defaults
		}
	}

	// Apply environment overrides
	if portStr := os.Getenv("AGENTOPS_PROXY_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			cfg.Proxy.Port = port
		}
	}

	return cfg, nil
}

// GlobalConfigDir returns the path to ~/.agentops.
func GlobalConfigDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".agentops")
	}
	return filepath.Join(homeDir, ".agentops")
}
