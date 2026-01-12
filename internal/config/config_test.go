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

func TestLoadConfigWithName(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
name: myapp
agent: test-agent
ports:
  web: 3000
  api: 8080
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "myapp" {
		t.Errorf("Name = %q, want %q", cfg.Name, "myapp")
	}
	if len(cfg.Ports) != 2 {
		t.Fatalf("Ports = %d, want 2", len(cfg.Ports))
	}
	if cfg.Ports["web"] != 3000 {
		t.Errorf("Ports[web] = %d, want 3000", cfg.Ports["web"])
	}
	if cfg.Ports["api"] != 8080 {
		t.Errorf("Ports[api] = %d, want 8080", cfg.Ports["api"])
	}
}

func TestParseRuntime(t *testing.T) {
	tests := []struct {
		input   string
		want    Runtime
		wantErr bool
	}{
		{
			input: "python:3.11",
			want:  Runtime{Python: "3.11"},
		},
		{
			input: "node:20",
			want:  Runtime{Node: "20"},
		},
		{
			input: "go:1.22",
			want:  Runtime{Go: "1.22"},
		},
		{
			input: "node:20.10.0",
			want:  Runtime{Node: "20.10.0"},
		},
		{
			input:   "python",
			wantErr: true, // missing version
		},
		{
			input:   "ruby:3.0",
			wantErr: true, // unsupported language
		},
		{
			input:   ":3.11",
			wantErr: true, // missing language
		},
		{
			input:   "python:",
			wantErr: true, // missing version
		},
		{
			input:   "",
			wantErr: true, // empty
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseRuntime(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseRuntime(%q) = %v, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRuntime(%q) error: %v", tt.input, err)
			}
			if got.Python != tt.want.Python || got.Node != tt.want.Node || got.Go != tt.want.Go {
				t.Errorf("ParseRuntime(%q) = %+v, want %+v", tt.input, *got, tt.want)
			}
		})
	}
}
