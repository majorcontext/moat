# Phase 2: Credential Broker Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Enable secure credential injection so containers can access GitHub API without seeing raw tokens.

**Architecture:** A credential store persists encrypted tokens on the host. An HTTP proxy intercepts container requests, looks up the run's granted capabilities, and injects Authorization headers before forwarding. Containers never see real credentials.

**Tech Stack:** Go stdlib `net/http`, `crypto/aes` for encryption, GitHub device flow OAuth.

---

## Task 1: Credential Types and Store Interface

**Files:**
- Create: `internal/credential/types.go`
- Create: `internal/credential/store.go`
- Create: `internal/credential/store_test.go`

**Step 1: Write the types**

```go
// internal/credential/types.go
package credential

import "time"

// Provider identifies a credential provider (github, aws, etc.)
type Provider string

const (
    ProviderGitHub Provider = "github"
    ProviderAWS    Provider = "aws"
)

// Credential represents a stored credential.
type Credential struct {
    Provider    Provider  `json:"provider"`
    Token       string    `json:"token"`
    Scopes      []string  `json:"scopes,omitempty"`
    ExpiresAt   time.Time `json:"expires_at,omitempty"`
    CreatedAt   time.Time `json:"created_at"`
}

// Store defines the credential storage interface.
type Store interface {
    Save(cred Credential) error
    Get(provider Provider) (*Credential, error)
    Delete(provider Provider) error
    List() ([]Credential, error)
}
```

**Step 2: Write the failing test for file store**

```go
// internal/credential/store_test.go
package credential

import (
    "os"
    "path/filepath"
    "testing"
    "time"
)

func TestFileStore_SaveAndGet(t *testing.T) {
    dir := t.TempDir()
    store, err := NewFileStore(dir, []byte("test-encryption-key-32b!"))
    if err != nil {
        t.Fatalf("NewFileStore: %v", err)
    }

    cred := Credential{
        Provider:  ProviderGitHub,
        Token:     "ghp_test123",
        Scopes:    []string{"repo", "read:user"},
        CreatedAt: time.Now(),
    }

    if err := store.Save(cred); err != nil {
        t.Fatalf("Save: %v", err)
    }

    got, err := store.Get(ProviderGitHub)
    if err != nil {
        t.Fatalf("Get: %v", err)
    }

    if got.Token != cred.Token {
        t.Errorf("Token = %q, want %q", got.Token, cred.Token)
    }
}

func TestFileStore_Delete(t *testing.T) {
    dir := t.TempDir()
    store, err := NewFileStore(dir, []byte("test-encryption-key-32b!"))
    if err != nil {
        t.Fatalf("NewFileStore: %v", err)
    }

    cred := Credential{
        Provider:  ProviderGitHub,
        Token:     "ghp_test123",
        CreatedAt: time.Now(),
    }

    store.Save(cred)
    if err := store.Delete(ProviderGitHub); err != nil {
        t.Fatalf("Delete: %v", err)
    }

    _, err = store.Get(ProviderGitHub)
    if err == nil {
        t.Error("expected error after delete, got nil")
    }
}

func TestFileStore_GetNotFound(t *testing.T) {
    dir := t.TempDir()
    store, err := NewFileStore(dir, []byte("test-encryption-key-32b!"))
    if err != nil {
        t.Fatalf("NewFileStore: %v", err)
    }

    _, err = store.Get(ProviderGitHub)
    if err == nil {
        t.Error("expected error for non-existent credential")
    }
}
```

**Step 3: Run test to verify it fails**

Run: `go test ./internal/credential/... -v`
Expected: FAIL - package does not exist

**Step 4: Implement FileStore**

```go
// internal/credential/store.go
package credential

import (
    "crypto/aes"
    "crypto/cipher"
    "crypto/rand"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "path/filepath"
)

// FileStore implements Store using encrypted files.
type FileStore struct {
    dir    string
    cipher cipher.AEAD
}

// NewFileStore creates a file-based credential store.
// key must be 32 bytes for AES-256.
func NewFileStore(dir string, key []byte) (*FileStore, error) {
    if len(key) != 32 {
        return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
    }

    if err := os.MkdirAll(dir, 0700); err != nil {
        return nil, fmt.Errorf("creating credential dir: %w", err)
    }

    block, err := aes.NewCipher(key)
    if err != nil {
        return nil, fmt.Errorf("creating cipher: %w", err)
    }

    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return nil, fmt.Errorf("creating GCM: %w", err)
    }

    return &FileStore{dir: dir, cipher: gcm}, nil
}

func (s *FileStore) path(provider Provider) string {
    return filepath.Join(s.dir, string(provider)+".enc")
}

func (s *FileStore) Save(cred Credential) error {
    data, err := json.Marshal(cred)
    if err != nil {
        return fmt.Errorf("marshaling credential: %w", err)
    }

    nonce := make([]byte, s.cipher.NonceSize())
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        return fmt.Errorf("generating nonce: %w", err)
    }

    encrypted := s.cipher.Seal(nonce, nonce, data, nil)
    if err := os.WriteFile(s.path(cred.Provider), encrypted, 0600); err != nil {
        return fmt.Errorf("writing credential file: %w", err)
    }

    return nil
}

func (s *FileStore) Get(provider Provider) (*Credential, error) {
    encrypted, err := os.ReadFile(s.path(provider))
    if err != nil {
        if os.IsNotExist(err) {
            return nil, fmt.Errorf("credential not found: %s", provider)
        }
        return nil, fmt.Errorf("reading credential file: %w", err)
    }

    nonceSize := s.cipher.NonceSize()
    if len(encrypted) < nonceSize {
        return nil, fmt.Errorf("invalid credential file")
    }

    nonce, ciphertext := encrypted[:nonceSize], encrypted[nonceSize:]
    data, err := s.cipher.Open(nil, nonce, ciphertext, nil)
    if err != nil {
        return nil, fmt.Errorf("decrypting credential: %w", err)
    }

    var cred Credential
    if err := json.Unmarshal(data, &cred); err != nil {
        return nil, fmt.Errorf("unmarshaling credential: %w", err)
    }

    return &cred, nil
}

func (s *FileStore) Delete(provider Provider) error {
    if err := os.Remove(s.path(provider)); err != nil && !os.IsNotExist(err) {
        return fmt.Errorf("deleting credential: %w", err)
    }
    return nil
}

func (s *FileStore) List() ([]Credential, error) {
    entries, err := os.ReadDir(s.dir)
    if err != nil {
        return nil, fmt.Errorf("reading credential dir: %w", err)
    }

    var creds []Credential
    for _, entry := range entries {
        if filepath.Ext(entry.Name()) != ".enc" {
            continue
        }
        provider := Provider(entry.Name()[:len(entry.Name())-4])
        cred, err := s.Get(provider)
        if err != nil {
            continue // Skip unreadable credentials
        }
        creds = append(creds, *cred)
    }

    return creds, nil
}
```

**Step 5: Run tests**

Run: `go test ./internal/credential/... -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/credential/
git commit -m "feat(credential): add encrypted file-based credential store"
```

---

## Task 2: GitHub Device Flow

**Files:**
- Create: `internal/credential/github.go`
- Create: `internal/credential/github_test.go`

**Step 1: Write the GitHub OAuth types and interface**

```go
// internal/credential/github.go
package credential

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"
)

const (
    githubDeviceCodeURL = "https://github.com/login/device/code"
    githubTokenURL      = "https://github.com/login/oauth/access_token"
)

// GitHubDeviceAuth handles GitHub device flow authentication.
type GitHubDeviceAuth struct {
    ClientID string
    Scopes   []string
}

// DeviceCodeResponse is the response from the device code endpoint.
type DeviceCodeResponse struct {
    DeviceCode      string `json:"device_code"`
    UserCode        string `json:"user_code"`
    VerificationURI string `json:"verification_uri"`
    ExpiresIn       int    `json:"expires_in"`
    Interval        int    `json:"interval"`
}

// TokenResponse is the response from the token endpoint.
type TokenResponse struct {
    AccessToken string `json:"access_token"`
    TokenType   string `json:"token_type"`
    Scope       string `json:"scope"`
    Error       string `json:"error,omitempty"`
}

// RequestDeviceCode initiates the device flow.
func (g *GitHubDeviceAuth) RequestDeviceCode(ctx context.Context) (*DeviceCodeResponse, error) {
    scope := "repo"
    if len(g.Scopes) > 0 {
        scope = ""
        for i, s := range g.Scopes {
            if i > 0 {
                scope += " "
            }
            scope += s
        }
    }

    body := fmt.Sprintf("client_id=%s&scope=%s", g.ClientID, scope)
    req, err := http.NewRequestWithContext(ctx, "POST", githubDeviceCodeURL, bytes.NewBufferString(body))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Accept", "application/json")
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("requesting device code: %w", err)
    }
    defer resp.Body.Close()

    var result DeviceCodeResponse
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, fmt.Errorf("decoding response: %w", err)
    }

    return &result, nil
}

// PollForToken polls for the access token until authorized or timeout.
func (g *GitHubDeviceAuth) PollForToken(ctx context.Context, deviceCode string, interval int) (*TokenResponse, error) {
    ticker := time.NewTicker(time.Duration(interval) * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-ticker.C:
            token, err := g.checkToken(ctx, deviceCode)
            if err != nil {
                return nil, err
            }
            if token.AccessToken != "" {
                return token, nil
            }
            if token.Error != "" && token.Error != "authorization_pending" && token.Error != "slow_down" {
                return nil, fmt.Errorf("token error: %s", token.Error)
            }
            if token.Error == "slow_down" {
                // Increase interval
                ticker.Reset(time.Duration(interval+5) * time.Second)
            }
        }
    }
}

func (g *GitHubDeviceAuth) checkToken(ctx context.Context, deviceCode string) (*TokenResponse, error) {
    body := fmt.Sprintf("client_id=%s&device_code=%s&grant_type=urn:ietf:params:oauth:grant-type:device_code", g.ClientID, deviceCode)
    req, err := http.NewRequestWithContext(ctx, "POST", githubTokenURL, bytes.NewBufferString(body))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Accept", "application/json")
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    var result TokenResponse
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, err
    }

    return &result, nil
}
```

**Step 2: Write basic test (mocked)**

```go
// internal/credential/github_test.go
package credential

import (
    "testing"
)

func TestGitHubDeviceAuth_Scopes(t *testing.T) {
    auth := &GitHubDeviceAuth{
        ClientID: "test-client-id",
        Scopes:   []string{"repo", "read:user"},
    }

    if auth.ClientID != "test-client-id" {
        t.Errorf("ClientID = %q, want %q", auth.ClientID, "test-client-id")
    }
    if len(auth.Scopes) != 2 {
        t.Errorf("Scopes length = %d, want 2", len(auth.Scopes))
    }
}
```

**Step 3: Run tests**

Run: `go test ./internal/credential/... -v`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/credential/github.go internal/credential/github_test.go
git commit -m "feat(credential): add GitHub device flow authentication"
```

---

## Task 3: CLI `grant` and `revoke` Commands

**Files:**
- Create: `cmd/agent/cli/grant.go`
- Create: `cmd/agent/cli/revoke.go`
- Modify: `internal/credential/store.go` (add default store helper)

**Step 1: Add default store helper**

```go
// Add to internal/credential/store.go

import (
    "os"
    "path/filepath"
)

// DefaultStoreDir returns the default credential store directory.
func DefaultStoreDir() string {
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".agentops", "credentials")
}

// DefaultEncryptionKey returns a key derived from the user's environment.
// In production, this should use a proper key derivation or keychain.
func DefaultEncryptionKey() []byte {
    // For now, use a fixed key. TODO: Use system keychain.
    return []byte("agentops-default-key-32bytes!!")
}
```

**Step 2: Write the grant command**

```go
// cmd/agent/cli/grant.go
package cli

import (
    "context"
    "fmt"
    "strings"
    "time"

    "github.com/andybons/agentops/internal/credential"
    "github.com/andybons/agentops/internal/log"
    "github.com/spf13/cobra"
)

// GitHub OAuth App Client ID - users should register their own app
// For development, this can be a test app or placeholder
const defaultGitHubClientID = "Ov23liYourClientID"

var grantCmd = &cobra.Command{
    Use:   "grant <provider>",
    Short: "Grant a credential for use in runs",
    Long: `Grant a credential that can be used by agent runs.

Examples:
  agent grant github                    # GitHub with default scopes
  agent grant github:repo               # GitHub with repo scope only
  agent grant github:repo,read:user     # GitHub with multiple scopes`,
    Args: cobra.ExactArgs(1),
    RunE: runGrant,
}

func init() {
    rootCmd.AddCommand(grantCmd)
}

func runGrant(cmd *cobra.Command, args []string) error {
    arg := args[0]

    // Parse provider:scopes format
    parts := strings.SplitN(arg, ":", 2)
    providerStr := parts[0]
    var scopes []string
    if len(parts) > 1 {
        scopes = strings.Split(parts[1], ",")
    }

    provider := credential.Provider(providerStr)

    switch provider {
    case credential.ProviderGitHub:
        return grantGitHub(scopes)
    default:
        return fmt.Errorf("unsupported provider: %s", providerStr)
    }
}

func grantGitHub(scopes []string) error {
    if len(scopes) == 0 {
        scopes = []string{"repo"}
    }

    auth := &credential.GitHubDeviceAuth{
        ClientID: defaultGitHubClientID,
        Scopes:   scopes,
    }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()

    log.Info("initiating GitHub device flow")

    deviceCode, err := auth.RequestDeviceCode(ctx)
    if err != nil {
        return fmt.Errorf("requesting device code: %w", err)
    }

    fmt.Printf("\nTo authorize, visit: %s\n", deviceCode.VerificationURI)
    fmt.Printf("Enter code: %s\n\n", deviceCode.UserCode)
    fmt.Println("Waiting for authorization...")

    token, err := auth.PollForToken(ctx, deviceCode.DeviceCode, deviceCode.Interval)
    if err != nil {
        return fmt.Errorf("getting token: %w", err)
    }

    // Store the credential
    store, err := credential.NewFileStore(
        credential.DefaultStoreDir(),
        credential.DefaultEncryptionKey(),
    )
    if err != nil {
        return fmt.Errorf("opening credential store: %w", err)
    }

    cred := credential.Credential{
        Provider:  credential.ProviderGitHub,
        Token:     token.AccessToken,
        Scopes:    scopes,
        CreatedAt: time.Now(),
    }

    if err := store.Save(cred); err != nil {
        return fmt.Errorf("saving credential: %w", err)
    }

    log.Info("GitHub credential saved", "scopes", scopes)
    fmt.Println("✓ GitHub credential saved successfully")
    return nil
}
```

**Step 3: Write the revoke command**

```go
// cmd/agent/cli/revoke.go
package cli

import (
    "fmt"

    "github.com/andybons/agentops/internal/credential"
    "github.com/andybons/agentops/internal/log"
    "github.com/spf13/cobra"
)

var revokeCmd = &cobra.Command{
    Use:   "revoke <provider>",
    Short: "Revoke a stored credential",
    Long: `Remove a stored credential from the local credential store.

Examples:
  agent revoke github`,
    Args: cobra.ExactArgs(1),
    RunE: runRevoke,
}

func init() {
    rootCmd.AddCommand(revokeCmd)
}

func runRevoke(cmd *cobra.Command, args []string) error {
    provider := credential.Provider(args[0])

    store, err := credential.NewFileStore(
        credential.DefaultStoreDir(),
        credential.DefaultEncryptionKey(),
    )
    if err != nil {
        return fmt.Errorf("opening credential store: %w", err)
    }

    // Check if credential exists
    if _, err := store.Get(provider); err != nil {
        return fmt.Errorf("no credential found for %s", provider)
    }

    if err := store.Delete(provider); err != nil {
        return fmt.Errorf("deleting credential: %w", err)
    }

    log.Info("credential revoked", "provider", provider)
    fmt.Printf("✓ %s credential revoked\n", provider)
    return nil
}
```

**Step 4: Test commands work**

Run: `go build ./cmd/agent && ./agent grant --help`
Expected: Shows grant command help

Run: `./agent revoke --help`
Expected: Shows revoke command help

**Step 5: Commit**

```bash
git add cmd/agent/cli/grant.go cmd/agent/cli/revoke.go internal/credential/store.go
git commit -m "feat(cli): add grant and revoke commands for credential management"
```

---

## Task 4: Auth Proxy - Basic Implementation

**Files:**
- Create: `internal/proxy/proxy.go`
- Create: `internal/proxy/proxy_test.go`

**Step 1: Write failing test for proxy**

```go
// internal/proxy/proxy_test.go
package proxy

import (
    "io"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestProxy_ForwardsRequests(t *testing.T) {
    // Create a test backend server
    backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("backend response"))
    }))
    defer backend.Close()

    // Create proxy
    p := NewProxy()

    // Create proxy server
    proxyServer := httptest.NewServer(p)
    defer proxyServer.Close()

    // Make request through proxy
    client := &http.Client{
        Transport: &http.Transport{
            Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
        },
    }

    resp, err := client.Get(backend.URL)
    if err != nil {
        t.Fatalf("request through proxy: %v", err)
    }
    defer resp.Body.Close()

    body, _ := io.ReadAll(resp.Body)
    if string(body) != "backend response" {
        t.Errorf("body = %q, want %q", string(body), "backend response")
    }
}

func TestProxy_InjectsAuthHeader(t *testing.T) {
    var receivedAuth string
    backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        receivedAuth = r.Header.Get("Authorization")
        w.Write([]byte("ok"))
    }))
    defer backend.Close()

    p := NewProxy()
    p.SetCredential("127.0.0.1", "Bearer test-token")

    proxyServer := httptest.NewServer(p)
    defer proxyServer.Close()

    client := &http.Client{
        Transport: &http.Transport{
            Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
        },
    }

    resp, err := client.Get(backend.URL)
    if err != nil {
        t.Fatalf("request: %v", err)
    }
    resp.Body.Close()

    if receivedAuth != "Bearer test-token" {
        t.Errorf("Authorization = %q, want %q", receivedAuth, "Bearer test-token")
    }
}

func mustParseURL(s string) *url.URL {
    u, err := url.Parse(s)
    if err != nil {
        panic(err)
    }
    return u
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/... -v`
Expected: FAIL - package does not exist

**Step 3: Implement basic proxy**

```go
// internal/proxy/proxy.go
package proxy

import (
    "io"
    "net"
    "net/http"
    "sync"
)

// Proxy is an HTTP proxy that injects credentials.
type Proxy struct {
    credentials map[string]string // host -> auth header value
    mu          sync.RWMutex
}

// NewProxy creates a new auth proxy.
func NewProxy() *Proxy {
    return &Proxy{
        credentials: make(map[string]string),
    }
}

// SetCredential sets the credential for a host.
func (p *Proxy) SetCredential(host, authHeader string) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.credentials[host] = authHeader
}

// ServeHTTP handles proxy requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if r.Method == http.MethodConnect {
        p.handleConnect(w, r)
        return
    }
    p.handleHTTP(w, r)
}

func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
    // Create outgoing request
    outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadGateway)
        return
    }

    // Copy headers
    for key, values := range r.Header {
        for _, value := range values {
            outReq.Header.Add(key, value)
        }
    }

    // Inject credentials if available
    p.mu.RLock()
    host := r.URL.Hostname()
    if auth, ok := p.credentials[host]; ok {
        outReq.Header.Set("Authorization", auth)
    }
    // Also check without port for localhost testing
    if auth, ok := p.credentials[r.URL.Host]; ok {
        outReq.Header.Set("Authorization", auth)
    }
    p.mu.RUnlock()

    // Remove proxy headers
    outReq.Header.Del("Proxy-Connection")
    outReq.Header.Del("Proxy-Authorization")

    // Forward request
    resp, err := http.DefaultTransport.RoundTrip(outReq)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadGateway)
        return
    }
    defer resp.Body.Close()

    // Copy response headers
    for key, values := range resp.Header {
        for _, value := range values {
            w.Header().Add(key, value)
        }
    }
    w.WriteHeader(resp.StatusCode)
    io.Copy(w, resp.Body)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
    // Establish connection to target
    targetConn, err := net.Dial("tcp", r.Host)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadGateway)
        return
    }

    // Send 200 OK
    hijacker, ok := w.(http.Hijacker)
    if !ok {
        http.Error(w, "hijacking not supported", http.StatusInternalServerError)
        targetConn.Close()
        return
    }

    clientConn, _, err := hijacker.Hijack()
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        targetConn.Close()
        return
    }

    clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

    // Tunnel data bidirectionally
    go func() {
        io.Copy(targetConn, clientConn)
        targetConn.Close()
    }()
    go func() {
        io.Copy(clientConn, targetConn)
        clientConn.Close()
    }()
}
```

**Step 4: Add missing import to test file**

```go
// Add to imports in proxy_test.go
import "net/url"
```

**Step 5: Run tests**

Run: `go test ./internal/proxy/... -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/proxy/
git commit -m "feat(proxy): add HTTP proxy with credential injection"
```

---

## Task 5: Integrate Proxy with Run Manager

**Files:**
- Modify: `internal/run/manager.go`
- Modify: `internal/docker/client.go`
- Create: `internal/proxy/server.go`

**Step 1: Add proxy server wrapper**

```go
// internal/proxy/server.go
package proxy

import (
    "context"
    "fmt"
    "net"
    "net/http"
)

// Server wraps a Proxy in an HTTP server.
type Server struct {
    proxy    *Proxy
    server   *http.Server
    listener net.Listener
    addr     string
}

// NewServer creates a new proxy server.
func NewServer(proxy *Proxy) *Server {
    return &Server{
        proxy: proxy,
    }
}

// Start starts the proxy server on an available port.
func (s *Server) Start() error {
    listener, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        return fmt.Errorf("creating listener: %w", err)
    }

    s.listener = listener
    s.addr = listener.Addr().String()

    s.server = &http.Server{
        Handler: s.proxy,
    }

    go s.server.Serve(listener)
    return nil
}

// Addr returns the proxy server address.
func (s *Server) Addr() string {
    return s.addr
}

// Stop stops the proxy server.
func (s *Server) Stop(ctx context.Context) error {
    if s.server != nil {
        return s.server.Shutdown(ctx)
    }
    return nil
}

// Proxy returns the underlying proxy.
func (s *Server) Proxy() *Proxy {
    return s.proxy
}
```

**Step 2: Update ContainerConfig to accept proxy env vars**

The ContainerConfig already has Env field, so we just need to use it.

**Step 3: Update Manager to start proxy and inject env vars**

```go
// Add to internal/run/manager.go in Create method, after creating the run struct:

// Start proxy server for this run if grants are specified
var proxyServer *proxy.Server
var proxyEnv []string
if len(opts.Grants) > 0 {
    p := proxy.NewProxy()

    // Load credentials for granted providers
    store, err := credential.NewFileStore(
        credential.DefaultStoreDir(),
        credential.DefaultEncryptionKey(),
    )
    if err == nil {
        for _, grant := range opts.Grants {
            provider := credential.Provider(strings.Split(grant, ":")[0])
            if cred, err := store.Get(provider); err == nil {
                // Map provider to host
                switch provider {
                case credential.ProviderGitHub:
                    p.SetCredential("api.github.com", "Bearer "+cred.Token)
                    p.SetCredential("github.com", "Bearer "+cred.Token)
                }
            }
        }
    }

    proxyServer = proxy.NewServer(p)
    if err := proxyServer.Start(); err != nil {
        return nil, fmt.Errorf("starting proxy: %w", err)
    }

    proxyEnv = []string{
        "HTTP_PROXY=http://" + proxyServer.Addr(),
        "HTTPS_PROXY=http://" + proxyServer.Addr(),
        "http_proxy=http://" + proxyServer.Addr(),
        "https_proxy=http://" + proxyServer.Addr(),
    }
}
```

**Step 4: Update the Create call to include env vars**

Modify the CreateContainer call to pass proxyEnv:

```go
containerID, err := m.docker.CreateContainer(ctx, docker.ContainerConfig{
    Name:       r.ID,
    Image:      defaultImage,
    Cmd:        cmd,
    WorkingDir: "/workspace",
    Env:        proxyEnv,
    Mounts: []docker.MountConfig{
        {
            Source:   opts.Workspace,
            Target:   "/workspace",
            ReadOnly: false,
        },
    },
})
```

**Step 5: Add imports to manager.go**

```go
import (
    "strings"

    "github.com/andybons/agentops/internal/credential"
    "github.com/andybons/agentops/internal/proxy"
)
```

**Step 6: Test the integration**

Run: `go build ./cmd/agent && ./agent run test . --grant github -- env | grep -i proxy`
Expected: Shows HTTP_PROXY and HTTPS_PROXY env vars

**Step 7: Commit**

```bash
git add internal/proxy/server.go internal/run/manager.go
git commit -m "feat(run): integrate auth proxy with run manager"
```

---

## Task 6: End-to-End Test with GitHub API

**Files:**
- No new files, manual testing

**Step 1: Grant GitHub credential (if not already done)**

Run: `./agent grant github`
Follow the device flow prompts.

**Step 2: Test GitHub API access through container**

Run: `./agent run test . --grant github -- curl -s https://api.github.com/user`
Expected: Returns authenticated user JSON (shows your GitHub username)

**Step 3: Verify without grant fails or returns anonymous**

Run: `./agent run test . -- curl -s https://api.github.com/user`
Expected: Returns error or anonymous response

**Step 4: Commit any final fixes**

```bash
git add -A
git commit -m "fix: address any issues found in e2e testing"
```

---

## Summary

After completing all tasks, Phase 2 delivers:

1. **Encrypted credential store** at `~/.agentops/credentials/`
2. **GitHub device flow** via `agent grant github`
3. **Credential revocation** via `agent revoke github`
4. **Auth proxy** that injects Authorization headers
5. **Container integration** with HTTP_PROXY/HTTPS_PROXY env vars
6. **End-to-end flow** where `agent run test . --grant github` enables authenticated GitHub API access

The container never sees the actual token - it just makes normal HTTP requests that get intercepted and authenticated by the proxy.
