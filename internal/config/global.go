package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// GlobalConfig holds global Moat settings from ~/.moat/config.yaml.
type GlobalConfig struct {
	Proxy  ProxyConfig  `yaml:"proxy"`
	Debug  DebugConfig  `yaml:"debug"`
	Mounts []MountEntry `yaml:"mounts,omitempty"`
}

// DebugConfig holds debug logging settings.
type DebugConfig struct {
	RetentionDays int `yaml:"retention_days"`
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
		Debug: DebugConfig{
			RetentionDays: 14,
		},
	}
}

// LoadGlobal reads ~/.moat/config.yaml and applies environment overrides.
func LoadGlobal() (*GlobalConfig, error) {
	cfg := DefaultGlobalConfig()

	// Try to load from file
	homeDir, err := os.UserHomeDir()
	if err == nil {
		configPath := filepath.Join(homeDir, ".moat", "config.yaml")
		if data, err := os.ReadFile(configPath); err == nil {
			_ = yaml.Unmarshal(data, cfg) // Ignore unmarshal errors, use defaults
		}

		// Validate global mounts: require absolute source paths and read-only mode.
		var validMounts []MountEntry
		for i, m := range cfg.Mounts {
			// Expand ~ in source path
			if strings.HasPrefix(m.Source, "~/") {
				m.Source = filepath.Join(homeDir, m.Source[2:])
			}

			if !filepath.IsAbs(m.Source) {
				return nil, fmt.Errorf("global mount %d: source %q must be an absolute path (no workspace to resolve relative paths against)", i+1, m.Source)
			}

			// Enforce read-only
			m.ReadOnly = true
			m.Mode = "ro"

			// Excludes not supported on global mounts
			if len(m.Exclude) > 0 {
				return nil, fmt.Errorf("global mount %d: excludes are not supported on global mounts", i+1)
			}

			validMounts = append(validMounts, m)
		}
		cfg.Mounts = validMounts
	}

	// Apply environment overrides
	if portStr := os.Getenv("MOAT_PROXY_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			cfg.Proxy.Port = port
		}
	}

	return cfg, nil
}

// GlobalConfigDir returns the path to ~/.moat.
func GlobalConfigDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".moat")
	}
	return filepath.Join(homeDir, ".moat")
}
