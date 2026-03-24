# OAuth Grant Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `moat grant oauth <name>` command with browser-based OAuth flow, automatic token refresh, and MCP OAuth discovery.

**Architecture:** OAuth is a credential provider (`internal/providers/oauth/`) following the GitHub provider pattern. The proxy's existing MCP relay handles injection — the OAuth provider just acquires and refreshes tokens. Grant names use the `oauth:<name>` format (like `ssh:<host>`).

**Tech Stack:** Go standard library for HTTP/OAuth, `github.com/pkg/browser` for browser opening (already a dependency pattern in the codebase).

**Spec:** `docs/plans/2026-03-23-oauth-grant-provider-design.md`

---

### Task 1: Fix grant name handling for colon-separated grants

The `credentialStoreKey()` and `validateGrants()` functions lose the suffix after `:` when looking up credentials. For OAuth grants like `oauth:notion`, the credential is stored under `oauth:notion` but looked up as `oauth`. This also affects the daemon refresh loop.

**Files:**
- Modify: `internal/run/imageneeds.go` — `credentialStoreKey` function
- Modify: `internal/run/run.go:263-303` — `validateGrants` function
- Modify: `internal/run/manager.go:580-620` — grant processing loop
- Modify: `internal/daemon/refresh.go:29-50,74-105` — refresh loop
- Test: `internal/run/run_test.go` (or new test file)

**Context:** Currently `grantName := strings.Split(grant, ":")[0]` is used everywhere. For SSH this works because SSH grants bypass the normal validation. For `mcp-*` grants they're also skipped. But `oauth:notion` needs the provider to be `"oauth"` AND the store key to be `"oauth:notion"`.

- [ ] **Step 1: Write test for credentialStoreKey with oauth grants**

Add to run test file:

```go
func TestCredentialStoreKeyOAuth(t *testing.T) {
	// oauth:notion should use the full grant name as store key
	got := credentialStoreKey("oauth", "oauth:notion")
	assert.Equal(t, credential.Provider("oauth:notion"), got)

	// github should still work as before
	got = credentialStoreKey("github", "github")
	assert.Equal(t, credential.Provider("github"), got)
}
```

- [ ] **Step 2: Update credentialStoreKey to accept full grant name**

In `internal/run/imageneeds.go`, change the function to accept both the base name and full grant name. When the base name is `"oauth"`, return the full grant name as the store key:

```go
func credentialStoreKey(baseName, fullGrant string) credential.Provider {
	canonical := provider.ResolveName(baseName)
	if canonical == providerCodex {
		return credential.ProviderOpenAI
	}
	// For namespaced providers (oauth:notion, ssh:host), use full grant name
	if baseName != fullGrant {
		return credential.Provider(fullGrant)
	}
	return credential.Provider(canonical)
}
```

- [ ] **Step 3: Update all callers of credentialStoreKey**

In `validateGrants` (run.go:282):
```go
credName := credentialStoreKey(grantName, grant)
```

In manager.go grant loop (~line 590):
```go
credName := credentialStoreKey(grantName, grant)
```

- [ ] **Step 4: Update validateGrants to skip oauth grants from provider check**

The `oauth:notion` grant's provider is `"oauth"` which is registered. But the error message currently says `Run: moat grant %s` using `grantName` (which is `"oauth"`). Fix to use the full grant name:

```go
// In the "not configured" error message (line 290):
errs = append(errs, fmt.Sprintf("  - %s: not configured\n    Run: moat grant %s", grant, grantToCommand(grant)))
```

Add helper:
```go
func grantToCommand(grant string) string {
	parts := strings.SplitN(grant, ":", 2)
	if len(parts) == 2 {
		return parts[0] + " " + parts[1] // "oauth:notion" -> "oauth notion"
	}
	return grant
}
```

- [ ] **Step 5: Update daemon refresh.go to use full grant name for store lookup**

In `StartTokenRefresh` (line 34) and `refreshTokensForRun` (line 80):
```go
// Change from:
credName := credential.Provider(provider.ResolveName(grantName))
// To:
credName := credential.Provider(grant) // use full grant name for OAuth
if grantName == grant {
	credName = credential.Provider(provider.ResolveName(grantName))
}
```

- [ ] **Step 6: Run tests**

Run: `make test-unit`

- [ ] **Step 7: Commit**

```
feat(grant): support colon-separated grant names in store lookups

oauth:notion grants need the full name as the credential store key,
not just the prefix. Update credentialStoreKey, validateGrants, and
the daemon refresh loop to pass the full grant name through.
```

---

### Task 2: OAuth provider core — config and provider registration

**Files:**
- Create: `internal/providers/oauth/config.go`
- Create: `internal/providers/oauth/provider.go`
- Create: `internal/providers/oauth/config_test.go`
- Modify: `internal/providers/register.go` — add import

- [ ] **Step 1: Write test for config loading**

```go
// internal/providers/oauth/config_test.go
func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "notion.yaml")
	os.WriteFile(configPath, []byte(`
auth_url: https://api.notion.com/v1/oauth/authorize
token_url: https://api.notion.com/v1/oauth/token
client_id: test-client
scopes: read,write
`), 0600)

	cfg, err := LoadConfig(dir, "notion")
	require.NoError(t, err)
	assert.Equal(t, "https://api.notion.com/v1/oauth/authorize", cfg.AuthURL)
	assert.Equal(t, "https://api.notion.com/v1/oauth/token", cfg.TokenURL)
	assert.Equal(t, "test-client", cfg.ClientID)
	assert.Equal(t, "read,write", cfg.Scopes)
}

func TestLoadConfigNotFound(t *testing.T) {
	_, err := LoadConfig(t.TempDir(), "nonexistent")
	assert.Error(t, err)
}

func TestLoadConfigValidation(t *testing.T) {
	dir := t.TempDir()
	// Missing required fields
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(`auth_url: https://example.com`), 0600)
	_, err := LoadConfig(dir, "bad")
	assert.ErrorContains(t, err, "token_url")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/oauth/ -run TestLoadConfig -v`

- [ ] **Step 3: Implement config.go**

```go
// internal/providers/oauth/config.go
package oauth

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// OAuthConfig holds OAuth provider configuration.
type OAuthConfig struct {
	AuthURL      string `yaml:"auth_url"`
	TokenURL     string `yaml:"token_url"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret,omitempty"`
	Scopes       string `yaml:"scopes,omitempty"`
}

// Validate checks that required fields are present.
func (c *OAuthConfig) Validate() error {
	if c.AuthURL == "" {
		return fmt.Errorf("auth_url is required")
	}
	if c.TokenURL == "" {
		return fmt.Errorf("token_url is required")
	}
	if c.ClientID == "" {
		return fmt.Errorf("client_id is required")
	}
	return nil
}

// DefaultConfigDir returns ~/.moat/oauth/.
func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".moat", "oauth")
}

// LoadConfig loads OAuth config from ~/.moat/oauth/<name>.yaml.
func LoadConfig(dir, name string) (*OAuthConfig, error) {
	path := filepath.Join(dir, name+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading OAuth config for %q: %w", name, err)
	}
	var cfg OAuthConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing OAuth config for %q: %w", name, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid OAuth config for %q: %w", name, err)
	}
	return &cfg, nil
}

// SaveConfig writes OAuth config (e.g., after DCR populates client_id).
func SaveConfig(dir, name string, cfg *OAuthConfig) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating OAuth config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling OAuth config: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, name+".yaml"), data, 0600)
}
```

- [ ] **Step 4: Run tests to verify they pass**

- [ ] **Step 5: Implement provider.go with registration**

```go
// internal/providers/oauth/provider.go
package oauth

import (
	"context"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider for OAuth.
type Provider struct{}

var (
	_ provider.CredentialProvider  = (*Provider)(nil)
	_ provider.RefreshableProvider = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
}

func (p *Provider) Name() string { return "oauth" }

func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	// Grant is handled by the dedicated grant_oauth.go CLI command,
	// not through the generic runGrant path, because OAuth needs
	// the sub-name (e.g., "notion" from "oauth:notion").
	return nil, fmt.Errorf("use 'moat grant oauth <name>' instead")
}

func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	// No-op: OAuth provider doesn't know which hosts to configure.
	// Host-to-credential mapping is handled by MCP server config in moat.yaml.
}

func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	return nil // OAuth tokens don't set container env vars
}

func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

func (p *Provider) Cleanup(cleanupPath string) {}

func (p *Provider) ImpliedDependencies() []string { return nil }

func (p *Provider) CanRefresh(cred *provider.Credential) bool {
	if cred.Metadata == nil {
		return false
	}
	if cred.Metadata["token_source"] != "oauth" {
		return false
	}
	// No refresh if token doesn't expire
	if cred.ExpiresAt.IsZero() {
		return false
	}
	// Need a refresh token
	return cred.Metadata["refresh_token"] != ""
}

func (p *Provider) RefreshInterval() time.Duration {
	return 5 * time.Minute
}

func (p *Provider) Refresh(ctx context.Context, proxy provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	// Implemented in refresh.go
	return refreshToken(ctx, cred)
}
```

- [ ] **Step 6: Add import to register.go**

Add `_ "github.com/majorcontext/moat/internal/providers/oauth"` to the imports in `internal/providers/register.go`.

- [ ] **Step 7: Run tests**

Run: `make test-unit`

- [ ] **Step 8: Commit**

```
feat(oauth): add OAuth provider config and registration

New internal/providers/oauth package with config loading from
~/.moat/oauth/<name>.yaml and provider registration. Implements
CredentialProvider and RefreshableProvider interfaces.
```

---

### Task 3: OAuth grant flow — PKCE, callback server, token exchange

**Files:**
- Create: `internal/providers/oauth/grant.go`
- Create: `internal/providers/oauth/grant_test.go`

- [ ] **Step 1: Write tests for PKCE generation**

```go
func TestGeneratePKCE(t *testing.T) {
	verifier, challenge := generatePKCE()
	assert.Len(t, verifier, 43) // minimum RFC 7636 length
	assert.NotEmpty(t, challenge)
	// Verify S256: challenge = base64url(sha256(verifier))
	h := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(h[:])
	assert.Equal(t, expected, challenge)
}
```

- [ ] **Step 2: Write tests for token exchange with mock server**

```go
func TestExchangeCode(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		r.ParseForm()
		assert.Equal(t, "authorization_code", r.Form.Get("grant_type"))
		assert.Equal(t, "test-code", r.Form.Get("code"))
		assert.Equal(t, "test-client", r.Form.Get("client_id"))
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
			"expires_in":    3600,
			"token_type":    "Bearer",
		})
	}))
	defer tokenServer.Close()

	result, err := exchangeCode(context.Background(), &OAuthConfig{
		TokenURL: tokenServer.URL,
		ClientID: "test-client",
	}, "test-code", "http://localhost:9999/callback", "test-verifier")
	require.NoError(t, err)
	assert.Equal(t, "new-access-token", result.AccessToken)
	assert.Equal(t, "new-refresh-token", result.RefreshToken)
	assert.Equal(t, 3600, result.ExpiresIn)
}

func TestExchangeCodeWithSecret(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		assert.Equal(t, "my-secret", r.Form.Get("client_secret"))
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "token",
			"token_type":   "Bearer",
		})
	}))
	defer tokenServer.Close()

	result, err := exchangeCode(context.Background(), &OAuthConfig{
		TokenURL:     tokenServer.URL,
		ClientID:     "test-client",
		ClientSecret: "my-secret",
	}, "code", "http://localhost/callback", "verifier")
	require.NoError(t, err)
	assert.Equal(t, "token", result.AccessToken)
}

func TestExchangeCodeNoExpiresIn(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "token",
			"token_type":   "Bearer",
		})
	}))
	defer tokenServer.Close()

	result, err := exchangeCode(context.Background(), &OAuthConfig{
		TokenURL: tokenServer.URL,
		ClientID: "test-client",
	}, "code", "http://localhost/callback", "verifier")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExpiresIn) // zero means non-expiring
}
```

- [ ] **Step 3: Write test for callback server**

```go
func TestCallbackServer(t *testing.T) {
	state := "test-state"
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv, port, err := startCallbackServer(state, codeCh, errCh)
	require.NoError(t, err)
	defer srv.Close()

	// Simulate browser redirect
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=auth-code&state=%s", port, state))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)

	select {
	case code := <-codeCh:
		assert.Equal(t, "auth-code", code)
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for code")
	}
}

func TestCallbackServerBadState(t *testing.T) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv, port, err := startCallbackServer("correct-state", codeCh, errCh)
	require.NoError(t, err)
	defer srv.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=code&state=wrong-state", port))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 400, resp.StatusCode)

	select {
	case err := <-errCh:
		assert.ErrorContains(t, err, "state")
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for error")
	}
}
```

- [ ] **Step 4: Implement grant.go**

```go
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TokenResponse represents the OAuth token endpoint response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// generatePKCE generates a PKCE code verifier and S256 challenge.
func generatePKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

// generateState generates a random state parameter for CSRF protection.
func generateState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// buildAuthURL constructs the authorization URL.
func buildAuthURL(cfg *OAuthConfig, state, challenge, redirectURI, resource string) string {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	if cfg.Scopes != "" {
		params.Set("scope", cfg.Scopes)
	}
	if resource != "" {
		params.Set("resource", resource)
	}
	return cfg.AuthURL + "?" + params.Encode()
}

// startCallbackServer starts an HTTP server on a random port to receive
// the OAuth callback. Returns the server, port, and any error.
func startCallbackServer(expectedState string, codeCh chan<- string, errCh chan<- error) (*http.Server, int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, fmt.Errorf("starting callback server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		if state != expectedState {
			http.Error(w, "Invalid state parameter", http.StatusBadRequest)
			errCh <- fmt.Errorf("state mismatch: expected %q, got %q", expectedState, state)
			return
		}
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			desc := r.URL.Query().Get("error_description")
			http.Error(w, "Authorization failed", http.StatusBadRequest)
			errCh <- fmt.Errorf("authorization error: %s: %s", errMsg, desc)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			errCh <- fmt.Errorf("missing authorization code in callback")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h1>Authorization successful</h1><p>You can close this window.</p></body></html>")
		codeCh <- code
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)
	return srv, port, nil
}

// exchangeCode exchanges an authorization code for tokens.
func exchangeCode(ctx context.Context, cfg *OAuthConfig, code, redirectURI, codeVerifier string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {cfg.ClientID},
		"code_verifier": {codeVerifier},
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: HTTP %d", resp.StatusCode)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}
	return &tokenResp, nil
}

// RunGrant performs the full OAuth authorization code flow with PKCE.
// Opens a browser for user authorization and returns a credential.
func RunGrant(ctx context.Context, name string, cfg *OAuthConfig, resource string) (*provider.Credential, error) {
	verifier, challenge := generatePKCE()
	state := generateState()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv, port, err := startCallbackServer(state, codeCh, errCh)
	if err != nil {
		return nil, err
	}
	defer srv.Close()

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	authURL := buildAuthURL(cfg, state, challenge, redirectURI, resource)

	fmt.Printf("Opening browser for authorization...\n")
	fmt.Printf("If the browser doesn't open, visit:\n  %s\n\n", authURL)

	// Open browser
	if browserErr := openBrowser(authURL); browserErr != nil {
		fmt.Printf("Could not open browser: %v\n", browserErr)
	}

	// Wait for callback
	fmt.Println("Waiting for authorization (5 minute timeout)...")
	timeout := time.After(5 * time.Minute)
	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return nil, err
	case <-timeout:
		return nil, fmt.Errorf("authorization timed out after 5 minutes")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Exchange code for tokens
	fmt.Println("Exchanging authorization code for tokens...")
	tokenResp, err := exchangeCode(ctx, cfg, code, redirectURI, verifier)
	if err != nil {
		return nil, err
	}

	// Build credential
	cred := &provider.Credential{
		Provider:  "oauth:" + name,
		Token:     tokenResp.AccessToken,
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			"token_source": "oauth",
			"token_url":    cfg.TokenURL,
			"client_id":    cfg.ClientID,
		},
	}
	if tokenResp.ExpiresIn > 0 {
		cred.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}
	if tokenResp.RefreshToken != "" {
		cred.Metadata["refresh_token"] = tokenResp.RefreshToken
	}
	if cfg.ClientSecret != "" {
		cred.Metadata["client_secret"] = cfg.ClientSecret
	}
	if resource != "" {
		cred.Metadata["resource"] = resource
	}

	return cred, nil
}

// openBrowser opens a URL in the default browser.
func openBrowser(url string) error {
	// Use exec to open browser - platform-specific
	return browser.OpenURL(url)
}
```

Note: Check if `github.com/pkg/browser` or similar is already a dependency. If not, use `exec.Command` with platform-specific commands (`xdg-open` on Linux, `open` on macOS).

- [ ] **Step 5: Run tests**

Run: `go test ./internal/providers/oauth/ -v`

- [ ] **Step 6: Commit**

```
feat(oauth): implement OAuth authorization code flow with PKCE

Add grant.go with browser-based OAuth flow: PKCE generation,
local callback server, authorization URL building, and token
exchange. Includes comprehensive tests with mock HTTP servers.
```

---

### Task 4: Token refresh

**Files:**
- Create: `internal/providers/oauth/refresh.go`
- Create: `internal/providers/oauth/refresh_test.go`
- Modify: `internal/daemon/refresh.go` — persist credentials after refresh

- [ ] **Step 1: Write tests for token refresh**

```go
func TestRefreshToken(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		assert.Equal(t, "refresh_token", r.Form.Get("grant_type"))
		assert.Equal(t, "old-refresh", r.Form.Get("refresh_token"))
		assert.Equal(t, "test-client", r.Form.Get("client_id"))
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	cred := &provider.Credential{
		Provider:  "oauth:test",
		Token:     "old-access",
		ExpiresAt: time.Now().Add(-time.Minute), // expired
		Metadata: map[string]string{
			"token_source":  "oauth",
			"refresh_token": "old-refresh",
			"token_url":     tokenServer.URL,
			"client_id":     "test-client",
		},
	}

	updated, err := refreshToken(context.Background(), cred)
	require.NoError(t, err)
	assert.Equal(t, "new-access", updated.Token)
	assert.Equal(t, "new-refresh", updated.Metadata["refresh_token"])
	assert.False(t, updated.ExpiresAt.IsZero())
}

func TestRefreshTokenNotNeeded(t *testing.T) {
	cred := &provider.Credential{
		Provider:  "oauth:test",
		Token:     "valid-token",
		ExpiresAt: time.Now().Add(time.Hour), // still valid
		Metadata: map[string]string{
			"token_source":  "oauth",
			"refresh_token": "refresh",
			"token_url":     "http://unused",
			"client_id":     "test",
		},
	}

	updated, err := refreshToken(context.Background(), cred)
	require.NoError(t, err)
	assert.Equal(t, "valid-token", updated.Token) // unchanged
}
```

- [ ] **Step 2: Implement refresh.go**

```go
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
)

const refreshBuffer = 10 * time.Minute

// refreshToken refreshes an OAuth token if it's near expiry.
// Returns the current credential unchanged if not near expiry.
func refreshToken(ctx context.Context, cred *provider.Credential) (*provider.Credential, error) {
	// Check if refresh is needed
	if !cred.ExpiresAt.IsZero() && time.Until(cred.ExpiresAt) > refreshBuffer {
		return cred, nil // not near expiry
	}

	tokenURL := cred.Metadata["token_url"]
	clientID := cred.Metadata["client_id"]
	refreshTok := cred.Metadata["refresh_token"]

	if tokenURL == "" || clientID == "" || refreshTok == "" {
		return nil, fmt.Errorf("missing refresh metadata (token_url, client_id, or refresh_token)")
	}

	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshTok},
		"client_id":     {clientID},
	}
	if secret := cred.Metadata["client_secret"]; secret != "" {
		data.Set("client_secret", secret)
	}
	if resource := cred.Metadata["resource"]; resource != "" {
		data.Set("resource", resource)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh failed: HTTP %d", resp.StatusCode)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("parsing refresh response: %w", err)
	}

	// Build updated credential (copy to avoid mutating original)
	updated := *cred
	updated.Token = tokenResp.AccessToken
	if tokenResp.ExpiresIn > 0 {
		updated.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	// Update metadata
	updated.Metadata = make(map[string]string)
	for k, v := range cred.Metadata {
		updated.Metadata[k] = v
	}
	if tokenResp.RefreshToken != "" {
		updated.Metadata["refresh_token"] = tokenResp.RefreshToken
	}

	log.Debug("OAuth token refreshed",
		"provider", cred.Provider,
		"expires_in", tokenResp.ExpiresIn)

	return &updated, nil
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/providers/oauth/ -run TestRefresh -v`

- [ ] **Step 4: Update daemon refresh.go to persist after refresh**

In `refreshTokensForRun`, after the `Refresh()` call succeeds (line 99), persist the updated credential:

```go
updated, err := rp.Refresh(refreshCtx, rc, provCred)
cancel()
if err != nil {
	log.Debug("token refresh failed", "provider", credName, "error", err)
	continue
}
// Persist refreshed credential to store
if updated != nil && updated.Token != provCred.Token {
	storeCred := credential.Credential{
		Provider:  credName,
		Token:     updated.Token,
		Scopes:    updated.Scopes,
		ExpiresAt: updated.ExpiresAt,
		CreatedAt: updated.CreatedAt,
		Metadata:  updated.Metadata,
	}
	if saveErr := store.Save(storeCred); saveErr != nil {
		log.Debug("failed to persist refreshed credential", "provider", credName, "error", saveErr)
	}
}
```

Note: `store` variable is a `credential.Store` interface. Check if `Save()` is on the interface. If not, need to use `*credential.FileStore` or add `Save` to the interface. The `StartTokenRefresh` function already creates a `*credential.FileStore` at line 22, so this should work.

- [ ] **Step 5: Run all tests**

Run: `make test-unit`

- [ ] **Step 6: Commit**

```
feat(oauth): add token refresh and persist refreshed credentials

OAuth tokens are refreshed when within 10 minutes of expiry.
The daemon refresh loop now persists updated credentials to the
store after successful refresh, preventing token loss on restart.
```

---

### Task 5: MCP OAuth discovery

**Files:**
- Create: `internal/providers/oauth/discovery.go`
- Create: `internal/providers/oauth/discovery_test.go`

- [ ] **Step 1: Write tests for Protected Resource Metadata**

```go
func TestDiscoverProtectedResourceMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"resource":              "https://mcp.example.com",
				"authorization_servers": []string{"https://auth.example.com"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	meta, err := discoverProtectedResourceMetadata(context.Background(), srv.URL+"/mcp")
	require.NoError(t, err)
	assert.Equal(t, "https://mcp.example.com", meta.Resource)
	assert.Equal(t, []string{"https://auth.example.com"}, meta.AuthorizationServers)
}

func TestDiscoverProtectedResourceMetadataNotFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	_, err := discoverProtectedResourceMetadata(context.Background(), srv.URL)
	assert.Error(t, err)
}
```

- [ ] **Step 2: Write tests for Auth Server Metadata**

```go
func TestDiscoverAuthServerMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"authorization_endpoint":          "https://auth.example.com/authorize",
				"token_endpoint":                  "https://auth.example.com/token",
				"registration_endpoint":           "https://auth.example.com/register",
				"code_challenge_methods_supported": []string{"S256"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	meta, err := discoverAuthServerMetadata(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, "https://auth.example.com/authorize", meta.AuthorizationEndpoint)
	assert.Equal(t, "https://auth.example.com/token", meta.TokenEndpoint)
	assert.Equal(t, "https://auth.example.com/register", meta.RegistrationEndpoint)
}
```

- [ ] **Step 3: Write tests for Dynamic Client Registration**

```go
func TestRegisterClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "moat", req["client_name"])
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"client_id": "registered-client-id",
		})
	}))
	defer srv.Close()

	reg, err := registerClient(context.Background(), srv.URL, "moat", []string{"http://127.0.0.1:9999/callback"})
	require.NoError(t, err)
	assert.Equal(t, "registered-client-id", reg.ClientID)
}
```

- [ ] **Step 4: Write test for full discovery flow**

```go
func TestDiscoverFromMCPServer(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"authorization_endpoint":          "https://auth.example.com/authorize",
				"token_endpoint":                  "https://auth.example.com/token",
				"registration_endpoint":           r.Host + "/register",
				"code_challenge_methods_supported": []string{"S256"},
			})
		case "/register":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"client_id": "new-id"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authSrv.Close()

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"resource":              mcpSrv.URL, // self-referential for test
				"authorization_servers": []string{authSrv.URL},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mcpSrv.Close()

	cfg, resource, err := DiscoverFromMCPServer(context.Background(), mcpSrv.URL+"/mcp")
	require.NoError(t, err)
	assert.NotEmpty(t, cfg.AuthURL)
	assert.NotEmpty(t, cfg.TokenURL)
	assert.NotEmpty(t, cfg.ClientID)
	assert.NotEmpty(t, resource)
}
```

- [ ] **Step 5: Implement discovery.go**

```go
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

var discoveryClient = &http.Client{Timeout: 5 * time.Second}

// ProtectedResourceMetadata represents RFC 9728 metadata.
type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
}

// AuthServerMetadata represents RFC 8414 metadata.
type AuthServerMetadata struct {
	AuthorizationEndpoint        string   `json:"authorization_endpoint"`
	TokenEndpoint                string   `json:"token_endpoint"`
	RegistrationEndpoint         string   `json:"registration_endpoint,omitempty"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported,omitempty"`
}

// ClientRegistration represents the DCR response.
type ClientRegistration struct {
	ClientID string `json:"client_id"`
}

// DiscoverFromMCPServer attempts full MCP OAuth discovery.
// Returns config, resource identifier, and error.
func DiscoverFromMCPServer(ctx context.Context, mcpServerURL string) (*OAuthConfig, string, error) {
	// Step 1: Protected Resource Metadata
	prm, err := discoverProtectedResourceMetadata(ctx, mcpServerURL)
	if err != nil {
		return nil, "", fmt.Errorf("protected resource metadata: %w", err)
	}
	if len(prm.AuthorizationServers) == 0 {
		return nil, "", fmt.Errorf("no authorization servers in protected resource metadata")
	}

	// Step 2: Auth Server Metadata
	authServerURL := prm.AuthorizationServers[0]
	asm, err := discoverAuthServerMetadata(ctx, authServerURL)
	if err != nil {
		return nil, "", fmt.Errorf("auth server metadata at %s: %w", authServerURL, err)
	}

	cfg := &OAuthConfig{
		AuthURL:  asm.AuthorizationEndpoint,
		TokenURL: asm.TokenEndpoint,
	}

	// Step 3: Dynamic Client Registration (if available)
	if asm.RegistrationEndpoint != "" {
		reg, dcrErr := registerClient(ctx, asm.RegistrationEndpoint, "moat", []string{
			"http://127.0.0.1:0/callback",
		})
		if dcrErr != nil {
			log.Debug("DCR failed, client_id must be provided manually", "error", dcrErr)
		} else {
			cfg.ClientID = reg.ClientID
		}
	}

	return cfg, prm.Resource, nil
}

func discoverProtectedResourceMetadata(ctx context.Context, serverURL string) (*ProtectedResourceMetadata, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}

	// Try path-based first, then root
	urls := []string{
		fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource%s", u.Scheme, u.Host, u.Path),
		fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource", u.Scheme, u.Host),
	}

	for _, tryURL := range urls {
		meta, fetchErr := fetchJSON[ProtectedResourceMetadata](ctx, tryURL)
		if fetchErr == nil {
			return meta, nil
		}
		log.Debug("PRM discovery attempt failed", "url", tryURL, "error", fetchErr)
	}

	return nil, fmt.Errorf("protected resource metadata not found at %s", serverURL)
}

func discoverAuthServerMetadata(ctx context.Context, authServerURL string) (*AuthServerMetadata, error) {
	u, err := url.Parse(authServerURL)
	if err != nil {
		return nil, err
	}

	// Try OAuth metadata first, then OIDC discovery
	urls := []string{
		fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server", u.Scheme, u.Host),
		fmt.Sprintf("%s://%s/.well-known/openid-configuration", u.Scheme, u.Host),
	}

	for _, tryURL := range urls {
		meta, fetchErr := fetchJSON[AuthServerMetadata](ctx, tryURL)
		if fetchErr == nil {
			if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
				continue
			}
			return meta, nil
		}
		log.Debug("ASM discovery attempt failed", "url", tryURL, "error", fetchErr)
	}

	return nil, fmt.Errorf("auth server metadata not found at %s", authServerURL)
}

func registerClient(ctx context.Context, endpoint, clientName string, redirectURIs []string) (*ClientRegistration, error) {
	body := map[string]interface{}{
		"client_name":                clientName,
		"redirect_uris":             redirectURIs,
		"grant_types":               []string{"authorization_code"},
		"response_types":            []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := discoveryClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DCR failed: HTTP %d", resp.StatusCode)
	}

	var reg ClientRegistration
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return nil, err
	}
	if reg.ClientID == "" {
		return nil, fmt.Errorf("DCR response missing client_id")
	}
	return &reg, nil
}

func fetchJSON[T any](ctx context.Context, url string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := discoveryClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/providers/oauth/ -run TestDiscover -v`

- [ ] **Step 7: Commit**

```
feat(oauth): add MCP OAuth discovery with DCR support

Implement RFC 9728 Protected Resource Metadata, RFC 8414 Auth Server
Metadata, and RFC 7591 Dynamic Client Registration for automatic
OAuth endpoint discovery from MCP server URLs.
```

---

### Task 6: CLI command — `moat grant oauth <name>`

**Files:**
- Create: `cmd/moat/cli/grant_oauth.go`

- [ ] **Step 1: Implement grant_oauth.go**

```go
package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/providers/oauth"
	"github.com/spf13/cobra"
)

var (
	oauthURL          string
	oauthAuthURL      string
	oauthTokenURL     string
	oauthClientID     string
	oauthClientSecret string
	oauthScopes       string
)

var grantOAuthCmd = &cobra.Command{
	Use:   "oauth <name>",
	Short: "Grant OAuth credentials for a service",
	Long: `Grant OAuth credentials via browser-based authorization.

Opens a browser for OAuth authorization and stores the token securely.
Supports automatic discovery for MCP servers that implement OAuth metadata.

Examples:
  # Auto-discover from MCP server URL
  moat grant oauth notion --url https://mcp.notion.com/mcp

  # Use config from ~/.moat/oauth/linear.yaml
  moat grant oauth linear

  # Explicit OAuth endpoints
  moat grant oauth custom \
    --auth-url https://auth.example.com/authorize \
    --token-url https://auth.example.com/token \
    --client-id abc123`,
	Args: cobra.ExactArgs(1),
	RunE: runGrantOAuth,
}

func init() {
	grantCmd.AddCommand(grantOAuthCmd)
	grantOAuthCmd.Flags().StringVar(&oauthURL, "url", "", "MCP server URL for OAuth discovery")
	grantOAuthCmd.Flags().StringVar(&oauthAuthURL, "auth-url", "", "OAuth authorization endpoint")
	grantOAuthCmd.Flags().StringVar(&oauthTokenURL, "token-url", "", "OAuth token endpoint")
	grantOAuthCmd.Flags().StringVar(&oauthClientID, "client-id", "", "OAuth client ID")
	grantOAuthCmd.Flags().StringVar(&oauthClientSecret, "client-secret", "", "OAuth client secret")
	grantOAuthCmd.Flags().StringVar(&oauthScopes, "scopes", "", "OAuth scopes")
}

func runGrantOAuth(cmd *cobra.Command, args []string) error {
	name := args[0]
	if strings.ContainsAny(name, "/\\:*?\"<>|") {
		return fmt.Errorf("invalid name: %q contains invalid characters", name)
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	var cfg *oauth.OAuthConfig
	var resource string

	// Resolution order: CLI flags → config file → MCP discovery

	// 1. CLI flags
	if oauthAuthURL != "" || oauthTokenURL != "" || oauthClientID != "" {
		cfg = &oauth.OAuthConfig{
			AuthURL:      oauthAuthURL,
			TokenURL:     oauthTokenURL,
			ClientID:     oauthClientID,
			ClientSecret: oauthClientSecret,
			Scopes:       oauthScopes,
		}
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("invalid OAuth flags: %w", err)
		}
	}

	// 2. Config file
	if cfg == nil {
		fileCfg, err := oauth.LoadConfig(oauth.DefaultConfigDir(), name)
		if err == nil {
			cfg = fileCfg
			log.Debug("loaded OAuth config from file", "name", name)
		}
	}

	// 3. MCP discovery
	if cfg == nil {
		mcpURL := oauthURL
		if mcpURL == "" {
			mcpURL = findMCPServerURL(name)
		}
		if mcpURL != "" {
			fmt.Printf("Attempting OAuth discovery for %s...\n", mcpURL)
			discovered, res, err := oauth.DiscoverFromMCPServer(ctx, mcpURL)
			if err == nil {
				cfg = discovered
				resource = res
				fmt.Println("Discovered OAuth endpoints.")
				// Cache discovered config (with DCR client_id)
				if cfg.ClientID != "" {
					if saveErr := oauth.SaveConfig(oauth.DefaultConfigDir(), name, cfg); saveErr != nil {
						log.Debug("failed to cache discovered config", "error", saveErr)
					}
				}
			} else {
				fmt.Printf("Discovery failed: %v\n", err)
			}
		}
	}

	if cfg == nil {
		return fmt.Errorf("no OAuth configuration found for %q\n\n"+
			"Provide one of:\n"+
			"  1. CLI flags: --auth-url, --token-url, --client-id\n"+
			"  2. Config file: ~/.moat/oauth/%s.yaml\n"+
			"  3. MCP server URL: --url <mcp-server-url>\n\n"+
			"See: https://majorcontext.com/moat/guides/mcp", name, name)
	}

	// Run the OAuth flow
	provCred, err := oauth.RunGrant(ctx, name, cfg, resource)
	if err != nil {
		return err
	}

	// Store credential
	cred := credential.Credential{
		Provider:  credential.Provider(provCred.Provider),
		Token:     provCred.Token,
		Scopes:    provCred.Scopes,
		ExpiresAt: provCred.ExpiresAt,
		CreatedAt: provCred.CreatedAt,
		Metadata:  provCred.Metadata,
	}

	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}

	fmt.Printf("\nOAuth credential 'oauth:%s' saved to %s\n", name, credPath)
	if !provCred.ExpiresAt.IsZero() {
		fmt.Printf("Expires: %s (auto-refresh enabled)\n", provCred.ExpiresAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Printf("\nUse in moat.yaml:\n\n")
	fmt.Printf("grants:\n  - oauth:%s\n\nmcp:\n  - name: %s\n    url: <server-url>\n    auth:\n      grant: oauth:%s\n      header: Authorization\n\n", name, name, name)

	return nil
}

// findMCPServerURL looks for an MCP server URL in moat.yaml matching the name.
func findMCPServerURL(name string) string {
	cfg, err := config.Load("moat.yaml")
	if err != nil {
		return ""
	}
	for _, mcp := range cfg.MCP {
		if mcp.Name == name {
			return mcp.URL
		}
	}
	return ""
}
```

- [ ] **Step 2: Run build to verify compilation**

Run: `go build ./cmd/moat/`

- [ ] **Step 3: Commit**

```
feat(cli): add moat grant oauth command

New subcommand for OAuth credential acquisition with config
resolution from CLI flags, ~/.moat/oauth/ files, and MCP discovery.
```

---

### Task 7: Proxy Bearer prefix for OAuth grants

**Files:**
- Modify: `internal/proxy/mcp.go:136,245`
- Modify: `internal/proxy/mcp_test.go`

- [ ] **Step 1: Add test for Bearer prefix injection**

Add to `mcp_test.go`:

```go
func TestMCPOAuthCredentialInjection(t *testing.T) {
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"oauth:notion": {
				Provider: "oauth:notion",
				Token:    "oauth-access-token",
			},
		},
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("Authorization")))
	}))
	defer backend.Close()

	mcpServers := []config.MCPServerConfig{
		{
			Name: "notion",
			URL:  backend.URL,
			Auth: &config.MCPAuthConfig{
				Grant:  "oauth:notion",
				Header: "Authorization",
			},
		},
	}

	// Test relay path
	p := &Proxy{
		credStore:  mockStore,
		mcpServers: mcpServers,
	}

	// ... test that relay injects "Bearer oauth-access-token"
}
```

- [ ] **Step 2: Add Bearer prefix in injectMCPCredentials**

In `internal/proxy/mcp.go`, after line 136 where `credValue` is set:

```go
// OAuth grants use Bearer token format
if strings.HasPrefix(matchedServer.Auth.Grant, "oauth:") {
	credValue = "Bearer " + credValue
}
targetReq.Header.Set(matchedServer.Auth.Header, credValue)
```

- [ ] **Step 3: Add Bearer prefix in handleMCPRelay**

In `handleMCPRelay`, after line 245 where `credValue` is injected:

```go
// OAuth grants use Bearer token format
if strings.HasPrefix(mcpServer.Auth.Grant, "oauth:") {
	credValue = "Bearer " + credValue
}
proxyReq.Header.Set(mcpServer.Auth.Header, credValue)
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/proxy/ -run TestMCP -v`

- [ ] **Step 5: Commit**

```
feat(proxy): add Bearer prefix for OAuth grant injection

When the MCP server auth grant starts with "oauth:", the proxy
prepends "Bearer " to the credential value during injection.
Static mcp-* grants continue to be injected raw.
```

---

### Task 8: Documentation and lint

**Files:**
- Modify: `docs/content/guides/09-mcp.md`
- Modify: `docs/content/reference/01-cli.md`
- Modify: `docs/content/reference/02-moat-yaml.md`

- [ ] **Step 1: Update MCP guide with OAuth section**

Add OAuth authentication section to `docs/content/guides/09-mcp.md`.

- [ ] **Step 2: Add `moat grant oauth` to CLI reference**

- [ ] **Step 3: Document `oauth:*` grant format in moat.yaml reference**

- [ ] **Step 4: Run lint**

Run: `make lint`
Fix any issues.

- [ ] **Step 5: Commit**

```
docs: add OAuth authentication for MCP servers

Document moat grant oauth command, OAuth discovery, custom provider
config, and oauth:* grant format in moat.yaml.
```

---

### Task 9: Integration verification

- [ ] **Step 1: Run full test suite**

Run: `make test-unit`
All tests must pass.

- [ ] **Step 2: Run lint**

Run: `make lint`
Zero issues.

- [ ] **Step 3: Build**

Run: `go build ./...`

- [ ] **Step 4: Manual smoke test**

```bash
# Create a test OAuth config
mkdir -p ~/.moat/oauth
cat > ~/.moat/oauth/test.yaml <<EOF
auth_url: https://httpbin.org/get
token_url: https://httpbin.org/post
client_id: test-client
EOF

# Verify the command parses and starts (will fail at browser step, that's fine)
moat grant oauth test 2>&1 | head -5
```

- [ ] **Step 5: Final commit if any fixups needed**
