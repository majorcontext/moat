package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFullConfigWorkflow(t *testing.T) {
	dir := t.TempDir()

	// Create agent.yaml
	yaml := `
agent: test-agent
version: 1.0.0

runtime:
  node: 20

grants:
  - github:repo

env:
  NODE_ENV: test
  DEBUG: "true"

mounts:
  - ./data:/data:ro
`
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	// Create data directory
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0755); err != nil {
		t.Fatal(err)
	}

	// Load config
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify all fields
	if cfg.Agent != "test-agent" {
		t.Errorf("Agent = %q", cfg.Agent)
	}
	if cfg.Version != "1.0.0" {
		t.Errorf("Version = %q", cfg.Version)
	}
	if cfg.Runtime.Node != "20" {
		t.Errorf("Runtime.Node = %q", cfg.Runtime.Node)
	}
	if len(cfg.Grants) != 1 || cfg.Grants[0] != "github:repo" {
		t.Errorf("Grants = %v", cfg.Grants)
	}
	if cfg.Env["NODE_ENV"] != "test" {
		t.Errorf("Env[NODE_ENV] = %q", cfg.Env["NODE_ENV"])
	}
	if cfg.Env["DEBUG"] != "true" {
		t.Errorf("Env[DEBUG] = %q", cfg.Env["DEBUG"])
	}

	// Parse mounts
	if len(cfg.Mounts) != 1 {
		t.Fatalf("Mounts = %d", len(cfg.Mounts))
	}
	m, err := ParseMount(cfg.Mounts[0])
	if err != nil {
		t.Fatalf("ParseMount: %v", err)
	}
	if m.Source != "./data" || m.Target != "/data" || !m.ReadOnly {
		t.Errorf("Mount = %+v", m)
	}
}
