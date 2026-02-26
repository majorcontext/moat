# Proxy Daemon Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Merge the per-run credential proxy and shared routing proxy into a single long-lived daemon process with a Unix socket API.

**Architecture:** A new `internal/daemon/` package contains the daemon server (HTTP over Unix socket), run context registry (per-run credentials keyed by auth token), and client. The `proxy.Proxy` is refactored to resolve credentials from a `RunContext` instead of its own maps. `manager.Create()` calls the daemon client instead of starting a per-run proxy.

**Tech Stack:** Go stdlib (`net`, `net/http`, `encoding/json`, `os/exec`), existing `proxy.Proxy`, `routing.ProxyServer`, `container.Runtime`

---

## Phase 1: Foundation Types

### Task 1: RunContext type

The core per-run state container. Implements `credential.ProxyConfigurer` so providers can configure it identically to how they configure `proxy.Proxy` today.

**Files:**
- Create: `internal/daemon/runcontext.go`
- Test: `internal/daemon/runcontext_test.go`

**Step 1: Write failing test**

```go
// internal/daemon/runcontext_test.go
package daemon

import (
	"testing"

	"github.com/majorcontext/moat/internal/credential"
)

func TestRunContext_ImplementsProxyConfigurer(t *testing.T) {
	var _ credential.ProxyConfigurer = (*RunContext)(nil)
}

func TestRunContext_SetCredential(t *testing.T) {
	rc := NewRunContext("run_1")
	rc.SetCredential("api.github.com", "token ghp_abc")

	cred, ok := rc.GetCredential("api.github.com")
	if !ok {
		t.Fatal("expected credential for api.github.com")
	}
	if cred.Name != "Authorization" {
		t.Errorf("expected header Authorization, got %s", cred.Name)
	}
	if cred.Value != "token ghp_abc" {
		t.Errorf("expected value 'token ghp_abc', got %s", cred.Value)
	}
}

func TestRunContext_SetCredentialHeader(t *testing.T) {
	rc := NewRunContext("run_1")
	rc.SetCredentialHeader("api.anthropic.com", "x-api-key", "sk-ant-123")

	cred, ok := rc.GetCredential("api.anthropic.com")
	if !ok {
		t.Fatal("expected credential")
	}
	if cred.Name != "x-api-key" || cred.Value != "sk-ant-123" {
		t.Errorf("unexpected credential: %+v", cred)
	}
}

func TestRunContext_AddExtraHeader(t *testing.T) {
	rc := NewRunContext("run_1")
	rc.AddExtraHeader("api.anthropic.com", "anthropic-beta", "flag1")
	rc.AddExtraHeader("api.anthropic.com", "anthropic-version", "2023-06-01")

	headers := rc.GetExtraHeaders("api.anthropic.com")
	if len(headers) != 2 {
		t.Fatalf("expected 2 extra headers, got %d", len(headers))
	}
}

func TestRunContext_GetCredentialWithPort(t *testing.T) {
	rc := NewRunContext("run_1")
	rc.SetCredential("api.github.com", "token abc")

	// Should match "api.github.com:443" -> "api.github.com"
	cred, ok := rc.GetCredential("api.github.com:443")
	if !ok {
		t.Fatal("expected credential for host:port lookup")
	}
	if cred.Value != "token abc" {
		t.Errorf("unexpected value: %s", cred.Value)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestRunContext -v`
Expected: FAIL — package doesn't exist yet

**Step 3: Write implementation**

```go
// internal/daemon/runcontext.go
package daemon

import (
	"net"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/proxy"
)

// CredentialEntry holds a credential header for proxy injection.
type CredentialEntry struct {
	Name  string `json:"name"`  // Header name (e.g., "Authorization", "x-api-key")
	Value string `json:"value"` // Header value
	Grant string `json:"grant"` // Grant name for logging
}

// ExtraHeaderEntry holds an additional header to inject.
type ExtraHeaderEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// TokenSubstitutionEntry holds a placeholder-to-real-token mapping.
type TokenSubstitutionEntry struct {
	Placeholder string `json:"placeholder"`
	RealToken   string `json:"real_token"`
}

// RunContext holds per-run proxy state. It implements credential.ProxyConfigurer
// so providers can configure it identically to how they configure proxy.Proxy.
type RunContext struct {
	RunID       string `json:"run_id"`
	ContainerID string `json:"container_id,omitempty"`
	AuthToken   string `json:"auth_token"`

	Credentials        map[string]CredentialEntry            `json:"credentials"`
	ExtraHeaders       map[string][]ExtraHeaderEntry         `json:"extra_headers"`
	RemoveHeaders      map[string][]string                   `json:"remove_headers"`
	TokenSubstitutions map[string]TokenSubstitutionEntry     `json:"token_substitutions"`
	ResponseTransformers map[string][]credential.ResponseTransformer `json:"-"` // not serialized

	MCPServers   []config.MCPServerConfig `json:"mcp_servers,omitempty"`
	NetworkPolicy string                  `json:"network_policy,omitempty"`
	NetworkAllow  []string                `json:"network_allow,omitempty"`
	AllowedHosts  []proxy.HostPattern     `json:"-"` // parsed from NetworkAllow, not serialized

	AWSConfig    *AWSConfig `json:"aws_config,omitempty"`

	RegisteredAt time.Time `json:"registered_at"`

	mu sync.RWMutex
}

// AWSConfig holds AWS credential provider configuration.
type AWSConfig struct {
	RoleARN         string        `json:"role_arn"`
	Region          string        `json:"region"`
	SessionDuration time.Duration `json:"session_duration"`
	ExternalID      string        `json:"external_id,omitempty"`
}

// NewRunContext creates a new RunContext for a run.
func NewRunContext(runID string) *RunContext {
	return &RunContext{
		RunID:              runID,
		Credentials:        make(map[string]CredentialEntry),
		ExtraHeaders:       make(map[string][]ExtraHeaderEntry),
		RemoveHeaders:      make(map[string][]string),
		TokenSubstitutions: make(map[string]TokenSubstitutionEntry),
		ResponseTransformers: make(map[string][]credential.ResponseTransformer),
		RegisteredAt:       time.Now(),
	}
}

// SetCredential implements credential.ProxyConfigurer.
func (rc *RunContext) SetCredential(host, value string) {
	rc.SetCredentialHeader(host, "Authorization", value)
}

// SetCredentialHeader implements credential.ProxyConfigurer.
func (rc *RunContext) SetCredentialHeader(host, headerName, headerValue string) {
	rc.SetCredentialWithGrant(host, headerName, headerValue, "")
}

// SetCredentialWithGrant implements credential.ProxyConfigurer.
func (rc *RunContext) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.Credentials[host] = CredentialEntry{Name: headerName, Value: headerValue, Grant: grant}
}

// AddExtraHeader implements credential.ProxyConfigurer.
func (rc *RunContext) AddExtraHeader(host, headerName, headerValue string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.ExtraHeaders[host] = append(rc.ExtraHeaders[host], ExtraHeaderEntry{Name: headerName, Value: headerValue})
}

// AddResponseTransformer implements credential.ProxyConfigurer.
func (rc *RunContext) AddResponseTransformer(host string, transformer credential.ResponseTransformer) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.ResponseTransformers[host] = append(rc.ResponseTransformers[host], transformer)
}

// RemoveRequestHeader implements credential.ProxyConfigurer.
func (rc *RunContext) RemoveRequestHeader(host, headerName string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.RemoveHeaders[host] = append(rc.RemoveHeaders[host], headerName)
}

// SetTokenSubstitution implements credential.ProxyConfigurer.
func (rc *RunContext) SetTokenSubstitution(host, placeholder, realToken string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.TokenSubstitutions[host] = TokenSubstitutionEntry{Placeholder: placeholder, RealToken: realToken}
}

// GetCredential returns the credential for a host, checking host:port fallback.
func (rc *RunContext) GetCredential(host string) (CredentialEntry, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if cred, ok := rc.Credentials[host]; ok {
		return cred, true
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		cred, ok := rc.Credentials[h]
		return cred, ok
	}
	return CredentialEntry{}, false
}

// GetExtraHeaders returns extra headers for a host, checking host:port fallback.
func (rc *RunContext) GetExtraHeaders(host string) []ExtraHeaderEntry {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if headers, ok := rc.ExtraHeaders[host]; ok {
		return headers
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		return rc.ExtraHeaders[h]
	}
	return nil
}

// GetRemoveHeaders returns headers to remove for a host.
func (rc *RunContext) GetRemoveHeaders(host string) []string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if headers, ok := rc.RemoveHeaders[host]; ok {
		return headers
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		return rc.RemoveHeaders[h]
	}
	return nil
}

// GetTokenSubstitution returns the token substitution for a host.
func (rc *RunContext) GetTokenSubstitution(host string) (TokenSubstitutionEntry, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if sub, ok := rc.TokenSubstitutions[host]; ok {
		return sub, true
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		sub, ok := rc.TokenSubstitutions[h]
		return sub, ok
	}
	return TokenSubstitutionEntry{}, false
}

// GetResponseTransformers returns response transformers for a host.
func (rc *RunContext) GetResponseTransformers(host string) []credential.ResponseTransformer {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if t, ok := rc.ResponseTransformers[host]; ok {
		return t
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		return rc.ResponseTransformers[h]
	}
	return nil
}
```

**Step 4: Run tests**

Run: `go test ./internal/daemon/ -run TestRunContext -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/daemon/runcontext.go internal/daemon/runcontext_test.go
git commit -m "feat(daemon): add RunContext type implementing ProxyConfigurer"
```

---

### Task 2: Run registry

Thread-safe in-memory registry mapping auth tokens to RunContexts.

**Files:**
- Create: `internal/daemon/registry.go`
- Test: `internal/daemon/registry_test.go`

**Step 1: Write failing test**

```go
// internal/daemon/registry_test.go
package daemon

import (
	"testing"
)

func TestRegistry_RegisterAndLookup(t *testing.T) {
	reg := NewRegistry()

	rc := NewRunContext("run_1")
	token := reg.Register(rc)

	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if rc.AuthToken != token {
		t.Error("expected RunContext.AuthToken to be set")
	}

	got, ok := reg.Lookup(token)
	if !ok {
		t.Fatal("expected to find registered run")
	}
	if got.RunID != "run_1" {
		t.Errorf("expected run_1, got %s", got.RunID)
	}
}

func TestRegistry_Unregister(t *testing.T) {
	reg := NewRegistry()
	rc := NewRunContext("run_1")
	token := reg.Register(rc)

	reg.Unregister(token)

	_, ok := reg.Lookup(token)
	if ok {
		t.Error("expected run to be unregistered")
	}
}

func TestRegistry_List(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewRunContext("run_1"))
	reg.Register(NewRunContext("run_2"))

	runs := reg.List()
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
}

func TestRegistry_UpdateContainerID(t *testing.T) {
	reg := NewRegistry()
	rc := NewRunContext("run_1")
	token := reg.Register(rc)

	ok := reg.UpdateContainerID(token, "container_abc")
	if !ok {
		t.Fatal("expected update to succeed")
	}

	got, _ := reg.Lookup(token)
	if got.ContainerID != "container_abc" {
		t.Errorf("expected container_abc, got %s", got.ContainerID)
	}
}

func TestRegistry_UniqueTokens(t *testing.T) {
	reg := NewRegistry()
	t1 := reg.Register(NewRunContext("run_1"))
	t2 := reg.Register(NewRunContext("run_2"))

	if t1 == t2 {
		t.Error("expected unique tokens")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestRegistry -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// internal/daemon/registry.go
package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// Registry is a thread-safe mapping of auth tokens to RunContexts.
type Registry struct {
	mu   sync.RWMutex
	runs map[string]*RunContext // token -> RunContext
}

// NewRegistry creates a new Registry.
func NewRegistry() *Registry {
	return &Registry{
		runs: make(map[string]*RunContext),
	}
}

// Register adds a RunContext to the registry and returns its generated auth token.
func (r *Registry) Register(rc *RunContext) string {
	token := generateToken()
	rc.AuthToken = token

	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[token] = rc
	return token
}

// Lookup finds a RunContext by auth token.
func (r *Registry) Lookup(token string) (*RunContext, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rc, ok := r.runs[token]
	return rc, ok
}

// Unregister removes a RunContext by auth token.
func (r *Registry) Unregister(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runs, token)
}

// UpdateContainerID sets the container ID for a registered run.
func (r *Registry) UpdateContainerID(token, containerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rc, ok := r.runs[token]
	if !ok {
		return false
	}
	rc.ContainerID = containerID
	return true
}

// List returns all registered RunContexts.
func (r *Registry) List() []*RunContext {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*RunContext, 0, len(r.runs))
	for _, rc := range r.runs {
		result = append(result, rc)
	}
	return result
}

// Count returns the number of registered runs.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.runs)
}

// generateToken creates a cryptographically secure 32-byte hex token.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
```

**Step 4: Run tests**

Run: `go test ./internal/daemon/ -run TestRegistry -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/daemon/registry.go internal/daemon/registry_test.go
git commit -m "feat(daemon): add run registry with token-based lookup"
```

---

### Task 3: Daemon API types

JSON-serializable request/response types for the Unix socket API.

**Files:**
- Create: `internal/daemon/api.go`
- Test: `internal/daemon/api_test.go`

**Step 1: Write failing test**

```go
// internal/daemon/api_test.go
package daemon

import (
	"encoding/json"
	"testing"
)

func TestRegisterRequest_JSON(t *testing.T) {
	req := RegisterRequest{
		RunID: "run_1",
		Credentials: []CredentialSpec{
			{Host: "api.github.com", Header: "Authorization", Value: "token abc"},
		},
		NetworkPolicy: "strict",
		NetworkAllow:  []string{"api.github.com"},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	var decoded RegisterRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RunID != "run_1" {
		t.Errorf("expected run_1, got %s", decoded.RunID)
	}
	if len(decoded.Credentials) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(decoded.Credentials))
	}
	if decoded.Credentials[0].Host != "api.github.com" {
		t.Error("credential host mismatch")
	}
}

func TestRegisterResponse_JSON(t *testing.T) {
	resp := RegisterResponse{
		AuthToken: "abc123",
		ProxyPort: 9100,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}

	var decoded RegisterResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AuthToken != "abc123" {
		t.Errorf("expected abc123, got %s", decoded.AuthToken)
	}
	if decoded.ProxyPort != 9100 {
		t.Errorf("expected 9100, got %d", decoded.ProxyPort)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestRegister.*_JSON -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// internal/daemon/api.go
package daemon

import (
	"github.com/majorcontext/moat/internal/config"
)

// CredentialSpec describes a credential to inject for a host.
type CredentialSpec struct {
	Host   string `json:"host"`
	Header string `json:"header"`
	Value  string `json:"value"`
	Grant  string `json:"grant,omitempty"`
}

// ExtraHeaderSpec describes an additional header to inject.
type ExtraHeaderSpec struct {
	Host       string `json:"host"`
	HeaderName string `json:"header_name"`
	Value      string `json:"value"`
}

// TokenSubstitutionSpec describes a token substitution.
type TokenSubstitutionSpec struct {
	Host        string `json:"host"`
	Placeholder string `json:"placeholder"`
	RealToken   string `json:"real_token"`
}

// RemoveHeaderSpec describes a header to remove from requests.
type RemoveHeaderSpec struct {
	Host       string `json:"host"`
	HeaderName string `json:"header_name"`
}

// RegisterRequest is sent to POST /v1/runs.
type RegisterRequest struct {
	RunID              string                    `json:"run_id"`
	Credentials        []CredentialSpec          `json:"credentials,omitempty"`
	ExtraHeaders       []ExtraHeaderSpec         `json:"extra_headers,omitempty"`
	RemoveHeaders      []RemoveHeaderSpec        `json:"remove_headers,omitempty"`
	TokenSubstitutions []TokenSubstitutionSpec   `json:"token_substitutions,omitempty"`
	MCPServers         []config.MCPServerConfig  `json:"mcp_servers,omitempty"`
	NetworkPolicy      string                    `json:"network_policy,omitempty"`
	NetworkAllow       []string                  `json:"network_allow,omitempty"`
	Grants             []string                  `json:"grants,omitempty"`
	AWSConfig          *AWSConfig                `json:"aws_config,omitempty"`
}

// RegisterResponse is returned from POST /v1/runs.
type RegisterResponse struct {
	AuthToken string `json:"auth_token"`
	ProxyPort int    `json:"proxy_port"`
}

// UpdateRunRequest is sent to PATCH /v1/runs/{token}.
type UpdateRunRequest struct {
	ContainerID string `json:"container_id"`
}

// HealthResponse is returned from GET /v1/health.
type HealthResponse struct {
	PID        int    `json:"pid"`
	ProxyPort  int    `json:"proxy_port"`
	RunCount   int    `json:"run_count"`
	StartedAt  string `json:"started_at"`
}

// RunInfo is an element of the list returned by GET /v1/runs.
type RunInfo struct {
	RunID       string `json:"run_id"`
	ContainerID string `json:"container_id,omitempty"`
	RegisteredAt string `json:"registered_at"`
}

// RouteRegistration is sent to POST /v1/routes/{agent}.
type RouteRegistration struct {
	Services map[string]string `json:"services"` // service name -> host:port
}

// ToRunContext converts a RegisterRequest into a RunContext.
func (req *RegisterRequest) ToRunContext() *RunContext {
	rc := NewRunContext(req.RunID)
	for _, c := range req.Credentials {
		rc.SetCredentialWithGrant(c.Host, c.Header, c.Value, c.Grant)
	}
	for _, h := range req.ExtraHeaders {
		rc.AddExtraHeader(h.Host, h.HeaderName, h.Value)
	}
	for _, r := range req.RemoveHeaders {
		rc.RemoveRequestHeader(r.Host, r.HeaderName)
	}
	for _, ts := range req.TokenSubstitutions {
		rc.SetTokenSubstitution(ts.Host, ts.Placeholder, ts.RealToken)
	}
	rc.MCPServers = req.MCPServers
	rc.NetworkPolicy = req.NetworkPolicy
	rc.NetworkAllow = req.NetworkAllow
	rc.AWSConfig = req.AWSConfig
	return rc
}
```

**Step 4: Run tests**

Run: `go test ./internal/daemon/ -run TestRegister -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/daemon/api.go internal/daemon/api_test.go
git commit -m "feat(daemon): add API request/response types"
```

---

### Task 4: Daemon server

HTTP server over Unix socket with API handlers.

**Files:**
- Create: `internal/daemon/server.go`
- Test: `internal/daemon/server_test.go`

**Step 1: Write failing test**

```go
// internal/daemon/server_test.go
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func testClient(sockPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}
}

func TestServer_HealthEndpoint(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")

	srv := NewServer(sockPath, 9100)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())

	client := testClient(sockPath)
	resp, err := client.Get("http://daemon/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.ProxyPort != 9100 {
		t.Errorf("expected proxy port 9100, got %d", health.ProxyPort)
	}
}

func TestServer_RegisterAndListRuns(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")

	srv := NewServer(sockPath, 9100)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())

	client := testClient(sockPath)

	// Register a run
	reqBody, _ := json.Marshal(RegisterRequest{
		RunID: "run_1",
		Credentials: []CredentialSpec{
			{Host: "api.github.com", Header: "Authorization", Value: "token abc"},
		},
	})
	resp, err := client.Post("http://daemon/v1/runs", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var regResp RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatal(err)
	}
	if regResp.AuthToken == "" {
		t.Error("expected non-empty auth token")
	}
	if regResp.ProxyPort != 9100 {
		t.Errorf("expected port 9100, got %d", regResp.ProxyPort)
	}

	// List runs
	listResp, err := client.Get("http://daemon/v1/runs")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()

	var runs []RunInfo
	if err := json.NewDecoder(listResp.Body).Decode(&runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].RunID != "run_1" {
		t.Errorf("expected run_1, got %s", runs[0].RunID)
	}
}

func TestServer_UnregisterRun(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")

	srv := NewServer(sockPath, 9100)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())

	client := testClient(sockPath)

	// Register
	reqBody, _ := json.Marshal(RegisterRequest{RunID: "run_1"})
	resp, err := client.Post("http://daemon/v1/runs", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	var regResp RegisterResponse
	json.NewDecoder(resp.Body).Decode(&regResp)
	resp.Body.Close()

	// Unregister
	req, _ := http.NewRequest(http.MethodDelete, "http://daemon/v1/runs/"+regResp.AuthToken, nil)
	delResp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	delResp.Body.Close()

	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", delResp.StatusCode)
	}

	// Verify empty list
	listResp, _ := client.Get("http://daemon/v1/runs")
	var runs []RunInfo
	json.NewDecoder(listResp.Body).Decode(&runs)
	listResp.Body.Close()

	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestServer_SocketCleanup(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")

	srv := NewServer(sockPath, 9100)
	srv.Start()
	srv.Stop(context.Background())

	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Error("expected socket file to be cleaned up")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestServer -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// internal/daemon/server.go
package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Server is the daemon's HTTP API server over a Unix socket.
type Server struct {
	sockPath  string
	proxyPort int
	registry  *Registry
	server    *http.Server
	listener  net.Listener
	startedAt time.Time

	// onEmpty is called when the last run is unregistered.
	// Used by the daemon to start the idle shutdown timer.
	onEmpty func()
}

// NewServer creates a new daemon API server.
func NewServer(sockPath string, proxyPort int) *Server {
	s := &Server{
		sockPath:  sockPath,
		proxyPort: proxyPort,
		registry:  NewRegistry(),
		startedAt: time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("POST /v1/runs", s.handleRegisterRun)
	mux.HandleFunc("GET /v1/runs", s.handleListRuns)
	mux.HandleFunc("PATCH /v1/runs/", s.handleUpdateRun)
	mux.HandleFunc("DELETE /v1/runs/", s.handleUnregisterRun)
	mux.HandleFunc("POST /v1/shutdown", s.handleShutdown)

	s.server = &http.Server{Handler: mux}
	return s
}

// Registry returns the server's run registry.
func (s *Server) Registry() *Registry {
	return s.registry
}

// SetOnEmpty sets a callback for when the registry becomes empty.
func (s *Server) SetOnEmpty(fn func()) {
	s.onEmpty = fn
}

// Start begins listening on the Unix socket.
func (s *Server) Start() error {
	// Remove stale socket if it exists
	os.Remove(s.sockPath)

	listener, err := net.Listen("unix", s.sockPath)
	if err != nil {
		return err
	}
	s.listener = listener

	go s.server.Serve(listener)
	return nil
}

// Stop gracefully shuts down the server and removes the socket.
func (s *Server) Stop(ctx context.Context) error {
	err := s.server.Shutdown(ctx)
	os.Remove(s.sockPath)
	return err
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		PID:       os.Getpid(),
		ProxyPort: s.proxyPort,
		RunCount:  s.registry.Count(),
		StartedAt: s.startedAt.Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleRegisterRun(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rc := req.ToRunContext()
	token := s.registry.Register(rc)

	resp := RegisterResponse{
		AuthToken: token,
		ProxyPort: s.proxyPort,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	runs := s.registry.List()
	infos := make([]RunInfo, len(runs))
	for i, rc := range runs {
		infos[i] = RunInfo{
			RunID:       rc.RunID,
			ContainerID: rc.ContainerID,
			RegisteredAt: rc.RegisteredAt.Format(time.RFC3339),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(infos)
}

func (s *Server) handleUpdateRun(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	var req UpdateRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !s.registry.UpdateContainerID(token, req.ContainerID) {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnregisterRun(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	s.registry.Unregister(token)

	if s.onEmpty != nil && s.registry.Count() == 0 {
		s.onEmpty()
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusAccepted)
	// Trigger async shutdown so the response is sent first
	go func() {
		s.Stop(context.Background())
	}()
}
```

**Step 4: Run tests**

Run: `go test ./internal/daemon/ -run TestServer -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/daemon/server.go internal/daemon/server_test.go
git commit -m "feat(daemon): add HTTP API server over Unix socket"
```

---

### Task 5: Daemon client

CLI-side client for the daemon API.

**Files:**
- Create: `internal/daemon/client.go`
- Test: `internal/daemon/client_test.go`

**Step 1: Write failing test**

```go
// internal/daemon/client_test.go
package daemon

import (
	"context"
	"path/filepath"
	"testing"
)

func TestClient_RegisterAndUnregister(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")

	srv := NewServer(sockPath, 9100)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())

	client := NewClient(sockPath)

	// Register
	resp, err := client.RegisterRun(context.Background(), RegisterRequest{
		RunID: "run_1",
		Credentials: []CredentialSpec{
			{Host: "api.github.com", Header: "Authorization", Value: "token abc"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.AuthToken == "" {
		t.Error("expected auth token")
	}
	if resp.ProxyPort != 9100 {
		t.Errorf("expected port 9100, got %d", resp.ProxyPort)
	}

	// Update container ID
	if err := client.UpdateRun(context.Background(), resp.AuthToken, "container_xyz"); err != nil {
		t.Fatal(err)
	}

	// Verify
	runs, err := client.ListRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].ContainerID != "container_xyz" {
		t.Errorf("expected container_xyz, got %s", runs[0].ContainerID)
	}

	// Unregister
	if err := client.UnregisterRun(context.Background(), resp.AuthToken); err != nil {
		t.Fatal(err)
	}

	runs, _ = client.ListRuns(context.Background())
	if len(runs) != 0 {
		t.Errorf("expected 0 runs after unregister, got %d", len(runs))
	}
}

func TestClient_Health(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")

	srv := NewServer(sockPath, 9100)
	srv.Start()
	defer srv.Stop(context.Background())

	client := NewClient(sockPath)
	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.ProxyPort != 9100 {
		t.Errorf("expected port 9100, got %d", health.ProxyPort)
	}
	if health.RunCount != 0 {
		t.Errorf("expected 0 runs, got %d", health.RunCount)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestClient -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// internal/daemon/client.go
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
)

// Client communicates with the daemon over a Unix socket.
type Client struct {
	sockPath   string
	httpClient *http.Client
}

// NewClient creates a daemon client connected to the given socket.
func NewClient(sockPath string) *Client {
	return &Client{
		sockPath: sockPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", sockPath)
				},
			},
		},
	}
}

// Health returns the daemon's health status.
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://daemon/v1/health", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer resp.Body.Close()

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, err
	}
	return &health, nil
}

// RegisterRun registers a new run with the daemon.
func (c *Client) RegisterRun(ctx context.Context, regReq RegisterRequest) (*RegisterResponse, error) {
	body, err := json.Marshal(regReq)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://daemon/v1/runs", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon returned %d", resp.StatusCode)
	}

	var regResp RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		return nil, err
	}
	return &regResp, nil
}

// UpdateRun updates a run's container ID (phase 2 of registration).
func (c *Client) UpdateRun(ctx context.Context, token, containerID string) error {
	body, err := json.Marshal(UpdateRunRequest{ContainerID: containerID})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, "http://daemon/v1/runs/"+token, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("run not found")
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("daemon returned %d", resp.StatusCode)
	}
	return nil
}

// UnregisterRun removes a run from the daemon.
func (c *Client) UnregisterRun(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, "http://daemon/v1/runs/"+token, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("daemon returned %d", resp.StatusCode)
	}
	return nil
}

// ListRuns returns all registered runs.
func (c *Client) ListRuns(ctx context.Context) ([]RunInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://daemon/v1/runs", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer resp.Body.Close()

	var runs []RunInfo
	if err := json.NewDecoder(resp.Body).Decode(&runs); err != nil {
		return nil, err
	}
	return runs, nil
}

// Shutdown requests the daemon to shut down gracefully.
func (c *Client) Shutdown(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://daemon/v1/shutdown", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Connection refused or reset is expected after shutdown
		return nil
	}
	defer resp.Body.Close()
	return nil
}
```

**Step 4: Run tests**

Run: `go test ./internal/daemon/ -run TestClient -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/daemon/client.go internal/daemon/client_test.go
git commit -m "feat(daemon): add Unix socket client"
```

---

## Phase 2: Proxy Refactor

### Task 6: Refactor proxy.Proxy to support per-RunContext credential resolution

This is the core refactor. The proxy's `ServeHTTP` method will extract the auth token from requests and look up a `RunContext` from the daemon's registry. Credential lookups use the `RunContext` instead of the proxy's own maps.

**Key design choice:** The `Proxy` struct gains a `ContextResolver` function field. The daemon sets this to a function that looks up `RunContext` from its registry. For backward compatibility during migration, if no resolver is set, the proxy falls back to its own maps (existing behavior).

**Files:**
- Modify: `internal/proxy/proxy.go`
- Test: `internal/proxy/proxy_test.go` (add new tests, existing tests keep passing via fallback)

**Step 1: Write failing test**

Add to `internal/proxy/proxy_test.go`:

```go
func TestProxy_PerRunContextCredentials(t *testing.T) {
	p := NewProxy()

	// Set up a context resolver that returns different credentials per token
	contexts := map[string]*RunContextData{
		"token_a": {
			Credentials: map[string]CredentialHeader{
				"api.github.com": {Name: "Authorization", Value: "token aaa"},
			},
		},
		"token_b": {
			Credentials: map[string]CredentialHeader{
				"api.github.com": {Name: "Authorization", Value: "token bbb"},
			},
		},
	}
	p.SetContextResolver(func(token string) (*RunContextData, bool) {
		rc, ok := contexts[token]
		return rc, ok
	})

	// Verify different credentials per token
	rcA, ok := p.ResolveContext("token_a")
	if !ok || rcA.Credentials["api.github.com"].Value != "token aaa" {
		t.Error("expected token_a credentials")
	}

	rcB, ok := p.ResolveContext("token_b")
	if !ok || rcB.Credentials["api.github.com"].Value != "token bbb" {
		t.Error("expected token_b credentials")
	}

	_, ok = p.ResolveContext("invalid")
	if ok {
		t.Error("expected invalid token to fail")
	}
}
```

Note: `RunContextData` is a proxy-level struct that mirrors the credential data from `daemon.RunContext` without importing the daemon package (to avoid circular dependencies). The daemon converts its `RunContext` to `RunContextData` when setting up the resolver.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/ -run TestProxy_PerRunContext -v`
Expected: FAIL

**Step 3: Write implementation**

Add to `internal/proxy/proxy.go`:

```go
// RunContextData holds per-run credential data for the proxy.
// This is a proxy-level type that avoids importing the daemon package.
type RunContextData struct {
	RunID              string
	Credentials        map[string]CredentialHeader
	ExtraHeaders       map[string][]ExtraHeader
	RemoveHeaders      map[string][]string
	TokenSubstitutions map[string]*TokenSubstitution
	ResponseTransformers map[string][]credential.ResponseTransformer
	MCPServers         []config.MCPServerConfig
	Policy             string
	AllowedHosts       []hostPattern
	AWSHandler         http.Handler
	CredStore          credential.Store
}

// CredentialHeader is the exported version of credentialHeader.
type CredentialHeader = credentialHeader

// ExtraHeader is the exported version of extraHeader.
type ExtraHeader = extraHeader

// TokenSubstitution is the exported version of tokenSubstitution.
type TokenSubstitution = tokenSubstitution

// ContextResolver resolves a proxy auth token to per-run context data.
type ContextResolver func(token string) (*RunContextData, bool)

// SetContextResolver sets the function used to resolve per-run contexts.
// When set, the proxy extracts the auth token from each request and uses
// the resolved context's credentials instead of its own maps.
func (p *Proxy) SetContextResolver(resolver ContextResolver) {
	p.contextResolver = resolver
}

// ResolveContext looks up a RunContextData by auth token.
func (p *Proxy) ResolveContext(token string) (*RunContextData, bool) {
	if p.contextResolver == nil {
		return nil, false
	}
	return p.contextResolver(token)
}
```

Add `contextResolver ContextResolver` field to `Proxy` struct.

Then refactor `ServeHTTP` to:
1. Extract auth token via `checkAuth` (which now returns the token)
2. If `contextResolver` is set, resolve the `RunContextData` and pass it through request context
3. All credential lookups (`getCredential`, `getExtraHeaders`, etc.) check for `RunContextData` in request context first, falling back to the proxy's own maps

The actual refactor of `handleHTTP`, `handleConnect`, `handleConnectWithInterception` to accept and use `RunContextData` is a careful but mechanical change — each call to `p.getCredential(host)` becomes `getCredentialFromContext(ctx, host)` which checks request context first.

**Step 4: Run tests**

Run: `go test ./internal/proxy/ -v`
Expected: ALL PASS (new test + existing tests via fallback)

**Step 5: Commit**

```bash
git add internal/proxy/proxy.go internal/proxy/proxy_test.go
git commit -m "feat(proxy): add ContextResolver for per-run credential scoping"
```

---

### Task 7: Wire proxy credential lookups through RunContextData

Complete the mechanical refactoring of all credential lookup paths in the proxy to use `RunContextData` when available.

**Files:**
- Modify: `internal/proxy/proxy.go` (handleHTTP, handleConnect, handleConnectWithInterception, checkNetworkPolicy, MCP relay)
- Modify: `internal/proxy/mcp.go` (injectMCPCredentials, handleMCPRelay)
- Test: `internal/proxy/proxy_test.go` (integration test: full request through proxy with per-context credentials)

**Step 1: Write failing integration test**

```go
func TestProxy_PerContextHTTPRequest(t *testing.T) {
	// Start a target HTTP server
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		w.Write([]byte(auth))
	}))
	defer target.Close()

	targetHost := target.Listener.Addr().String()

	p := NewProxy()
	p.SetContextResolver(func(token string) (*RunContextData, bool) {
		if token == "test_token" {
			h, _, _ := net.SplitHostPort(targetHost)
			return &RunContextData{
				Credentials: map[string]CredentialHeader{
					h: {Name: "Authorization", Value: "Bearer injected"},
				},
				Policy: "permissive",
			}, true
		}
		return nil, false
	})

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	// Make request through proxy with token
	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	req, _ := http.NewRequest("GET", target.URL+"/test", nil)
	req.Header.Set("Proxy-Authorization", "Bearer test_token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Bearer injected" {
		t.Errorf("expected 'Bearer injected', got %q", string(body))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/ -run TestProxy_PerContextHTTPRequest -v`
Expected: FAIL

**Step 3: Implement the refactoring**

This involves:

1. Modify `checkAuth()` to return the extracted token as a second return value
2. In `ServeHTTP`, store the resolved `*RunContextData` in the request context via `context.WithValue`
3. Create helper functions that read from context first:
   - `getCredentialForRequest(r *http.Request, host string) (credentialHeader, bool)`
   - `getExtraHeadersForRequest(r *http.Request, host string) []extraHeader`
   - `checkNetworkPolicyForRequest(r *http.Request, host string, port int) bool`
   - etc.
4. Replace all calls in `handleHTTP`, `handleConnect`, `handleConnectWithInterception` to use these helpers
5. Update MCP relay to read MCPServers and CredStore from context

Each helper follows the same pattern:
```go
func (p *Proxy) getCredentialForRequest(r *http.Request, host string) (credentialHeader, bool) {
	if rc := getRunContext(r); rc != nil {
		// ... look up in rc.Credentials with host:port fallback
	}
	return p.getCredential(host) // fallback to proxy's own maps
}
```

**Step 4: Run all proxy tests**

Run: `go test ./internal/proxy/ -v -race`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/proxy/
git commit -m "refactor(proxy): wire credential lookups through RunContextData"
```

---

## Phase 3: Daemon Lifecycle

### Task 8: Daemon startup with self-exec and lock file

The daemon starts itself as a detached background process via self-exec.

**Files:**
- Create: `internal/daemon/lifecycle.go`
- Test: `internal/daemon/lifecycle_test.go`

**Step 1: Write failing test**

```go
// internal/daemon/lifecycle_test.go
package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockFile_WriteAndRead(t *testing.T) {
	dir := t.TempDir()

	info := LockInfo{
		PID:       os.Getpid(),
		ProxyPort: 9100,
		SockPath:  filepath.Join(dir, "daemon.sock"),
	}

	if err := WriteLockFile(dir, info); err != nil {
		t.Fatal(err)
	}

	loaded, err := ReadLockFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PID != info.PID {
		t.Errorf("expected PID %d, got %d", info.PID, loaded.PID)
	}
	if loaded.ProxyPort != 9100 {
		t.Errorf("expected port 9100, got %d", loaded.ProxyPort)
	}
}

func TestLockFile_IsAlive(t *testing.T) {
	info := LockInfo{PID: os.Getpid()}
	if !info.IsAlive() {
		t.Error("current process should be alive")
	}

	info2 := LockInfo{PID: 999999999}
	if info2.IsAlive() {
		t.Error("non-existent PID should not be alive")
	}
}

func TestLockFile_Remove(t *testing.T) {
	dir := t.TempDir()
	WriteLockFile(dir, LockInfo{PID: 1})
	RemoveLockFile(dir)

	loaded, err := ReadLockFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != nil {
		t.Error("expected nil after removal")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestLockFile -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// internal/daemon/lifecycle.go
package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const lockFileName = "daemon.lock"

// LockInfo holds information about a running daemon.
type LockInfo struct {
	PID       int       `json:"pid"`
	ProxyPort int       `json:"proxy_port"`
	SockPath  string    `json:"sock_path"`
	StartedAt time.Time `json:"started_at"`
}

// IsAlive checks if the daemon process is still running.
func (l *LockInfo) IsAlive() bool {
	process, err := os.FindProcess(l.PID)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// WriteLockFile writes the daemon lock file.
func WriteLockFile(dir string, info LockInfo) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if info.StartedAt.IsZero() {
		info.StartedAt = time.Now()
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, lockFileName), data, 0644)
}

// ReadLockFile reads the daemon lock file. Returns nil, nil if not found.
func ReadLockFile(dir string) (*LockInfo, error) {
	data, err := os.ReadFile(filepath.Join(dir, lockFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// RemoveLockFile removes the daemon lock file.
func RemoveLockFile(dir string) {
	os.Remove(filepath.Join(dir, lockFileName))
}

// EnsureRunning starts the daemon if not already running.
// Returns a Client connected to the daemon.
func EnsureRunning(dir string, proxyPort int) (*Client, error) {
	sockPath := filepath.Join(dir, "daemon.sock")

	// Check existing daemon
	lock, err := ReadLockFile(dir)
	if err != nil {
		return nil, fmt.Errorf("reading daemon lock: %w", err)
	}

	if lock != nil && lock.IsAlive() {
		// Daemon already running
		client := NewClient(lock.SockPath)
		return client, nil
	}

	// Clean up stale lock
	if lock != nil {
		RemoveLockFile(dir)
		os.Remove(lock.SockPath)
	}

	// Start new daemon via self-exec
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("finding executable: %w", err)
	}

	args := []string{exe, "_daemon",
		"--dir", dir,
		"--proxy-port", fmt.Sprintf("%d", proxyPort),
	}

	// Start detached process
	attr := &os.ProcAttr{
		Dir:   "/",
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, nil, nil}, // detach stdout/stderr
		Sys: &syscall.SysProcAttr{
			Setsid: true, // new session, detach from terminal
		},
	}

	proc, err := os.StartProcess(exe, args, attr)
	if err != nil {
		return nil, fmt.Errorf("starting daemon: %w", err)
	}
	proc.Release()

	// Wait for socket to appear
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			client := NewClient(sockPath)
			// Verify health
			if _, err := client.Health(nil); err == nil {
				return client, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	return nil, fmt.Errorf("daemon did not start within 5 seconds")
}
```

**Step 4: Run tests**

Run: `go test ./internal/daemon/ -run TestLockFile -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/daemon/lifecycle.go internal/daemon/lifecycle_test.go
git commit -m "feat(daemon): add lock file management and EnsureRunning lifecycle"
```

---

### Task 9: Hidden `_daemon` CLI command

The entry point that the daemon process runs when started via self-exec.

**Files:**
- Create: `cmd/moat/cli/daemon.go`

**Step 1: Write implementation** (no TDD for CLI wiring — tested via integration)

```go
// cmd/moat/cli/daemon.go
package cli

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/proxy"
	"github.com/spf13/cobra"
)

var daemonDir string
var daemonProxyPort int

var daemonCmd = &cobra.Command{
	Use:    "_daemon",
	Hidden: true,
	Short:  "Run the proxy daemon (internal use)",
	RunE:   runDaemon,
}

func init() {
	daemonCmd.Flags().StringVar(&daemonDir, "dir", "", "daemon working directory")
	daemonCmd.Flags().IntVar(&daemonProxyPort, "proxy-port", 0, "proxy port")
	rootCmd.AddCommand(daemonCmd)
}

func runDaemon(cmd *cobra.Command, args []string) error {
	if daemonDir == "" {
		daemonDir = filepath.Join(config.GlobalConfigDir(), "proxy")
	}

	sockPath := filepath.Join(daemonDir, "daemon.sock")

	// Create API server
	apiServer := daemon.NewServer(sockPath, daemonProxyPort)

	// Create credential proxy
	p := proxy.NewProxy()

	// Set up CA for TLS interception
	caDir := filepath.Join(daemonDir, "ca")
	ca, err := proxy.NewCA(caDir)
	if err != nil {
		return err
	}
	p.SetCA(ca)

	// Wire the proxy's context resolver to the API server's registry
	p.SetContextResolver(func(token string) (*proxy.RunContextData, bool) {
		rc, ok := apiServer.Registry().Lookup(token)
		if !ok {
			return nil, false
		}
		return rc.ToProxyContextData(), true
	})

	// Start credential proxy on fixed port
	proxyServer := proxy.NewServer(p)
	proxyServer.SetBindAddr("0.0.0.0") // daemon always binds to all interfaces
	if err := proxyServer.Start(); err != nil {
		return err
	}

	// Start API server
	if err := apiServer.Start(); err != nil {
		proxyServer.Stop(context.Background())
		return err
	}

	// Write lock file
	if err := daemon.WriteLockFile(daemonDir, daemon.LockInfo{
		PID:       os.Getpid(),
		ProxyPort: daemonProxyPort,
		SockPath:  sockPath,
	}); err != nil {
		apiServer.Stop(context.Background())
		proxyServer.Stop(context.Background())
		return err
	}

	log.Info("daemon started", "pid", os.Getpid(), "proxy_port", daemonProxyPort, "sock", sockPath)

	// Set up idle auto-shutdown (5 min)
	idleShutdown := daemon.NewIdleTimer(5*60, func() {
		log.Info("daemon idle timeout, shutting down")
		apiServer.Stop(context.Background())
		proxyServer.Stop(context.Background())
		daemon.RemoveLockFile(daemonDir)
	})
	apiServer.SetOnEmpty(idleShutdown.Reset)

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info("daemon shutting down")
	apiServer.Stop(context.Background())
	proxyServer.Stop(context.Background())
	daemon.RemoveLockFile(daemonDir)

	return nil
}
```

**Step 2: Add `ToProxyContextData()` method to `RunContext`**

This conversion method goes in `internal/daemon/runcontext.go` to bridge daemon types to proxy types without circular imports.

**Step 3: Add `IdleTimer` to `internal/daemon/idle.go`**

```go
package daemon

import (
	"sync"
	"time"
)

// IdleTimer triggers a callback after a period of inactivity.
type IdleTimer struct {
	duration time.Duration
	callback func()
	timer    *time.Timer
	mu       sync.Mutex
}

// NewIdleTimer creates an idle timer. It does not start automatically.
func NewIdleTimer(seconds int, callback func()) *IdleTimer {
	return &IdleTimer{
		duration: time.Duration(seconds) * time.Second,
		callback: callback,
	}
}

// Reset restarts the idle timer. If runs exist, it stops the timer.
// Call this from the onEmpty callback to start countdown after last unregister.
func (t *IdleTimer) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(t.duration, t.callback)
}

// Cancel stops the idle timer without firing.
func (t *IdleTimer) Cancel() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
	}
}
```

**Step 4: Build to verify compilation**

Run: `go build ./cmd/moat/`
Expected: SUCCESS

**Step 5: Commit**

```bash
git add cmd/moat/cli/daemon.go internal/daemon/idle.go
git commit -m "feat(daemon): add hidden _daemon CLI command and idle timer"
```

---

### Task 10: Container liveness checker

Background goroutine that cleans up registrations for dead containers.

**Files:**
- Create: `internal/daemon/liveness.go`
- Test: `internal/daemon/liveness_test.go`

**Step 1: Write failing test**

```go
// internal/daemon/liveness_test.go
package daemon

import (
	"context"
	"testing"
	"time"
)

type mockContainerChecker struct {
	alive map[string]bool
}

func (m *mockContainerChecker) IsContainerRunning(ctx context.Context, id string) bool {
	return m.alive[id]
}

func TestLivenessChecker_RemovesDeadContainers(t *testing.T) {
	reg := NewRegistry()

	rc1 := NewRunContext("run_1")
	rc1.ContainerID = "alive_container"
	reg.Register(rc1)

	rc2 := NewRunContext("run_2")
	rc2.ContainerID = "dead_container"
	reg.Register(rc2)

	checker := &mockContainerChecker{
		alive: map[string]bool{
			"alive_container": true,
			"dead_container":  false,
		},
	}

	lc := NewLivenessChecker(reg, checker)
	lc.CheckOnce(context.Background())

	if reg.Count() != 1 {
		t.Errorf("expected 1 run after cleanup, got %d", reg.Count())
	}

	// Verify the alive one is still there
	runs := reg.List()
	if runs[0].RunID != "run_1" {
		t.Errorf("expected run_1 to survive, got %s", runs[0].RunID)
	}
}

func TestLivenessChecker_SkipsRunsWithoutContainerID(t *testing.T) {
	reg := NewRegistry()

	rc := NewRunContext("run_pending")
	// No ContainerID set (phase 1 only)
	reg.Register(rc)

	checker := &mockContainerChecker{alive: map[string]bool{}}

	lc := NewLivenessChecker(reg, checker)
	lc.CheckOnce(context.Background())

	if reg.Count() != 1 {
		t.Error("runs without ContainerID should not be cleaned up")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestLivenessChecker -v`
Expected: FAIL

**Step 3: Write implementation**

```go
// internal/daemon/liveness.go
package daemon

import (
	"context"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

// ContainerChecker checks if a container is still running.
type ContainerChecker interface {
	IsContainerRunning(ctx context.Context, id string) bool
}

// LivenessChecker periodically checks container liveness and cleans up dead runs.
type LivenessChecker struct {
	registry *Registry
	checker  ContainerChecker
	interval time.Duration
	onCleanup func(token string) // called when a run is cleaned up
}

// NewLivenessChecker creates a new liveness checker.
func NewLivenessChecker(registry *Registry, checker ContainerChecker) *LivenessChecker {
	return &LivenessChecker{
		registry: registry,
		checker:  checker,
		interval: 30 * time.Second,
	}
}

// SetOnCleanup sets a callback invoked when a run is cleaned up.
func (lc *LivenessChecker) SetOnCleanup(fn func(token string)) {
	lc.onCleanup = fn
}

// CheckOnce performs a single liveness check for all registered runs.
func (lc *LivenessChecker) CheckOnce(ctx context.Context) {
	for _, rc := range lc.registry.List() {
		// Skip runs that haven't completed phase 2 yet
		if rc.ContainerID == "" {
			continue
		}

		if !lc.checker.IsContainerRunning(ctx, rc.ContainerID) {
			log.Info("container no longer running, cleaning up",
				"run_id", rc.RunID,
				"container_id", rc.ContainerID)
			lc.registry.Unregister(rc.AuthToken)
			if lc.onCleanup != nil {
				lc.onCleanup(rc.AuthToken)
			}
		}
	}
}

// Run starts the periodic liveness check loop. Blocks until ctx is canceled.
func (lc *LivenessChecker) Run(ctx context.Context) {
	ticker := time.NewTicker(lc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lc.CheckOnce(ctx)
		}
	}
}
```

**Step 4: Run tests**

Run: `go test ./internal/daemon/ -run TestLivenessChecker -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/daemon/liveness.go internal/daemon/liveness_test.go
git commit -m "feat(daemon): add container liveness checker"
```

---

## Phase 4: Integration

### Task 11: Manager.Create() integration with daemon client

Replace the proxy setup block in `manager.Create()` with daemon client calls. This is the largest single change.

**Files:**
- Modify: `internal/run/manager.go` (lines ~396-640, plus all cleanupProxy calls)
- Modify: `internal/run/run.go` (remove ProxyServer field, simplify stopProxyServer)
- Modify: `internal/run/manager.go` (Manager struct: add daemonClient field)

**Step 1: Add daemon client to Manager**

In `internal/run/manager.go`, add to the `Manager` struct:

```go
type Manager struct {
	runtime        container.Runtime
	runs           map[string]*Run
	routes         *routing.RouteTable
	proxyLifecycle *routing.Lifecycle
	daemonClient   *daemon.Client // daemon proxy client
	mu             sync.RWMutex
}
```

In `NewManagerWithOptions`, after creating the routing lifecycle:

```go
// Ensure daemon is running for credential proxy
daemonDir := filepath.Join(config.GlobalConfigDir(), "proxy")
daemonClient, err := daemon.EnsureRunning(daemonDir, 0) // port 0 = use default or existing
if err != nil {
	log.Debug("daemon not available, will start on first run", "error", err)
}

m := &Manager{
	runtime:        rt,
	runs:           make(map[string]*Run),
	routes:         lifecycle.Routes(),
	proxyLifecycle: lifecycle,
	daemonClient:   daemonClient,
}
```

**Step 2: Replace proxy setup block in Create()**

Replace lines ~396-640 (the `needsProxyForGrants || needsProxyForFirewall` block) with:

```go
if needsProxyForGrants || needsProxyForFirewall {
	// Ensure daemon is running
	if m.daemonClient == nil {
		daemonDir := filepath.Join(config.GlobalConfigDir(), "proxy")
		client, err := daemon.EnsureRunning(daemonDir, 0)
		if err != nil {
			return nil, fmt.Errorf("starting proxy daemon: %w", err)
		}
		m.daemonClient = client
	}

	// Build registration request from grants
	regReq := daemon.RegisterRequest{
		RunID: r.ID,
	}

	// Load credentials and configure request
	key, keyErr := credential.DefaultEncryptionKey()
	if keyErr != nil {
		return nil, fmt.Errorf("getting encryption key: %w", keyErr)
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return nil, fmt.Errorf("opening credential store: %w", err)
	}

	// Use a temporary RunContext as ProxyConfigurer to capture what providers set
	tempRC := daemon.NewRunContext(r.ID)
	for _, grant := range opts.Grants {
		grantName := strings.Split(grant, ":")[0]
		if grantName == "ssh" {
			continue
		}
		credName := credential.Provider(provider.ResolveName(grantName))
		cred, getErr := store.Get(credName)
		if getErr != nil {
			return nil, fmt.Errorf("grant %q: credential not found: %w", grantName, getErr)
		}
		provCred := provider.FromLegacy(cred)
		prov := provider.Get(grantName)
		if prov == nil {
			continue
		}
		prov.ConfigureProxy(tempRC, provCred)
		providerEnv = append(providerEnv, prov.ContainerEnv(provCred)...)

		// Handle AWS
		if ep := provider.GetEndpoint(string(credName)); ep != nil {
			awsCfg, err := awsprov.ConfigFromCredential(provCred)
			if err != nil {
				return nil, fmt.Errorf("parsing AWS credential: %w", err)
			}
			regReq.AWSConfig = &daemon.AWSConfig{
				RoleARN:         awsCfg.RoleARN,
				Region:          awsCfg.Region,
				SessionDuration: awsCfg.SessionDuration,
				ExternalID:      awsCfg.ExternalID,
			}
		}
	}

	// Convert tempRC to registration request
	regReq = tempRC.ToRegisterRequest(regReq)

	if opts.Config != nil {
		regReq.NetworkPolicy = opts.Config.Network.Policy
		regReq.NetworkAllow = opts.Config.Network.Allow
		regReq.Grants = opts.Grants
		regReq.MCPServers = opts.Config.MCP
	}

	// Phase 1: register with daemon, get token and port
	regResp, err := m.daemonClient.RegisterRun(ctx, regReq)
	if err != nil {
		return nil, fmt.Errorf("registering with proxy daemon: %w", err)
	}

	r.ProxyAuthToken = regResp.AuthToken
	r.ProxyPort = regResp.ProxyPort

	// Set up deferred cleanup
	proxyRegistered := true
	defer func() {
		if proxyRegistered && r.ProxyServer == nil {
			// Only unregister if we registered but didn't complete creation
			// (the nil ProxyServer check is removed in later cleanup)
		}
	}()

	// Build proxy URL for container
	hostAddr := m.runtime.GetHostAddress()
	proxyURL := fmt.Sprintf("http://moat:%s@%s:%d", regResp.AuthToken, hostAddr, regResp.ProxyPort)

	if needsProxyForFirewall {
		r.FirewallEnabled = true
		r.ProxyHost = hostAddr
	}

	// ... rest of proxy env setup stays the same ...
}
```

**Step 3: Replace all cleanupProxy() calls**

Replace the `cleanupProxy` helper and all its call sites with:

```go
cleanupDaemonRun := func() {
	if r.ProxyAuthToken != "" && m.daemonClient != nil {
		if err := m.daemonClient.UnregisterRun(context.Background(), r.ProxyAuthToken); err != nil {
			log.Debug("failed to unregister run from daemon", "error", err)
		}
		r.ProxyAuthToken = ""
	}
}
```

Then replace every `cleanupProxy(proxyServer)` with `cleanupDaemonRun()`.

**Step 4: Add phase 2 call after container creation**

After `m.runtime.Create()` succeeds and we have a container ID:

```go
if r.ProxyAuthToken != "" && m.daemonClient != nil {
	if err := m.daemonClient.UpdateRun(ctx, r.ProxyAuthToken, r.ContainerID); err != nil {
		log.Debug("failed to update daemon with container ID", "error", err)
	}
}
```

**Step 5: Update Run struct**

In `internal/run/run.go`:
- Remove `ProxyServer *proxy.Server` field
- Remove `proxyStopOnce sync.Once` field
- Remove `tokenRefreshCancel context.CancelFunc` field
- Simplify `stopProxyServer` to be a no-op (or remove entirely and update all callers)

**Step 6: Update Destroy()**

In `Manager.Destroy()`, replace:
```go
if err := r.stopProxyServer(ctx); err != nil {
	ui.Warnf("Stopping proxy: %v", err)
}
```
with:
```go
if r.ProxyAuthToken != "" && m.daemonClient != nil {
	if err := m.daemonClient.UnregisterRun(ctx, r.ProxyAuthToken); err != nil {
		ui.Warnf("Unregistering from proxy daemon: %v", err)
	}
}
```

**Step 7: Run tests**

Run: `make test-unit`
Expected: PASS (existing unit tests should still pass; integration tests may need daemon running)

**Step 8: Commit**

```bash
git add internal/run/manager.go internal/run/run.go
git commit -m "feat(run): integrate daemon client into manager lifecycle"
```

---

### Task 12: Update `moat proxy` CLI commands

Update the existing proxy CLI commands to work with the daemon.

**Files:**
- Modify: `cmd/moat/cli/proxy.go`

**Step 1: Update startProxy**

The `moat proxy start` command should start the daemon in the foreground (for manual use) or ensure it's running.

```go
func startProxy(cmd *cobra.Command, args []string) error {
	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")

	// If --foreground flag, run daemon inline
	// Otherwise, ensure daemon is running in background
	return runDaemon(cmd, args) // reuse the daemon entry point
}
```

**Step 2: Update stopProxy**

```go
func stopProxy(cmd *cobra.Command, args []string) error {
	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")
	sockPath := filepath.Join(proxyDir, "daemon.sock")

	client := daemon.NewClient(sockPath)
	if err := client.Shutdown(context.Background()); err != nil {
		// Try SIGTERM as fallback
		lock, _ := daemon.ReadLockFile(proxyDir)
		if lock != nil && lock.IsAlive() {
			process, _ := os.FindProcess(lock.PID)
			process.Signal(syscall.SIGTERM)
			fmt.Printf("Stopped daemon (pid %d)\n", lock.PID)
			return nil
		}
		fmt.Println("Daemon is not running")
		return nil
	}

	fmt.Println("Daemon shutdown requested")
	return nil
}
```

**Step 3: Update statusProxy**

```go
func statusProxy(cmd *cobra.Command, args []string) error {
	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")
	sockPath := filepath.Join(proxyDir, "daemon.sock")

	client := daemon.NewClient(sockPath)
	health, err := client.Health(context.Background())
	if err != nil {
		fmt.Println("Daemon is not running")
		return nil
	}

	fmt.Printf("Daemon running (pid %d)\n", health.PID)
	fmt.Printf("  Proxy port: %d\n", health.ProxyPort)
	fmt.Printf("  Active runs: %d\n", health.RunCount)
	fmt.Printf("  Started: %s\n", health.StartedAt)

	// List runs
	runs, err := client.ListRuns(context.Background())
	if err == nil && len(runs) > 0 {
		fmt.Println("\nRegistered runs:")
		for _, r := range runs {
			fmt.Printf("  - %s", r.RunID)
			if r.ContainerID != "" {
				fmt.Printf(" (container: %s)", r.ContainerID[:12])
			}
			fmt.Println()
		}
	}

	return nil
}
```

**Step 4: Build and verify**

Run: `go build ./cmd/moat/`
Expected: SUCCESS

**Step 5: Commit**

```bash
git add cmd/moat/cli/proxy.go
git commit -m "feat(cli): update proxy commands for daemon"
```

---

### Task 13: Token refresh in daemon

Move the token refresh loop from the manager to the daemon.

**Files:**
- Create: `internal/daemon/refresh.go`
- Modify: `internal/daemon/server.go` (start refresh on registration)

**Step 1: Write implementation**

The daemon needs access to the credential store and provider registry to refresh tokens. The `RegisterRequest` includes grant names so the daemon can look up providers.

```go
// internal/daemon/refresh.go
package daemon

import (
	"context"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
)

// StartTokenRefresh begins a background goroutine that periodically
// refreshes credentials for the given run context.
func StartTokenRefresh(ctx context.Context, rc *RunContext, grants []string) {
	// Find refreshable providers
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		log.Debug("token refresh: cannot get encryption key", "error", err)
		return
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		log.Debug("token refresh: cannot open store", "error", err)
		return
	}

	var hasRefreshable bool
	for _, grant := range grants {
		credName := credential.Provider(provider.ResolveName(grant))
		prov := provider.Get(grant)
		if prov == nil {
			continue
		}
		if rp, ok := prov.(provider.RefreshableProvider); ok {
			cred, err := store.Get(credName)
			if err != nil {
				continue
			}
			provCred := provider.FromLegacy(cred)
			if rp.CanRefresh(provCred) {
				hasRefreshable = true
				break
			}
		}
	}

	if !hasRefreshable {
		return
	}

	go func() {
		ticker := time.NewTicker(5 * time.Minute) // match existing interval logic
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refreshTokensForRun(ctx, rc, grants, store)
			}
		}
	}()
}

func refreshTokensForRun(ctx context.Context, rc *RunContext, grants []string, store credential.Store) {
	for _, grant := range grants {
		credName := credential.Provider(provider.ResolveName(grant))
		prov := provider.Get(grant)
		if prov == nil {
			continue
		}
		rp, ok := prov.(provider.RefreshableProvider)
		if !ok {
			continue
		}
		cred, err := store.Get(credName)
		if err != nil {
			continue
		}
		provCred := provider.FromLegacy(cred)
		if !rp.CanRefresh(provCred) {
			continue
		}

		refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = rp.Refresh(refreshCtx, rc, provCred)
		cancel()
		if err != nil {
			log.Debug("token refresh failed", "provider", credName, "error", err)
		}
	}
}
```

**Step 2: Wire refresh into server registration**

In `server.go`'s `handleRegisterRun`, after registering:

```go
if len(req.Grants) > 0 {
	refreshCtx, cancel := context.WithCancel(context.Background())
	// Store cancel func for cleanup on unregister
	rc.refreshCancel = cancel
	StartTokenRefresh(refreshCtx, rc, req.Grants)
}
```

**Step 3: Cancel refresh on unregister**

In `handleUnregisterRun`, before unregistering:

```go
rc, ok := s.registry.Lookup(token)
if ok && rc.refreshCancel != nil {
	rc.refreshCancel()
}
```

**Step 4: Build and test**

Run: `go build ./cmd/moat/ && go test ./internal/daemon/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/daemon/refresh.go internal/daemon/server.go
git commit -m "feat(daemon): add token refresh support"
```

---

### Task 14: Routing proxy integration

Move routing proxy ownership from `routing.Lifecycle` in the manager to the daemon.

**Files:**
- Modify: `internal/daemon/server.go` (add route endpoints)
- Modify: `cmd/moat/cli/daemon.go` (start routing proxy in daemon)
- Modify: `internal/run/manager.go` (route registration via daemon client)

**Step 1: Add route handlers to daemon server**

In `server.go`, add handlers and wire `routing.RouteTable`:

```go
func (s *Server) handleRegisterRoutes(w http.ResponseWriter, r *http.Request) {
	agent := strings.TrimPrefix(r.URL.Path, "/v1/routes/")
	var reg RouteRegistration
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.routes.Add(agent, reg.Services); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnregisterRoutes(w http.ResponseWriter, r *http.Request) {
	agent := strings.TrimPrefix(r.URL.Path, "/v1/routes/")
	s.routes.Remove(agent)
	w.WriteHeader(http.StatusNoContent)
}
```

**Step 2: Add route registration to daemon client**

```go
func (c *Client) RegisterRoutes(ctx context.Context, agent string, services map[string]string) error {
	// POST /v1/routes/{agent}
}

func (c *Client) UnregisterRoutes(ctx context.Context, agent string) error {
	// DELETE /v1/routes/{agent}
}
```

**Step 3: Start routing proxy in daemon**

In `cmd/moat/cli/daemon.go`'s `runDaemon()`, after starting the credential proxy, start the routing proxy using existing `routing.ProxyServer`.

**Step 4: Update manager to use daemon for route registration**

Replace direct `m.routes.Add()` calls with `m.daemonClient.RegisterRoutes()`.

**Step 5: Build and test**

Run: `go build ./cmd/moat/ && make test-unit`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/daemon/ cmd/moat/cli/daemon.go internal/run/manager.go
git commit -m "feat(daemon): integrate routing proxy into daemon"
```

---

## Phase 5: Cleanup

### Task 15: Remove obsolete code and run full test suite

**Files:**
- Modify: `internal/run/manager.go` (remove `runTokenRefreshLoop`, `refreshToken`, `refreshTarget`, unused imports)
- Modify: `internal/run/run.go` (remove obsolete fields)
- Modify: `internal/run/manager.go` (remove `proxyLifecycle` field if fully migrated)

**Step 1: Remove dead code**

- Remove `runTokenRefreshLoop()` method
- Remove `refreshToken()` method
- Remove `refreshTarget` type
- Remove `cleanupProxy` helper (already replaced)
- Remove `proxyLifecycle` from Manager if routing proxy is fully in daemon
- Clean up unused imports

**Step 2: Run linter**

Run: `make lint`
Expected: PASS

**Step 3: Run full test suite**

Run: `make test-unit`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/run/
git commit -m "refactor(run): remove obsolete proxy lifecycle code"
```

---

### Task 16: Update documentation

**Files:**
- Modify: `docs/content/reference/01-cli.md` (add daemon info to proxy commands)
- Modify: `CLAUDE.md` (update architecture section)

**Step 1: Update CLI reference**

Add `moat proxy status` output changes (shows daemon PID, active runs).

**Step 2: Update CLAUDE.md architecture**

Update the proxy section to describe the daemon model.

**Step 3: Commit**

```bash
git add docs/ CLAUDE.md
git commit -m "docs: update architecture for proxy daemon"
```

---

## Implementation Order Summary

| Task | Description | Dependencies |
|------|-------------|--------------|
| 1 | RunContext type | None |
| 2 | Run registry | Task 1 |
| 3 | API types | Task 1 |
| 4 | Daemon server | Tasks 2, 3 |
| 5 | Daemon client | Task 4 |
| 6 | Proxy ContextResolver | None (parallel with 1-5) |
| 7 | Wire proxy credential lookups | Task 6 |
| 8 | Daemon lifecycle (lock, self-exec) | Tasks 4, 5 |
| 9 | Hidden _daemon CLI command | Tasks 4, 6, 7, 8 |
| 10 | Container liveness checker | Task 2 |
| 11 | Manager.Create() integration | Tasks 5, 7, 8, 9 |
| 12 | Update moat proxy CLI | Tasks 5, 8 |
| 13 | Token refresh in daemon | Tasks 4, 11 |
| 14 | Routing proxy integration | Tasks 4, 5, 9 |
| 15 | Remove obsolete code | Tasks 11, 13, 14 |
| 16 | Update documentation | Task 15 |

Tasks 1-5 and 6-7 can be developed in parallel.
