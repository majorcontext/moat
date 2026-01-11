package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: claude-code
version: 1.0.46

runtime:
  node: 20
  python: 3.11

grants:
  - github:repo
  - aws:s3.read

env:
  NODE_ENV: development
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agent != "claude-code" {
		t.Errorf("Agent = %q, want %q", cfg.Agent, "claude-code")
	}
	if cfg.Version != "1.0.46" {
		t.Errorf("Version = %q, want %q", cfg.Version, "1.0.46")
	}
	if cfg.Runtime.Node != "20" {
		t.Errorf("Runtime.Node = %q, want %q", cfg.Runtime.Node, "20")
	}
	if len(cfg.Grants) != 2 {
		t.Errorf("Grants = %d, want 2", len(cfg.Grants))
	}
	if cfg.Env["NODE_ENV"] != "development" {
		t.Errorf("Env[NODE_ENV] = %q, want %q", cfg.Env["NODE_ENV"], "development")
	}
}

func TestLoadConfigNotFound(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load should not error for missing config: %v", err)
	}
	if cfg != nil {
		t.Error("Expected nil config when agent.yaml doesn't exist")
	}
}

func TestLoadConfigWithMounts(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
mounts:
  - ./data:/data:ro
  - ./cache:/cache
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Mounts) != 2 {
		t.Fatalf("Mounts = %d, want 2", len(cfg.Mounts))
	}
	if cfg.Mounts[0] != "./data:/data:ro" {
		t.Errorf("Mounts[0] = %q, want %q", cfg.Mounts[0], "./data:/data:ro")
	}
}
