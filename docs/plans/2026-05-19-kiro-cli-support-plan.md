# Kiro CLI Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `kiro-cli` as a first-class MOAT agent provider at full parity with the Codex provider (grant, proxy injection, container config staging, local + remote MCP, runtime context, `moat kiro` command).

**Architecture:** A new `internal/providers/kiro` package implementing `provider.CredentialProvider` + `provider.AgentProvider`, mirroring `internal/providers/codex`. Standard wiring: deps registry/install, credential constant, config section, imageneeds detection, run-manager dispatch block, auto-registered CLI command. Auth uses the codex pattern — a placeholder `KIRO_API_KEY` env in the container while the proxy injects the real Bearer token on `q.*.amazonaws.com`.

**Tech Stack:** Go, Cobra, the MOAT provider/credential/deps/config/run packages.

**Spec:** `docs/plans/2026-05-19-kiro-cli-support-design.md`. Read it before starting.

**Working directory:** worktree `/workspace/.claude/worktrees/kiro-cli-support`, branch `worktree-kiro-cli-support`. Run all commands from there.

**Conventions:**
- TDD: failing test first, then minimal code.
- Run `go build ./...` after each task; `make lint` (fallback `go vet ./...`) before each commit.
- Commit messages: Conventional Commits, no `Co-Authored-By`.
- Mirror codex naming exactly so reviewers can diff the two packages.

---

### Task 1: Register `kiro-cli` dependency

**Files:**
- Modify: `internal/deps/registry.yaml` (add `kiro-cli` entry near `gemini-cli`)
- Modify: `internal/deps/install.go` (add `case "kiro-cli":` in `getCustomCommands`)
- Test: `internal/deps/install_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/deps/install_test.go`:

```go
func TestGetCustomCommandsKiroCLI(t *testing.T) {
	cmds := getCustomCommands("kiro-cli", "")
	if len(cmds.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d: %v", len(cmds.Commands), cmds.Commands)
	}
	want := "curl -fsSL https://cli.kiro.dev/install | bash -s -- --force"
	if cmds.Commands[0] != want {
		t.Errorf("command = %q, want %q", cmds.Commands[0], want)
	}
	if got := cmds.EnvVars["PATH"]; got != "/home/moatuser/.local/bin:$PATH" {
		t.Errorf("PATH = %q, want %q", got, "/home/moatuser/.local/bin:$PATH")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deps/ -run TestGetCustomCommandsKiroCLI -v`
Expected: FAIL (kiro-cli falls through to the default `InstallCommands{}`, length 0).

- [ ] **Step 3: Add the install case**

In `internal/deps/install.go`, inside `getCustomCommands`, immediately after the `case "claude-code":` block (ends with its closing `}` before `case "protoc":`), add:

```go
	case "kiro-cli":
		// Native installer from cli.kiro.dev; binary lands in ~/.local/bin.
		return InstallCommands{
			Commands: []string{
				`curl -fsSL https://cli.kiro.dev/install | bash -s -- --force`,
			},
			EnvVars: map[string]string{
				"PATH": "/home/moatuser/.local/bin:$PATH",
			},
		}
```

- [ ] **Step 4: Add the registry entry**

In `internal/deps/registry.yaml`, after the `gemini-cli:` block (the 4-line block ending with `requires: [node]`) and before `graphite-cli:`, add:

```yaml
kiro-cli:
  description: Kiro CLI
  type: custom
  user-install: true
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/deps/ -run TestGetCustomCommandsKiroCLI -v && go test ./internal/deps/`
Expected: PASS (all deps tests).

- [ ] **Step 6: Commit**

```bash
cd /workspace/.claude/worktrees/kiro-cli-support
go vet ./internal/deps/
git add internal/deps/registry.yaml internal/deps/install.go internal/deps/install_test.go
git commit -m "feat(deps): register kiro-cli dependency"
```

---

### Task 2: Add `ProviderKiro` credential constant

**Files:**
- Modify: `internal/credential/types.go` (constant + `KnownProviders` + `IsKnownProvider`)
- Test: `internal/credential/types_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/credential/types_test.go`:

```go
func TestProviderKiroIsKnown(t *testing.T) {
	if ProviderKiro != "kiro" {
		t.Errorf("ProviderKiro = %q, want %q", ProviderKiro, "kiro")
	}
	if !IsKnownProvider(ProviderKiro) {
		t.Error("IsKnownProvider(ProviderKiro) = false, want true")
	}
	found := false
	for _, p := range KnownProviders() {
		if p == ProviderKiro {
			found = true
		}
	}
	if !found {
		t.Error("KnownProviders() does not include ProviderKiro")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/credential/ -run TestProviderKiroIsKnown -v`
Expected: FAIL (undefined: ProviderKiro — compile error).

- [ ] **Step 3: Add the constant and register it**

In `internal/credential/types.go`:

1. After the `ProviderMeta Provider = "meta"` line, add:
   ```go
	ProviderKiro      Provider = "kiro"
   ```
2. In `KnownProviders`, change the `base` slice to append `ProviderKiro`:
   ```go
	base := []Provider{ProviderGitHub, ProviderAWS, ProviderAnthropic, ProviderClaude, ProviderOpenAI, ProviderGemini, ProviderNpm, ProviderGraphite, ProviderMeta, ProviderKiro}
   ```
3. In `IsKnownProvider`, add `ProviderKiro` to the `case` list:
   ```go
	case ProviderGitHub, ProviderAWS, ProviderAnthropic, ProviderClaude, ProviderOpenAI, ProviderGemini, ProviderNpm, ProviderGraphite, ProviderMeta, ProviderKiro:
   ```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/credential/ -run TestProviderKiroIsKnown -v && go test ./internal/credential/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
go vet ./internal/credential/
git add internal/credential/types.go internal/credential/types_test.go
git commit -m "feat(credential): add ProviderKiro constant"
```

---

### Task 3: Kiro provider package — constants + credential provider

**Files:**
- Create: `internal/providers/kiro/constants.go`
- Create: `internal/providers/kiro/provider.go`
- Create: `internal/providers/kiro/doc.go`
- Test: `internal/providers/kiro/provider_test.go`

- [ ] **Step 1: Create the package doc and constants**

Create `internal/providers/kiro/doc.go`:

```go
// Package kiro implements the Kiro CLI agent provider for Moat.
//
// It mirrors the codex provider: a placeholder KIRO_API_KEY is set in the
// container while the Moat proxy injects the real Bearer token on the Kiro
// API hosts. Container config is staged and copied to ~/.kiro by moat-init.
package kiro
```

Create `internal/providers/kiro/constants.go`:

```go
package kiro

// KiroAPIKeyPlaceholder is a syntactically plausible placeholder API key.
// kiro-cli runs in API-key mode when KIRO_API_KEY is set and sends this
// value as a Bearer token; the Moat proxy replaces it with the real token
// at the network layer. The real token never enters the container.
const KiroAPIKeyPlaceholder = "kiro-moat-proxy-injected-placeholder-000000000000000000000000000000"

// KiroInitMountPath is where the staging directory is mounted in containers.
const KiroInitMountPath = "/moat/kiro-init"

// kiroAPIHosts are the hosts the proxy injects the Kiro Bearer token for.
//
// VERIFICATION POINT (spec §Verification 3): if gatekeeper v0.2.0 does not
// match wildcard host patterns for credential injection, replace this slice
// with the single concrete host "q.us-east-1.amazonaws.com" and document how
// to add other regions. Confirm during implementation (see Task 10 Step "verify").
var kiroAPIHosts = []string{
	"q.*.amazonaws.com",
	"*.q.*.amazonaws.com",
}

// kiroPassthroughHosts are allowlisted but receive no credential injection.
var kiroPassthroughHosts = []string{
	"cognito-identity.*.amazonaws.com",
}
```

- [ ] **Step 2: Write the failing provider test**

Create `internal/providers/kiro/provider_test.go`:

```go
package kiro

import (
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

type mockProxyConfigurer struct {
	headers map[string]map[string]string
}

func newMockProxy() *mockProxyConfigurer {
	return &mockProxyConfigurer{headers: make(map[string]map[string]string)}
}

func (m *mockProxyConfigurer) SetCredential(host, value string) {}
func (m *mockProxyConfigurer) SetCredentialHeader(host, h, v string) {
	if m.headers[host] == nil {
		m.headers[host] = map[string]string{}
	}
	m.headers[host][h] = v
}
func (m *mockProxyConfigurer) SetCredentialWithGrant(host, h, v, g string) {
	if m.headers[host] == nil {
		m.headers[host] = map[string]string{}
	}
	m.headers[host][h] = v
}
func (m *mockProxyConfigurer) AddExtraHeader(host, h, v string)            {}
func (m *mockProxyConfigurer) AddResponseTransformer(host string, _ provider.ResponseTransformer) {}
func (m *mockProxyConfigurer) RemoveRequestHeader(host, h string)          {}
func (m *mockProxyConfigurer) SetTokenSubstitution(host, p, r string)      {}

func TestProviderName(t *testing.T) {
	if (&Provider{}).Name() != "kiro" {
		t.Errorf("Name() = %q, want kiro", (&Provider{}).Name())
	}
}

func TestConfigureProxyInjectsBearerOnKiroHosts(t *testing.T) {
	m := newMockProxy()
	(&Provider{}).ConfigureProxy(m, &provider.Credential{Token: "real-token"})
	for _, host := range kiroAPIHosts {
		got := m.headers[host]["Authorization"]
		if got != "Bearer real-token" {
			t.Errorf("host %s Authorization = %q, want %q", host, got, "Bearer real-token")
		}
	}
	for _, host := range kiroPassthroughHosts {
		if _, ok := m.headers[host]; ok {
			t.Errorf("passthrough host %s should not receive credential injection", host)
		}
	}
}

func TestContainerEnvSetsPlaceholder(t *testing.T) {
	env := (&Provider{}).ContainerEnv(&provider.Credential{Token: "real"})
	want := "KIRO_API_KEY=" + KiroAPIKeyPlaceholder
	if len(env) != 1 || env[0] != want {
		t.Errorf("ContainerEnv() = %v, want [%q]", env, want)
	}
}

func TestInterfaceCompliance(t *testing.T) {
	var _ provider.CredentialProvider = (*Provider)(nil)
	var _ provider.AgentProvider = (*Provider)(nil)
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/providers/kiro/ -v`
Expected: FAIL (undefined: Provider — compile error).

- [ ] **Step 4: Create the credential provider**

Create `internal/providers/kiro/provider.go`:

```go
package kiro

import (
	"context"

	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider and provider.AgentProvider
// for the Kiro CLI.
type Provider struct{}

var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.AgentProvider      = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "kiro" }

// Grant acquires a Kiro token interactively or from environment.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	return NewGrant().Execute(ctx)
}

// ConfigureProxy injects the real Kiro Bearer token on the Kiro API hosts.
// Passthrough hosts receive no injection (they are only allowlisted).
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	for _, host := range kiroAPIHosts {
		proxy.SetCredentialWithGrant(host, "Authorization", "Bearer "+cred.Token, "kiro")
	}
}

// ContainerEnv sets a placeholder KIRO_API_KEY. kiro-cli runs in API-key
// mode and sends the placeholder; the proxy swaps in the real token.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	return []string{"KIRO_API_KEY=" + KiroAPIKeyPlaceholder}
}

// ContainerMounts returns no direct mounts — Kiro uses the staging-dir
// approach populated by PrepareContainer.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup is a no-op; the staging directory is cleaned by the caller.
func (p *Provider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns no implied dependencies.
func (p *Provider) ImpliedDependencies() []string { return nil }
```

(`Grant`, `PrepareContainer`, and `RegisterCLI` are completed in Tasks 4–6. The package will not build until Task 6; that is expected — do not run the full build until Step 6 here is reached for the *unit* file, and the package compiles after Task 6.)

- [ ] **Step 5: Stub Grant/PrepareContainer/RegisterCLI so the package compiles for this task's test**

To keep Task 3 independently testable, create temporary stubs that Tasks 4–6 replace. Create `internal/providers/kiro/agent.go`:

```go
package kiro

import (
	"context"
	"errors"

	"github.com/majorcontext/moat/internal/provider"
)

// PrepareContainer is implemented in Task 5.
func (p *Provider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	return nil, errors.New("not implemented")
}
```

Create `internal/providers/kiro/grant.go`:

```go
package kiro

import (
	"context"
	"errors"

	"github.com/majorcontext/moat/internal/provider"
)

// Grant is implemented in Task 4.
type Grant struct{}

func NewGrant() *Grant { return &Grant{} }

func (g *Grant) Execute(ctx context.Context) (*provider.Credential, error) {
	return nil, errors.New("not implemented")
}
```

Create `internal/providers/kiro/cli.go`:

```go
package kiro

import "github.com/spf13/cobra"

// RegisterCLI is implemented in Task 6.
func (p *Provider) RegisterCLI(root *cobra.Command) {}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/providers/kiro/ -v`
Expected: PASS (4 tests). `go build ./internal/providers/kiro/` succeeds.

- [ ] **Step 7: Commit**

```bash
go vet ./internal/providers/kiro/
git add internal/providers/kiro/
git commit -m "feat(kiro): add credential provider (proxy injection + container env)"
```

---

### Task 4: Kiro grant flow

**Files:**
- Modify: `internal/providers/kiro/grant.go` (replace the Task 3 stub)
- Test: `internal/providers/kiro/grant_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/providers/kiro/grant_test.go`:

```go
package kiro

import (
	"context"
	"strings"
	"testing"
)

func TestExecuteReadsEnv(t *testing.T) {
	t.Setenv("KIRO_API_KEY", "env-token-123")
	cred, err := NewGrant().Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if cred.Token != "env-token-123" {
		t.Errorf("Token = %q, want env-token-123", cred.Token)
	}
	if cred.Provider != "kiro" {
		t.Errorf("Provider = %q, want kiro", cred.Provider)
	}
	if cred.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

func TestExecutePromptsWhenNoEnv(t *testing.T) {
	t.Setenv("KIRO_API_KEY", "")
	g := NewGrant()
	g.readToken = func() (string, error) { return "  prompted-token  ", nil }
	cred, err := g.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if cred.Token != "prompted-token" {
		t.Errorf("Token = %q, want trimmed prompted-token", cred.Token)
	}
}

func TestExecuteEmptyTokenIsError(t *testing.T) {
	t.Setenv("KIRO_API_KEY", "")
	g := NewGrant()
	g.readToken = func() (string, error) { return "   ", nil }
	_, err := g.Execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty-token error, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/kiro/ -run TestExecute -v`
Expected: FAIL (`g.readToken` undefined; stub returns "not implemented").

- [ ] **Step 3: Implement the grant**

Replace the entire contents of `internal/providers/kiro/grant.go` with:

```go
package kiro

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

// Grant handles the Kiro token grant. It reads KIRO_API_KEY from the
// environment, falling back to an interactive prompt. The token is stored
// as-is and validated only by the upstream API at run time (no local
// validation endpoint — see spec).
type Grant struct {
	// readToken reads a token interactively. Overridable in tests.
	readToken func() (string, error)
}

// NewGrant creates a Grant with the default interactive reader.
func NewGrant() *Grant {
	return &Grant{readToken: promptForToken}
}

func promptForToken() (string, error) {
	fmt.Print("Enter your Kiro token: ")
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("reading token: %w", err)
	}
	return line, nil
}

// Execute performs the grant. Returns a provider.Credential; the CLI wrapper
// persists it to the credential store.
func (g *Grant) Execute(ctx context.Context) (*provider.Credential, error) {
	token := os.Getenv("KIRO_API_KEY")
	if token != "" {
		fmt.Println("Using token from KIRO_API_KEY environment variable")
	} else {
		read := g.readToken
		if read == nil {
			read = promptForToken
		}
		var err error
		token, err = read()
		if err != nil {
			return nil, fmt.Errorf("reading Kiro token: %w", err)
		}
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("Kiro token is empty")
	}

	return &provider.Credential{
		Provider:  "kiro",
		Token:     token,
		CreatedAt: time.Now(),
	}, nil
}

// HasCredential reports whether a Kiro credential exists in the store.
func HasCredential() bool {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return false
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return false
	}
	cred, err := store.Get(credential.ProviderKiro)
	return err == nil && cred != nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/providers/kiro/ -v`
Expected: PASS (all kiro tests, including Task 3's).

- [ ] **Step 5: Commit**

```bash
go vet ./internal/providers/kiro/
git add internal/providers/kiro/grant.go internal/providers/kiro/grant_test.go
git commit -m "feat(kiro): implement token grant flow"
```

---

### Task 5: Kiro container config staging (`PrepareContainer`)

**Files:**
- Modify: `internal/providers/kiro/agent.go` (replace the Task 3 stub)
- Test: `internal/providers/kiro/agent_test.go`

**Reference for MCP JSON shape:** `https://kiro.dev/docs/cli/mcp/configuration/#remote-server` (memory: kiro-mcp-config-docs). Remote HTTP servers use a `url` (+ optional `headers`) entry; local servers use `command`/`args`. Confirm exact key names against that doc before finalizing — if they differ, adjust `mcpHTTPServer`/`mcpLocalServer` struct tags only.

- [ ] **Step 1: Write the failing test**

Create `internal/providers/kiro/agent_test.go`:

```go
package kiro

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return m
}

func TestPrepareContainerWritesConfig(t *testing.T) {
	p := &Provider{}
	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		Credential:     &provider.Credential{Provider: "kiro", Token: "t"},
		ContainerHome:  "/home/moatuser",
		RuntimeContext: "# Moat context\nhello",
		LocalMCPServers: map[string]provider.LocalMCPServerConfig{
			"local1": {Command: "mcp-local", Args: []string{"--x"}},
		},
		MCPServers: map[string]provider.MCPServerConfig{
			"remote1": {URL: "http://proxy/mcp/tok/remote1", Headers: map[string]string{"X-A": "b"}},
		},
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	dir := cfg.StagingDir

	cli := readJSON(t, filepath.Join(dir, "settings", "cli.json"))
	if cli["chat.disableTrustAllConfirmation"] != true {
		t.Errorf("cli.json missing chat.disableTrustAllConfirmation=true: %v", cli)
	}

	mcp := readJSON(t, filepath.Join(dir, "settings", "mcp.json"))
	servers, ok := mcp["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp.json mcpServers not an object: %v", mcp)
	}
	if _, ok := servers["local1"]; !ok {
		t.Error("mcp.json missing local1")
	}
	if _, ok := servers["remote1"]; !ok {
		t.Error("mcp.json missing remote1")
	}

	if _, err := os.Stat(filepath.Join(dir, "agents", "default.json")); err != nil {
		t.Errorf("agents/default.json missing: %v", err)
	}

	ctx, err := os.ReadFile(filepath.Join(dir, "steering", "moat-context.md"))
	if err != nil {
		t.Fatalf("steering/moat-context.md: %v", err)
	}
	if string(ctx) != "# Moat context\nhello" {
		t.Errorf("steering content = %q", string(ctx))
	}

	foundEnv := false
	for _, e := range cfg.Env {
		if e == "KIRO_API_KEY="+KiroAPIKeyPlaceholder {
			foundEnv = true
		}
	}
	if !foundEnv {
		t.Errorf("env missing KIRO_API_KEY placeholder: %v", cfg.Env)
	}
	if len(cfg.Mounts) != 1 || cfg.Mounts[0].Target != KiroInitMountPath || !cfg.Mounts[0].ReadOnly {
		t.Errorf("unexpected mounts: %+v", cfg.Mounts)
	}
}

func TestPrepareContainerOmitsEmptySteering(t *testing.T) {
	p := &Provider{}
	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		Credential:    &provider.Credential{Provider: "kiro", Token: "t"},
		ContainerHome: "/home/moatuser",
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()
	if _, err := os.Stat(filepath.Join(cfg.StagingDir, "steering", "moat-context.md")); !os.IsNotExist(err) {
		t.Errorf("steering file should not exist when RuntimeContext empty (err=%v)", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/kiro/ -run TestPrepareContainer -v`
Expected: FAIL (stub returns "not implemented").

- [ ] **Step 3: Implement `PrepareContainer`**

Replace the entire contents of `internal/providers/kiro/agent.go` with:

```go
package kiro

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/provider"
)

// mcpLocalServer is a stdio MCP server entry in ~/.kiro/settings/mcp.json.
type mcpLocalServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
}

// mcpHTTPServer is a remote HTTP MCP server entry. Key names per
// https://kiro.dev/docs/cli/mcp/configuration/#remote-server — confirm
// during implementation (see Task 5 header note).
type mcpHTTPServer struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

type mcpFile struct {
	MCPServers map[string]any `json:"mcpServers"`
}

// PrepareContainer stages a ~/.kiro tree (settings, agents, steering) that
// moat-init copies into the container. The real token is never written —
// auth is via the proxy.
func (p *Provider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	tmpDir, err := os.MkdirTemp("", "moat-kiro-staging-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	for _, sub := range []string{"settings", "agents", "steering"} {
		if mkErr := os.MkdirAll(filepath.Join(tmpDir, sub), 0o755); mkErr != nil {
			cleanup()
			return nil, fmt.Errorf("creating %s dir: %w", sub, mkErr)
		}
	}

	// settings/cli.json — allow --trust-all-tools non-interactively.
	cliJSON, _ := json.MarshalIndent(map[string]any{
		"chat.disableTrustAllConfirmation": true,
	}, "", "  ")
	if wErr := os.WriteFile(filepath.Join(tmpDir, "settings", "cli.json"), cliJSON, 0o600); wErr != nil {
		cleanup()
		return nil, fmt.Errorf("writing cli.json: %w", wErr)
	}

	// settings/mcp.json — local + remote relay servers.
	servers := map[string]any{}
	for name, c := range opts.LocalMCPServers {
		servers[name] = mcpLocalServer{Command: c.Command, Args: c.Args, Env: c.Env, Cwd: c.Cwd}
	}
	for name, c := range opts.MCPServers {
		servers[name] = mcpHTTPServer{URL: c.URL, Headers: c.Headers}
	}
	mcpJSON, mErr := json.MarshalIndent(mcpFile{MCPServers: servers}, "", "  ")
	if mErr != nil {
		cleanup()
		return nil, fmt.Errorf("marshaling mcp.json: %w", mErr)
	}
	if wErr := os.WriteFile(filepath.Join(tmpDir, "settings", "mcp.json"), mcpJSON, 0o600); wErr != nil {
		cleanup()
		return nil, fmt.Errorf("writing mcp.json: %w", wErr)
	}

	// agents/default.json — trimmed default agent including steering resources.
	agentJSON, _ := json.MarshalIndent(map[string]any{
		"name":        "default",
		"description": "Moat sandbox agent",
		"tools":       []string{"*"},
		"resources": []string{
			"file://README.md",
			"file://AGENTS.md",
			"file://.kiro/steering/**/*.md",
			"file://~/.kiro/steering/**/*.md",
		},
		"includeMcpJson": true,
	}, "", "  ")
	if wErr := os.WriteFile(filepath.Join(tmpDir, "agents", "default.json"), agentJSON, 0o600); wErr != nil {
		cleanup()
		return nil, fmt.Errorf("writing default.json: %w", wErr)
	}

	// steering/moat-context.md — runtime context (only when non-empty).
	if opts.RuntimeContext != "" {
		if wErr := os.WriteFile(filepath.Join(tmpDir, "steering", "moat-context.md"), []byte(opts.RuntimeContext), 0o644); wErr != nil {
			cleanup()
			return nil, fmt.Errorf("writing steering context: %w", wErr)
		}
	}

	env := p.ContainerEnv(opts.Credential)
	env = append(env, "MOAT_KIRO_INIT="+KiroInitMountPath)

	return &provider.ContainerConfig{
		Env: env,
		Mounts: []provider.MountConfig{
			{Source: tmpDir, Target: KiroInitMountPath, ReadOnly: true},
		},
		StagingDir: tmpDir,
		Cleanup:    cleanup,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/providers/kiro/ -v`
Expected: PASS (all kiro tests).

- [ ] **Step 5: Commit**

```bash
go vet ./internal/providers/kiro/
git add internal/providers/kiro/agent.go internal/providers/kiro/agent_test.go
git commit -m "feat(kiro): stage ~/.kiro container config (settings, mcp, agent, steering)"
```

---

### Task 6: Config section + log sync

> Done before the CLI task because `internal/providers/kiro/cli.go` references `cfg.Kiro`.

**Files:**
- Modify: `internal/config/config.go` (add `Kiro` field, `KiroConfig` type, `ShouldSyncKiroLogs`, MCP validation)
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestKiroConfigParsesAndSyncLogs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "moat.yaml")
	os.WriteFile(path, []byte("agent: kiro\ngrants: [kiro]\nkiro:\n  mcp:\n    s1:\n      command: foo\n"), 0644)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := cfg.Kiro.MCP["s1"]; !ok {
		t.Errorf("kiro.mcp.s1 not parsed: %+v", cfg.Kiro)
	}
	if !cfg.ShouldSyncKiroLogs() {
		t.Error("ShouldSyncKiroLogs() = false, want true (kiro grant present)")
	}
	no := false
	cfg.Kiro.SyncLogs = &no
	if cfg.ShouldSyncKiroLogs() {
		t.Error("ShouldSyncKiroLogs() = true, want false (explicitly disabled)")
	}
}
```

(If `Load` is not the loader name used elsewhere in `config_test.go`, match the existing pattern in that file — check a neighboring test like the codex one.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestKiroConfigParsesAndSyncLogs -v`
Expected: FAIL (cfg.Kiro undefined — compile error).

- [ ] **Step 3: Add the config field and type**

In `internal/config/config.go`:

1. After the `Gemini GeminiConfig \`yaml:"gemini,omitempty"\`` field, add:
   ```go
	Kiro         KiroConfig        `yaml:"kiro,omitempty"`
   ```
2. After the `GeminiConfig` struct definition, add:
   ```go
// KiroConfig configures Kiro CLI integration options.
type KiroConfig struct {
	// SyncLogs controls whether Kiro session logs are synced to the host.
	// Default: false, unless the "kiro" grant is configured (then true).
	SyncLogs *bool `yaml:"sync_logs,omitempty"`

	// MCP defines local MCP (Model Context Protocol) server configurations.
	MCP map[string]MCPServerSpec `yaml:"mcp,omitempty"`
}
   ```
3. After the `ShouldSyncGeminiLogs` function, add:
   ```go
// ShouldSyncKiroLogs returns true if Kiro session logs should be synced.
// - If kiro.sync_logs is explicitly set, use that value
// - Otherwise, enable sync_logs if "kiro" is in grants
func (c *Config) ShouldSyncKiroLogs() bool {
	if c.Kiro.SyncLogs != nil {
		return *c.Kiro.SyncLogs
	}
	for _, grant := range c.Grants {
		if grant == "kiro" || strings.HasPrefix(grant, "kiro:") {
			return true
		}
	}
	return false
}
   ```
4. In the validation function, after the Gemini MCP validation loop, add:
   ```go
	// Validate Kiro MCP server specs
	for name, spec := range cfg.Kiro.MCP {
		if err := validateMCPServerSpec("kiro", name, spec); err != nil {
			return nil, err
		}
	}
   ```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -run TestKiroConfigParsesAndSyncLogs -v && go test ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
go vet ./internal/config/
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add kiro config section and log sync"
```

---

### Task 7: Kiro CLI command

**Files:**
- Modify: `internal/providers/kiro/cli.go` (replace the Task 3 stub)
- Test: `internal/providers/kiro/cli_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/providers/kiro/cli_test.go`:

```go
package kiro

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

func TestNetworkHosts(t *testing.T) {
	hosts := NetworkHosts()
	for _, want := range []string{"q.*.amazonaws.com", "cognito-identity.*.amazonaws.com", "cli.kiro.dev"} {
		if !slices.Contains(hosts, want) {
			t.Errorf("NetworkHosts() missing %q: %v", want, hosts)
		}
	}
}

func TestDefaultDependencies(t *testing.T) {
	deps := DefaultDependencies()
	if !slices.Contains(deps, "kiro-cli") || !slices.Contains(deps, "git") {
		t.Errorf("DefaultDependencies() = %v, want kiro-cli and git", deps)
	}
}

func TestRegisterCLIAddsKiroCommand(t *testing.T) {
	root := &cobra.Command{Use: "moat"}
	(&Provider{}).RegisterCLI(root)
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "kiro" {
			found = true
		}
	}
	if !found {
		t.Error("RegisterCLI did not add 'kiro' command")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/kiro/ -run 'TestNetworkHosts|TestDefaultDependencies|TestRegisterCLI' -v`
Expected: FAIL (undefined: NetworkHosts/DefaultDependencies; stub adds no command).

- [ ] **Step 3: Implement the CLI command**

Replace the entire contents of `internal/providers/kiro/cli.go` with:

```go
package kiro

import (
	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

var (
	kiroFlags        cli.ExecFlags
	kiroPromptFlag   string
	kiroAllowedHosts []string
	kiroWtFlag       string
)

// NetworkHosts returns the hosts Kiro needs network access to.
func NetworkHosts() []string {
	hosts := []string{"cli.kiro.dev"}
	hosts = append(hosts, kiroAPIHosts...)
	hosts = append(hosts, kiroPassthroughHosts...)
	return hosts
}

// DefaultDependencies returns the default dependencies for running Kiro CLI.
func DefaultDependencies() []string {
	return []string{"kiro-cli", "git"}
}

// RegisterCLI registers the `moat kiro` command. Called automatically for
// every AgentProvider by cmd/moat/cli/root.go.
func (p *Provider) RegisterCLI(root *cobra.Command) {
	kiroCmd := &cobra.Command{
		Use:   "kiro [workspace] [flags]",
		Short: "Run Kiro CLI in an isolated container",
		Long: `Run the Kiro CLI in an isolated container with automatic credential injection.

Your workspace is mounted at /workspace inside the container. Kiro credentials
are injected transparently via the Moat proxy - Kiro never sees raw tokens.

Examples:
  # Start Kiro in the current directory (interactive)
  moat kiro

  # Start Kiro in a specific project
  moat kiro ./my-project

  # Ask Kiro to do something specific (non-interactive)
  moat kiro -p "explain this codebase"

  # Add additional grants (e.g., for GitHub API access)
  moat kiro --grant github

Use 'moat list' to see running and recent runs.`,
		Args: cobra.ArbitraryArgs,
		RunE: runKiro,
	}

	cli.AddExecFlags(kiroCmd, &kiroFlags)
	kiroCmd.Flags().StringVarP(&kiroPromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	kiroCmd.Flags().StringSliceVar(&kiroAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	kiroCmd.Flags().StringVar(&kiroWtFlag, "worktree", "", "run in a git worktree for this branch")
	kiroCmd.Flags().StringVar(&kiroWtFlag, "wt", "", "alias for --worktree")
	_ = kiroCmd.Flags().MarkHidden("wt")

	root.AddCommand(kiroCmd)
}

func runKiro(cmd *cobra.Command, args []string) error {
	return cli.RunProvider(cmd, args, cli.ProviderRunConfig{
		Name:                  "kiro",
		Flags:                 &kiroFlags,
		PromptFlag:            kiroPromptFlag,
		AllowedHosts:          kiroAllowedHosts,
		WtFlag:                kiroWtFlag,
		GetCredentialGrant:    GetCredentialName,
		Dependencies:          DefaultDependencies(),
		NetworkHosts:          NetworkHosts(),
		SupportsInitialPrompt: true,
		DryRunNote:            "Note: No Kiro token configured. Run 'moat grant kiro'.",
		BuildCommand: func(promptFlag, initialPrompt string) ([]string, error) {
			containerCmd := []string{"kiro-cli", "chat", "--trust-all-tools", "--trust-tools=execute_bash"}
			if promptFlag != "" {
				return append(containerCmd, "--no-interactive", promptFlag), nil
			}
			if initialPrompt != "" {
				containerCmd = append(containerCmd, initialPrompt)
			}
			return containerCmd, nil
		},
		ConfigureAgent: func(cfg *config.Config) {
			syncLogs := true
			cfg.Kiro.SyncLogs = &syncLogs
		},
	})
}

// GetCredentialName returns "kiro" if a Kiro credential exists, else "".
func GetCredentialName() string {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return ""
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return ""
	}
	if _, err := store.Get(credential.ProviderKiro); err == nil {
		return "kiro"
	}
	return ""
}
```

`cfg.Kiro` was added in Task 6, so this file compiles directly.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/providers/kiro/ -v`
Expected: PASS (all kiro tests).

- [ ] **Step 5: Commit**

```bash
go vet ./internal/providers/kiro/
git add internal/providers/kiro/cli.go internal/providers/kiro/cli_test.go
git commit -m "feat(kiro): add 'moat kiro' command"
```

---

### Task 8: Register the kiro provider

**Files:**
- Modify: `internal/providers/register.go`

- [ ] **Step 1: Add the import**

In `internal/providers/register.go`, add to the import block (keep alphabetical-ish grouping, place after the `gemini` line and before `github`):

```go
	_ "github.com/majorcontext/moat/internal/providers/kiro"     // registers Kiro provider
```

- [ ] **Step 2: Verify registration via a test**

Add to `internal/providers/register_test.go` (create the file if it does not exist):

```go
package providers

import (
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestKiroProviderRegistered(t *testing.T) {
	if provider.Get("kiro") == nil {
		t.Fatal("kiro provider not registered")
	}
	if provider.GetAgent("kiro") == nil {
		t.Fatal("kiro provider does not implement AgentProvider")
	}
}
```

- [ ] **Step 3: Run test to verify it passes**

Run: `go test ./internal/providers/ -run TestKiroProviderRegistered -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
go vet ./internal/providers/
git add internal/providers/register.go internal/providers/register_test.go
git commit -m "feat(kiro): register provider"
```

---

### Task 9: Image-needs detection for kiro init

**Files:**
- Modify: `internal/run/imageneeds.go` (add `case "kiro":` + dependency fallback)
- Test: `internal/run/imageneeds_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/run/imageneeds_test.go` (mirror the existing codex/gemini detection tests in that file — find one named like `TestResolveImageNeeds*` and copy its store-mock setup):

```go
func TestResolveImageNeedsKiro(t *testing.T) {
	store := newTestStore(t) // use whatever helper the existing codex test uses
	_ = store.Save(credential.Credential{Provider: credential.ProviderKiro, Token: "t", CreatedAt: time.Now()})

	needs := resolveImageNeedsWithStore([]string{"kiro"}, nil, store)
	if !slices.Contains(needs.initProviders, "kiro") {
		t.Errorf("initProviders = %v, want to contain kiro", needs.initProviders)
	}
}

func TestResolveImageNeedsKiroDepFallback(t *testing.T) {
	needs := resolveImageNeedsWithStore(nil, []deps.Dependency{{Name: "kiro-cli"}}, nil)
	if !slices.Contains(needs.initProviders, "kiro") {
		t.Errorf("initProviders = %v, want to contain kiro (dep fallback)", needs.initProviders)
	}
}
```

If the existing tests use a different store-construction helper or `deps.Dependency` literal shape, match that exactly (read a neighboring test first).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/run/ -run TestResolveImageNeedsKiro -v`
Expected: FAIL (no "kiro" in initProviders).

- [ ] **Step 3: Add the detection case and fallback**

In `internal/run/imageneeds.go`, in `resolveImageNeedsWithStore`:

1. In the `switch canonical` block, after the `case "gemini":` block, add:
   ```go
		case "kiro":
			if store != nil {
				if _, err := store.Get(credential.ProviderKiro); err == nil {
					initSet["kiro"] = true
				}
			}
   ```
2. After the existing `if !initSet["gemini"] && hasDep(depList, "gemini-cli")` block, add:
   ```go
	if !initSet["kiro"] && hasDep(depList, "kiro-cli") {
		initSet["kiro"] = true
	}
   ```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/run/ -run TestResolveImageNeedsKiro -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
go vet ./internal/run/
git add internal/run/imageneeds.go internal/run/imageneeds_test.go
git commit -m "feat(run): detect kiro init from grant or kiro-cli dependency"
```

---

### Task 10: Run-manager dispatch block

**Files:**
- Modify: `internal/run/manager.go` (add `needsKiroInit`, kiro `PrepareContainer` block, grant maps, cleanup)
- Test: covered by build + existing manager tests + `go test ./...`

- [ ] **Step 1: Add `needsKiroInit`**

In `internal/run/manager.go`, next to:
```go
	needsGeminiInit := slices.Contains(imgNeeds.initProviders, "gemini")
```
add:
```go
	needsKiroInit := slices.Contains(imgNeeds.initProviders, "kiro")
```

- [ ] **Step 2: Extend grant→env maps for kiro MCP child processes**

In `grantToEnvVar`, add before `default:`:
```go
	case "kiro":
		return "KIRO_API_KEY", true
```
In `grantToPlaceholder`, add before `default:`:
```go
	case "kiro":
		return kiro.KiroAPIKeyPlaceholder
```
Add the import `"github.com/majorcontext/moat/internal/providers/kiro"` to `manager.go`'s import block. Also update the error message in the codex local-MCP block that lists supported grants (`"... supported: github, openai, anthropic, gemini"`) to include `kiro` if a kiro MCP block reuses that validation path; otherwise leave codex's message untouched and rely on the kiro block's own message (Step 3).

- [ ] **Step 3: Add the kiro PrepareContainer dispatch block**

Locate the Gemini dispatch block (search for `needsGeminiInit ||` and its closing — it ends right before the section that assembles the final container spec). Immediately after the Gemini block's closing brace, add the following, structured identically to the Codex block (search `needsCodexInit || hasCodexLocalMCP` to copy its exact structure including the `grantToEnvVar`/`hasGrant` handling and the `cleanupAgentConfig` chaining):

```go
	// Set up Kiro staging directory for init script.
	var kiroConfig *provider.ContainerConfig
	hasKiroLocalMCP := opts.Config != nil && len(opts.Config.Kiro.MCP) > 0
	if needsKiroInit || hasKiroLocalMCP || (opts.Config != nil && opts.Config.ShouldSyncKiroLogs()) {
		kiroProvider := provider.GetAgent("kiro")
		if kiroProvider == nil {
			cleanupDaemonRun()
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			cleanupAgentConfig(geminiConfig)
			return nil, fmt.Errorf("kiro provider not registered")
		}

		// Kiro credential (stored under "kiro").
		var kiroCred *provider.Credential
		if needsKiroInit {
			key, keyErr := credential.DefaultEncryptionKey()
			if keyErr == nil {
				store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
				if storeErr == nil {
					if cred, err := store.Get(credential.ProviderKiro); err == nil {
						kiroCred = provider.FromLegacy(cred)
					}
				}
			}
		}

		// Remote MCP relay map (proxy relay URLs), same as the Claude block.
		kiroMCPServers := make(map[string]provider.MCPServerConfig)
		if opts.Config != nil && len(opts.Config.MCP) > 0 {
			proxyAddr := fmt.Sprintf("%s:%d", m.defaultRuntime().GetHostAddress(), r.ProxyPort)
			for _, mcp := range opts.Config.MCP {
				relayURL := fmt.Sprintf("http://%s/mcp/%s/%s", proxyAddr, r.ProxyAuthToken, mcp.Name)
				mc := provider.MCPServerConfig{URL: relayURL}
				if mcp.Auth != nil {
					mc.Headers = map[string]string{mcp.Auth.Header: "moat-stub-" + mcp.Auth.Grant}
				}
				kiroMCPServers[mcp.Name] = mc
			}
		}

		// Local MCP servers from kiro.mcp (with grant→placeholder-env, same
		// handling as the Codex block).
		var kiroLocalMCP map[string]provider.LocalMCPServerConfig
		if opts.Config != nil && len(opts.Config.Kiro.MCP) > 0 {
			kiroLocalMCP = make(map[string]provider.LocalMCPServerConfig)
			for name, spec := range opts.Config.Kiro.MCP {
				env := spec.Env
				if spec.Grant != "" {
					v, ok := grantToEnvVar(spec.Grant)
					if !ok {
						cleanupDaemonRun()
						cleanupSSH(sshServer)
						cleanupAgentConfig(claudeConfig)
						cleanupAgentConfig(codexConfig)
						cleanupAgentConfig(geminiConfig)
						return nil, fmt.Errorf("kiro.mcp.%s: unknown grant %q (supported: github, openai, anthropic, gemini, kiro)", name, spec.Grant)
					}
					if !hasGrant(opts.Config.Grants, spec.Grant) {
						cleanupDaemonRun()
						cleanupSSH(sshServer)
						cleanupAgentConfig(claudeConfig)
						cleanupAgentConfig(codexConfig)
						cleanupAgentConfig(geminiConfig)
						return nil, fmt.Errorf("kiro.mcp.%s: grant %q not declared in top-level grants list — add 'grants: [%s]' to agent.yaml", name, spec.Grant, spec.Grant)
					}
					if env == nil {
						env = make(map[string]string)
					} else {
						envCopy := make(map[string]string, len(env)+1)
						for k, val := range env {
							envCopy[k] = val
						}
						env = envCopy
					}
					env[v] = grantToPlaceholder(spec.Grant)
				}
				kiroLocalMCP[name] = provider.LocalMCPServerConfig{
					Command: spec.Command,
					Args:    spec.Args,
					Env:     env,
					Cwd:     spec.Cwd,
				}
			}
		}

		var prepErr error
		kiroConfig, prepErr = kiroProvider.PrepareContainer(ctx, provider.PrepareOpts{
			Credential:      kiroCred,
			ContainerHome:   containerHome,
			MCPServers:      kiroMCPServers,
			RuntimeContext:  renderedContext,
			LocalMCPServers: kiroLocalMCP,
		})
		if prepErr != nil {
			cleanupDaemonRun()
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			cleanupAgentConfig(geminiConfig)
			return nil, fmt.Errorf("preparing Kiro container config: %w", prepErr)
		}

		mounts = append(mounts, kiroConfig.Mounts...)
		proxyEnv = append(proxyEnv, kiroConfig.Env...)
	}
```

**IMPORTANT:** the exact variable names (`codexConfig`, `geminiConfig`, `cleanupAgentConfig`, `cleanupSSH`, `sshServer`, `containerHome`, `renderedContext`, `proxyEnv`, `mounts`, `m.defaultRuntime()`, `r.ProxyPort`, `r.ProxyAuthToken`) must match what the Codex/Gemini blocks actually use at that point in `manager.go`. Read the Codex block (search `needsCodexInit || hasCodexLocalMCP`) and the Gemini block first and copy their exact cleanup-chain and helper calls. Then find where `codexConfig`/`geminiConfig` get added to the global `cleanupAgentConfig` teardown path (search `cleanupAgentConfig(codexConfig)`) and add `cleanupAgentConfig(kiroConfig)` everywhere `cleanupAgentConfig(geminiConfig)` appears after this block.

- [ ] **Step 4: Verify wildcard credential injection assumption**

Confirm gatekeeper v0.2.0 matches wildcard host patterns for credential injection. Run:

```bash
go doc github.com/majorcontext/gatekeeper/proxy 2>/dev/null | head -40
grep -rn "filepath.Match\|HasSuffix\|wildcard\|\\*\\." "$(go env GOMODCACHE)"/github.com/majorcontext/gatekeeper@*/proxy/*.go 2>/dev/null | grep -iv test | head
```

- If wildcard matching is supported: leave `kiroAPIHosts` as-is.
- If only exact hosts match: in `internal/providers/kiro/constants.go`, replace `kiroAPIHosts` with `[]string{"q.us-east-1.amazonaws.com"}` and add a code comment + a docs note (Task 11) explaining how to add other regions. Update `provider_test.go`'s expected hosts accordingly. Re-run `go test ./internal/providers/kiro/`.

Record the outcome in the commit message.

- [ ] **Step 5: Build and test**

Run: `go build ./... && go test ./internal/run/ ./internal/providers/kiro/`
Expected: PASS / build clean.

- [ ] **Step 6: Commit**

```bash
make lint || go vet ./...
git add internal/run/manager.go internal/providers/kiro/
git commit -m "feat(run): wire kiro PrepareContainer dispatch (incl. local + remote MCP)"
```

---

### Task 11: Documentation + changelog

**Files:**
- Modify: `docs/content/reference/01-cli.md`
- Modify: `docs/content/reference/02-moat-yaml.md`
- Create: `docs/content/guides/<NN>-kiro.md` (use the next free number; mirror an existing agent guide such as the codex guide)
- Modify: `CHANGELOG.md`

- [ ] **Step 1: CLI reference**

In `docs/content/reference/01-cli.md`, find the section documenting `moat codex` / `moat grant codex` and add parallel entries for:
- `moat kiro [workspace] [flags]` — flags `-p/--prompt`, `--allow-host`, `--worktree/--wt`, plus the shared exec flags; one-line description matching the command's `Short`.
- `moat grant kiro` — prompts for a Kiro token (or reads `KIRO_API_KEY`); static credential, re-grant when it expires.

Match the surrounding formatting exactly (verify by reading the codex entry first).

- [ ] **Step 2: moat.yaml reference**

In `docs/content/reference/02-moat-yaml.md`, find the `codex:` section and add a parallel `kiro:` section documenting `sync_logs` and `mcp:` (local MCP server specs), noting `agent: kiro` and `grants: [kiro]`.

- [ ] **Step 3: Guide**

Create the guide (next free `docs/content/guides/<NN>-kiro.md`), mirroring the structure of the existing codex guide: prerequisites (`moat grant kiro`), quick start (`moat kiro`), MCP config example, and the network hosts Kiro uses. State facts only (per STYLE-GUIDE).

- [ ] **Step 4: Changelog**

In `CHANGELOG.md`, under the unreleased/next version's `### Added` heading (create the version stanza if absent, following the existing format), add:

```markdown
- **Kiro CLI support** — `moat kiro` runs the Kiro CLI in an isolated container with transparent credential injection; `moat grant kiro` stores a Kiro token. Supports local and remote MCP and runtime-context injection. ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
```

Leave `NNN` to be replaced with the real PR number when the PR is opened (this is the one place a number is unknown until PR creation — note it in the PR description).

- [ ] **Step 5: Commit**

```bash
git add docs/ CHANGELOG.md
git commit -m "docs: document kiro-cli support"
```

---

### Task 12: Full verification

- [ ] **Step 1: Full build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 2: Full test suite with race detector**

Run: `make test-unit`
Expected: PASS. If a pre-existing unrelated failure appears, capture the output and report it rather than masking it.

- [ ] **Step 3: Lint**

Run: `make lint` (fallback `go vet ./...`)
Expected: clean. Fix any kiro-related findings.

- [ ] **Step 4: Manual smoke (dry run, no real container)**

Run: `go run ./cmd/moat kiro --help` and `go run ./cmd/moat grant --help`
Expected: `kiro` command and `grant kiro` listed; help text renders.

- [ ] **Step 5: Final commit (if any lint fixes)**

```bash
make lint || go vet ./...
git add -A
git commit -m "chore(kiro): lint and final cleanup"
```

- [ ] **Step 6: Hand back to the operator**

Summarize: tasks completed, the wildcard-injection verification outcome (Task 10 Step 4), the MCP-JSON-shape confirmation (Task 5), and any deviations. Do NOT open a PR unless the operator asks (per CLAUDE.md, use `gh pr create` with default flags only if requested).

---

## Self-Review

**Spec coverage:**
- Install (spec §1) → Task 1 ✓
- Credential provider: provider.go/constants.go/grant.go (spec §2) → Tasks 2,3,4 ✓
- Hosts table (spec §2) → Task 3 constants + Task 10 Step 4 verification ✓
- Agent staging: cli.json/mcp.json/default.json/steering (spec §3) → Task 5 ✓
- Config section + ShouldSyncKiroLogs (spec §4) → Task 6 ✓
- Run wiring incl. remote+local MCP, cleanup (spec §5) → Tasks 9,10 ✓
- `moat kiro` command (spec §6) → Task 7 ✓
- Registration + ProviderKiro constant (spec §7) → Tasks 2,8 ✓
- Docs + changelog (spec §8) → Task 11 ✓
- Verification points (spec §Verification) → Task 5 header (MCP shape), Task 10 Step 4 (wildcards), `~/.kiro` layout noted in Task 5 ✓
- Out-of-scope items: none implemented ✓

**Placeholder scan:** No TBD/TODO. The single unavoidable unknown (`#NNN` PR number in CHANGELOG) is explicitly called out with handling. Verification points are concrete steps with commands and decision branches, not placeholders.

**Type consistency:** `Provider`, `NewGrant()`/`Grant.Execute`, `KiroAPIKeyPlaceholder`, `KiroInitMountPath`, `kiroAPIHosts`, `kiroPassthroughHosts`, `NetworkHosts()`, `DefaultDependencies()`, `GetCredentialName()`, `credential.ProviderKiro`, `config.KiroConfig`/`cfg.Kiro`/`ShouldSyncKiroLogs()`, `needsKiroInit`, `kiroConfig` — used consistently across Tasks 2–10.

**Cross-task ordering note:** Tasks are ordered so each compiles when executed in sequence. Key dependency: `internal/providers/kiro/cli.go` (Task 7) references `cfg.Kiro`, which is why the config section is Task 6 (before it). Task 8 (register) requires the kiro package to compile, which it does after Tasks 3–7. Execute tasks strictly in ascending order.
