# Gemini Provider Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Port the Gemini CLI integration from `feat/gemini-cli` to `main`, rewritten to follow the new provider framework patterns established in the `internal/provider/` + `internal/providers/` refactor.

**Architecture:** Create `internal/providers/gemini/` as a new `AgentProvider` implementation, following the patterns of `internal/providers/claude/` and `internal/providers/codex/`. The Gemini business logic (OAuth auth, API key validation, token refresh, settings generation, proxy token substitution) is preserved from the original branch; only the structural wiring changes to use the provider interfaces.

**Tech Stack:** Go, Cobra CLI, Google OAuth2, provider framework (`internal/provider/`)

---

## Pre-requisite: Start from main

Before starting, create a new branch from `main`:

```bash
git checkout main && git pull && git checkout -b feat/gemini-provider
```

---

### Task 1: Add proxy token substitution and header removal

The Gemini provider requires two proxy features that don't exist on `main`:
1. `SetTokenSubstitution(host, placeholder, realToken)` — replaces placeholder tokens in request bodies/headers
2. `RemoveRequestHeader(host, headerName)` — strips client-sent headers

These are needed because Gemini CLI validates its OAuth token by POSTing to `oauth2.googleapis.com/tokeninfo` with the placeholder from `oauth_creds.json`. The proxy must substitute the real token before forwarding.

**Files:**
- Modify: `internal/credential/provider.go` (add methods to `ProxyConfigurer` interface)
- Modify: `internal/proxy/proxy.go` (implement the methods)

**Step 1: Add methods to ProxyConfigurer interface**

In `internal/credential/provider.go`, add to the `ProxyConfigurer` interface:

```go
// RemoveRequestHeader removes a client-sent header before forwarding.
// Used when injected credentials conflict with client headers.
RemoveRequestHeader(host, headerName string)

// SetTokenSubstitution replaces placeholder tokens with real tokens in both
// Authorization headers and request bodies for a specific host.
// Body substitution is limited to 64KB requests to avoid memory issues.
SetTokenSubstitution(host, placeholder, realToken string)
```

**Step 2: Add data structures and implement methods in proxy**

In `internal/proxy/proxy.go`, add:

```go
// tokenSubstitution maps a placeholder string to the real token for a host.
type tokenSubstitution struct {
	placeholder string
	realToken   string
}
```

Add fields to the `Proxy` struct:

```go
removeHeaders      map[string][]string            // host -> []headerName
tokenSubstitutions map[string]*tokenSubstitution   // host -> substitution
```

Initialize in `NewProxy()`:

```go
removeHeaders:      make(map[string][]string),
tokenSubstitutions: make(map[string]*tokenSubstitution),
```

Implement `RemoveRequestHeader`:

```go
func (p *Proxy) RemoveRequestHeader(host, headerName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.removeHeaders[host] = append(p.removeHeaders[host], headerName)
}
```

Implement `SetTokenSubstitution`:

```go
func (p *Proxy) SetTokenSubstitution(host, placeholder, realToken string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tokenSubstitutions[host] = &tokenSubstitution{
		placeholder: placeholder,
		realToken:   realToken,
	}
}
```

Add helper `getTokenSubstitution`:

```go
func (p *Proxy) getTokenSubstitution(host string) *tokenSubstitution {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.tokenSubstitutions[host]
}
```

Add helper `getRemoveHeaders`:

```go
func (p *Proxy) getRemoveHeaders(host string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.removeHeaders[host]
}
```

Add `applyTokenSubstitution` that:
- Reads the request body (up to 64KB limit)
- Replaces placeholder with real token in body using `strings.ReplaceAll`
- Also replaces in `Authorization` header
- Returns the modified request

Integrate into `handleHTTP` and `handleConnectWithInterception`:
- Before forwarding, call `getRemoveHeaders` and delete matching headers
- Before forwarding, call `getTokenSubstitution` and `applyTokenSubstitution`

**Step 3: Run tests**

Run: `go test ./internal/proxy/ -v`
Run: `go build ./...`

**Step 4: Commit**

```bash
git add internal/credential/provider.go internal/proxy/proxy.go
git commit -m "feat(proxy): add token substitution and header removal support"
```

---

### Task 2: Add `ProviderGemini` to credential types

**Files:**
- Modify: `internal/credential/types.go`

**Step 1: Add the constant**

```go
ProviderGemini Provider = "gemini"
```

Add it to `KnownProviders()` and `IsKnownProvider()`.

**Step 2: Run tests**

Run: `go test ./internal/credential/ -v`

**Step 3: Commit**

```bash
git add internal/credential/types.go
git commit -m "feat(credential): add gemini provider type"
```

---

### Task 3: Add GeminiConfig to agent.yaml config

**Files:**
- Modify: `internal/config/config.go`

**Step 1: Add GeminiConfig struct and field**

Add after `CodexConfig`:

```go
// GeminiConfig holds Gemini-specific configuration.
type GeminiConfig struct {
	// SyncLogs enables syncing Gemini session logs from container to host.
	SyncLogs *bool                      `yaml:"sync_logs,omitempty"`
	// MCP defines local MCP server configurations for Gemini.
	MCP      map[string]MCPServerSpec   `yaml:"mcp,omitempty"`
}
```

Add field to `Config`:

```go
Gemini GeminiConfig `yaml:"gemini,omitempty"`
```

Add method:

```go
// ShouldSyncGeminiLogs returns whether Gemini logs should be synced.
// Defaults to true if "gemini" is in the grants list.
func (c *Config) ShouldSyncGeminiLogs() bool {
	if c.Gemini.SyncLogs != nil {
		return *c.Gemini.SyncLogs
	}
	for _, g := range c.Grants {
		if g == "gemini" {
			return true
		}
	}
	return false
}
```

Add Gemini MCP validation in `Load()` (same pattern as Claude/Codex MCP validation):

```go
for name, spec := range cfg.Gemini.MCP {
	if err := validateMCPServerSpec(name, spec); err != nil {
		return nil, err
	}
}
```

**Step 2: Run tests**

Run: `go test ./internal/config/ -v`

**Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add gemini section to agent.yaml"
```

---

### Task 4: Create the Gemini provider package — core types and registration

Create the new provider package with core types, settings structs, and provider registration.

**Files:**
- Create: `internal/providers/gemini/doc.go`
- Create: `internal/providers/gemini/constants.go`
- Create: `internal/providers/gemini/settings.go`

**Step 1: Create doc.go**

```go
// Package gemini provides Google Gemini CLI integration for Moat.
//
// This package handles:
//   - Credential injection through proxy configuration
//   - Configuration file generation for Gemini CLI
//   - Session management for Gemini runs
//   - Background OAuth token refresh
//
// Authentication:
//   - API Key: via x-goog-api-key header to generativelanguage.googleapis.com
//   - OAuth: via Bearer token to cloudcode-pa.googleapis.com (Cloud Code Private API)
//
// Credentials are handled via proxy injection — containers never see real tokens.
package gemini
```

**Step 2: Create constants.go**

```go
package gemini

import "github.com/majorcontext/moat/internal/credential"

const (
	// GeminiInitMountPath is where the staging directory is mounted in containers.
	GeminiInitMountPath = "/moat/gemini-init"

	// GeminiAPIHost is the API endpoint used by Gemini CLI in OAuth mode.
	GeminiAPIHost = "cloudcode-pa.googleapis.com"

	// GeminiOAuthHost is Google's OAuth2 endpoint.
	GeminiOAuthHost = "oauth2.googleapis.com"

	// ProxyInjectedPlaceholder is the placeholder for proxy-injected credentials.
	ProxyInjectedPlaceholder = credential.ProxyInjectedPlaceholder

	// OAuthClientID is the public OAuth client ID used by Gemini CLI.
	// Source: @google/gemini-cli-core npm package, code_assist/oauth2.ts
	// This is an installed/desktop OAuth application credential — safe to embed.
	// See: https://developers.google.com/identity/protocols/oauth2#installed
	OAuthClientID = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"

	// OAuthClientSecret is the public OAuth client secret used by Gemini CLI.
	// Source: same as OAuthClientID. Per Google's docs, this is not treated as secret
	// for installed applications.
	OAuthClientSecret = "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"

	// OAuthTokenURL is Google's OAuth2 token endpoint.
	OAuthTokenURL = "https://oauth2.googleapis.com/token"

	// ModelsURL is the Gemini API models endpoint for key validation.
	ModelsURL = "https://generativelanguage.googleapis.com/v1beta/models"

	// CredentialsFile is the path to Gemini CLI's OAuth credentials relative to home.
	CredentialsFile = ".gemini/oauth_creds.json"
)
```

**Step 3: Create settings.go**

```go
package gemini

// Settings represents Gemini CLI settings.json structure.
type Settings struct {
	Security SecuritySettings `json:"security"`
}

// SecuritySettings holds security-related settings.
type SecuritySettings struct {
	Auth AuthSettings `json:"auth"`
}

// AuthSettings holds authentication configuration.
type AuthSettings struct {
	SelectedType string `json:"selectedType"` // "oauth-personal", "gemini-api-key"
}

// OAuthCreds represents the ~/.gemini/oauth_creds.json file structure.
type OAuthCreds struct {
	AccessToken  string `json:"access_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	IDToken      string `json:"id_token,omitempty"`
	ExpiryDate   int64  `json:"expiry_date"`
	RefreshToken string `json:"refresh_token"`
}
```

**Step 4: Run build**

Run: `go build ./internal/providers/gemini/`

**Step 5: Commit**

```bash
git add internal/providers/gemini/
git commit -m "feat(gemini): add provider package with types and constants"
```

---

### Task 5: Create the Gemini provider — auth and token refresh

Port the authentication logic and token refresh from the old branch.

**Files:**
- Create: `internal/providers/gemini/auth.go`
- Create: `internal/providers/gemini/cli_credentials.go`
- Create: `internal/providers/gemini/token_refresh.go`
- Create: `internal/providers/gemini/credential_refresh.go`

**Step 1: Create auth.go**

Port from `internal/gemini/auth.go`. The key changes:
- Change package from `gemini` to `gemini` (same, but in new location)
- Use `provider.Credential` instead of `credential.Credential` for `CreateCredential` and `CreateOAuthCredential`
- Update import paths

The auth struct handles:
- `PromptForAPIKey()` — interactive prompt
- `ValidateKey(ctx, apiKey)` — validates via Gemini API models endpoint
- `CreateCredential(apiKey)` — creates API key credential
- `CreateOAuthCredential(accessToken, refreshToken, expiresAt)` — creates OAuth credential

`IsOAuthCredential(cred)` helper function checks `Metadata["auth_type"] == "oauth"` — this should accept both `*provider.Credential` and `*credential.Credential` via a small helper.

**Step 2: Create cli_credentials.go**

Port from `internal/gemini/cli_credentials.go`. Handles reading `~/.gemini/oauth_creds.json`.
- `CLIOAuthToken` struct with `AccessToken`, `RefreshToken`, `Scope`, `TokenType`, `ExpiryDate`
- `CLICredentials.GetCredentials()` — reads and parses the file
- `CLICredentials.HasCredentials()` — checks if file exists
- `CLICredentials.CreateMoatCredential(token)` — converts to `provider.Credential`

**Step 3: Create token_refresh.go**

Port from `internal/gemini/token_refresh.go`:
- `OAuthError` type with `Code`, `Description`, `IsRevoked()`
- `TokenRefresher` with `Refresh(ctx, refreshToken) (*RefreshResult, error)`
- `RefreshResult` with `AccessToken`, `ExpiresAt`

**Step 4: Create credential_refresh.go**

Port from `internal/gemini/credential_refresh.go`:
- `CredentialRefresher` (renamed from `CredentialProvider` to avoid confusion with the provider interface) that manages background OAuth token refresh
- Implements `io.Closer`
- `NewCredentialRefresher(accessToken, refreshToken, expiresAt, proxy)`
- `Start()` launches goroutine
- `Close()` stops it
- Background loop refreshes 5 min before expiry with exponential backoff
- On revocation (`invalid_grant`), stops permanently

**Step 5: Run build**

Run: `go build ./internal/providers/gemini/`

**Step 6: Commit**

```bash
git add internal/providers/gemini/
git commit -m "feat(gemini): add auth, CLI credentials, and token refresh"
```

---

### Task 6: Create the Gemini provider — tests for auth and token refresh

**Files:**
- Create: `internal/providers/gemini/auth_test.go`
- Create: `internal/providers/gemini/cli_credentials_test.go`
- Create: `internal/providers/gemini/token_refresh_test.go`
- Create: `internal/providers/gemini/credential_refresh_test.go`
- Create: `internal/providers/gemini/helpers_test.go`

Port all test files from the old branch, updating:
- Package name to `gemini`
- Import paths
- Type references (`credential.Credential` → `provider.Credential` where applicable)
- `CredentialProvider` → `CredentialRefresher` (the renamed type)

The `helpers_test.go` provides `mockProxyConfigurer` for testing.

**Step 1: Create all test files**

Port from old branch with necessary adjustments.

**Step 2: Run tests**

Run: `go test ./internal/providers/gemini/ -v`
Expected: All tests pass.

**Step 3: Commit**

```bash
git add internal/providers/gemini/
git commit -m "test(gemini): add tests for auth, credentials, and token refresh"
```

---

### Task 7: Create the Gemini provider — provider.go (CredentialProvider + AgentProvider)

This is the core provider implementation.

**Files:**
- Create: `internal/providers/gemini/provider.go`

**Step 1: Implement the provider**

```go
package gemini

import (
	"context"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider and provider.AgentProvider
// for Google Gemini CLI credentials.
type Provider struct{}

var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.AgentProvider     = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
	provider.RegisterAlias("google", "gemini")
}

func (p *Provider) Name() string { return "gemini" }

func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	if isOAuthProviderCredential(cred) {
		proxy.SetCredential(GeminiAPIHost, "Bearer "+cred.Token)
		proxy.SetTokenSubstitution(GeminiOAuthHost, ProxyInjectedPlaceholder, cred.Token)
	} else {
		proxy.SetCredentialHeader("generativelanguage.googleapis.com", "x-goog-api-key", cred.Token)
	}
}

func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	if isOAuthProviderCredential(cred) {
		return nil
	}
	return []string{"GEMINI_API_KEY=" + ProxyInjectedPlaceholder}
}

func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

func (p *Provider) CanRefresh(cred *provider.Credential) bool {
	return isOAuthProviderCredential(cred)
}

func (p *Provider) RefreshInterval() time.Duration {
	return 45 * time.Minute // Google OAuth tokens expire after 1 hour
}

func (p *Provider) Refresh(ctx context.Context, proxy provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	if !isOAuthProviderCredential(cred) {
		return nil, provider.ErrRefreshNotSupported
	}
	refreshToken := cred.Metadata["refresh_token"]
	if refreshToken == "" {
		return nil, provider.ErrRefreshNotSupported
	}
	refresher := &TokenRefresher{}
	result, err := refresher.Refresh(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	proxy.SetCredential(GeminiAPIHost, "Bearer "+result.AccessToken)
	proxy.SetTokenSubstitution(GeminiOAuthHost, ProxyInjectedPlaceholder, result.AccessToken)
	newCred := *cred
	newCred.Token = result.AccessToken
	newCred.ExpiresAt = result.ExpiresAt
	return &newCred, nil
}

func (p *Provider) Cleanup(cleanupPath string) {}

func (p *Provider) ImpliedDependencies() []string { return nil }

// isOAuthProviderCredential checks if a provider.Credential is OAuth.
func isOAuthProviderCredential(cred *provider.Credential) bool {
	return cred != nil && cred.Metadata != nil && cred.Metadata["auth_type"] == "oauth"
}
```

**Step 2: Run build**

Run: `go build ./internal/providers/gemini/`

**Step 3: Commit**

```bash
git add internal/providers/gemini/provider.go
git commit -m "feat(gemini): implement CredentialProvider interface"
```

---

### Task 8: Create the Gemini provider — grant.go

**Files:**
- Create: `internal/providers/gemini/grant.go`

**Step 1: Implement Grant**

Port grant logic from `cmd/moat/cli/grant.go` (the `grantGemini`, `grantGeminiViaAPIKey`, `grantGeminiViaExistingCreds` functions) into a `Grant` method on the `Provider`.

```go
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	// Check GEMINI_API_KEY env var first
	// Check for existing Gemini CLI credentials
	// Offer menu if both options available
	// Validate and return provider.Credential
}
```

Also add `HasCredential() bool` helper (same pattern as codex).

**Step 2: Run build**

Run: `go build ./internal/providers/gemini/`

**Step 3: Commit**

```bash
git add internal/providers/gemini/grant.go
git commit -m "feat(gemini): implement credential grant flow"
```

---

### Task 9: Create the Gemini provider — agent.go (PrepareContainer, Sessions, ResumeSession)

**Files:**
- Create: `internal/providers/gemini/agent.go`

**Step 1: Implement AgentProvider methods**

Follow the Codex pattern:

```go
func (p *Provider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	// Create temp staging directory
	// Write settings.json (auth type based on credential)
	// For OAuth: write oauth_creds.json with placeholder tokens
	// Return ContainerConfig with mounts, env, cleanup
}

func (p *Provider) Sessions() ([]provider.Session, error) { ... }
func (p *Provider) ResumeSession(id string) error { ... }
```

Port the staging directory logic from old `internal/gemini/provider.go` (`PopulateStagingDir`, `WriteGeminiConfig`, `writeSettings`).

**Step 2: Run build**

Run: `go build ./internal/providers/gemini/`

**Step 3: Commit**

```bash
git add internal/providers/gemini/agent.go
git commit -m "feat(gemini): implement AgentProvider (PrepareContainer, sessions)"
```

---

### Task 10: Create the Gemini provider — provider_test.go

**Files:**
- Create: `internal/providers/gemini/provider_test.go`

**Step 1: Write tests**

Port from old branch's `provider_test.go`, adapting:
- `ConfigureProxy` tests for both OAuth and API key
- `ContainerEnv` tests
- `PopulateStagingDir` tests (now via `PrepareContainer`)
- Update type references to `provider.Credential`

**Step 2: Run tests**

Run: `go test ./internal/providers/gemini/ -v`

**Step 3: Commit**

```bash
git add internal/providers/gemini/provider_test.go
git commit -m "test(gemini): add provider tests"
```

---

### Task 11: Create the Gemini provider — cli.go (RegisterCLI)

**Files:**
- Create: `internal/providers/gemini/cli.go`

**Step 1: Implement RegisterCLI**

Follow the Codex/Claude pattern. Port from old `cmd/moat/cli/gemini.go` and `gemini_run.go`:

```go
func (p *Provider) RegisterCLI(root *cobra.Command) {
	geminiCmd := &cobra.Command{
		Use:   "gemini [workspace] [flags]",
		Short: "Run Google Gemini CLI in an isolated container",
		RunE:  runGemini,
	}
	cli.AddExecFlags(geminiCmd, &geminiFlags)
	geminiCmd.Flags().StringVarP(&geminiPromptFlag, "prompt", "p", "", "...")
	geminiCmd.Flags().StringSliceVar(&geminiAllowedHosts, "allow-host", nil, "...")

	sessionsCmd := &cobra.Command{...}
	geminiCmd.AddCommand(sessionsCmd)
	root.AddCommand(geminiCmd)
}
```

Key: use `cli.ExecuteRun` function pointer (same as Claude/Codex) to call `cmd/moat/cli.ExecuteRun` without import cycles.

`NetworkHosts()` returns: `"generativelanguage.googleapis.com"`, `"*.googleapis.com"`, `"oauth2.googleapis.com"`

`DefaultDependencies()` returns: `"node@20"`, `"git"`, `"gemini-cli"`

**Step 2: Run build**

Run: `go build ./internal/providers/gemini/`

**Step 3: Commit**

```bash
git add internal/providers/gemini/cli.go
git commit -m "feat(gemini): add CLI commands (moat gemini, moat gemini sessions)"
```

---

### Task 12: Create the Gemini provider — session.go and config.go

**Files:**
- Create: `internal/providers/gemini/session.go`
- Create: `internal/providers/gemini/config.go`

**Step 1: Create session.go**

Port from old `internal/gemini/session.go` — wraps `session.Manager`:

```go
type SessionManager struct { *session.Manager }
func NewSessionManager() (*SessionManager, error) { ... }
func DefaultSessionDir() (string, error) { ... }
```

**Step 2: Create config.go**

Port from old `internal/gemini/generate.go` — MCP config generation:

```go
func GenerateMCPConfig(cfg *config.Config, grants []string) ([]byte, error) { ... }
func WriteMCPConfig(workspaceDir string, mcpJSON []byte) error { ... }
```

**Step 3: Run build**

Run: `go build ./internal/providers/gemini/`

**Step 4: Commit**

```bash
git add internal/providers/gemini/session.go internal/providers/gemini/config.go
git commit -m "feat(gemini): add session management and MCP config generation"
```

---

### Task 13: Register the Gemini provider

**Files:**
- Modify: `internal/providers/register.go`

**Step 1: Add blank import**

```go
_ "github.com/majorcontext/moat/internal/providers/gemini" // registers Gemini/Google provider
```

**Step 2: Run build**

Run: `go build ./...`

**Step 3: Commit**

```bash
git add internal/providers/register.go
git commit -m "feat(gemini): register gemini provider"
```

---

### Task 14: Update grant CLI for Gemini

**Files:**
- Modify: `cmd/moat/cli/grant.go`

**Step 1: Update help text and name mapping**

Add `gemini` to the supported providers list in the `Long` description. Add name mapping:

```go
case "google":
	providerName = "gemini"
```

Add examples for Gemini in the help text.

**Step 2: Run tests**

Run: `go test ./cmd/moat/cli/ -v`

**Step 3: Commit**

```bash
git add cmd/moat/cli/grant.go
git commit -m "feat(cli): add gemini to grant command"
```

---

### Task 15: Update image resolver and Dockerfile builder for Gemini

**Files:**
- Modify: `internal/image/resolver.go`
- Modify: `internal/deps/dockerfile.go`

**Step 1: Add NeedsGeminiInit**

In `internal/image/resolver.go`, add `NeedsGeminiInit bool` to `ResolveOptions` and include in `needsCustomImage` check:

```go
needsCustomImage := len(depList) > 0 || opts.NeedsSSH || opts.NeedsClaudeInit || opts.NeedsCodexInit || opts.NeedsGeminiInit || len(opts.ClaudePlugins) > 0
```

Pass to `deps.ImageTagOptions`.

In `internal/deps/dockerfile.go`, add `NeedsGeminiInit bool` to `DockerfileOptions` and include in the `needsInit` check.

**Step 2: Run tests**

Run: `go test ./internal/image/ ./internal/deps/ -v`

**Step 3: Commit**

```bash
git add internal/image/resolver.go internal/deps/dockerfile.go
git commit -m "feat(image): support gemini init in image resolution"
```

---

### Task 16: Update run manager for Gemini provider

**Files:**
- Modify: `internal/run/manager.go`
- Modify: `internal/run/run.go`

**Step 1: Add GeminiConfigTempDir to Run struct**

In `internal/run/run.go`, add:

```go
GeminiConfigTempDir string
```

**Step 2: Add Gemini init detection in manager.go**

Follow the exact pattern of Claude/Codex init detection. After the `needsCodexInit` block (~line 1086), add:

```go
// Determine if we need Gemini init
var needsGeminiInit bool
for _, grant := range opts.Grants {
	providerName := credential.Provider(strings.Split(grant, ":")[0])
	if providerName == credential.ProviderGemini {
		key, keyErr := credential.DefaultEncryptionKey()
		if keyErr == nil {
			store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
			if storeErr == nil {
				if _, err := store.Get(providerName); err == nil {
					needsGeminiInit = true
				}
			}
		}
		break
	}
}
```

**Step 3: Pass NeedsGeminiInit to image resolver**

Add to the `image.Resolve` call:

```go
NeedsGeminiInit: needsGeminiInit,
```

And to the `deps.GenerateDockerfile` call:

```go
NeedsGeminiInit: needsGeminiInit,
```

And to the `needsCustomImage` check:

```go
needsCustomImage := ... || needsGeminiInit
```

**Step 4: Add Gemini PrepareContainer block**

After the Codex config block (~line 1358), add a Gemini config block following the same pattern:

```go
var geminiConfig *provider.ContainerConfig
if needsGeminiInit || (opts.Config != nil && opts.Config.ShouldSyncGeminiLogs()) {
	geminiProvider := provider.GetAgent("gemini")
	if geminiProvider == nil {
		// cleanup...
		return nil, fmt.Errorf("gemini provider not registered")
	}

	var geminiCred *provider.Credential
	if needsGeminiInit {
		key, keyErr := credential.DefaultEncryptionKey()
		if keyErr == nil {
			store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
			if storeErr == nil {
				if cred, err := store.Get(credential.ProviderGemini); err == nil {
					geminiCred = provider.FromLegacy(cred)
				}
			}
		}
	}

	var prepErr error
	geminiConfig, prepErr = geminiProvider.PrepareContainer(ctx, provider.PrepareOpts{
		Credential:    geminiCred,
		ContainerHome: containerHome,
	})
	if prepErr != nil {
		// cleanup...
		return nil, fmt.Errorf("preparing Gemini container config: %w", prepErr)
	}

	mounts = append(mounts, geminiConfig.Mounts...)
	proxyEnv = append(proxyEnv, geminiConfig.Env...)
}
```

**Step 5: Add cleanup calls**

Add `cleanupAgentConfig(geminiConfig)` alongside existing Claude/Codex cleanup calls in all error paths.

After container creation, store the staging dir:

```go
if geminiConfig != nil {
	r.GeminiConfigTempDir = geminiConfig.StagingDir
}
```

Add cleanup in `Wait()` and `Destroy()`:

```go
if r.GeminiConfigTempDir != "" {
	if rmErr := os.RemoveAll(r.GeminiConfigTempDir); rmErr != nil {
		log.Debug("failed to remove Gemini config temp dir", "path", r.GeminiConfigTempDir, "error", rmErr)
	}
}
```

**Step 6: Run build**

Run: `go build ./...`

**Step 7: Commit**

```bash
git add internal/run/manager.go internal/run/run.go
git commit -m "feat(run): integrate gemini provider into run lifecycle"
```

---

### Task 17: Add example and documentation

**Files:**
- Create: `examples/agent-gemini/agent.yaml`
- Create: `docs/content/guides/08-running-gemini.md`
- Modify: `docs/content/reference/01-cli.md` (add `moat gemini` command)
- Modify: `docs/content/reference/02-agent-yaml.md` (add `gemini` section)

**Step 1: Create example**

```yaml
# Example: Gemini CLI agent
name: gemini-agent
dependencies:
  - node@20
  - gemini-cli
  - git
grants:
  - gemini
```

**Step 2: Create guide**

Port from old branch's `docs/content/guides/08-running-gemini.md`.

**Step 3: Update reference docs**

Add `moat gemini` to CLI reference. Add `gemini:` section to agent.yaml reference.

**Step 4: Commit**

```bash
git add examples/agent-gemini/ docs/
git commit -m "docs: add gemini guide, example, and reference updates"
```

---

### Task 18: Full build and test verification

**Step 1: Build**

Run: `go build ./...`

**Step 2: Run all tests**

Run: `go test ./...`

**Step 3: Run lint**

Run: `make lint` (or `go vet ./...` if golangci-lint not available)

**Step 4: Fix any issues and commit**

```bash
git add -A
git commit -m "fix: address lint and test issues"
```

---

### Task 19: Create PR

```bash
gh pr create --title "feat(gemini): first-class Gemini CLI support" --body "$(cat <<'EOF'
## Summary
- Adds Google Gemini CLI as a first-class agent provider following the provider framework patterns
- Supports API key and OAuth authentication with background token refresh
- Adds proxy token substitution for transparent credential injection
- Includes `moat gemini` CLI command, session management, and documentation

## Changes
- New package `internal/providers/gemini/` implementing `AgentProvider` interface
- Proxy: token substitution and header removal for Gemini OAuth flow
- Config: `gemini:` section in agent.yaml
- CLI: `moat gemini`, `moat gemini sessions`, `moat grant gemini`
- Run manager: Gemini init detection and container preparation
- Image/deps: `NeedsGeminiInit` support
- Docs: guide, example, CLI and agent.yaml reference updates

## Test plan
- [ ] `go test ./...` passes
- [ ] `moat grant gemini` with API key works
- [ ] `moat grant gemini` with OAuth import works
- [ ] `moat gemini` launches interactive session
- [ ] `moat gemini -p "hello"` runs non-interactive
- [ ] `moat gemini sessions` lists sessions
- [ ] OAuth token refresh works (set short expiry, observe refresh logs)
- [ ] API key mode injects x-goog-api-key header correctly
EOF
)"
```
