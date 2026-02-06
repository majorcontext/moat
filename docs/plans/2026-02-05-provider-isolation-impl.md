# Provider Isolation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Refactor provider-specific code into isolated packages with clean interfaces, eliminating hardcoded provider logic from manager and CLI.

**Architecture:** Create `internal/provider/` for interfaces and registry, `internal/providers/{github,aws,claude,codex}/` for implementations. Manager and CLI import only interfaces. Providers register explicitly via `RegisterAll()`.

**Tech Stack:** Go, Cobra CLI, existing credential/proxy infrastructure

---

## Task Overview

| Task | Name | Dependencies | Parallelizable With |
|------|------|--------------|---------------------|
| 1 | Core interfaces & registry | None | - |
| 2 | Shared utilities | Task 1 | - |
| 3 | GitHub provider | Tasks 1, 2 | Tasks 4, 5, 6 |
| 4 | AWS provider | Tasks 1, 2 | Tasks 3, 5, 6 |
| 5 | Claude provider | Tasks 1, 2 | Tasks 3, 4, 6 |
| 6 | Codex provider | Tasks 1, 2 | Tasks 3, 4, 5 |
| 7 | Provider registration | Tasks 3-6 | - |
| 8 | Update manager | Task 7 | Task 9 |
| 9 | Update CLI grant command | Task 7 | Task 8 |
| 10 | Agent CLI registration | Tasks 5, 6, 8 | - |
| 11 | Cleanup old code | Tasks 8-10 | - |
| 12 | Final verification | Task 11 | - |

---

## Task 1: Core Interfaces and Registry

**Files:**
- Create: `internal/provider/interfaces.go`
- Create: `internal/provider/registry.go`
- Create: `internal/provider/credential.go`
- Create: `internal/provider/errors.go`
- Create: `internal/provider/doc.go`
- Test: `internal/provider/registry_test.go`

**Step 1: Create package doc**

```go
// internal/provider/doc.go

// Package provider defines interfaces for credential and agent providers.
//
// All providers implement CredentialProvider for credential acquisition,
// proxy configuration, and container setup. Agent providers (Claude, Codex)
// additionally implement AgentProvider for session management and CLI commands.
// Endpoint providers (AWS) implement EndpointProvider to expose HTTP endpoints.
//
// Providers are registered explicitly via Register() and looked up via Get().
package provider
```

**Step 2: Create credential types**

```go
// internal/provider/credential.go

package provider

import (
	"time"

	"github.com/majorcontext/moat/internal/container"
)

// MetaKeyTokenSource is the metadata key for recording how a token was obtained.
const MetaKeyTokenSource = "token_source"

// Credential represents a stored credential.
type Credential struct {
	Provider  string            `json:"provider"`
	Token     string            `json:"token"`
	Scopes    []string          `json:"scopes,omitempty"`
	ExpiresAt time.Time         `json:"expires_at,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// MountConfig re-exports container.MountConfig for provider use.
type MountConfig = container.MountConfig

// PrepareOpts contains options for AgentProvider.PrepareContainer.
type PrepareOpts struct {
	Credential    *Credential
	ContainerHome string
	MCPServers    map[string]MCPServerConfig
	HostConfig    map[string]interface{}
}

// MCPServerConfig defines an MCP server configuration.
type MCPServerConfig struct {
	URL     string
	Headers map[string]string
}

// ContainerConfig is returned by AgentProvider.PrepareContainer.
type ContainerConfig struct {
	Env     []string
	Mounts  []MountConfig
	Cleanup func()
}

// Session represents an agent session.
type Session struct {
	ID        string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}
```

**Step 3: Create error types**

```go
// internal/provider/errors.go

package provider

import (
	"errors"
	"fmt"
)

var (
	// ErrProviderNotFound is returned when a provider is not registered.
	ErrProviderNotFound = errors.New("provider not found")
	// ErrCredentialNotFound is returned when no credential exists for a provider.
	ErrCredentialNotFound = errors.New("credential not found")
	// ErrCredentialExpired is returned when a credential has expired.
	ErrCredentialExpired = errors.New("credential expired")
	// ErrRefreshNotSupported is returned when refresh is attempted on a static credential.
	ErrRefreshNotSupported = errors.New("credential refresh not supported")
)

// GrantError wraps provider-specific grant failures with actionable guidance.
type GrantError struct {
	Provider string
	Cause    error
	Hint     string
}

func (e *GrantError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("grant %s: %v\n\n%s", e.Provider, e.Cause, e.Hint)
	}
	return fmt.Sprintf("grant %s: %v", e.Provider, e.Cause)
}

func (e *GrantError) Unwrap() error {
	return e.Cause
}
```

**Step 4: Create interfaces**

```go
// internal/provider/interfaces.go

package provider

import (
	"context"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// ProxyConfigurer configures proxy credentials and response transformations.
type ProxyConfigurer interface {
	SetCredential(host, value string)
	SetCredentialHeader(host, headerName, headerValue string)
	AddExtraHeader(host, headerName, headerValue string)
	AddResponseTransformer(host string, transformer ResponseTransformer)
}

// ResponseTransformer modifies HTTP responses for a host.
type ResponseTransformer func(req, resp interface{}) (interface{}, bool)

// CredentialProvider is implemented by all providers.
// Handles credential acquisition, proxy configuration, and container setup.
type CredentialProvider interface {
	// Name returns the provider identifier (e.g., "github", "claude").
	Name() string

	// Grant acquires credentials interactively or from environment.
	Grant(ctx context.Context) (*Credential, error)

	// ConfigureProxy sets up proxy headers for this credential.
	ConfigureProxy(p ProxyConfigurer, cred *Credential)

	// ContainerEnv returns environment variables to set in the container.
	ContainerEnv(cred *Credential) []string

	// ContainerMounts returns mounts needed for this credential.
	ContainerMounts(cred *Credential, containerHome string) ([]MountConfig, error)

	// CanRefresh reports whether this credential can be refreshed.
	CanRefresh(cred *Credential) bool

	// RefreshInterval returns how often to attempt refresh.
	// Returns 0 if refresh is not supported.
	RefreshInterval() time.Duration

	// Refresh re-acquires a fresh token and updates the proxy.
	// Returns ErrRefreshNotSupported if the credential cannot be refreshed.
	Refresh(ctx context.Context, p ProxyConfigurer, cred *Credential) (*Credential, error)

	// Cleanup is called when the run ends to clean up any resources.
	Cleanup(cleanupPath string)

	// ImpliedDependencies returns dependencies implied by this provider.
	// For example, github implies ["gh", "git"].
	ImpliedDependencies() []string
}

// AgentProvider extends CredentialProvider for AI agent runtimes.
// Implemented by claude and codex providers.
type AgentProvider interface {
	CredentialProvider

	// PrepareContainer sets up staging directories and config files.
	PrepareContainer(ctx context.Context, opts PrepareOpts) (*ContainerConfig, error)

	// Sessions returns all sessions for this agent.
	Sessions() ([]Session, error)

	// ResumeSession resumes an existing session by ID.
	ResumeSession(id string) error

	// RegisterCLI adds provider-specific commands to the root command.
	RegisterCLI(root *cobra.Command)
}

// EndpointProvider exposes HTTP endpoints to containers.
// Implemented by aws for the credential endpoint.
type EndpointProvider interface {
	CredentialProvider

	// RegisterEndpoints registers HTTP handlers on the proxy mux.
	RegisterEndpoints(mux *http.ServeMux, cred *Credential)
}
```

**Step 5: Create registry**

```go
// internal/provider/registry.go

package provider

import (
	"sort"
	"sync"
)

var (
	mu        sync.RWMutex
	providers = make(map[string]CredentialProvider)
)

// Register adds a provider to the registry.
func Register(p CredentialProvider) {
	mu.Lock()
	defer mu.Unlock()
	providers[p.Name()] = p
}

// Get returns a provider by name, or nil if not found.
func Get(name string) CredentialProvider {
	mu.RLock()
	defer mu.RUnlock()
	return providers[name]
}

// GetAgent returns an AgentProvider by name.
// Returns nil if not found or not an agent provider.
func GetAgent(name string) AgentProvider {
	p := Get(name)
	if agent, ok := p.(AgentProvider); ok {
		return agent
	}
	return nil
}

// GetEndpoint returns an EndpointProvider by name.
// Returns nil if not found or not an endpoint provider.
func GetEndpoint(name string) EndpointProvider {
	p := Get(name)
	if ep, ok := p.(EndpointProvider); ok {
		return ep
	}
	return nil
}

// All returns all registered providers.
func All() []CredentialProvider {
	mu.RLock()
	defer mu.RUnlock()
	result := make([]CredentialProvider, 0, len(providers))
	for _, p := range providers {
		result = append(result, p)
	}
	return result
}

// Agents returns all providers that implement AgentProvider.
func Agents() []AgentProvider {
	mu.RLock()
	defer mu.RUnlock()
	var result []AgentProvider
	for _, p := range providers {
		if agent, ok := p.(AgentProvider); ok {
			result = append(result, agent)
		}
	}
	return result
}

// Names returns the names of all registered providers, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Clear removes all registered providers. For testing only.
func Clear() {
	mu.Lock()
	defer mu.Unlock()
	providers = make(map[string]CredentialProvider)
}
```

**Step 6: Write registry tests**

```go
// internal/provider/registry_test.go

package provider

import (
	"context"
	"testing"
	"time"
)

// mockProvider is a minimal CredentialProvider for testing.
type mockProvider struct {
	name string
}

func (m *mockProvider) Name() string                            { return m.name }
func (m *mockProvider) Grant(context.Context) (*Credential, error) { return nil, nil }
func (m *mockProvider) ConfigureProxy(ProxyConfigurer, *Credential) {}
func (m *mockProvider) ContainerEnv(*Credential) []string       { return nil }
func (m *mockProvider) ContainerMounts(*Credential, string) ([]MountConfig, error) { return nil, nil }
func (m *mockProvider) CanRefresh(*Credential) bool             { return false }
func (m *mockProvider) RefreshInterval() time.Duration          { return 0 }
func (m *mockProvider) Refresh(context.Context, ProxyConfigurer, *Credential) (*Credential, error) {
	return nil, ErrRefreshNotSupported
}
func (m *mockProvider) Cleanup(string)        {}
func (m *mockProvider) ImpliedDependencies() []string { return nil }

func TestRegistry(t *testing.T) {
	Clear() // Start fresh
	defer Clear()

	t.Run("register and get", func(t *testing.T) {
		p := &mockProvider{name: "test"}
		Register(p)

		got := Get("test")
		if got == nil {
			t.Fatal("expected provider, got nil")
		}
		if got.Name() != "test" {
			t.Errorf("expected name 'test', got %q", got.Name())
		}
	})

	t.Run("get unknown returns nil", func(t *testing.T) {
		got := Get("unknown")
		if got != nil {
			t.Errorf("expected nil for unknown provider, got %v", got)
		}
	})

	t.Run("names returns sorted list", func(t *testing.T) {
		Clear()
		Register(&mockProvider{name: "zeta"})
		Register(&mockProvider{name: "alpha"})
		Register(&mockProvider{name: "beta"})

		names := Names()
		if len(names) != 3 {
			t.Fatalf("expected 3 names, got %d", len(names))
		}
		if names[0] != "alpha" || names[1] != "beta" || names[2] != "zeta" {
			t.Errorf("expected sorted names, got %v", names)
		}
	})
}
```

**Step 7: Run tests**

Run: `go test -v ./internal/provider/...`
Expected: PASS

**Step 8: Commit**

```bash
git add internal/provider/
git commit -m "feat(provider): add core interfaces and registry

- CredentialProvider interface for all providers
- AgentProvider interface for Claude/Codex
- EndpointProvider interface for AWS
- Explicit registration via Register()/Get()
- Shared types: Credential, MountConfig, ContainerConfig"
```

---

## Task 2: Shared Utilities

**Files:**
- Create: `internal/provider/util/prompt.go`
- Create: `internal/provider/util/env.go`
- Create: `internal/provider/util/validate.go`
- Create: `internal/provider/util/doc.go`
- Test: `internal/provider/util/env_test.go`

**Step 1: Create package doc**

```go
// internal/provider/util/doc.go

// Package util provides shared utilities for provider implementations.
// Includes helpers for prompting users, checking environment variables,
// and validating token formats.
package util
```

**Step 2: Create env utilities**

```go
// internal/provider/util/env.go

package util

import "os"

// CheckEnvVars returns the value of the first non-empty environment variable.
// Returns empty string if none are set.
func CheckEnvVars(names ...string) string {
	for _, name := range names {
		if val := os.Getenv(name); val != "" {
			return val
		}
	}
	return ""
}

// CheckEnvVarWithName returns the value and name of the first non-empty env var.
// Returns empty strings if none are set.
func CheckEnvVarWithName(names ...string) (value, name string) {
	for _, n := range names {
		if val := os.Getenv(n); val != "" {
			return val, n
		}
	}
	return "", ""
}
```

**Step 3: Create prompt utilities**

```go
// internal/provider/util/prompt.go

package util

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// PromptForToken prompts the user for a token with the given message.
// Input is hidden (not echoed to terminal).
func PromptForToken(prompt string) (string, error) {
	fmt.Print(prompt + ": ")

	// Check if stdin is a terminal
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		bytes, err := term.ReadPassword(fd)
		fmt.Println() // Add newline after hidden input
		if err != nil {
			return "", fmt.Errorf("failed to read token: %w", err)
		}
		return strings.TrimSpace(string(bytes)), nil
	}

	// Fallback for non-terminal (piped input)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read token: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// PromptForChoice displays options and returns the selected index (0-based).
// Returns -1 and error if input is invalid.
func PromptForChoice(prompt string, options []string) (int, error) {
	fmt.Println(prompt)
	for i, opt := range options {
		fmt.Printf("  %d. %s\n", i+1, opt)
	}
	fmt.Print("Enter choice: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return -1, fmt.Errorf("failed to read choice: %w", err)
	}

	var choice int
	if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &choice); err != nil {
		return -1, fmt.Errorf("invalid choice: %w", err)
	}

	if choice < 1 || choice > len(options) {
		return -1, fmt.Errorf("choice %d out of range [1-%d]", choice, len(options))
	}

	return choice - 1, nil
}

// Confirm prompts for yes/no confirmation. Returns true for yes.
func Confirm(prompt string) (bool, error) {
	fmt.Print(prompt + " [y/N]: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("failed to read confirmation: %w", err)
	}

	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
```

**Step 4: Create validation utilities**

```go
// internal/provider/util/validate.go

package util

import (
	"fmt"
	"strings"
)

// ValidateTokenPrefix checks that a token has an expected prefix.
func ValidateTokenPrefix(token, prefix, tokenType string) error {
	if !strings.HasPrefix(token, prefix) {
		return fmt.Errorf("%s must start with %q", tokenType, prefix)
	}
	return nil
}

// ValidateTokenLength checks that a token has a minimum length.
func ValidateTokenLength(token string, minLen int, tokenType string) error {
	if len(token) < minLen {
		return fmt.Errorf("%s must be at least %d characters", tokenType, minLen)
	}
	return nil
}
```

**Step 5: Write tests**

```go
// internal/provider/util/env_test.go

package util

import (
	"os"
	"testing"
)

func TestCheckEnvVars(t *testing.T) {
	// Clear any existing values
	os.Unsetenv("TEST_VAR_A")
	os.Unsetenv("TEST_VAR_B")

	t.Run("returns first set value", func(t *testing.T) {
		os.Setenv("TEST_VAR_B", "value_b")
		defer os.Unsetenv("TEST_VAR_B")

		got := CheckEnvVars("TEST_VAR_A", "TEST_VAR_B")
		if got != "value_b" {
			t.Errorf("expected 'value_b', got %q", got)
		}
	})

	t.Run("returns empty when none set", func(t *testing.T) {
		got := CheckEnvVars("TEST_VAR_A", "TEST_VAR_B")
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("prefers first set", func(t *testing.T) {
		os.Setenv("TEST_VAR_A", "value_a")
		os.Setenv("TEST_VAR_B", "value_b")
		defer os.Unsetenv("TEST_VAR_A")
		defer os.Unsetenv("TEST_VAR_B")

		got := CheckEnvVars("TEST_VAR_A", "TEST_VAR_B")
		if got != "value_a" {
			t.Errorf("expected 'value_a', got %q", got)
		}
	})
}
```

**Step 6: Run tests**

Run: `go test -v ./internal/provider/util/...`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/provider/util/
git commit -m "feat(provider): add shared utilities for providers

- CheckEnvVars for environment variable lookup
- PromptForToken for hidden input
- PromptForChoice for menu selection
- Token validation helpers"
```

---

## Task 3: GitHub Provider

**Files:**
- Create: `internal/providers/github/provider.go`
- Create: `internal/providers/github/grant.go`
- Create: `internal/providers/github/refresh.go`
- Create: `internal/providers/github/doc.go`
- Reference: `internal/github/setup.go` (existing code to migrate)
- Test: `internal/providers/github/provider_test.go`

**Step 1: Create package doc**

```go
// internal/providers/github/doc.go

// Package github implements the GitHub credential provider.
//
// Supports credential acquisition from:
//   - gh CLI (gh auth token)
//   - Environment variables (GITHUB_TOKEN, GH_TOKEN)
//   - Manual PAT entry
//
// Tokens from gh CLI or environment can be refreshed automatically.
// PATs are static and cannot be refreshed.
package github
```

**Step 2: Create provider struct and basic methods**

```go
// internal/providers/github/provider.go

package github

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

const (
	// SourceCLI indicates the token came from gh CLI.
	SourceCLI = "cli"
	// SourceEnv indicates the token came from environment variable.
	SourceEnv = "env"
	// SourcePAT indicates the token was entered manually.
	SourcePAT = "pat"
)

// TokenPlaceholder is a format-valid placeholder that passes gh CLI validation.
// Real token is injected by proxy at network layer.
const TokenPlaceholder = "ghp_moatProxyInjectedPlaceholder000000000000"

// Provider implements provider.CredentialProvider for GitHub.
type Provider struct{}

// New creates a new GitHub provider.
func New() *Provider {
	return &Provider{}
}

// Name returns "github".
func (p *Provider) Name() string {
	return "github"
}

// ConfigureProxy sets Bearer token for GitHub hosts.
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	proxy.SetCredential("api.github.com", "Bearer "+cred.Token)
	proxy.SetCredential("github.com", "Bearer "+cred.Token)
}

// ContainerEnv returns environment variables for the container.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	return []string{
		"GH_TOKEN=" + TokenPlaceholder,
		"GIT_TERMINAL_PROMPT=0",
	}
}

// ContainerMounts copies the user's gh config to the container.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, error) {
	// Check for host gh config
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, nil // No mounts if can't find home
	}

	ghConfigSrc := filepath.Join(homeDir, ".config", "gh", "config.yml")
	if _, err := os.Stat(ghConfigSrc); os.IsNotExist(err) {
		return nil, nil // No config to copy
	}

	// Create temp dir for config copy
	tmpDir, err := os.MkdirTemp("", "moat-gh-")
	if err != nil {
		return nil, err
	}

	// Copy config (hosts are redacted, just aliases/preferences)
	srcData, err := os.ReadFile(ghConfigSrc)
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, err
	}

	ghConfigDir := filepath.Join(tmpDir, ".config", "gh")
	if err := os.MkdirAll(ghConfigDir, 0700); err != nil {
		os.RemoveAll(tmpDir)
		return nil, err
	}

	if err := os.WriteFile(filepath.Join(ghConfigDir, "config.yml"), srcData, 0600); err != nil {
		os.RemoveAll(tmpDir)
		return nil, err
	}

	// Store cleanup path in credential metadata for later
	if cred.Metadata == nil {
		cred.Metadata = make(map[string]string)
	}
	cred.Metadata["cleanup_path"] = tmpDir

	return []provider.MountConfig{
		{
			Source:   filepath.Join(tmpDir, ".config", "gh"),
			Target:   filepath.Join(containerHome, ".config", "gh"),
			ReadOnly: true,
		},
	}, nil
}

// Cleanup removes the temporary config directory.
func (p *Provider) Cleanup(cleanupPath string) {
	if cleanupPath != "" {
		os.RemoveAll(cleanupPath)
	}
}

// ImpliedDependencies returns dependencies for GitHub.
func (p *Provider) ImpliedDependencies() []string {
	return []string{"gh", "git"}
}
```

**Step 3: Create grant logic**

```go
// internal/providers/github/grant.go

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

// Grant acquires GitHub credentials.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	// 1. Try environment variables
	if token, envName := util.CheckEnvVarWithName("GITHUB_TOKEN", "GH_TOKEN"); token != "" {
		cred, err := p.validateAndCreate(ctx, token, SourceEnv)
		if err != nil {
			return nil, &provider.GrantError{
				Provider: "github",
				Cause:    err,
				Hint:     fmt.Sprintf("Token from %s is invalid", envName),
			}
		}
		return cred, nil
	}

	// 2. Try gh CLI
	if token := p.tryGHCLI(); token != "" {
		cred, err := p.validateAndCreate(ctx, token, SourceCLI)
		if err != nil {
			return nil, &provider.GrantError{
				Provider: "github",
				Cause:    err,
				Hint:     "Token from 'gh auth token' is invalid. Try 'gh auth login' to refresh.",
			}
		}
		return cred, nil
	}

	// 3. Prompt for PAT
	fmt.Println("No GitHub token found in environment or gh CLI.")
	fmt.Println("Create a Personal Access Token at: https://github.com/settings/tokens")
	fmt.Println()

	token, err := util.PromptForToken("GitHub Personal Access Token")
	if err != nil {
		return nil, err
	}

	cred, err := p.validateAndCreate(ctx, token, SourcePAT)
	if err != nil {
		return nil, &provider.GrantError{
			Provider: "github",
			Cause:    err,
			Hint:     "Ensure the token has appropriate scopes (repo, read:org recommended)",
		}
	}
	return cred, nil
}

func (p *Provider) tryGHCLI() string {
	cmd := exec.Command("gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (p *Provider) validateAndCreate(ctx context.Context, token, source string) (*provider.Credential, error) {
	// Validate by calling GitHub API
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to validate token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token validation failed: HTTP %d", resp.StatusCode)
	}

	// Extract username for metadata
	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("failed to decode user response: %w", err)
	}

	return &provider.Credential{
		Provider:  "github",
		Token:     token,
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			provider.MetaKeyTokenSource: source,
			"username":                  user.Login,
		},
	}, nil
}
```

**Step 4: Create refresh logic**

```go
// internal/providers/github/refresh.go

package github

import (
	"context"
	"fmt"
	"time"

	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

// CanRefresh reports whether the credential can be refreshed.
// CLI and env tokens can be refreshed; PATs cannot.
func (p *Provider) CanRefresh(cred *provider.Credential) bool {
	if cred == nil || cred.Metadata == nil {
		return false
	}
	source := cred.Metadata[provider.MetaKeyTokenSource]
	return source == SourceCLI || source == SourceEnv
}

// RefreshInterval returns 30 minutes.
func (p *Provider) RefreshInterval() time.Duration {
	return 30 * time.Minute
}

// Refresh re-acquires the token from the original source.
func (p *Provider) Refresh(ctx context.Context, proxy provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	if cred == nil || cred.Metadata == nil {
		return nil, provider.ErrRefreshNotSupported
	}

	source := cred.Metadata[provider.MetaKeyTokenSource]
	var token string

	switch source {
	case SourceCLI:
		token = p.tryGHCLI()
		if token == "" {
			return nil, fmt.Errorf("gh CLI no longer returns a token")
		}
	case SourceEnv:
		token = util.CheckEnvVars("GITHUB_TOKEN", "GH_TOKEN")
		if token == "" {
			return nil, fmt.Errorf("environment variable no longer set")
		}
	default:
		return nil, provider.ErrRefreshNotSupported
	}

	// Update proxy with new token
	proxy.SetCredential("api.github.com", "Bearer "+token)
	proxy.SetCredential("github.com", "Bearer "+token)

	// Return updated credential
	newCred := *cred
	newCred.Token = token
	return &newCred, nil
}
```

**Step 5: Write tests**

```go
// internal/providers/github/provider_test.go

package github

import (
	"os"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestProvider_Name(t *testing.T) {
	p := New()
	if p.Name() != "github" {
		t.Errorf("expected 'github', got %q", p.Name())
	}
}

func TestProvider_ContainerEnv(t *testing.T) {
	p := New()
	env := p.ContainerEnv(&provider.Credential{Token: "test"})

	if len(env) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(env))
	}

	foundToken := false
	foundPrompt := false
	for _, e := range env {
		if e == "GH_TOKEN="+TokenPlaceholder {
			foundToken = true
		}
		if e == "GIT_TERMINAL_PROMPT=0" {
			foundPrompt = true
		}
	}

	if !foundToken {
		t.Error("missing GH_TOKEN env var")
	}
	if !foundPrompt {
		t.Error("missing GIT_TERMINAL_PROMPT env var")
	}
}

func TestProvider_CanRefresh(t *testing.T) {
	p := New()

	tests := []struct {
		name   string
		cred   *provider.Credential
		expect bool
	}{
		{
			name:   "nil credential",
			cred:   nil,
			expect: false,
		},
		{
			name:   "cli source",
			cred:   &provider.Credential{Metadata: map[string]string{provider.MetaKeyTokenSource: SourceCLI}},
			expect: true,
		},
		{
			name:   "env source",
			cred:   &provider.Credential{Metadata: map[string]string{provider.MetaKeyTokenSource: SourceEnv}},
			expect: true,
		},
		{
			name:   "pat source",
			cred:   &provider.Credential{Metadata: map[string]string{provider.MetaKeyTokenSource: SourcePAT}},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.CanRefresh(tt.cred)
			if got != tt.expect {
				t.Errorf("CanRefresh() = %v, want %v", got, tt.expect)
			}
		})
	}
}

func TestProvider_ImpliedDependencies(t *testing.T) {
	p := New()
	deps := p.ImpliedDependencies()

	if len(deps) != 2 {
		t.Fatalf("expected 2 deps, got %d", len(deps))
	}

	hasGh := false
	hasGit := false
	for _, d := range deps {
		if d == "gh" {
			hasGh = true
		}
		if d == "git" {
			hasGit = true
		}
	}

	if !hasGh || !hasGit {
		t.Errorf("expected [gh, git], got %v", deps)
	}
}

func TestProvider_Grant_FromEnv(t *testing.T) {
	// Skip if no network (integration test)
	if os.Getenv("GITHUB_TOKEN") == "" && os.Getenv("GH_TOKEN") == "" {
		t.Skip("No GitHub token in environment")
	}

	p := New()
	cred, err := p.Grant(t.Context())
	if err != nil {
		t.Fatalf("Grant failed: %v", err)
	}

	if cred.Provider != "github" {
		t.Errorf("expected provider 'github', got %q", cred.Provider)
	}
	if cred.Metadata[provider.MetaKeyTokenSource] != SourceEnv {
		t.Errorf("expected source 'env', got %q", cred.Metadata[provider.MetaKeyTokenSource])
	}
}
```

**Step 6: Run tests**

Run: `go test -v ./internal/providers/github/...`
Expected: PASS (some tests may skip without GitHub token)

**Step 7: Commit**

```bash
git add internal/providers/github/
git commit -m "feat(providers): add GitHub provider implementation

- Credential acquisition from gh CLI, env vars, or PAT
- Token refresh for CLI/env sources (30m interval)
- Container mounts for gh config
- Format-valid placeholder token for gh CLI validation"
```

---

## Task 4: AWS Provider

**Files:**
- Create: `internal/providers/aws/provider.go`
- Create: `internal/providers/aws/grant.go`
- Create: `internal/providers/aws/endpoint.go`
- Create: `internal/providers/aws/doc.go`
- Reference: `internal/credential/aws.go` (existing code to migrate)
- Test: `internal/providers/aws/provider_test.go`

**Step 1: Create package doc**

```go
// internal/providers/aws/doc.go

// Package aws implements the AWS credential provider.
//
// Unlike other providers, AWS uses a credential endpoint pattern instead of
// header injection. The container's AWS SDK calls the credential endpoint
// to obtain temporary credentials via STS AssumeRole.
//
// Configuration is stored as:
//   - Credential.Token = RoleARN
//   - Credential.Metadata = {region, session_duration, external_id}
package aws
```

**Step 2: Create provider struct**

```go
// internal/providers/aws/provider.go

package aws

import (
	"context"
	"net/http"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider and provider.EndpointProvider.
type Provider struct{}

// New creates a new AWS provider.
func New() *Provider {
	return &Provider{}
}

// Name returns "aws".
func (p *Provider) Name() string {
	return "aws"
}

// ConfigureProxy is a no-op for AWS (uses endpoint pattern instead).
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	// AWS doesn't use header injection
}

// ContainerEnv returns environment variables pointing to credential endpoint.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	// These are set by the manager when it knows the endpoint URL
	return nil
}

// ContainerMounts returns nil (no mounts needed for AWS).
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, error) {
	return nil, nil
}

// CanRefresh returns false (AWS uses runtime credential fetching).
func (p *Provider) CanRefresh(cred *provider.Credential) bool {
	return false
}

// RefreshInterval returns 0 (refresh not applicable).
func (p *Provider) RefreshInterval() time.Duration {
	return 0
}

// Refresh returns ErrRefreshNotSupported.
func (p *Provider) Refresh(ctx context.Context, proxy provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	return nil, provider.ErrRefreshNotSupported
}

// Cleanup is a no-op for AWS.
func (p *Provider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns ["aws"].
func (p *Provider) ImpliedDependencies() []string {
	return []string{"aws"}
}
```

**Step 3: Create grant logic**

```go
// internal/providers/aws/grant.go

package aws

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

// Config holds AWS IAM role configuration.
type Config struct {
	RoleARN         string
	Region          string
	SessionDuration time.Duration
	ExternalID      string
}

var arnRegex = regexp.MustCompile(`^arn:aws:iam::(\d{12}):role/[\w+=,.@-]+$`)

// Grant prompts for an IAM role ARN and validates it.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	fmt.Println("Enter the IAM role ARN to assume.")
	fmt.Println("Format: arn:aws:iam::ACCOUNT_ID:role/ROLE_NAME")
	fmt.Println()

	arn, err := util.PromptForToken("Role ARN")
	if err != nil {
		return nil, err
	}

	cfg, err := ParseRoleARN(arn)
	if err != nil {
		return nil, &provider.GrantError{
			Provider: "aws",
			Cause:    err,
			Hint:     "Ensure the ARN matches: arn:aws:iam::ACCOUNT_ID:role/ROLE_NAME",
		}
	}

	// Test assume-role
	if err := p.testAssumeRole(ctx, cfg); err != nil {
		return nil, &provider.GrantError{
			Provider: "aws",
			Cause:    err,
			Hint:     "Ensure your AWS credentials can assume this role and the trust policy allows it.",
		}
	}

	return &provider.Credential{
		Provider:  "aws",
		Token:     cfg.RoleARN,
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			"region":           cfg.Region,
			"session_duration": cfg.SessionDuration.String(),
		},
	}, nil
}

// ParseRoleARN validates and parses an IAM role ARN.
func ParseRoleARN(arn string) (*Config, error) {
	if !arnRegex.MatchString(arn) {
		return nil, fmt.Errorf("invalid role ARN format: %s", arn)
	}

	return &Config{
		RoleARN:         arn,
		Region:          "us-east-1", // Default
		SessionDuration: 15 * time.Minute,
	}, nil
}

func (p *Provider) testAssumeRole(ctx context.Context, cfg *Config) error {
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := sts.NewFromConfig(awsCfg)
	sessionName := "moat-grant-test"
	durationSecs := int32(cfg.SessionDuration.Seconds())

	_, err = client.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         &cfg.RoleARN,
		RoleSessionName: &sessionName,
		DurationSeconds: &durationSecs,
	})
	if err != nil {
		return fmt.Errorf("assume-role failed: %w", err)
	}

	return nil
}
```

**Step 4: Create endpoint handler**

```go
// internal/providers/aws/endpoint.go

package aws

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/majorcontext/moat/internal/provider"
)

// RegisterEndpoints registers the AWS credential endpoint.
func (p *Provider) RegisterEndpoints(mux *http.ServeMux, cred *provider.Credential) {
	cfg := &Config{
		RoleARN:         cred.Token,
		Region:          cred.Metadata["region"],
		SessionDuration: 15 * time.Minute,
	}
	if dur, err := time.ParseDuration(cred.Metadata["session_duration"]); err == nil {
		cfg.SessionDuration = dur
	}

	handler := &credentialHandler{cfg: cfg}
	mux.Handle("/aws-credentials", handler)
}

type credentialHandler struct {
	cfg *Config
}

type credentialResponse struct {
	AccessKeyId     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	Token           string `json:"Token"`
	Expiration      string `json:"Expiration"`
}

func (h *credentialHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(h.cfg.Region))
	if err != nil {
		http.Error(w, "failed to load AWS config", http.StatusInternalServerError)
		return
	}

	client := sts.NewFromConfig(awsCfg)
	sessionName := "moat-container"
	durationSecs := int32(h.cfg.SessionDuration.Seconds())

	result, err := client.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         &h.cfg.RoleARN,
		RoleSessionName: &sessionName,
		DurationSeconds: &durationSecs,
	})
	if err != nil {
		http.Error(w, "assume-role failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := credentialResponse{
		AccessKeyId:     *result.Credentials.AccessKeyId,
		SecretAccessKey: *result.Credentials.SecretAccessKey,
		Token:           *result.Credentials.SessionToken,
		Expiration:      result.Credentials.Expiration.Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
```

**Step 5: Write tests**

```go
// internal/providers/aws/provider_test.go

package aws

import (
	"testing"
)

func TestProvider_Name(t *testing.T) {
	p := New()
	if p.Name() != "aws" {
		t.Errorf("expected 'aws', got %q", p.Name())
	}
}

func TestProvider_ImpliedDependencies(t *testing.T) {
	p := New()
	deps := p.ImpliedDependencies()

	if len(deps) != 1 || deps[0] != "aws" {
		t.Errorf("expected [aws], got %v", deps)
	}
}

func TestParseRoleARN(t *testing.T) {
	tests := []struct {
		name    string
		arn     string
		wantErr bool
	}{
		{
			name:    "valid ARN",
			arn:     "arn:aws:iam::123456789012:role/MyRole",
			wantErr: false,
		},
		{
			name:    "invalid format",
			arn:     "not-an-arn",
			wantErr: true,
		},
		{
			name:    "wrong service",
			arn:     "arn:aws:s3::123456789012:bucket/mybucket",
			wantErr: true,
		},
		{
			name:    "missing role name",
			arn:     "arn:aws:iam::123456789012:role/",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseRoleARN(tt.arn)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if cfg.RoleARN != tt.arn {
					t.Errorf("expected RoleARN %q, got %q", tt.arn, cfg.RoleARN)
				}
			}
		})
	}
}

func TestProvider_CanRefresh(t *testing.T) {
	p := New()
	if p.CanRefresh(nil) {
		t.Error("expected CanRefresh to return false")
	}
}
```

**Step 6: Run tests**

Run: `go test -v ./internal/providers/aws/...`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/providers/aws/
git commit -m "feat(providers): add AWS provider implementation

- EndpointProvider for credential endpoint pattern
- Role ARN validation and STS assume-role testing
- Credential endpoint handler for container SDK"
```

---

## Task 5: Claude Provider

**Files:**
- Create: `internal/providers/claude/provider.go`
- Create: `internal/providers/claude/grant.go`
- Create: `internal/providers/claude/agent.go`
- Create: `internal/providers/claude/cli.go`
- Create: `internal/providers/claude/session.go`
- Create: `internal/providers/claude/config.go`
- Create: `internal/providers/claude/oauth_workarounds.go`
- Create: `internal/providers/claude/doc.go`
- Reference: `internal/claude/` (existing code to migrate)
- Test: `internal/providers/claude/provider_test.go`

This is a larger provider. Key points:
- Implements both CredentialProvider and AgentProvider
- Has OAuth workarounds for 403 responses
- Generates `.claude.json` config
- Manages staging directories
- Has CLI commands (sessions, plugins)

**Step 1: Create package doc**

```go
// internal/providers/claude/doc.go

// Package claude implements the Claude Code credential and agent provider.
//
// Supports credential acquisition via:
//   - claude setup-token (OAuth)
//   - API key (ANTHROPIC_API_KEY or manual entry)
//   - Import from existing Claude Code installation
//
// As an agent provider, handles:
//   - Staging directory with .claude.json config
//   - Session management
//   - CLI commands (moat claude, sessions, plugins)
//
// OAuth tokens have special workarounds for 403 responses on
// non-critical endpoints (/api/oauth/profile, /api/oauth/usage).
package claude
```

**Due to length, I'll outline the remaining steps for Task 5:**

**Step 2:** Create `provider.go` with Provider struct implementing CredentialProvider basics (Name, ConfigureProxy, ContainerEnv, ContainerMounts, CanRefresh, RefreshInterval, Refresh, Cleanup, ImpliedDependencies)

**Step 3:** Create `grant.go` with Grant() implementing:
- Menu for OAuth vs API key vs import
- `claude setup-token` execution and token extraction
- API key validation via Anthropic API
- Import from ~/.claude/.credentials.json

**Step 4:** Create `agent.go` with AgentProvider methods:
- PrepareContainer() - creates staging dir, writes config
- Sessions() - lists sessions
- ResumeSession() - resumes by ID

**Step 5:** Create `config.go` with config generation:
- WriteClaudeConfig() - generates .claude.json
- ReadHostConfig() - reads allowlisted fields from host
- HostConfigAllowlist constant

**Step 6:** Create `oauth_workarounds.go`:
- CreateOAuthEndpointTransformer() - returns 200 for 403 on specific endpoints

**Step 7:** Create `cli.go` with RegisterCLI():
- `moat claude` command
- `moat claude sessions` subcommand
- `moat claude plugins` subcommand

**Step 8:** Create `session.go` with session management logic

**Step 9:** Write `provider_test.go`

**Step 10:** Run tests

**Step 11:** Commit

---

## Task 6: Codex Provider

**Files:**
- Create: `internal/providers/codex/provider.go`
- Create: `internal/providers/codex/grant.go`
- Create: `internal/providers/codex/agent.go`
- Create: `internal/providers/codex/cli.go`
- Create: `internal/providers/codex/session.go`
- Create: `internal/providers/codex/config.go`
- Create: `internal/providers/codex/doc.go`
- Reference: `internal/codex/` (existing code to migrate)
- Test: `internal/providers/codex/provider_test.go`

Similar structure to Claude but simpler (no OAuth workarounds).

---

## Task 7: Provider Registration

**Files:**
- Create: `internal/providers/register.go`
- Modify: `cmd/moat/main.go`

**Step 1: Create registration file**

```go
// internal/providers/register.go

// Package providers registers all credential and agent providers.
package providers

import (
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/providers/aws"
	"github.com/majorcontext/moat/internal/providers/claude"
	"github.com/majorcontext/moat/internal/providers/codex"
	"github.com/majorcontext/moat/internal/providers/github"
)

// RegisterAll registers all providers with the registry.
// Call this once at startup before using any providers.
func RegisterAll() {
	provider.Register(github.New())
	provider.Register(aws.New())
	provider.Register(claude.New())
	provider.Register(codex.New())
}
```

**Step 2: Update main.go**

Add call to `providers.RegisterAll()` before CLI execution.

**Step 3: Run full test suite**

Run: `go test ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/providers/register.go cmd/moat/main.go
git commit -m "feat(providers): add explicit provider registration

RegisterAll() called at startup to register all providers.
Replaces init() magic with explicit registration."
```

---

## Task 8: Update Manager

**Files:**
- Modify: `internal/run/manager.go`

**Goal:** Remove hardcoded imports of `internal/claude` and `internal/codex`. Use only `internal/provider` interfaces.

**Key changes:**
1. Remove imports: `internal/claude`, `internal/codex`
2. Replace `credential.GetProviderSetup()` with `provider.Get()`
3. Replace special Claude/Codex staging dir handling with `agent.PrepareContainer()`
4. Replace `if provider == "anthropic"` blocks with interface calls

This is a large file (~2500 lines). Work incrementally:
- First: Update provider lookup to use new registry
- Then: Replace staging dir handling
- Then: Remove special cases
- Finally: Clean up unused imports

---

## Task 9: Update CLI Grant Command

**Files:**
- Modify: `cmd/moat/cli/grant.go`

**Goal:** Replace switch-based dispatch with registry lookup.

**Key changes:**
1. Remove: `grantGitHub()`, `grantAnthropic()`, `grantOpenAI()`, `grantAWS()`
2. Remove: Token extraction helpers (move to claude provider)
3. Add: Generic grant using `provider.Get(name).Grant(ctx)`
4. Keep: `saveCredential()`, basic error handling

---

## Task 10: Agent CLI Registration

**Files:**
- Modify: `cmd/moat/cli/root.go`
- Delete: `cmd/moat/cli/claude.go`
- Delete: `cmd/moat/cli/claude_run.go`
- Delete: `cmd/moat/cli/claude_sessions.go`
- Delete: `cmd/moat/cli/codex.go`
- Delete: `cmd/moat/cli/codex_run.go`

**Goal:** Agent providers register their own CLI commands.

**Key changes:**
1. In root.go, iterate `provider.Agents()` and call `RegisterCLI(root)`
2. Remove hardcoded claude/codex CLI files
3. Provider packages now own their CLI commands

---

## Task 11: Cleanup Old Code

**Files:**
- Delete: `internal/claude/` (entire directory)
- Delete: `internal/codex/` (entire directory)
- Delete: `internal/github/` (entire directory)
- Delete: `internal/credential/claude.go`
- Delete: `internal/credential/anthropic.go`
- Delete: `internal/credential/openai.go`
- Delete: `internal/credential/aws.go` (most of it)
- Delete: `internal/providers/register.go` (old blank import version)
- Update: `internal/credential/types.go` - remove provider-specific code

**Verify:** Run full test suite after each deletion to catch missing dependencies.

---

## Task 12: Final Verification

**Steps:**
1. Run: `go build ./...`
2. Run: `go test ./...`
3. Run: `make lint`
4. Test manually:
   - `moat grant github`
   - `moat grant anthropic`
   - `moat claude --help`
   - `moat codex --help`
5. Commit any fixes

**Final commit:**
```bash
git commit -m "refactor(providers): complete provider isolation

- All providers in internal/providers/{github,aws,claude,codex}
- Manager imports only internal/provider interfaces
- CLI uses registry for grant and agent commands
- Removed old provider locations"
```

---

## Parallelization Guide for Subagents

**Wave 1 (sequential):**
- Task 1: Core interfaces (must complete first)
- Task 2: Shared utilities (depends on Task 1)

**Wave 2 (parallel - 4 subagents):**
- Task 3: GitHub provider
- Task 4: AWS provider
- Task 5: Claude provider
- Task 6: Codex provider

**Wave 3 (sequential):**
- Task 7: Registration (waits for all providers)

**Wave 4 (parallel - 2 subagents):**
- Task 8: Update manager
- Task 9: Update CLI grant

**Wave 5 (sequential):**
- Task 10: Agent CLI registration
- Task 11: Cleanup
- Task 12: Final verification
