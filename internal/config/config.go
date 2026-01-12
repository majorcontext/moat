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
	Agent   string            `yaml:"agent"`
	Version string            `yaml:"version,omitempty"`
	Runtime Runtime           `yaml:"runtime,omitempty"`
	Grants  []string          `yaml:"grants,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Mounts  []string          `yaml:"mounts,omitempty"`
	Ports   map[string]int    `yaml:"ports,omitempty"`
}

// Runtime specifies language runtime versions.
type Runtime struct {
	Node   string   `yaml:"node,omitempty"`
	Python string   `yaml:"python,omitempty"`
	Go     string   `yaml:"go,omitempty"`
	System []string `yaml:"system,omitempty"` // System packages to install
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

	return &cfg, nil
}

// DefaultConfig returns a default configuration.
func DefaultConfig() *Config {
	return &Config{
		Env: make(map[string]string),
	}
}

// ParseRuntime parses a runtime string in "language:version" format.
// Supported languages: python, node, go.
// Example: "python:3.11", "node:20", "go:1.22"
func ParseRuntime(s string) (*Runtime, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid runtime format %q, expected language:version (e.g., python:3.11)", s)
	}

	lang, version := parts[0], parts[1]
	r := &Runtime{}

	switch lang {
	case "python":
		r.Python = version
	case "node":
		r.Node = version
	case "go":
		r.Go = version
	default:
		return nil, fmt.Errorf("unsupported runtime %q, supported: python, node, go", lang)
	}

	return r, nil
}
