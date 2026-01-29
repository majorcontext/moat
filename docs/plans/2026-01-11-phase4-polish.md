# Phase 4: Polish Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add agent.yaml configuration parsing, improve image selection, and polish the CLI experience.

**Architecture:** Parse agent.yaml from workspace directory to configure runs. Support curated base images based on runtime requirements. Add environment variable support and improve error messages.

**Tech Stack:** Go, YAML parsing (gopkg.in/yaml.v3), Cobra CLI

---

## Task 1: Config Package - agent.yaml Parsing

Create a config package to parse agent.yaml manifests.

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Step 1: Write failing tests**

```go
// internal/config/config_test.go
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
```

**Step 2: Run tests to verify they fail**

Run: `go test -v ./internal/config/...`
Expected: FAIL (package does not exist)

**Step 3: Implement config package**

```go
// internal/config/config.go
package config

import (
	"fmt"
	"os"
	"path/filepath"

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
```

**Step 4: Add yaml dependency**

Run: `go get gopkg.in/yaml.v3`

**Step 5: Run tests to verify they pass**

Run: `go test -v ./internal/config/...`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/config/ go.mod go.sum
git commit -m "feat(config): add agent.yaml parsing"
```

---

## Task 2: Image Resolver

Create an image resolver that selects base images based on runtime requirements.

**Files:**
- Create: `internal/image/resolver.go`
- Create: `internal/image/resolver_test.go`

**Step 1: Write failing tests**

```go
// internal/image/resolver_test.go
package image

import (
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestResolveDefault(t *testing.T) {
	img := Resolve(nil)
	if img != DefaultImage {
		t.Errorf("Resolve(nil) = %q, want %q", img, DefaultImage)
	}
}

func TestResolveNode(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.Runtime{Node: "20"},
	}
	img := Resolve(cfg)
	if img != "node:20" {
		t.Errorf("Resolve(node:20) = %q, want %q", img, "node:20")
	}
}

func TestResolvePython(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.Runtime{Python: "3.11"},
	}
	img := Resolve(cfg)
	if img != "python:3.11" {
		t.Errorf("Resolve(python:3.11) = %q, want %q", img, "python:3.11")
	}
}

func TestResolvePolyglot(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.Runtime{
			Node:   "20",
			Python: "3.11",
		},
	}
	img := Resolve(cfg)
	// When both are specified, use ubuntu as base
	if img != DefaultImage {
		t.Errorf("Resolve(polyglot) = %q, want %q", img, DefaultImage)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test -v ./internal/image/...`
Expected: FAIL (package does not exist)

**Step 3: Implement image resolver**

```go
// internal/image/resolver.go
package image

import "github.com/majorcontext/moat/internal/config"

// DefaultImage is the default container image.
const DefaultImage = "ubuntu:22.04"

// Resolve selects the best base image for the given config.
func Resolve(cfg *config.Config) string {
	if cfg == nil {
		return DefaultImage
	}

	// Count runtimes specified
	runtimes := 0
	if cfg.Runtime.Node != "" {
		runtimes++
	}
	if cfg.Runtime.Python != "" {
		runtimes++
	}
	if cfg.Runtime.Go != "" {
		runtimes++
	}

	// If multiple runtimes, use ubuntu base
	if runtimes > 1 {
		return DefaultImage
	}

	// Single runtime - use official image
	if cfg.Runtime.Node != "" {
		return "node:" + cfg.Runtime.Node
	}
	if cfg.Runtime.Python != "" {
		return "python:" + cfg.Runtime.Python
	}
	if cfg.Runtime.Go != "" {
		return "golang:" + cfg.Runtime.Go
	}

	return DefaultImage
}
```

**Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/image/...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/image/
git commit -m "feat(image): add runtime-based image resolver"
```

---

## Task 3: Integrate Config with Run Manager

Wire config parsing into the run command.

**Files:**
- Modify: `internal/run/run.go` - add Config field to Options
- Modify: `internal/run/manager.go` - use config for image, env, mounts
- Modify: `cmd/agent/cli/run.go` - load and apply config

**Step 1: Update Options in run.go**

```go
// internal/run/run.go
import "github.com/majorcontext/moat/internal/config"

type Options struct {
	Agent     string
	Workspace string
	Grants    []string
	Cmd       []string
	Config    *config.Config // Optional agent.yaml config
	Env       []string       // Additional environment variables
}
```

**Step 2: Update manager.go to use config**

In Create():
1. Use image.Resolve(opts.Config) instead of hardcoded defaultImage
2. Merge config env vars with proxy env vars
3. Parse and add config mounts

**Step 3: Update run.go CLI to load config**

In runRun():
```go
// Load agent.yaml if present
cfg, err := config.Load(workspace)
if err != nil {
	return err
}

// Apply config defaults
if cfg != nil {
	if agent == "" && cfg.Agent != "" {
		agent = cfg.Agent
	}
	if len(grants) == 0 && len(cfg.Grants) > 0 {
		grants = cfg.Grants
	}
}

opts := run.Options{
	Agent:     agent,
	Workspace: workspace,
	Grants:    grants,
	Cmd:       cmd,
	Config:    cfg,
}
```

**Step 4: Run tests**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/run/ internal/image/ cmd/agent/cli/
git commit -m "feat(run): integrate config and image resolver"
```

---

## Task 4: Environment Variable Support

Add --env flag to run command and merge with config env vars.

**Files:**
- Modify: `cmd/agent/cli/run.go` - add --env flag
- Modify: `internal/run/manager.go` - merge env vars

**Step 1: Add --env flag to run.go**

```go
var runEnv []string

func init() {
	// ... existing flags ...
	runCmd.Flags().StringArrayVarP(&runEnv, "env", "e", nil, "environment variables (KEY=VALUE)")
}
```

**Step 2: Update manager.go to merge env vars**

In Create(), after proxy env setup:
```go
// Add config env vars
if opts.Config != nil {
	for k, v := range opts.Config.Env {
		proxyEnv = append(proxyEnv, k+"="+v)
	}
}

// Add explicit env vars (highest priority)
proxyEnv = append(proxyEnv, opts.Env...)
```

**Step 3: Pass env to options**

In runRun():
```go
opts := run.Options{
	// ...
	Env: runEnv,
}
```

**Step 4: Test manually**

```bash
./agent run test -e FOO=bar -- sh -c 'echo $FOO'
```

**Step 5: Commit**

```bash
git add cmd/agent/cli/run.go internal/run/
git commit -m "feat(cli): add --env flag to run command"
```

---

## Task 5: Mount Parsing from Config

Parse mount strings from agent.yaml and add to container.

**Files:**
- Create: `internal/config/mount.go`
- Create: `internal/config/mount_test.go`
- Modify: `internal/run/manager.go` - use parsed mounts

**Step 1: Write mount parsing tests**

```go
// internal/config/mount_test.go
package config

import "testing"

func TestParseMount(t *testing.T) {
	tests := []struct {
		input    string
		source   string
		target   string
		readOnly bool
	}{
		{"./data:/data", "./data", "/data", false},
		{"./data:/data:ro", "./data", "/data", true},
		{"/abs/path:/container", "/abs/path", "/container", false},
	}

	for _, tt := range tests {
		m, err := ParseMount(tt.input)
		if err != nil {
			t.Errorf("ParseMount(%q): %v", tt.input, err)
			continue
		}
		if m.Source != tt.source {
			t.Errorf("Source = %q, want %q", m.Source, tt.source)
		}
		if m.Target != tt.target {
			t.Errorf("Target = %q, want %q", m.Target, tt.target)
		}
		if m.ReadOnly != tt.readOnly {
			t.Errorf("ReadOnly = %v, want %v", m.ReadOnly, tt.readOnly)
		}
	}
}
```

**Step 2: Implement mount parsing**

```go
// internal/config/mount.go
package config

import (
	"fmt"
	"strings"
)

// Mount represents a volume mount.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// ParseMount parses a mount string like "./data:/data:ro".
func ParseMount(s string) (*Mount, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid mount: %s (expected source:target[:ro])", s)
	}

	m := &Mount{
		Source: parts[0],
		Target: parts[1],
	}

	if len(parts) >= 3 && parts[2] == "ro" {
		m.ReadOnly = true
	}

	return m, nil
}
```

**Step 3: Update manager to use config mounts**

In Create(), after workspace mount:
```go
// Add mounts from config
if opts.Config != nil {
	for _, mountStr := range opts.Config.Mounts {
		m, err := config.ParseMount(mountStr)
		if err != nil {
			return nil, fmt.Errorf("parsing mount: %w", err)
		}
		// Resolve relative paths against workspace
		source := m.Source
		if !filepath.IsAbs(source) {
			source = filepath.Join(opts.Workspace, source)
		}
		mounts = append(mounts, docker.MountConfig{
			Source:   source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
}
```

**Step 4: Run tests**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/ internal/run/
git commit -m "feat(config): add mount parsing from agent.yaml"
```

---

## Task 6: Improve Error Messages and Help

Polish CLI with better error messages and examples.

**Files:**
- Modify: `cmd/agent/cli/run.go` - improve help and errors
- Modify: `cmd/agent/cli/grant.go` - improve help

**Step 1: Update run.go help**

```go
var runCmd = &cobra.Command{
	Use:   "run <agent> [path]",
	Short: "Run an agent in an isolated environment",
	Long: `Run an agent in an isolated container with workspace mounting,
credential injection, and full observability.

If an agent.yaml exists in the workspace, its settings are used as defaults.

Examples:
  # Run an agent on the current directory
  agent run claude-code .

  # Run with GitHub credentials
  agent run claude-code . --grant github

  # Run with environment variables
  agent run test . -e DEBUG=true -e API_KEY=xxx

  # Run a custom command
  agent run test . -- npm test`,
	// ...
}
```

**Step 2: Improve error messages**

Add helpful context to errors:
```go
if err != nil {
	return fmt.Errorf("failed to start run: %w\n\nTry: agent grant github (if credentials are needed)", err)
}
```

**Step 3: Test help output**

```bash
./agent run --help
./agent grant --help
```

**Step 4: Commit**

```bash
git add cmd/agent/cli/
git commit -m "docs(cli): improve help text and examples"
```

---

## Task 7: Add promote Command (Stub)

Add a stub for the promote command mentioned in the design.

**Files:**
- Create: `cmd/agent/cli/promote.go`

**Step 1: Create promote stub**

```go
// cmd/agent/cli/promote.go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var promoteCmd = &cobra.Command{
	Use:   "promote [run-id]",
	Short: "Promote run artifacts to persistent storage",
	Long: `Promote preserves artifacts from a run or graduates a workspace
to a persistent state.

Examples:
  agent promote                 # Promote latest run
  agent promote run-abc123      # Promote specific run`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("promote command not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(promoteCmd)
}
```

**Step 2: Commit**

```bash
git add cmd/agent/cli/promote.go
git commit -m "feat(cli): add promote command stub"
```

---

## Task 8: Final Integration Test

Test the full config-driven workflow.

**Files:**
- Create: `internal/config/integration_test.go`

**Step 1: Write integration test**

```go
// internal/config/integration_test.go
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
```

**Step 2: Run all tests**

Run: `go test ./...`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/config/
git commit -m "test(config): add full config workflow integration test"
```

---

## Final Verification

Run all tests and build:

```bash
go test ./...
go build -o agent ./cmd/agent
./agent --help
./agent run --help
```

Expected: All tests pass, CLI shows improved help.
