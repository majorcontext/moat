# AI Quickstart and Agent Context Injection — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add `moat <agent> quickstart` to auto-generate moat.yaml for existing projects, and inject runtime context into agent instruction files (CLAUDE.md, AGENTS.md, GEMINI.md).

**Architecture:** Two independent features sharing no code except both touching provider `PrepareContainer` methods. Feature 1 adds a `quickstart` subcommand under each agent command that runs the agent in a bootstrap container with an embedded prompt. Feature 2 adds an `internal/context/` package that renders runtime context to markdown, written by each provider into its staging directory.

**Tech Stack:** Go, Cobra CLI, `//go:embed`, `text/template`, `gopkg.in/yaml.v3`

---

## Feature 2: Agent Context Injection

_Feature 2 first because Feature 1's quickstart container will benefit from context injection._

### Task 1: Create `internal/context/` package — types and render

**Files:**
- Create: `internal/context/context.go`
- Create: `internal/context/context_test.go`

**Step 1: Write the failing test**

```go
// internal/context/context_test.go
package context

import (
	"strings"
	"testing"
)

func TestRender_minimal(t *testing.T) {
	rc := RuntimeContext{
		RunID:     "abc123",
		Agent:     "claude",
		Workspace: "/workspace",
	}

	out := Render(rc)

	if !strings.Contains(out, "# Moat Environment") {
		t.Error("missing header")
	}
	if !strings.Contains(out, "abc123") {
		t.Error("missing run ID")
	}
	if !strings.Contains(out, "/workspace") {
		t.Error("missing workspace path")
	}
	// No grants section when empty
	if strings.Contains(out, "## Grants") {
		t.Error("should not include empty Grants section")
	}
}

func TestRender_full(t *testing.T) {
	rc := RuntimeContext{
		RunID:     "run-456",
		Agent:     "codex",
		Workspace: "/workspace",
		Grants: []Grant{
			{Name: "github", Description: "GitHub access via `gh` CLI. Credentials are auto-injected at the network layer."},
			{Name: "anthropic", Description: "Anthropic API access via proxy."},
		},
		Services: []Service{
			{Name: "postgres", Version: "17", EnvURL: "MOAT_POSTGRES_URL"},
			{Name: "redis", Version: "7", EnvURL: "MOAT_REDIS_URL"},
		},
		NetworkPolicy: &NetworkPolicy{
			Policy:       "strict",
			AllowedHosts: []string{"api.github.com", "*.npmjs.org"},
		},
		MCPServers: []MCPServer{
			{Name: "github", Description: "GitHub tools (issues, PRs, search)"},
		},
		Ports: []Port{
			{Name: "api", ContainerPort: 8080, EnvHostPort: "MOAT_HOST_API"},
		},
	}

	out := Render(rc)

	checks := []string{
		"## Grants",
		"`github`",
		"`anthropic`",
		"## Services",
		"PostgreSQL 17",
		"`$MOAT_POSTGRES_URL`",
		"Redis 7",
		"## Network Policy",
		"strict",
		"api.github.com",
		"## MCP Servers",
		"`github`",
		"## Ports",
		"`api` (8080)",
		"## Run Metadata",
		"run-456",
		"codex",
	}

	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Errorf("missing %q in output:\n%s", check, out)
		}
	}
}

func TestRender_omitsEmptySections(t *testing.T) {
	rc := RuntimeContext{
		RunID:     "x",
		Agent:     "claude",
		Workspace: "/workspace",
		Grants: []Grant{
			{Name: "github", Description: "GitHub access."},
		},
	}

	out := Render(rc)

	absent := []string{"## Services", "## Network Policy", "## MCP Servers", "## Ports"}
	for _, s := range absent {
		if strings.Contains(out, s) {
			t.Errorf("should not contain %q when section is empty", s)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /workspace && go test ./internal/context/ -run TestRender -v`
Expected: FAIL — package doesn't exist yet.

**Step 3: Write the implementation**

```go
// internal/context/context.go
package context

import (
	"fmt"
	"strings"
)

// RuntimeContext holds all environment information for an agent running inside Moat.
type RuntimeContext struct {
	RunID         string
	Agent         string
	Workspace     string
	Grants        []Grant
	Services      []Service
	Ports         []Port
	NetworkPolicy *NetworkPolicy
	MCPServers    []MCPServer
}

// Grant describes a credential grant available to the agent.
type Grant struct {
	Name        string
	Description string
}

// Service describes a running service dependency.
type Service struct {
	Name    string
	Version string
	EnvURL  string // e.g. MOAT_POSTGRES_URL
}

// Port describes an exposed port.
type Port struct {
	Name          string
	ContainerPort int
	EnvHostPort   string // e.g. MOAT_HOST_API
}

// NetworkPolicy describes the container's network access rules.
type NetworkPolicy struct {
	Policy       string   // "permissive" or "strict"
	AllowedHosts []string // only relevant for strict
}

// MCPServer describes an available MCP server.
type MCPServer struct {
	Name        string
	Description string
}

// Render produces a markdown string describing the runtime context.
// Only non-empty sections are included.
func Render(rc RuntimeContext) string {
	var b strings.Builder

	b.WriteString("# Moat Environment\n\n")
	b.WriteString("You are running inside a Moat sandbox — an isolated container with\n")
	b.WriteString("credential injection and network controls.\n")

	// Workspace
	b.WriteString("\n## Workspace\n")
	fmt.Fprintf(&b, "- Path: %s\n", rc.Workspace)
	b.WriteString("- Mount: read-write\n")

	// Grants
	if len(rc.Grants) > 0 {
		b.WriteString("\n## Grants\n")
		for _, g := range rc.Grants {
			fmt.Fprintf(&b, "- `%s` — %s\n", g.Name, g.Description)
		}
	}

	// Services
	if len(rc.Services) > 0 {
		b.WriteString("\n## Services\n")
		for _, s := range rc.Services {
			fmt.Fprintf(&b, "- %s %s available at `$%s`\n", serviceDisplayName(s.Name), s.Version, s.EnvURL)
		}
	}

	// Network Policy
	if rc.NetworkPolicy != nil {
		b.WriteString("\n## Network Policy\n")
		fmt.Fprintf(&b, "- Policy: %s\n", rc.NetworkPolicy.Policy)
		if len(rc.NetworkPolicy.AllowedHosts) > 0 {
			fmt.Fprintf(&b, "- Allowed hosts: %s\n", strings.Join(rc.NetworkPolicy.AllowedHosts, ", "))
		}
	}

	// MCP Servers
	if len(rc.MCPServers) > 0 {
		b.WriteString("\n## MCP Servers\n")
		for _, m := range rc.MCPServers {
			fmt.Fprintf(&b, "- `%s` — %s\n", m.Name, m.Description)
		}
	}

	// Ports
	if len(rc.Ports) > 0 {
		b.WriteString("\n## Ports\n")
		for _, p := range rc.Ports {
			fmt.Fprintf(&b, "- `%s` (%d) — host port at `$%s`\n", p.Name, p.ContainerPort, p.EnvHostPort)
		}
	}

	// Run Metadata
	b.WriteString("\n## Run Metadata\n")
	fmt.Fprintf(&b, "- Run ID: %s\n", rc.RunID)
	fmt.Fprintf(&b, "- Agent: %s\n", rc.Agent)

	return b.String()
}

// serviceDisplayName returns a human-friendly name for a service.
func serviceDisplayName(name string) string {
	switch strings.ToLower(name) {
	case "postgres":
		return "PostgreSQL"
	case "mysql":
		return "MySQL"
	case "redis":
		return "Redis"
	default:
		return strings.Title(name)
	}
}
```

**Step 4: Run tests to verify they pass**

Run: `cd /workspace && go test ./internal/context/ -run TestRender -v`
Expected: PASS (all three tests)

**Step 5: Commit**

```
feat(context): add runtime context rendering for agent instruction files
```

---

### Task 2: Build RuntimeContext from run config

**Files:**
- Modify: `internal/context/context.go`
- Create: `internal/context/build.go`
- Create: `internal/context/build_test.go`

**Step 1: Write the failing test**

```go
// internal/context/build_test.go
package context

import (
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestBuildFromConfig(t *testing.T) {
	cfg := &config.Config{
		Name:   "test-agent",
		Agent:  "claude",
		Grants: []string{"github", "anthropic"},
		Dependencies: []string{"postgres@17", "redis@7"},
		Network: config.NetworkConfig{
			Policy: "strict",
			Allow:  []string{"api.github.com"},
		},
		Ports: map[string]int{
			"api": 8080,
		},
		MCP: []config.MCPServerConfig{
			{Name: "github", URL: "https://github.mcp.example.com"},
		},
	}

	rc := BuildFromConfig(cfg, "run-789")

	if rc.RunID != "run-789" {
		t.Errorf("RunID = %q, want %q", rc.RunID, "run-789")
	}
	if rc.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", rc.Agent, "claude")
	}
	if len(rc.Grants) != 2 {
		t.Errorf("got %d grants, want 2", len(rc.Grants))
	}
	if rc.Grants[0].Name != "github" {
		t.Errorf("first grant = %q, want %q", rc.Grants[0].Name, "github")
	}
	if len(rc.Services) != 2 {
		t.Errorf("got %d services, want 2", len(rc.Services))
	}
	if rc.NetworkPolicy == nil || rc.NetworkPolicy.Policy != "strict" {
		t.Error("missing or incorrect network policy")
	}
	if len(rc.Ports) != 1 {
		t.Errorf("got %d ports, want 1", len(rc.Ports))
	}
	if len(rc.MCPServers) != 1 {
		t.Errorf("got %d MCP servers, want 1", len(rc.MCPServers))
	}
}

func TestBuildFromConfig_noOptionalSections(t *testing.T) {
	cfg := &config.Config{
		Agent: "codex",
	}

	rc := BuildFromConfig(cfg, "run-x")

	if len(rc.Grants) != 0 {
		t.Error("expected no grants")
	}
	if len(rc.Services) != 0 {
		t.Error("expected no services")
	}
	if rc.NetworkPolicy != nil {
		t.Error("expected nil network policy")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /workspace && go test ./internal/context/ -run TestBuildFromConfig -v`
Expected: FAIL — `BuildFromConfig` not defined.

**Step 3: Write the implementation**

```go
// internal/context/build.go
package context

import (
	"fmt"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/deps"
)

// grantDescriptions maps grant names to human-readable descriptions.
var grantDescriptions = map[string]string{
	"github":    "GitHub access via `gh` CLI. Credentials are auto-injected at the network layer.",
	"anthropic": "Anthropic API access via proxy.",
	"openai":    "OpenAI API access via proxy.",
	"gemini":    "Google Gemini API access via proxy.",
	"aws":       "AWS credentials via IAM role assumption.",
	"telegram":  "Telegram Bot API access.",
}

// BuildFromConfig constructs a RuntimeContext from a moat config and run ID.
func BuildFromConfig(cfg *config.Config, runID string) RuntimeContext {
	rc := RuntimeContext{
		RunID:     runID,
		Agent:     cfg.Agent,
		Workspace: "/workspace",
	}

	// Grants
	for _, g := range cfg.Grants {
		desc, ok := grantDescriptions[g]
		if !ok {
			desc = fmt.Sprintf("%s access via proxy.", strings.Title(g))
		}
		rc.Grants = append(rc.Grants, Grant{Name: g, Description: desc})
	}

	// Services — extracted from dependencies
	for _, d := range cfg.Dependencies {
		dep, err := deps.Parse(d)
		if err != nil {
			continue
		}
		spec, ok := deps.GetSpec(dep.Name)
		if !ok {
			continue
		}
		if spec.Type != deps.TypeService {
			continue
		}
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		envURL := fmt.Sprintf("MOAT_%s_URL", strings.ToUpper(dep.Name))
		rc.Services = append(rc.Services, Service{
			Name:    dep.Name,
			Version: version,
			EnvURL:  envURL,
		})
	}

	// Network policy
	if cfg.Network.Policy != "" {
		rc.NetworkPolicy = &NetworkPolicy{
			Policy:       cfg.Network.Policy,
			AllowedHosts: cfg.Network.Allow,
		}
	}

	// Ports
	for name, port := range cfg.Ports {
		rc.Ports = append(rc.Ports, Port{
			Name:          name,
			ContainerPort: port,
			EnvHostPort:   fmt.Sprintf("MOAT_HOST_%s", strings.ToUpper(name)),
		})
	}

	// MCP servers (remote)
	for _, mcp := range cfg.MCP {
		rc.MCPServers = append(rc.MCPServers, MCPServer{
			Name:        mcp.Name,
			Description: fmt.Sprintf("Remote MCP server at %s", mcp.URL),
		})
	}

	return rc
}
```

**Step 4: Run tests to verify they pass**

Run: `cd /workspace && go test ./internal/context/ -v`
Expected: PASS (all tests)

**Step 5: Commit**

```
feat(context): build RuntimeContext from moat config
```

---

### Task 3: Inject context into Claude provider

**Files:**
- Modify: `internal/providers/claude/agent.go`
- Modify: `internal/deps/scripts/moat-init.sh`
- Create: `internal/providers/claude/context_test.go`

**Step 1: Write the failing test**

```go
// internal/providers/claude/context_test.go
package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteContextFile(t *testing.T) {
	tmpDir := t.TempDir()
	content := "# Moat Environment\n\nTest context content.\n"

	err := writeContextFile(tmpDir, content)
	if err != nil {
		t.Fatalf("writeContextFile() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}

	if string(data) != content {
		t.Errorf("content = %q, want %q", string(data), content)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /workspace && go test ./internal/providers/claude/ -run TestWriteContextFile -v`
Expected: FAIL — `writeContextFile` not defined.

**Step 3: Write the implementation**

Add to `internal/providers/claude/agent.go`:

```go
// writeContextFile writes the Moat runtime context as CLAUDE.md to the staging dir.
func writeContextFile(stagingDir, content string) error {
	return os.WriteFile(filepath.Join(stagingDir, "CLAUDE.md"), []byte(content), 0644)
}
```

Then modify the `PrepareContainer` method to call it. After `WriteClaudeConfig(...)`, add:

```go
	// Write runtime context as CLAUDE.md for agent awareness
	if opts.RuntimeContext != "" {
		if err := writeContextFile(tmpDir, opts.RuntimeContext); err != nil {
			return nil, fmt.Errorf("writing context file: %w", err)
		}
	}
```

Add `RuntimeContext string` field to `provider.PrepareOpts`:

```go
// In internal/provider/interfaces.go
type PrepareOpts struct {
	Credential    *provider.Credential
	ContainerHome string
	MCPServers    map[string]MCPServerConfig
	HostConfig    map[string]interface{}
	RuntimeContext string // Rendered markdown context for agent instruction file
}
```

Update `moat-init.sh` — in the Claude section, after existing file copies:

```bash
  # Copy CLAUDE.md if present (runtime context for agent awareness)
  [ -f "$MOAT_CLAUDE_INIT/CLAUDE.md" ] && \
    cp -p "$MOAT_CLAUDE_INIT/CLAUDE.md" "$TARGET_HOME/.claude/"
```

**Step 4: Run tests to verify they pass**

Run: `cd /workspace && go test ./internal/providers/claude/ -run TestWriteContextFile -v`
Expected: PASS

**Step 5: Commit**

```
feat(claude): write CLAUDE.md runtime context to container
```

---

### Task 4: Inject context into Codex and Gemini providers

**Files:**
- Modify: `internal/providers/codex/agent.go`
- Modify: `internal/providers/gemini/agent.go`
- Modify: `internal/deps/scripts/moat-init.sh`

**Step 1: Write tests for both providers**

Add similar `writeContextFile` functions and tests for Codex (`AGENTS.md`) and Gemini (`GEMINI.md`), following the same pattern as Task 3.

**Step 2: Implement for Codex**

In `internal/providers/codex/agent.go`, add `writeContextFile` writing `AGENTS.md` to staging dir. Add the call in `PrepareContainer` after `WriteCodexConfig(tmpDir)`.

Update `moat-init.sh` Codex section:

```bash
  # Copy AGENTS.md if present (runtime context for agent awareness)
  [ -f "$MOAT_CODEX_INIT/AGENTS.md" ] && \
    cp -p "$MOAT_CODEX_INIT/AGENTS.md" "$TARGET_HOME/.codex/"
```

**Step 3: Implement for Gemini**

In `internal/providers/gemini/agent.go`, add `writeContextFile` writing `GEMINI.md` to staging dir. Add the call in `PrepareContainer`.

Update `moat-init.sh` Gemini section:

```bash
  # Copy GEMINI.md if present (runtime context for agent awareness)
  [ -f "$MOAT_GEMINI_INIT/GEMINI.md" ] && \
    cp -p "$MOAT_GEMINI_INIT/GEMINI.md" "$TARGET_HOME/.gemini/"
```

**Step 4: Run all provider tests**

Run: `cd /workspace && go test ./internal/providers/... -v`
Expected: PASS

**Step 5: Commit**

```
feat(codex,gemini): write agent instruction files with runtime context
```

---

### Task 5: Wire context building into run manager

**Files:**
- Modify: `internal/run/manager.go`

**Step 1: Find where `PrepareContainer` is called in the run manager**

In `manager.go`, the `Create` method calls each provider's `PrepareContainer`. Before that call, build the `RuntimeContext` and render it. Pass the rendered markdown via `opts.RuntimeContext`.

```go
import moatctx "github.com/majorcontext/moat/internal/context"

// Inside Create(), before calling prov.PrepareContainer:
rc := moatctx.BuildFromConfig(opts.Config, m.id)
renderedContext := moatctx.Render(rc)

// Then pass it in PrepareOpts:
prepOpts := provider.PrepareOpts{
	// ... existing fields ...
	RuntimeContext: renderedContext,
}
```

**Step 2: Add MOAT_* environment variables**

In the same `Create` method, where other env vars are set, add:

```go
// Runtime context environment variables
if opts.Config != nil {
	proxyEnv = append(proxyEnv, "MOAT_RUN_ID="+m.id)
	proxyEnv = append(proxyEnv, "MOAT_AGENT="+opts.Config.Agent)
	if len(opts.Config.Grants) > 0 {
		proxyEnv = append(proxyEnv, "MOAT_GRANTS="+strings.Join(opts.Config.Grants, ","))
	}
	if opts.Config.Network.Policy != "" {
		proxyEnv = append(proxyEnv, "MOAT_NETWORK_POLICY="+opts.Config.Network.Policy)
	}
	proxyEnv = append(proxyEnv, "MOAT_WORKSPACE=/workspace")
}
```

**Step 3: Run unit tests**

Run: `cd /workspace && make test-unit`
Expected: PASS

**Step 4: Commit**

```
feat(run): wire runtime context into container creation
```

---

### Task 6: Add MOAT_* env vars to documentation

**Files:**
- Modify: `docs/content/reference/02-moat-yaml.md` or relevant docs page

Add a section documenting the `MOAT_*` environment variables that are automatically set inside containers:

- `MOAT_RUN_ID` — unique run identifier
- `MOAT_AGENT` — agent type (claude, codex, gemini)
- `MOAT_GRANTS` — comma-separated list of active grants
- `MOAT_NETWORK_POLICY` — network policy (permissive or strict)
- `MOAT_WORKSPACE` — workspace mount path

Also document that agents receive runtime context via their native instruction files (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, `~/.gemini/GEMINI.md`).

**Commit:**

```
docs: document MOAT_* env vars and agent context injection
```

---

## Feature 1: `moat <agent> quickstart`

### Task 7: Generate schema reference from Go types

**Files:**
- Create: `internal/quickstart/schema.go`
- Create: `internal/quickstart/schema_test.go`

**Step 1: Write the failing test**

```go
// internal/quickstart/schema_test.go
package quickstart

import (
	"strings"
	"testing"
)

func TestGenerateSchemaReference(t *testing.T) {
	ref := GenerateSchemaReference()

	// Must contain key fields from Config struct
	checks := []string{
		"name",
		"agent",
		"dependencies",
		"grants",
		"env",
		"mounts",
		"ports",
		"network",
		"command",
		"hooks",
		"interactive",
		"volumes",
		"container",
		"mcp",
		"claude",
		"codex",
		"gemini",
	}

	for _, field := range checks {
		if !strings.Contains(ref, field) {
			t.Errorf("schema reference missing field %q", field)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /workspace && go test ./internal/quickstart/ -run TestGenerateSchemaReference -v`
Expected: FAIL — package doesn't exist.

**Step 3: Write the implementation**

Use `reflect` to walk the `config.Config` struct and generate a markdown reference. Extract YAML tag names, types, and nesting:

```go
// internal/quickstart/schema.go
package quickstart

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/majorcontext/moat/internal/config"
)

// GenerateSchemaReference produces a markdown reference of all moat.yaml fields
// by reflecting on the config.Config struct.
func GenerateSchemaReference() string {
	var b strings.Builder
	b.WriteString("## moat.yaml Schema Reference\n\n")
	writeStructFields(&b, reflect.TypeOf(config.Config{}), "")
	return b.String()
}

func writeStructFields(b *strings.Builder, t reflect.Type, prefix string) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Skip unexported or deprecated fields
		if !field.IsExported() {
			continue
		}
		yamlTag := field.Tag.Get("yaml")
		if yamlTag == "" || yamlTag == "-" {
			continue
		}

		// Parse yaml tag name
		name := strings.Split(yamlTag, ",")[0]
		if name == "" {
			continue
		}

		fullName := name
		if prefix != "" {
			fullName = prefix + "." + name
		}

		fieldType := field.Type
		// Unwrap pointer
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}

		typeName := friendlyTypeName(fieldType)
		fmt.Fprintf(b, "- `%s` (%s)\n", fullName, typeName)

		// Recurse into structs (but not maps/slices of structs)
		if fieldType.Kind() == reflect.Struct && !isStdlibType(fieldType) {
			writeStructFields(b, fieldType, fullName)
		}
	}
}

func friendlyTypeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int64:
		return "int"
	case reflect.Slice:
		elem := t.Elem()
		if elem.Kind() == reflect.String {
			return "[]string"
		}
		return "[]" + friendlyTypeName(elem)
	case reflect.Map:
		return fmt.Sprintf("map[%s]%s", friendlyTypeName(t.Key()), friendlyTypeName(t.Elem()))
	case reflect.Struct:
		return "object"
	default:
		return t.Kind().String()
	}
}

func isStdlibType(t reflect.Type) bool {
	pkg := t.PkgPath()
	return pkg != "" && !strings.Contains(pkg, "majorcontext/moat")
}
```

**Step 4: Run tests**

Run: `cd /workspace && go test ./internal/quickstart/ -run TestGenerateSchemaReference -v`
Expected: PASS

**Step 5: Commit**

```
feat(quickstart): generate schema reference from config struct
```

---

### Task 8: Generate dependency reference from registry

**Files:**
- Modify: `internal/quickstart/schema.go` (or create `internal/quickstart/deps.go`)
- Modify: `internal/quickstart/schema_test.go`

**Step 1: Write the failing test**

```go
func TestGenerateDepsReference(t *testing.T) {
	ref := GenerateDepsReference()

	// Must contain key deps from registry
	checks := []string{
		"node",
		"python",
		"go",
		"postgres",
		"redis",
		"claude-code",
		"npm:",
		"pip:",
	}

	for _, dep := range checks {
		if !strings.Contains(ref, dep) {
			t.Errorf("deps reference missing %q", dep)
		}
	}
}
```

**Step 2: Implement**

```go
// internal/quickstart/deps.go
package quickstart

import (
	"fmt"
	"strings"

	"github.com/majorcontext/moat/internal/deps"
)

// GenerateDepsReference produces a markdown reference of all available dependencies.
func GenerateDepsReference() string {
	var b strings.Builder
	b.WriteString("## Available Dependencies\n\n")

	names := deps.List()
	for _, name := range names {
		spec, _ := deps.GetSpec(name)
		line := fmt.Sprintf("- `%s`", name)
		if spec.Default != "" {
			line += fmt.Sprintf(" (default: %s)", spec.Default)
		}
		if len(spec.Versions) > 0 {
			line += fmt.Sprintf(" [versions: %s]", strings.Join(spec.Versions, ", "))
		}
		line += fmt.Sprintf(" — %s", spec.Description)
		b.WriteString(line + "\n")
	}

	b.WriteString("\n## Dynamic Dependencies\n\n")
	b.WriteString("For packages not in the registry, use prefix syntax:\n")
	b.WriteString("- `npm:<package>` — Install npm package globally (requires node)\n")
	b.WriteString("- `pip:<package>` — Install pip package (requires python)\n")
	b.WriteString("- `uv:<package>` — Install uv tool (requires uv)\n")
	b.WriteString("- `cargo:<package>` — Install cargo crate (requires rust)\n")
	b.WriteString("- `go:<package>` — Install Go binary (requires go)\n")
	b.WriteString("\nFor system packages not available through any prefix, use lifecycle hooks:\n")
	b.WriteString("```yaml\nhooks:\n  post_build_root: \"apt-get update && apt-get install -y <package>\"\n```\n")

	return b.String()
}
```

**Step 3: Run tests**

Run: `cd /workspace && go test ./internal/quickstart/ -v`
Expected: PASS

**Step 4: Commit**

```
feat(quickstart): generate dependency reference from registry
```

---

### Task 9: Create the quickstart prompt template

**Files:**
- Create: `internal/quickstart/prompt.go`
- Create: `internal/quickstart/prompt_test.go`

**Step 1: Write the failing test**

```go
// internal/quickstart/prompt_test.go
package quickstart

import (
	"strings"
	"testing"
)

func TestBuildPrompt(t *testing.T) {
	prompt := BuildPrompt()

	// Must contain all three sections
	checks := []string{
		"moat.yaml Schema Reference",
		"Available Dependencies",
		"Dynamic Dependencies",
		"Analyze the project",
		"Output only valid YAML",
	}

	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("prompt missing %q", check)
		}
	}

	// Must not be unreasonably large (prompt should be under ~10KB)
	if len(prompt) > 20000 {
		t.Errorf("prompt is %d bytes, expected under 20000", len(prompt))
	}
}
```

**Step 2: Implement**

```go
// internal/quickstart/prompt.go
package quickstart

import "strings"

// BuildPrompt assembles the full quickstart prompt from schema, deps, and instructions.
func BuildPrompt() string {
	var b strings.Builder

	b.WriteString("You are a Moat configuration expert. Moat runs AI agents in isolated containers.\n\n")
	b.WriteString("Your task: analyze the project at /workspace and generate a moat.yaml configuration file.\n\n")

	b.WriteString(GenerateSchemaReference())
	b.WriteString("\n")
	b.WriteString(GenerateDepsReference())
	b.WriteString("\n")
	b.WriteString(examples())
	b.WriteString("\n")
	b.WriteString(instructions())

	return b.String()
}

func examples() string {
	return `## Examples

### Node.js web app with PostgreSQL
` + "```yaml" + `
name: my-app
dependencies: [node@20, postgres@17, psql, git]
grants: [github]
hooks:
  pre_run: "npm install"
ports:
  web: 3000
` + "```" + `

### Python ML project
` + "```yaml" + `
name: ml-project
dependencies: [python@3.11, uv, git]
grants: [github]
hooks:
  pre_run: "uv sync"
` + "```" + `

### Go service with Redis
` + "```yaml" + `
name: api-service
dependencies: [go@1.25, redis@7, git]
grants: [github]
ports:
  api: 8080
` + "```" + `
`
}

func instructions() string {
	return `## Instructions

1. Read manifest files: package.json, go.mod, pyproject.toml, Gemfile, requirements.txt, Cargo.toml, etc.
2. Check for docker-compose.yml or docker-compose.yaml for service hints (databases, caches).
3. Check .env.example, .env.sample, or README for environment variable and credential hints.
4. Detect which runtime and version the project needs.
5. Detect database or cache dependencies (postgres, mysql, redis).
6. Only include grants if there is evidence the project uses that service (e.g. GitHub API calls, AWS SDK).
7. Keep the config minimal — only include what the project actually needs.
8. Use pre_run hooks for dependency installation (npm install, pip install, etc.).
9. Use post_build_root hooks only for system packages not available as dependencies.

Output only valid YAML, nothing else. No markdown fences, no explanation.
`
}
```

**Step 3: Run tests**

Run: `cd /workspace && go test ./internal/quickstart/ -v`
Expected: PASS

**Step 4: Commit**

```
feat(quickstart): assemble quickstart prompt from schema + deps + examples
```

---

### Task 10: Add `quickstart` subcommand to Claude CLI

**Files:**
- Modify: `internal/providers/claude/cli.go`

**Step 1: Add the subcommand**

Register a `quickstart` subcommand under `claudeCmd`:

```go
quickstartCmd := &cobra.Command{
	Use:   "quickstart [workspace]",
	Short: "Auto-generate moat.yaml for an existing project",
	Long: `Analyze the project and generate a moat.yaml configuration file.

Runs Claude Code in a bootstrap container to analyze your project's
manifest files, source code, and README, then generates an appropriate
moat.yaml configuration.

Requires an Anthropic credential (run 'moat grant anthropic' first).

Examples:
  moat claude quickstart
  moat claude quickstart /path/to/project`,
	Args: cobra.MaximumNArgs(1),
	RunE: runClaudeQuickstart,
}
claudeCmd.AddCommand(quickstartCmd)
```

The `runClaudeQuickstart` function:

```go
func runClaudeQuickstart(cmd *cobra.Command, args []string) error {
	workspace := "."
	if len(args) > 0 {
		workspace = args[0]
	}

	absPath, err := cli.ResolveWorkspacePath(workspace)
	if err != nil {
		return err
	}

	// Check if moat.yaml already exists
	if _, err := os.Stat(filepath.Join(absPath, "moat.yaml")); err == nil {
		return fmt.Errorf("moat.yaml already exists in %s", absPath)
	}

	prompt := quickstart.BuildPrompt()

	return cli.RunProvider(cmd, args, cli.ProviderRunConfig{
		Name:                  "claude",
		Flags:                 &claudeFlags,
		PromptFlag:            prompt,
		GetCredentialGrant:    getClaudeCredentialName,
		Dependencies:          []string{"node@20", "git", "claude-code"},
		NetworkHosts:          []string{"claude.ai", "*.claude.ai"},
		SupportsInitialPrompt: false,
		BuildCommand: func(promptFlag, initialPrompt string) ([]string, error) {
			return []string{
				"claude",
				"--dangerously-skip-permissions",
				"-p", promptFlag,
				"--output-format", "text",
			}, nil
		},
	})
}
```

_Note: The exact output capture mechanism (how to get the YAML from stdout into a file) will need refinement. The agent writes to stdout; we may need to capture container stdout and write `moat.yaml` after the container exits, or instruct the agent to write the file directly to `/workspace/moat.yaml`._

**Step 2: Run compilation check**

Run: `cd /workspace && go build ./...`
Expected: PASS

**Step 3: Commit**

```
feat(claude): add quickstart subcommand for auto-generating moat.yaml
```

---

### Task 11: Add `quickstart` to Codex and Gemini CLIs

**Files:**
- Modify: `internal/providers/codex/cli.go`
- Modify: `internal/providers/gemini/cli.go` (or `agent.go` — wherever `RegisterCLI` lives)

Follow the same pattern as Task 10, adapting for each provider:
- Codex: uses `codex-cli`, `openai` grant, different build command
- Gemini: uses `gemini-cli`, `gemini` grant, different build command

**Commit:**

```
feat(codex,gemini): add quickstart subcommand
```

---

### Task 12: Run lint and full test suite

**Step 1: Run linter**

Run: `cd /workspace && make lint`
Fix any issues.

**Step 2: Run full test suite**

Run: `cd /workspace && make test-unit`
Expected: PASS

**Step 3: Commit any lint fixes**

```
style: fix lint issues from quickstart and context features
```

---

## Summary

| Task | Feature | Description |
|------|---------|-------------|
| 1 | Context | Create `internal/context/` with types and `Render()` |
| 2 | Context | Add `BuildFromConfig()` to construct context from config |
| 3 | Context | Inject context into Claude provider (`CLAUDE.md`) |
| 4 | Context | Inject context into Codex (`AGENTS.md`) and Gemini (`GEMINI.md`) |
| 5 | Context | Wire context building into run manager |
| 6 | Context | Document MOAT_* env vars |
| 7 | Quickstart | Generate schema reference from Go types |
| 8 | Quickstart | Generate dependency reference from registry |
| 9 | Quickstart | Create prompt template |
| 10 | Quickstart | Add `quickstart` subcommand to Claude CLI |
| 11 | Quickstart | Add `quickstart` to Codex and Gemini CLIs |
| 12 | Both | Lint and full test suite |

## Follow-up Work

- `apt:` dynamic dependency prefix
- Verify Gemini CLI instruction file path (`~/.gemini/GEMINI.md`)
- Output capture: refine how quickstart captures agent YAML output and writes `moat.yaml`
- Interactive quickstart: let the agent ask questions before generating config
