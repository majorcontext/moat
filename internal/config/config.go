// Package config handles agent.yaml manifest parsing.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents an agent.yaml manifest.
type Config struct {
	Name         string            `yaml:"name,omitempty"`
	Agent        string            `yaml:"agent"`
	Version      string            `yaml:"version,omitempty"`
	Dependencies []string          `yaml:"dependencies,omitempty"`
	Grants       []string          `yaml:"grants,omitempty"`
	Env          map[string]string `yaml:"env,omitempty"`
	Secrets      map[string]string `yaml:"secrets,omitempty"`
	Mounts       []string          `yaml:"mounts,omitempty"`
	Ports        map[string]int    `yaml:"ports,omitempty"`
	Network      NetworkConfig     `yaml:"network,omitempty"`
	Command      []string          `yaml:"command,omitempty"`
	Claude       ClaudeConfig      `yaml:"claude,omitempty"`

	// Deprecated: use Dependencies instead
	Runtime *deprecatedRuntime `yaml:"runtime,omitempty"`
}

// NetworkConfig configures network access policies for the agent.
type NetworkConfig struct {
	Policy string   `yaml:"policy,omitempty"` // "permissive" or "strict", default "permissive"
	Allow  []string `yaml:"allow,omitempty"`  // allowed host patterns
}

// ClaudeConfig configures Claude Code integration options.
type ClaudeConfig struct {
	// SyncLogs enables mounting Claude's session logs directory so logs from
	// inside the container appear on the host at the correct project location.
	// Default: false, unless the "anthropic" grant is configured (then true).
	SyncLogs *bool `yaml:"sync_logs,omitempty"`
}

// ShouldSyncClaudeLogs returns true if Claude session logs should be synced.
// The logic is:
// - If claude.sync_logs is explicitly set, use that value
// - Otherwise, enable sync_logs if "anthropic" is in grants (Claude Code integration)
func (c *Config) ShouldSyncClaudeLogs() bool {
	if c.Claude.SyncLogs != nil {
		return *c.Claude.SyncLogs
	}
	// Default: enable if anthropic grant is configured
	for _, grant := range c.Grants {
		if grant == "anthropic" || strings.HasPrefix(grant, "anthropic:") {
			return true
		}
	}
	return false
}

// deprecatedRuntime is kept only to detect and reject old configs.
type deprecatedRuntime struct {
	Node   string `yaml:"node,omitempty"`
	Python string `yaml:"python,omitempty"`
	Go     string `yaml:"go,omitempty"`
}

// Load reads agent.yaml from the given directory.
// Returns nil, nil if the file doesn't exist.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, "agent.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading agent.yaml: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing agent.yaml: %w", err)
	}

	// Reject deprecated runtime field
	if cfg.Runtime != nil && (cfg.Runtime.Node != "" || cfg.Runtime.Python != "" || cfg.Runtime.Go != "") {
		return nil, fmt.Errorf("'runtime' field is no longer supported\n\n  Replace this:\n    runtime:\n      node: %q\n\n  With this:\n    dependencies:\n      - node@%s",
			cfg.Runtime.Node, cfg.Runtime.Node)
	}

	// Set default network policy if not specified
	if cfg.Network.Policy == "" {
		cfg.Network.Policy = "permissive"
	}

	// Validate network policy
	if cfg.Network.Policy != "permissive" && cfg.Network.Policy != "strict" {
		return nil, fmt.Errorf("invalid network policy %q: must be 'permissive' or 'strict'", cfg.Network.Policy)
	}

	// Check for overlapping env and secrets keys
	for key := range cfg.Secrets {
		if _, exists := cfg.Env[key]; exists {
			return nil, fmt.Errorf("key %q defined in both 'env' and 'secrets' - use one or the other", key)
		}
	}

	// Validate secret references have valid URI format
	for key, ref := range cfg.Secrets {
		if !strings.Contains(ref, "://") {
			return nil, fmt.Errorf("secret %q has invalid reference %q: missing scheme (expected format: scheme://path, e.g., op://vault/item/field)", key, ref)
		}
	}

	// Validate command if specified
	if len(cfg.Command) > 0 && cfg.Command[0] == "" {
		return nil, fmt.Errorf("command[0] cannot be empty: the first element must be the executable")
	}

	return &cfg, nil
}

// DefaultConfig returns a default configuration.
func DefaultConfig() *Config {
	return &Config{
		Env: make(map[string]string),
		Network: NetworkConfig{
			Policy: "permissive",
		},
	}
}
