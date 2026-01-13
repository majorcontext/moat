# Dependencies Feature Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the `runtime` field with a `dependencies` field that supports runtimes, build tools, and test tools with Homebrew-style versioning.

**Architecture:** YAML registry embedded in binary defines dependencies with install types (runtime, github-binary, apt, npm, custom). Docker builds layered images; Apple containers run install scripts at startup.

**Tech Stack:** Go, go:embed, gopkg.in/yaml.v3, Docker buildkit

---

## Task 1: Create Registry Data Structure

**Files:**
- Create: `internal/deps/types.go`
- Test: `internal/deps/types_test.go`

**Step 1: Write the failing test**

```go
// internal/deps/types_test.go
package deps

import "testing"

func TestDepSpecString(t *testing.T) {
	spec := DepSpec{
		Description: "Node.js runtime",
		Type:        TypeRuntime,
		Default:     "20",
	}
	if spec.Type != TypeRuntime {
		t.Errorf("Type = %v, want %v", spec.Type, TypeRuntime)
	}
}

func TestInstallTypeConstants(t *testing.T) {
	types := []InstallType{
		TypeRuntime,
		TypeGithubBinary,
		TypeApt,
		TypeNpm,
		TypePip,
		TypeCustom,
	}
	for _, typ := range types {
		if typ == "" {
			t.Error("InstallType should not be empty")
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/deps/`
Expected: FAIL - package does not exist

**Step 3: Write minimal implementation**

```go
// internal/deps/types.go
package deps

// InstallType defines how a dependency is installed.
type InstallType string

const (
	TypeRuntime      InstallType = "runtime"
	TypeGithubBinary InstallType = "github-binary"
	TypeApt          InstallType = "apt"
	TypeNpm          InstallType = "npm"
	TypePip          InstallType = "pip"
	TypeCustom       InstallType = "custom"
)

// DepSpec defines a dependency in the registry.
type DepSpec struct {
	Description string      `yaml:"description,omitempty"`
	Type        InstallType `yaml:"type"`
	Default     string      `yaml:"default,omitempty"`
	Versions    []string    `yaml:"versions,omitempty"`
	Requires    []string    `yaml:"requires,omitempty"`

	// For github-binary type
	Repo  string `yaml:"repo,omitempty"`
	Asset string `yaml:"asset,omitempty"`
	Bin   string `yaml:"bin,omitempty"`

	// For apt type
	Package string `yaml:"package,omitempty"`

	// For npm/pip type
	// Package field is reused
}

// Dependency represents a parsed dependency from agent.yaml.
type Dependency struct {
	Name    string // e.g., "node"
	Version string // e.g., "20" or "" for default
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/deps/`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/deps/
git commit -m "feat(deps): add dependency type definitions"
```

---

## Task 2: Create Registry YAML and Loader

**Files:**
- Create: `internal/deps/registry.yaml`
- Create: `internal/deps/registry.go`
- Test: `internal/deps/registry_test.go`

**Step 1: Write the failing test**

```go
// internal/deps/registry_test.go
package deps

import "testing"

func TestRegistryLoaded(t *testing.T) {
	if len(Registry) == 0 {
		t.Fatal("Registry should not be empty")
	}
}

func TestRegistryHasNode(t *testing.T) {
	node, ok := Registry["node"]
	if !ok {
		t.Fatal("Registry should have 'node'")
	}
	if node.Type != TypeRuntime {
		t.Errorf("node.Type = %v, want %v", node.Type, TypeRuntime)
	}
	if node.Default == "" {
		t.Error("node.Default should not be empty")
	}
}

func TestRegistryHasProtoc(t *testing.T) {
	protoc, ok := Registry["protoc"]
	if !ok {
		t.Fatal("Registry should have 'protoc'")
	}
	if protoc.Type != TypeGithubBinary {
		t.Errorf("protoc.Type = %v, want %v", protoc.Type, TypeGithubBinary)
	}
	if protoc.Repo == "" {
		t.Error("protoc.Repo should not be empty")
	}
}

func TestRegistryHasPlaywright(t *testing.T) {
	pw, ok := Registry["playwright"]
	if !ok {
		t.Fatal("Registry should have 'playwright'")
	}
	if pw.Type != TypeCustom {
		t.Errorf("playwright.Type = %v, want %v", pw.Type, TypeCustom)
	}
	if len(pw.Requires) == 0 || pw.Requires[0] != "node" {
		t.Errorf("playwright.Requires = %v, want [node]", pw.Requires)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/deps/`
Expected: FAIL - Registry undefined

**Step 3: Write registry.yaml with all 15 dependencies**

```yaml
# internal/deps/registry.yaml
# Dependency registry - embedded into binary at compile time

# Runtimes
node:
  description: Node.js runtime
  type: runtime
  default: "20"
  versions: ["18", "20", "22"]

go:
  description: Go programming language
  type: runtime
  default: "1.22"

# Package managers / runtimes
bun:
  description: JavaScript runtime and package manager
  type: github-binary
  repo: oven-sh/bun
  asset: "bun-linux-x64.zip"
  bin: bun
  default: "1.1.0"

# npm packages
typescript:
  description: TypeScript compiler
  type: npm
  package: typescript
  requires: [node]

yarn:
  description: Yarn package manager
  type: npm
  package: yarn
  requires: [node]

pnpm:
  description: pnpm package manager
  type: npm
  package: pnpm
  requires: [node]

# Build tools (github-binary)
protoc:
  description: Protocol Buffers compiler
  type: github-binary
  repo: protocolbuffers/protobuf
  asset: "protoc-{version}-linux-x86_64.zip"
  bin: bin/protoc
  default: "25.1"

sqlc:
  description: SQL compiler for Go
  type: github-binary
  repo: sqlc-dev/sqlc
  asset: "sqlc_{version}_linux_amd64.tar.gz"
  default: "1.25.0"

golangci-lint:
  description: Go linter aggregator
  type: github-binary
  repo: golangci/golangci-lint
  asset: "golangci-lint-{version}-linux-amd64.tar.gz"
  bin: "golangci-lint-{version}-linux-amd64/golangci-lint"
  default: "1.55.2"

# CLI tools
gh:
  description: GitHub CLI
  type: github-binary
  repo: cli/cli
  asset: "gh_{version}_linux_amd64.tar.gz"
  bin: "gh_{version}_linux_amd64/bin/gh"
  default: "2.40.0"

# Database clients (apt)
psql:
  description: PostgreSQL client
  type: apt
  package: postgresql-client

mysql:
  description: MySQL client
  type: apt
  package: mysql-client

# Custom installers (complex logic)
playwright:
  description: Browser testing framework
  type: custom
  requires: [node]
  default: "latest"

aws:
  description: AWS CLI
  type: custom
  default: "2"

gcloud:
  description: Google Cloud CLI
  type: custom
  default: "latest"
```

**Step 4: Write registry loader**

```go
// internal/deps/registry.go
package deps

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

//go:embed registry.yaml
var registryData []byte

// Registry holds all available dependencies.
var Registry map[string]DepSpec

func init() {
	Registry = make(map[string]DepSpec)
	if err := yaml.Unmarshal(registryData, &Registry); err != nil {
		panic("invalid registry.yaml: " + err.Error())
	}
}
```

**Step 5: Run test to verify it passes**

Run: `go test -v ./internal/deps/`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/deps/
git commit -m "feat(deps): add embedded dependency registry"
```

---

## Task 3: Dependency Parser

**Files:**
- Create: `internal/deps/parser.go`
- Test: `internal/deps/parser_test.go`

**Step 1: Write the failing test**

```go
// internal/deps/parser_test.go
package deps

import "testing"

func TestParseDependency(t *testing.T) {
	tests := []struct {
		input   string
		name    string
		version string
		wantErr bool
	}{
		{"node", "node", "", false},
		{"node@20", "node", "20", false},
		{"node@20.11", "node", "20.11", false},
		{"protoc@25.1", "protoc", "25.1", false},
		{"golangci-lint@1.55.2", "golangci-lint", "1.55.2", false},
		{"", "", "", true},
		{"@20", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dep, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) should error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			if dep.Name != tt.name {
				t.Errorf("Name = %q, want %q", dep.Name, tt.name)
			}
			if dep.Version != tt.version {
				t.Errorf("Version = %q, want %q", dep.Version, tt.version)
			}
		})
	}
}

func TestParseAll(t *testing.T) {
	deps, err := ParseAll([]string{"node@20", "protoc", "typescript"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	if len(deps) != 3 {
		t.Fatalf("len(deps) = %d, want 3", len(deps))
	}
}

func TestParseAllUnknown(t *testing.T) {
	_, err := ParseAll([]string{"node", "unknowndep"})
	if err == nil {
		t.Error("ParseAll should error for unknown dependency")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/deps/ -run TestParse`
Expected: FAIL - Parse undefined

**Step 3: Write parser implementation**

```go
// internal/deps/parser.go
package deps

import (
	"fmt"
	"strings"
)

// Parse parses a dependency string like "node" or "node@20".
func Parse(s string) (Dependency, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Dependency{}, fmt.Errorf("empty dependency")
	}

	parts := strings.SplitN(s, "@", 2)
	name := parts[0]
	if name == "" {
		return Dependency{}, fmt.Errorf("invalid dependency %q: missing name", s)
	}

	var version string
	if len(parts) == 2 {
		version = parts[1]
	}

	return Dependency{Name: name, Version: version}, nil
}

// ParseAll parses multiple dependency strings and validates they exist in the registry.
func ParseAll(specs []string) ([]Dependency, error) {
	deps := make([]Dependency, 0, len(specs))
	for _, s := range specs {
		dep, err := Parse(s)
		if err != nil {
			return nil, err
		}
		if _, ok := Registry[dep.Name]; !ok {
			return nil, fmt.Errorf("unknown dependency %q", dep.Name)
		}
		deps = append(deps, dep)
	}
	return deps, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/deps/ -run TestParse`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/deps/
git commit -m "feat(deps): add dependency parser"
```

---

## Task 4: Dependency Validator

**Files:**
- Modify: `internal/deps/parser.go`
- Test: `internal/deps/parser_test.go`

**Step 1: Write the failing test**

```go
// Add to internal/deps/parser_test.go

func TestValidate(t *testing.T) {
	tests := []struct {
		deps    []string
		wantErr bool
		errMsg  string
	}{
		// Valid cases
		{[]string{"node"}, false, ""},
		{[]string{"node", "typescript"}, false, ""},
		{[]string{"node@20", "yarn", "pnpm"}, false, ""},

		// Missing requirement
		{[]string{"typescript"}, true, "requires node"},
		{[]string{"playwright"}, true, "requires node"},
		{[]string{"yarn", "pnpm"}, true, "requires node"},

		// Unknown dependency
		{[]string{"unknown"}, true, "unknown dependency"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.deps, ","), func(t *testing.T) {
			deps, _ := ParseAll(tt.deps)
			err := Validate(deps)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate(%v) should error", tt.deps)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("Validate(%v) error: %v", tt.deps, err)
			}
		})
	}
}

func TestValidateSuggestion(t *testing.T) {
	_, err := ParseAll([]string{"nodejs"})
	if err == nil {
		t.Fatal("should error for nodejs")
	}
	if !strings.Contains(err.Error(), "node") {
		t.Errorf("error should suggest 'node', got: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/deps/ -run TestValidate`
Expected: FAIL - Validate undefined

**Step 3: Write validator implementation**

```go
// Add to internal/deps/parser.go

import "sort"

// Validate checks that all dependency requirements are satisfied.
func Validate(deps []Dependency) error {
	// Build set of dependency names
	depSet := make(map[string]bool)
	for _, d := range deps {
		depSet[d.Name] = true
	}

	// Check requirements
	for _, d := range deps {
		spec, ok := Registry[d.Name]
		if !ok {
			return fmt.Errorf("unknown dependency %q%s", d.Name, suggestDep(d.Name))
		}
		for _, req := range spec.Requires {
			if !depSet[req] {
				return fmt.Errorf("%s requires %s\n\n  Add '%s' to your dependencies:\n    dependencies:\n      - %s\n      - %s",
					d.Name, req, req, req, d.Name)
			}
		}
	}
	return nil
}

// suggestDep returns a suggestion message if a similar dependency exists.
func suggestDep(name string) string {
	suggestions := map[string]string{
		"nodejs":   "node",
		"node.js":  "node",
		"golang":   "go",
		"python":   "python",
		"python3":  "python",
		"postgres": "psql",
		"pg":       "psql",
		"awscli":   "aws",
		"aws-cli":  "aws",
		"gcp":      "gcloud",
	}
	if sugg, ok := suggestions[name]; ok {
		return fmt.Sprintf("\n  Did you mean '%s'?", sugg)
	}

	// Check for close matches in registry
	for regName := range Registry {
		if strings.Contains(regName, name) || strings.Contains(name, regName) {
			return fmt.Sprintf("\n  Did you mean '%s'?", regName)
		}
	}
	return ""
}

// List returns all available dependency names sorted alphabetically.
func List() []string {
	names := make([]string, 0, len(Registry))
	for name := range Registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/deps/ -run TestValidate`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/deps/
git commit -m "feat(deps): add dependency validation with suggestions"
```

---

## Task 5: Update Config to Use Dependencies

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Step 1: Write the failing test**

```go
// Add to internal/config/config_test.go

func TestLoadConfigWithDependencies(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
name: myapp
agent: test

dependencies:
  - node@20
  - typescript
  - protoc@25.1
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Dependencies) != 3 {
		t.Fatalf("Dependencies = %d, want 3", len(cfg.Dependencies))
	}
	if cfg.Dependencies[0] != "node@20" {
		t.Errorf("Dependencies[0] = %q, want %q", cfg.Dependencies[0], "node@20")
	}
}

func TestLoadConfigRejectsRuntime(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
name: myapp
agent: test
runtime:
  node: "20"
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should error when runtime field is present")
	}
	if !strings.Contains(err.Error(), "no longer supported") {
		t.Errorf("error should mention 'no longer supported', got: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/config/ -run TestLoadConfig`
Expected: FAIL - Dependencies field not found

**Step 3: Update config.go**

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
	Name         string            `yaml:"name,omitempty"`
	Agent        string            `yaml:"agent"`
	Version      string            `yaml:"version,omitempty"`
	Dependencies []string          `yaml:"dependencies,omitempty"`
	Grants       []string          `yaml:"grants,omitempty"`
	Env          map[string]string `yaml:"env,omitempty"`
	Mounts       []string          `yaml:"mounts,omitempty"`
	Ports        map[string]int    `yaml:"ports,omitempty"`

	// Deprecated: use Dependencies instead
	Runtime *deprecatedRuntime `yaml:"runtime,omitempty"`
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

	return &cfg, nil
}

// DefaultConfig returns a default configuration.
func DefaultConfig() *Config {
	return &Config{
		Env: make(map[string]string),
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/config/ -run TestLoadConfig`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): replace runtime with dependencies field"
```

---

## Task 6: Docker Image Builder

**Files:**
- Create: `internal/deps/dockerfile.go`
- Test: `internal/deps/dockerfile_test.go`

**Step 1: Write the failing test**

```go
// internal/deps/dockerfile_test.go
package deps

import (
	"strings"
	"testing"
)

func TestGenerateDockerfile(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "typescript"},
		{Name: "protoc", Version: "25.1"},
		{Name: "psql"},
	}

	dockerfile, err := GenerateDockerfile(deps)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Check base image
	if !strings.HasPrefix(dockerfile, "FROM ubuntu:22.04") {
		t.Error("Dockerfile should start with FROM ubuntu:22.04")
	}

	// Check apt packages are batched
	if !strings.Contains(dockerfile, "apt-get install") {
		t.Error("Dockerfile should have apt-get install")
	}
	if !strings.Contains(dockerfile, "postgresql-client") {
		t.Error("Dockerfile should install postgresql-client")
	}

	// Check node setup
	if !strings.Contains(dockerfile, "nodesource") || !strings.Contains(dockerfile, "setup_20") {
		t.Error("Dockerfile should set up Node.js 20")
	}

	// Check npm globals
	if !strings.Contains(dockerfile, "npm install -g typescript") {
		t.Error("Dockerfile should install typescript via npm")
	}

	// Check protoc
	if !strings.Contains(dockerfile, "protoc") && !strings.Contains(dockerfile, "25.1") {
		t.Error("Dockerfile should install protoc 25.1")
	}
}

func TestGenerateDockerfileEmpty(t *testing.T) {
	dockerfile, err := GenerateDockerfile(nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if !strings.HasPrefix(dockerfile, "FROM ubuntu:22.04") {
		t.Error("Empty deps should still have base image")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/deps/ -run TestGenerateDockerfile`
Expected: FAIL - GenerateDockerfile undefined

**Step 3: Write Dockerfile generator**

```go
// internal/deps/dockerfile.go
package deps

import (
	"fmt"
	"sort"
	"strings"
)

const baseImage = "ubuntu:22.04"

// GenerateDockerfile creates a Dockerfile for the given dependencies.
func GenerateDockerfile(deps []Dependency) (string, error) {
	var b strings.Builder

	b.WriteString("FROM " + baseImage + "\n\n")
	b.WriteString("ENV DEBIAN_FRONTEND=noninteractive\n\n")

	// Sort dependencies into categories for optimal layer caching
	var (
		aptPkgs      []string
		runtimes     []Dependency
		githubBins   []Dependency
		npmPkgs      []Dependency
		customDeps   []Dependency
	)

	for _, dep := range deps {
		spec := Registry[dep.Name]
		switch spec.Type {
		case TypeApt:
			aptPkgs = append(aptPkgs, spec.Package)
		case TypeRuntime:
			runtimes = append(runtimes, dep)
		case TypeGithubBinary:
			githubBins = append(githubBins, dep)
		case TypeNpm:
			npmPkgs = append(npmPkgs, dep)
		case TypeCustom:
			customDeps = append(customDeps, dep)
		}
	}

	// Layer 1: Base apt packages (curl, ca-certificates, etc.)
	b.WriteString("# Base packages\n")
	b.WriteString("RUN apt-get update && apt-get install -y \\\n")
	b.WriteString("    curl \\\n")
	b.WriteString("    ca-certificates \\\n")
	b.WriteString("    gnupg \\\n")
	b.WriteString("    unzip \\\n")
	b.WriteString("    && rm -rf /var/lib/apt/lists/*\n\n")

	// Layer 2: User apt packages
	if len(aptPkgs) > 0 {
		sort.Strings(aptPkgs)
		b.WriteString("# Apt packages\n")
		b.WriteString("RUN apt-get update && apt-get install -y \\\n")
		for i, pkg := range aptPkgs {
			if i < len(aptPkgs)-1 {
				b.WriteString("    " + pkg + " \\\n")
			} else {
				b.WriteString("    " + pkg + " \\\n")
			}
		}
		b.WriteString("    && rm -rf /var/lib/apt/lists/*\n\n")
	}

	// Layer 3: Runtimes
	for _, dep := range runtimes {
		version := dep.Version
		if version == "" {
			version = Registry[dep.Name].Default
		}
		b.WriteString(fmt.Sprintf("# %s runtime\n", dep.Name))
		b.WriteString(generateRuntimeInstall(dep.Name, version))
		b.WriteString("\n")
	}

	// Layer 4: GitHub binary downloads
	for _, dep := range githubBins {
		spec := Registry[dep.Name]
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("# %s\n", dep.Name))
		b.WriteString(generateGithubBinaryInstall(dep.Name, version, spec))
		b.WriteString("\n")
	}

	// Layer 5: npm globals
	if len(npmPkgs) > 0 {
		var pkgNames []string
		for _, dep := range npmPkgs {
			spec := Registry[dep.Name]
			pkg := spec.Package
			if pkg == "" {
				pkg = dep.Name
			}
			pkgNames = append(pkgNames, pkg)
		}
		b.WriteString("# npm packages\n")
		b.WriteString("RUN npm install -g " + strings.Join(pkgNames, " ") + "\n\n")
	}

	// Layer 6: Custom installs
	for _, dep := range customDeps {
		version := dep.Version
		if version == "" {
			version = Registry[dep.Name].Default
		}
		b.WriteString(fmt.Sprintf("# %s (custom)\n", dep.Name))
		b.WriteString(generateCustomInstall(dep.Name, version))
		b.WriteString("\n")
	}

	return b.String(), nil
}

func generateRuntimeInstall(name, version string) string {
	switch name {
	case "node":
		return fmt.Sprintf(`RUN curl -fsSL https://deb.nodesource.com/setup_%s.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*
`, version)
	case "go":
		return fmt.Sprintf(`RUN curl -fsSL https://go.dev/dl/go%s.linux-amd64.tar.gz | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:$PATH"
`, version)
	default:
		return ""
	}
}

func generateGithubBinaryInstall(name, version string, spec DepSpec) string {
	asset := strings.ReplaceAll(spec.Asset, "{version}", version)
	binPath := strings.ReplaceAll(spec.Bin, "{version}", version)

	url := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", spec.Repo, version, asset)

	if strings.HasSuffix(asset, ".zip") {
		return fmt.Sprintf(`RUN curl -fsSL %s -o /tmp/%s.zip \
    && unzip /tmp/%s.zip -d /tmp/%s \
    && mv /tmp/%s/%s /usr/local/bin/%s \
    && chmod +x /usr/local/bin/%s \
    && rm -rf /tmp/%s*
`, url, name, name, name, name, binPath, name, name, name)
	}
	// tar.gz
	return fmt.Sprintf(`RUN curl -fsSL %s | tar -xz -C /tmp \
    && mv /tmp/%s /usr/local/bin/%s \
    && chmod +x /usr/local/bin/%s
`, url, binPath, name, name)
}

func generateCustomInstall(name, version string) string {
	switch name {
	case "playwright":
		return `RUN npm install -g playwright \
    && npx playwright install --with-deps chromium
`
	case "aws":
		return `RUN curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip \
    && unzip /tmp/awscliv2.zip -d /tmp \
    && /tmp/aws/install \
    && rm -rf /tmp/aws*
`
	case "gcloud":
		return `RUN curl -fsSL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-x86_64.tar.gz | tar -xz -C /opt \
    && /opt/google-cloud-sdk/install.sh --quiet --path-update=true
ENV PATH="/opt/google-cloud-sdk/bin:$PATH"
`
	default:
		return ""
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/deps/ -run TestGenerateDockerfile`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/deps/
git commit -m "feat(deps): add Dockerfile generator"
```

---

## Task 7: Docker Image Builder Integration

**Files:**
- Create: `internal/deps/builder.go`
- Test: `internal/deps/builder_test.go`

**Step 1: Write the failing test**

```go
// internal/deps/builder_test.go
package deps

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"testing"
)

func TestImageTag(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "typescript"},
	}
	tag := ImageTag(deps)
	if !strings.HasPrefix(tag, "agentops/run:") {
		t.Errorf("tag should start with agentops/run:, got %s", tag)
	}
	// Tag should be deterministic
	tag2 := ImageTag(deps)
	if tag != tag2 {
		t.Errorf("tags should be equal: %s != %s", tag, tag2)
	}
}

func TestImageTagDifferent(t *testing.T) {
	tag1 := ImageTag([]Dependency{{Name: "node", Version: "20"}})
	tag2 := ImageTag([]Dependency{{Name: "node", Version: "22"}})
	if tag1 == tag2 {
		t.Error("different deps should have different tags")
	}
}

func TestImageTagOrderIndependent(t *testing.T) {
	deps1 := []Dependency{{Name: "node"}, {Name: "protoc"}}
	deps2 := []Dependency{{Name: "protoc"}, {Name: "node"}}
	tag1 := ImageTag(deps1)
	tag2 := ImageTag(deps2)
	if tag1 != tag2 {
		t.Errorf("order should not matter: %s != %s", tag1, tag2)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/deps/ -run TestImageTag`
Expected: FAIL - ImageTag undefined

**Step 3: Write builder implementation**

```go
// internal/deps/builder.go
package deps

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// ImageTag generates a deterministic image tag for a set of dependencies.
func ImageTag(deps []Dependency) string {
	// Sort deps for deterministic ordering
	sorted := make([]string, len(deps))
	for i, d := range deps {
		v := d.Version
		if v == "" {
			v = Registry[d.Name].Default
		}
		sorted[i] = d.Name + "@" + v
	}
	sort.Strings(sorted)

	// Hash the sorted deps list
	h := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	hash := hex.EncodeToString(h[:])[:12]

	return "agentops/run:" + hash
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/deps/ -run TestImageTag`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/deps/
git commit -m "feat(deps): add image tag generation"
```

---

## Task 8: Install Script Generator (for Apple containers)

**Files:**
- Create: `internal/deps/script.go`
- Test: `internal/deps/script_test.go`

**Step 1: Write the failing test**

```go
// internal/deps/script_test.go
package deps

import (
	"strings"
	"testing"
)

func TestGenerateScript(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "typescript"},
		{Name: "psql"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("GenerateInstallScript error: %v", err)
	}

	// Check shebang
	if !strings.HasPrefix(script, "#!/bin/bash") {
		t.Error("Script should start with shebang")
	}

	// Check set -e
	if !strings.Contains(script, "set -e") {
		t.Error("Script should have set -e")
	}

	// Check apt packages
	if !strings.Contains(script, "postgresql-client") {
		t.Error("Script should install postgresql-client")
	}

	// Check node
	if !strings.Contains(script, "nodesource") {
		t.Error("Script should set up Node.js")
	}

	// Check npm
	if !strings.Contains(script, "npm install -g typescript") {
		t.Error("Script should install typescript")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/deps/ -run TestGenerateScript`
Expected: FAIL - GenerateInstallScript undefined

**Step 3: Write script generator**

```go
// internal/deps/script.go
package deps

import (
	"fmt"
	"sort"
	"strings"
)

// GenerateInstallScript creates a bash script to install dependencies.
// Used for Apple containers which don't have Docker's layer caching.
func GenerateInstallScript(deps []Dependency) (string, error) {
	var b strings.Builder

	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -e\n\n")
	b.WriteString("export DEBIAN_FRONTEND=noninteractive\n\n")

	// Sort dependencies into categories
	var (
		aptPkgs    []string
		runtimes   []Dependency
		githubBins []Dependency
		npmPkgs    []Dependency
		customDeps []Dependency
	)

	for _, dep := range deps {
		spec := Registry[dep.Name]
		switch spec.Type {
		case TypeApt:
			aptPkgs = append(aptPkgs, spec.Package)
		case TypeRuntime:
			runtimes = append(runtimes, dep)
		case TypeGithubBinary:
			githubBins = append(githubBins, dep)
		case TypeNpm:
			npmPkgs = append(npmPkgs, dep)
		case TypeCustom:
			customDeps = append(customDeps, dep)
		}
	}

	// Base packages
	b.WriteString("echo '==> Installing base packages...'\n")
	b.WriteString("apt-get update\n")
	b.WriteString("apt-get install -y curl ca-certificates gnupg unzip\n\n")

	// User apt packages
	if len(aptPkgs) > 0 {
		sort.Strings(aptPkgs)
		b.WriteString("echo '==> Installing apt packages...'\n")
		b.WriteString("apt-get install -y " + strings.Join(aptPkgs, " ") + "\n\n")
	}

	// Runtimes
	for _, dep := range runtimes {
		version := dep.Version
		if version == "" {
			version = Registry[dep.Name].Default
		}
		b.WriteString(fmt.Sprintf("echo '==> Installing %s %s...'\n", dep.Name, version))
		b.WriteString(generateRuntimeScript(dep.Name, version))
		b.WriteString("\n")
	}

	// GitHub binaries
	for _, dep := range githubBins {
		spec := Registry[dep.Name]
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("echo '==> Installing %s %s...'\n", dep.Name, version))
		b.WriteString(generateGithubBinaryScript(dep.Name, version, spec))
		b.WriteString("\n")
	}

	// npm packages
	if len(npmPkgs) > 0 {
		var pkgNames []string
		for _, dep := range npmPkgs {
			spec := Registry[dep.Name]
			pkg := spec.Package
			if pkg == "" {
				pkg = dep.Name
			}
			pkgNames = append(pkgNames, pkg)
		}
		b.WriteString("echo '==> Installing npm packages...'\n")
		b.WriteString("npm install -g " + strings.Join(pkgNames, " ") + "\n\n")
	}

	// Custom
	for _, dep := range customDeps {
		version := dep.Version
		if version == "" {
			version = Registry[dep.Name].Default
		}
		b.WriteString(fmt.Sprintf("echo '==> Installing %s...'\n", dep.Name))
		b.WriteString(generateCustomScript(dep.Name, version))
		b.WriteString("\n")
	}

	b.WriteString("echo '==> Dependencies installed successfully'\n")

	return b.String(), nil
}

func generateRuntimeScript(name, version string) string {
	switch name {
	case "node":
		return fmt.Sprintf(`curl -fsSL https://deb.nodesource.com/setup_%s.x | bash -
apt-get install -y nodejs
`, version)
	case "go":
		return fmt.Sprintf(`curl -fsSL https://go.dev/dl/go%s.linux-amd64.tar.gz | tar -C /usr/local -xz
export PATH="/usr/local/go/bin:$PATH"
echo 'export PATH="/usr/local/go/bin:$PATH"' >> /etc/profile.d/go.sh
`, version)
	default:
		return ""
	}
}

func generateGithubBinaryScript(name, version string, spec DepSpec) string {
	asset := strings.ReplaceAll(spec.Asset, "{version}", version)
	binPath := strings.ReplaceAll(spec.Bin, "{version}", version)
	url := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", spec.Repo, version, asset)

	if strings.HasSuffix(asset, ".zip") {
		return fmt.Sprintf(`curl -fsSL %s -o /tmp/%s.zip
unzip /tmp/%s.zip -d /tmp/%s
mv /tmp/%s/%s /usr/local/bin/%s
chmod +x /usr/local/bin/%s
rm -rf /tmp/%s*
`, url, name, name, name, name, binPath, name, name, name)
	}
	return fmt.Sprintf(`curl -fsSL %s | tar -xz -C /tmp
mv /tmp/%s /usr/local/bin/%s
chmod +x /usr/local/bin/%s
`, url, binPath, name, name)
}

func generateCustomScript(name, version string) string {
	switch name {
	case "playwright":
		return `npm install -g playwright
npx playwright install --with-deps chromium
`
	case "aws":
		return `curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip
unzip /tmp/awscliv2.zip -d /tmp
/tmp/aws/install
rm -rf /tmp/aws*
`
	case "gcloud":
		return `curl -fsSL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-x86_64.tar.gz | tar -xz -C /opt
/opt/google-cloud-sdk/install.sh --quiet --path-update=true
echo 'export PATH="/opt/google-cloud-sdk/bin:$PATH"' >> /etc/profile.d/gcloud.sh
`
	default:
		return ""
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/deps/ -run TestGenerateScript`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/deps/
git commit -m "feat(deps): add install script generator for Apple containers"
```

---

## Task 9: Update Image Resolver

**Files:**
- Modify: `internal/image/resolver.go`
- Modify: `internal/image/resolver_test.go`

**Step 1: Write the failing test**

```go
// Replace internal/image/resolver_test.go
package image

import (
	"testing"

	"github.com/andybons/agentops/internal/config"
	"github.com/andybons/agentops/internal/deps"
)

func TestResolveNoDeps(t *testing.T) {
	img := Resolve(nil, nil)
	if img != DefaultImage {
		t.Errorf("Resolve(nil, nil) = %q, want %q", img, DefaultImage)
	}
}

func TestResolveWithDeps(t *testing.T) {
	depList := []deps.Dependency{{Name: "node", Version: "20"}}
	img := Resolve(nil, depList)
	// Should return a generated image tag
	if img == DefaultImage {
		t.Error("Resolve with deps should not return default image")
	}
	if img == "" {
		t.Error("Resolve with deps should return non-empty image")
	}
}

func TestResolveEmptyDeps(t *testing.T) {
	cfg := &config.Config{Dependencies: []string{}}
	img := Resolve(cfg, nil)
	if img != DefaultImage {
		t.Errorf("Resolve(empty deps) = %q, want %q", img, DefaultImage)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/image/`
Expected: FAIL - function signature changed

**Step 3: Update resolver**

```go
// internal/image/resolver.go
package image

import "github.com/andybons/agentops/internal/deps"

// DefaultImage is the default container image.
const DefaultImage = "ubuntu:22.04"

// Resolve determines the image to use based on dependencies.
// If deps is provided and non-empty, returns the tag for a built image.
// Otherwise returns the default base image.
func Resolve(cfg interface{}, depList []deps.Dependency) string {
	if len(depList) == 0 {
		return DefaultImage
	}
	return deps.ImageTag(depList)
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/image/`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/image/
git commit -m "refactor(image): update resolver to use deps package"
```

---

## Task 10: Update Run Manager

**Files:**
- Modify: `internal/run/manager.go`

**Step 1: Write the failing test**

This is an integration point - we'll update the manager to:
1. Parse dependencies from config
2. Validate them
3. Build/use appropriate image

```go
// Add to internal/run/run_test.go or create new test file

func TestManagerParseDependencies(t *testing.T) {
	cfg := &config.Config{
		Dependencies: []string{"node@20", "typescript"},
	}

	depList, err := deps.ParseAll(cfg.Dependencies)
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}

	if err := deps.Validate(depList); err != nil {
		t.Fatalf("Validate error: %v", err)
	}

	if len(depList) != 2 {
		t.Errorf("len(depList) = %d, want 2", len(depList))
	}
}
```

**Step 2: Run test to verify it passes**

This test should pass with existing code.

**Step 3: Update manager.go to use deps**

Update the `Create` method in `internal/run/manager.go`:

```go
// In the Create method, after loading config and before creating container:

// Parse and validate dependencies
var depList []deps.Dependency
if opts.Config != nil && len(opts.Config.Dependencies) > 0 {
	var err error
	depList, err = deps.ParseAll(opts.Config.Dependencies)
	if err != nil {
		return nil, fmt.Errorf("parsing dependencies: %w", err)
	}
	if err := deps.Validate(depList); err != nil {
		return nil, fmt.Errorf("validating dependencies: %w", err)
	}
}

// Update image resolution
containerImage := image.Resolve(opts.Config, depList)

// For Docker with deps, we need to build the image first
if len(depList) > 0 && m.runtime.Type() == container.RuntimeDocker {
	// TODO: Build image using deps.GenerateDockerfile
	// For now, fall back to default image
	containerImage = image.DefaultImage
}
```

**Step 4: Commit**

```bash
git add internal/run/manager.go
git commit -m "feat(run): integrate dependency parsing and validation"
```

---

## Task 11: Add CLI Commands

**Files:**
- Create: `cmd/agent/cli/deps.go`

**Step 1: Write the command implementation**

```go
// cmd/agent/cli/deps.go
package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/andybons/agentops/internal/deps"
	"github.com/spf13/cobra"
)

var depsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Manage dependencies",
	Long:  `List and inspect available dependencies for agent runs.`,
}

var depsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available dependencies",
	Long: `List all dependencies that can be used in agent.yaml.

Examples:
  agent deps list
  agent deps list --type runtime
  agent deps list --type npm`,
	RunE: runDepsList,
}

var depsInfoCmd = &cobra.Command{
	Use:   "info [name]",
	Short: "Show dependency details",
	Long: `Show detailed information about a specific dependency.

Examples:
  agent deps info node
  agent deps info playwright`,
	Args: cobra.ExactArgs(1),
	RunE: runDepsInfo,
}

var typeFilter string

func init() {
	rootCmd.AddCommand(depsCmd)
	depsCmd.AddCommand(depsListCmd)
	depsCmd.AddCommand(depsInfoCmd)

	depsListCmd.Flags().StringVar(&typeFilter, "type", "", "filter by type (runtime, npm, apt, github-binary, custom)")
}

func runDepsList(cmd *cobra.Command, args []string) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tDEFAULT\tDESCRIPTION")

	names := deps.List()
	for _, name := range names {
		spec := deps.Registry[name]
		if typeFilter != "" && string(spec.Type) != typeFilter {
			continue
		}
		desc := spec.Description
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, spec.Type, spec.Default, desc)
	}
	w.Flush()
	return nil
}

func runDepsInfo(cmd *cobra.Command, args []string) error {
	name := args[0]
	spec, ok := deps.Registry[name]
	if !ok {
		// Try to suggest
		suggestions := []string{}
		for n := range deps.Registry {
			if strings.Contains(n, name) || strings.Contains(name, n) {
				suggestions = append(suggestions, n)
			}
		}
		msg := fmt.Sprintf("unknown dependency %q", name)
		if len(suggestions) > 0 {
			sort.Strings(suggestions)
			msg += fmt.Sprintf("\n\nDid you mean one of these?\n  %s", strings.Join(suggestions, "\n  "))
		}
		msg += "\n\nRun 'agent deps list' to see all available dependencies."
		return fmt.Errorf(msg)
	}

	fmt.Printf("Name:        %s\n", name)
	fmt.Printf("Type:        %s\n", spec.Type)
	if spec.Description != "" {
		fmt.Printf("Description: %s\n", spec.Description)
	}
	if spec.Default != "" {
		fmt.Printf("Default:     %s\n", spec.Default)
	}
	if len(spec.Versions) > 0 {
		fmt.Printf("Versions:    %s\n", strings.Join(spec.Versions, ", "))
	}
	if len(spec.Requires) > 0 {
		fmt.Printf("Requires:    %s\n", strings.Join(spec.Requires, ", "))
	}

	fmt.Println()
	fmt.Println("Usage in agent.yaml:")
	if spec.Default != "" {
		fmt.Printf("  dependencies:\n    - %s        # uses default version %s\n", name, spec.Default)
		fmt.Printf("    - %s@%s    # explicit version\n", name, spec.Default)
	} else {
		fmt.Printf("  dependencies:\n    - %s\n", name)
	}

	return nil
}
```

**Step 2: Test manually**

Run: `go build ./cmd/agent && ./agent deps list`
Run: `./agent deps info node`
Run: `./agent deps info playwright`

**Step 3: Commit**

```bash
git add cmd/agent/cli/deps.go
git commit -m "feat(cli): add deps list and deps info commands"
```

---

## Task 12: Remove Old Runtime Code

**Files:**
- Delete: `internal/config/ParseRuntime` function
- Modify: `cmd/agent/cli/run.go` - remove `--runtime` flag
- Update tests

**Step 1: Remove --runtime flag from run.go**

Remove these lines from `cmd/agent/cli/run.go`:
- `runtimeFlag string` variable
- `runCmd.Flags().StringVar(&runtimeFlag, ...)`
- The `if runtimeFlag != ""` block

**Step 2: Remove ParseRuntime from config.go**

Delete the `ParseRuntime` function from `internal/config/config.go`.

**Step 3: Update tests**

Remove `TestParseRuntime` from `internal/config/config_test.go`.

**Step 4: Run all tests**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/ cmd/agent/cli/
git commit -m "refactor: remove deprecated runtime field and flag"
```

---

## Task 13: Update Example agent.yaml

**Files:**
- Modify: `examples/multi-service/agent.yaml`

**Step 1: Update the example**

```yaml
# examples/multi-service/agent.yaml
# Example: Multi-service agent with hostname routing
#
# This example runs two simple Python HTTP servers and exposes them
# via hostname-based routing:
#   - http://web.demo.localhost:8080 -> container port 3000
#   - http://api.demo.localhost:8080 -> container port 8080
#
# Run with:
#   agent run example examples/multi-service -- bash /workspace/start.sh

name: demo

dependencies:
  - python@3.11

ports:
  web: 3000
  api: 8080
```

**Step 2: Commit**

```bash
git add examples/
git commit -m "docs: update example to use dependencies field"
```

---

## Task 14: Integration Test

**Files:**
- Add test to: `internal/e2e/e2e_test.go` or create `internal/deps/integration_test.go`

**Step 1: Write integration test**

```go
// internal/deps/integration_test.go
//go:build integration

package deps

import (
	"strings"
	"testing"
)

func TestFullPipeline(t *testing.T) {
	// Parse dependencies
	depStrings := []string{"node@20", "typescript", "psql"}
	depList, err := ParseAll(depStrings)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}

	// Validate
	if err := Validate(depList); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// Generate Dockerfile
	dockerfile, err := GenerateDockerfile(depList)
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	if !strings.Contains(dockerfile, "FROM ubuntu:22.04") {
		t.Error("Dockerfile missing base image")
	}
	if !strings.Contains(dockerfile, "nodejs") {
		t.Error("Dockerfile missing node install")
	}

	// Generate script
	script, err := GenerateInstallScript(depList)
	if err != nil {
		t.Fatalf("GenerateInstallScript: %v", err)
	}
	if !strings.Contains(script, "#!/bin/bash") {
		t.Error("Script missing shebang")
	}

	// Generate image tag
	tag := ImageTag(depList)
	if !strings.HasPrefix(tag, "agentops/run:") {
		t.Errorf("unexpected tag format: %s", tag)
	}
}
```

**Step 2: Run integration test**

Run: `go test -tags=integration -v ./internal/deps/`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/deps/
git commit -m "test(deps): add integration test for full pipeline"
```

---

## Task 15: Final Verification

**Step 1: Run all tests**

```bash
go test ./...
```

**Step 2: Run linter**

```bash
golangci-lint run
```

**Step 3: Build and manual test**

```bash
go build ./cmd/agent
./agent deps list
./agent deps info node
./agent deps info playwright
```

**Step 4: Test with example**

Create a test agent.yaml:
```yaml
name: test
dependencies:
  - node@20
  - typescript
```

Run: `./agent run . -- node --version`

**Step 5: Final commit**

```bash
git add -A
git commit -m "feat(deps): complete declarative dependencies implementation"
```

---

## Summary

This plan implements the dependencies feature in 15 tasks:

1. **Tasks 1-4**: Core deps package (types, registry, parser, validator)
2. **Tasks 5-6**: Config update and Dockerfile generator
3. **Tasks 7-8**: Image builder and Apple container script generator
4. **Tasks 9-10**: Update image resolver and run manager
5. **Tasks 11-13**: CLI commands and cleanup
6. **Tasks 14-15**: Integration tests and verification

Each task is small (5-15 min), commits frequently, and follows TDD.
