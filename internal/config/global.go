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

// LoadGlobal reads the moat global config file and applies environment overrides.
// The config path is <GlobalConfigDir>/config.yaml — by default ~/.moat/config.yaml,
// or $MOAT_HOME/config.yaml when MOAT_HOME is set.
func LoadGlobal() (*GlobalConfig, error) {
	cfg := DefaultGlobalConfig()

	configPath := filepath.Join(GlobalConfigDir(), "config.yaml")
	if data, err := os.ReadFile(configPath); err == nil {
		_ = yaml.Unmarshal(data, cfg) // Ignore unmarshal errors, use defaults
	}

	// Tilde expansion in mount sources resolves against the real user home,
	// not MOAT_HOME — `~/foo` is a user-facing alias for the OS home dir.
	homeDir, _ := os.UserHomeDir()

	// Validate global mounts: require absolute source paths and read-only mode.
	var validMounts []MountEntry
	for i, m := range cfg.Mounts {
		// Expand ~ in source path
		if strings.HasPrefix(m.Source, "~/") && homeDir != "" {
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

	// Apply environment overrides
	if portStr := os.Getenv("MOAT_PROXY_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			cfg.Proxy.Port = port
		}
	}

	return cfg, nil
}

// GlobalConfigDir returns the path to the moat configuration directory.
//
// By default this is ~/.moat, but the MOAT_HOME environment variable may
// override it to an absolute path. MOAT_HOME is the complete moat directory,
// not a parent containing .moat — set it to e.g. /tmp/moat-test, not /tmp.
// Primarily used for hermetic test runs and rare multi-version setups where
// one daemon must not see another's state.
func GlobalConfigDir() string {
	if override := os.Getenv("MOAT_HOME"); override != "" {
		return override
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".moat")
	}
	return filepath.Join(homeDir, ".moat")
}
