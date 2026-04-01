# Gate Keeper Extraction Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract moat's TLS-intercepting credential-injecting proxy into a reusable `internal/gatekeeper/` package with a standalone `cmd/gatekeeper/` binary, while keeping moat's CLI surface unchanged.

**Architecture:** Four-phase migration. Phase 1 moves API types to `internal/gatekeeper/api/`. Phase 2 builds the Gate Keeper server wiring. Phase 3 splits `CredentialProvider` into composable interfaces. Phase 4 adds credential sources and the standalone binary. Moat's test suite passes at every phase boundary.

**Tech Stack:** Go 1.25, `internal/proxy` (existing TLS proxy), `github.com/majorcontext/keep` (policy engine), `gopkg.in/yaml.v3`, `github.com/aws/aws-sdk-go-v2` (for Secrets Manager source)

**Spec:** `docs/plans/2026-03-30-gatekeeper-extraction-design.md`

---

## Phase 1: Extract API Types and Registry

Move serialization types, registry, API server, and client from `internal/daemon/` to `internal/gatekeeper/api/`. The daemon becomes a thin wrapper importing from `gatekeeper/api`.

### Task 1.1: Create `internal/gatekeeper/api/` with API types

**Files:**
- Create: `internal/gatekeeper/api/types.go`
- Create: `internal/gatekeeper/api/types_test.go`
- Modify: `internal/daemon/api.go` (will be replaced with aliases in Task 1.5)

- [ ] **Step 1: Write JSON round-trip test**

Create `internal/gatekeeper/api/types_test.go` with a test that marshals a `RegisterRequest` with all fields populated, then unmarshals it, and asserts equality. This ensures the new types have identical JSON serialization to the old ones.

```go
// internal/gatekeeper/api/types_test.go
package api

import (
    "encoding/json"
    "testing"
    "time"
)

func TestRegisterRequestJSONRoundTrip(t *testing.T) {
    req := RegisterRequest{
        RunID:     "run-123",
        AuthToken: "tok-abc",
        Credentials: []CredentialSpec{
            {Host: "api.github.com", Header: "Authorization", Value: "Bearer xxx", Grant: "github"},
        },
        ExtraHeaders: []ExtraHeaderSpec{
            {Host: "api.github.com", HeaderName: "X-Custom", Value: "val"},
        },
        RemoveHeaders: []RemoveHeaderSpec{
            {Host: "api.github.com", HeaderName: "X-Remove"},
        },
        TokenSubstitutions: []TokenSubstitutionSpec{
            {Host: "api.telegram.org", Placeholder: "PLACEHOLDER", RealToken: "real"},
        },
        NetworkPolicy: "strict",
        NetworkAllow:  []string{"api.github.com"},
        Grants:        []string{"github"},
        ResponseTransformers: []TransformerSpec{
            {Host: "api.github.com", Kind: "oauth-endpoint-workaround"},
        },
        PolicyYAML:     map[string]string{"http": "rules: []"},
        PolicyRuleSets: []PolicyRuleSetSpec{
            {Scope: "http", Mode: "deny", Deny: []string{"rm -rf"}},
        },
    }

    data, err := json.Marshal(req)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }

    var got RegisterRequest
    if err := json.Unmarshal(data, &got); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }

    // Verify key fields survived round-trip
    if got.RunID != req.RunID {
        t.Errorf("RunID: got %q, want %q", got.RunID, req.RunID)
    }
    if len(got.Credentials) != 1 || got.Credentials[0].Grant != "github" {
        t.Errorf("Credentials: got %+v", got.Credentials)
    }
    if len(got.TokenSubstitutions) != 1 || got.TokenSubstitutions[0].Placeholder != "PLACEHOLDER" {
        t.Errorf("TokenSubstitutions: got %+v", got.TokenSubstitutions)
    }
    if got.NetworkPolicy != "strict" {
        t.Errorf("NetworkPolicy: got %q", got.NetworkPolicy)
    }
    if len(got.PolicyRuleSets) != 1 || got.PolicyRuleSets[0].Scope != "http" {
        t.Errorf("PolicyRuleSets: got %+v", got.PolicyRuleSets)
    }
}

func TestRegisterResponseJSON(t *testing.T) {
    resp := RegisterResponse{
        AuthToken: "tok-abc",
        ProxyPort: 19080,
    }
    data, err := json.Marshal(resp)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    var got RegisterResponse
    if err := json.Unmarshal(data, &got); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if got.AuthToken != resp.AuthToken || got.ProxyPort != resp.ProxyPort {
        t.Errorf("got %+v, want %+v", got, resp)
    }
}

func TestHealthResponseJSON(t *testing.T) {
    resp := HealthResponse{
        PID:          1234,
        ProxyPort:    19080,
        RunCount:     3,
        StartedAt:    time.Now().UTC().Truncate(time.Second),
        Commit:       "abc123",
        Capabilities: []string{"keep-policy"},
    }
    data, err := json.Marshal(resp)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    var got HealthResponse
    if err := json.Unmarshal(data, &got); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if got.PID != resp.PID || got.RunCount != resp.RunCount {
        t.Errorf("got %+v, want %+v", got, resp)
    }
    if len(got.Capabilities) != 1 || got.Capabilities[0] != "keep-policy" {
        t.Errorf("Capabilities: got %+v", got.Capabilities)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gatekeeper/api/ -run TestRegisterRequestJSONRoundTrip -v`
Expected: FAIL — package does not exist yet

- [ ] **Step 3: Create types.go by moving types from daemon/api.go**

Copy all struct types from `internal/daemon/api.go` (lines 27-123) to `internal/gatekeeper/api/types.go`. Preserve exact JSON tags. Include `CredentialSpec`, `ExtraHeaderSpec`, `TokenSubstitutionSpec`, `RemoveHeaderSpec`, `TransformerSpec`, `RegisterRequest`, `PolicyRuleSetSpec`, `RegisterResponse`, `UpdateRunRequest`, `HealthResponse`, `RunInfo`, `RouteRegistration`.

Do NOT copy `ToRunContext()` — that method stays in `internal/daemon/` as it has moat-specific dependencies.

Do NOT copy the package doc about backwards compatibility — copy it to a doc.go file instead.

```go
// internal/gatekeeper/api/types.go
package api

import "time"

// CredentialSpec defines a credential to inject for a specific host.
type CredentialSpec struct {
    Host   string `json:"host"`
    Header string `json:"header"`
    Value  string `json:"value"`
    Grant  string `json:"grant,omitempty"`
}

// ExtraHeaderSpec defines an additional header to inject.
type ExtraHeaderSpec struct {
    Host       string `json:"host"`
    HeaderName string `json:"header_name"`
    Value      string `json:"value"`
}

// TokenSubstitutionSpec defines a placeholder-to-real-token substitution.
type TokenSubstitutionSpec struct {
    Host        string `json:"host"`
    Placeholder string `json:"placeholder"`
    RealToken   string `json:"real_token"`
}

// RemoveHeaderSpec defines a header to strip from requests.
type RemoveHeaderSpec struct {
    Host       string `json:"host"`
    HeaderName string `json:"header_name"`
}

// TransformerSpec defines a response transformer to apply.
type TransformerSpec struct {
    Host string `json:"host"`
    Kind string `json:"kind"`
}

// RegisterRequest registers a new run with the proxy.
type RegisterRequest struct {
    RunID                string                `json:"run_id"`
    AuthToken            string                `json:"auth_token,omitempty"`
    Credentials          []CredentialSpec      `json:"credentials,omitempty"`
    ExtraHeaders         []ExtraHeaderSpec     `json:"extra_headers,omitempty"`
    RemoveHeaders        []RemoveHeaderSpec    `json:"remove_headers,omitempty"`
    TokenSubstitutions   []TokenSubstitutionSpec `json:"token_substitutions,omitempty"`
    MCPServers           []MCPServerSpec       `json:"mcp_servers,omitempty"`
    NetworkPolicy        string                `json:"network_policy,omitempty"`
    NetworkAllow         []string              `json:"network_allow,omitempty"`
    NetworkRules         []NetworkRuleSpec     `json:"network_rules,omitempty"`
    Grants               []string              `json:"grants,omitempty"`
    AWSConfig            *AWSConfigSpec        `json:"aws_config,omitempty"`
    ResponseTransformers []TransformerSpec     `json:"response_transformers,omitempty"`
    PolicyYAML           map[string]string     `json:"policy_yaml,omitempty"`
    PolicyRuleSets       []PolicyRuleSetSpec   `json:"policy_rule_sets,omitempty"`
}

// PolicyRuleSetSpec defines an inline Keep policy rule set.
type PolicyRuleSetSpec struct {
    Scope string   `json:"scope"`
    Mode  string   `json:"mode"`
    Deny  []string `json:"deny,omitempty"`
}

// RegisterResponse is the response after registering a run.
type RegisterResponse struct {
    AuthToken string `json:"auth_token"`
    ProxyPort int    `json:"proxy_port"`
    Error     string `json:"error,omitempty"`
}

// UpdateRunRequest updates an existing registered run.
type UpdateRunRequest struct {
    ContainerID string `json:"container_id"`
}

// HealthResponse reports daemon health status.
type HealthResponse struct {
    PID          int       `json:"pid"`
    ProxyPort    int       `json:"proxy_port"`
    RunCount     int       `json:"run_count"`
    StartedAt    time.Time `json:"started_at"`
    Commit       string    `json:"commit"`
    Capabilities []string  `json:"capabilities,omitempty"`
}

// RunInfo describes a registered run.
type RunInfo struct {
    RunID        string    `json:"run_id"`
    ContainerID  string    `json:"container_id,omitempty"`
    RegisteredAt time.Time `json:"registered_at"`
}

// RouteRegistration maps service names to addresses.
type RouteRegistration struct {
    Services map[string]string `json:"services"`
}
```

**Important:** The exact field names and JSON tags must match `internal/daemon/api.go` exactly. Read the actual file to verify before writing. Some types referenced above (`MCPServerSpec`, `NetworkRuleSpec`, `AWSConfigSpec`) may need to be defined or imported — check what `internal/daemon/api.go` actually references and copy those too.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/gatekeeper/api/ -v`
Expected: PASS

- [ ] **Step 5: Write cross-compatibility test**

Add a test that verifies JSON produced by the old daemon types can be unmarshaled by the new gatekeeper/api types. This is the critical regression test.

```go
// internal/gatekeeper/api/compat_test.go
package api_test

import (
    "encoding/json"
    "testing"

    gkapi "github.com/majorcontext/moat/internal/gatekeeper/api"
    "github.com/majorcontext/moat/internal/daemon"
)

func TestDaemonToGatekeeperCompat(t *testing.T) {
    // Marshal using daemon types
    old := daemon.RegisterRequest{
        RunID: "run-compat",
        Credentials: []daemon.CredentialSpec{
            {Host: "api.github.com", Header: "Authorization", Value: "Bearer tok", Grant: "github"},
        },
        NetworkPolicy: "strict",
        NetworkAllow:  []string{"api.github.com"},
    }
    data, err := json.Marshal(old)
    if err != nil {
        t.Fatalf("marshal old: %v", err)
    }

    // Unmarshal using new gatekeeper/api types
    var got gkapi.RegisterRequest
    if err := json.Unmarshal(data, &got); err != nil {
        t.Fatalf("unmarshal new: %v", err)
    }

    if got.RunID != "run-compat" {
        t.Errorf("RunID: got %q", got.RunID)
    }
    if len(got.Credentials) != 1 || got.Credentials[0].Host != "api.github.com" {
        t.Errorf("Credentials: got %+v", got.Credentials)
    }
}
```

- [ ] **Step 6: Run compat test**

Run: `go test ./internal/gatekeeper/api/ -run TestDaemonToGatekeeperCompat -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/gatekeeper/api/types.go internal/gatekeeper/api/types_test.go internal/gatekeeper/api/compat_test.go
git commit -m "feat(gatekeeper): add API types package with JSON compat tests"
```

### Task 1.2: Move Registry to `internal/gatekeeper/api/`

**Files:**
- Create: `internal/gatekeeper/api/registry.go`
- Create: `internal/gatekeeper/api/registry_test.go`
- Reference: `internal/daemon/registry.go` (103 lines)

- [ ] **Step 1: Write registry tests**

```go
// internal/gatekeeper/api/registry_test.go
package api

import "testing"

func TestRegistryRegisterAndLookup(t *testing.T) {
    r := NewRegistry()
    rc := &RunContext{RunID: "run-1"}
    token := r.Register(rc)

    if token == "" {
        t.Fatal("empty token")
    }

    got, ok := r.Lookup(token)
    if !ok {
        t.Fatal("lookup failed")
    }
    if got.RunID != "run-1" {
        t.Errorf("RunID: got %q", got.RunID)
    }
}

func TestRegistryRegisterWithToken(t *testing.T) {
    r := NewRegistry()
    rc := &RunContext{RunID: "run-1"}
    r.RegisterWithToken(rc, "fixed-token")

    got, ok := r.Lookup("fixed-token")
    if !ok {
        t.Fatal("lookup failed")
    }
    if got.RunID != "run-1" {
        t.Errorf("RunID: got %q", got.RunID)
    }
}

func TestRegistryRegisterWithTokenReplacesExisting(t *testing.T) {
    r := NewRegistry()
    rc1 := &RunContext{RunID: "run-1"}
    rc2 := &RunContext{RunID: "run-2"}
    r.RegisterWithToken(rc1, "tok")
    r.RegisterWithToken(rc2, "tok")

    got, ok := r.Lookup("tok")
    if !ok {
        t.Fatal("lookup failed")
    }
    if got.RunID != "run-2" {
        t.Errorf("RunID: got %q, want run-2", got.RunID)
    }
    if r.Count() != 1 {
        t.Errorf("Count: got %d, want 1", r.Count())
    }
}

func TestRegistryUnregister(t *testing.T) {
    r := NewRegistry()
    rc := &RunContext{RunID: "run-1"}
    token := r.Register(rc)
    r.Unregister(token)

    _, ok := r.Lookup(token)
    if ok {
        t.Error("lookup should fail after unregister")
    }
    if r.Count() != 0 {
        t.Errorf("Count: got %d, want 0", r.Count())
    }
}

func TestRegistryList(t *testing.T) {
    r := NewRegistry()
    r.Register(&RunContext{RunID: "run-1"})
    r.Register(&RunContext{RunID: "run-2"})

    list := r.List()
    if len(list) != 2 {
        t.Errorf("List: got %d items, want 2", len(list))
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gatekeeper/api/ -run TestRegistry -v`
Expected: FAIL — `NewRegistry`, `RunContext` not defined yet

- [ ] **Step 3: Create RunContext and Registry**

Create `internal/gatekeeper/api/runcontext.go` — a minimal RunContext that holds credential state and implements `credential.ProxyConfigurer`. This is the Gate Keeper's equivalent of `daemon.RunContext`, but without moat-specific lifecycle (no container monitoring, no daemon refresh cancel, no AWS handler).

Read `internal/daemon/runcontext.go` carefully. The new RunContext needs:
- All the credential storage fields (Credentials, ExtraHeaders, RemoveHeaders, TokenSubstitutions, ResponseTransformers maps)
- All the ProxyConfigurer method implementations (SetCredential through SetTokenSubstitution)
- All the getter methods (GetCredential through GetResponseTransformers) with the host:port fallback pattern
- Network policy fields (NetworkPolicy, NetworkAllow, NetworkRules)
- MCPServers, KeepEngines, Grants, PolicyYAML, PolicyRuleSets
- The `ToProxyContextData()` conversion method
- Thread-safety (sync.RWMutex)

It should NOT include: `refreshCancel`, `awsHandler`, `SetRefreshCancel`, `SetAWSHandler`, `GetContainerID`, `Close` (with the 2s engine close delay), container-specific methods. Those stay in `daemon.RunContext`.

Create `internal/gatekeeper/api/registry.go` — moved from `internal/daemon/registry.go` with `RunContext` type updated.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gatekeeper/api/ -run TestRegistry -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gatekeeper/api/runcontext.go internal/gatekeeper/api/registry.go internal/gatekeeper/api/registry_test.go
git commit -m "feat(gatekeeper): add RunContext and Registry"
```

### Task 1.3: Move API Server to `internal/gatekeeper/api/`

**Files:**
- Create: `internal/gatekeeper/api/server.go`
- Create: `internal/gatekeeper/api/server_test.go`
- Reference: `internal/daemon/server.go` (394 lines)

- [ ] **Step 1: Write server tests**

Test the core API routes: health check, register run, list runs, unregister run. The server should be testable without a real Unix socket (use `httptest.NewServer`).

```go
// internal/gatekeeper/api/server_test.go
package api

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestServerHealth(t *testing.T) {
    srv := NewServer(ServerConfig{})
    ts := httptest.NewServer(srv.Handler())
    defer ts.Close()

    resp, err := http.Get(ts.URL + "/v1/health")
    if err != nil {
        t.Fatal(err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        t.Fatalf("status: %d", resp.StatusCode)
    }

    var health HealthResponse
    if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
        t.Fatal(err)
    }
    if health.PID == 0 {
        t.Error("PID should be set")
    }
}

func TestServerRegisterAndList(t *testing.T) {
    srv := NewServer(ServerConfig{})
    ts := httptest.NewServer(srv.Handler())
    defer ts.Close()

    // Register a run
    reqBody := RegisterRequest{
        RunID: "run-1",
        Credentials: []CredentialSpec{
            {Host: "api.github.com", Header: "Authorization", Value: "Bearer tok", Grant: "github"},
        },
        NetworkPolicy: "strict",
    }
    body, _ := json.Marshal(reqBody)
    resp, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewReader(body))
    if err != nil {
        t.Fatal(err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusCreated {
        t.Fatalf("register status: %d", resp.StatusCode)
    }

    var regResp RegisterResponse
    json.NewDecoder(resp.Body).Decode(&regResp)
    if regResp.AuthToken == "" {
        t.Error("empty auth token")
    }

    // List runs
    resp2, err := http.Get(ts.URL + "/v1/runs")
    if err != nil {
        t.Fatal(err)
    }
    defer resp2.Body.Close()

    var runs []RunInfo
    json.NewDecoder(resp2.Body).Decode(&runs)
    if len(runs) != 1 || runs[0].RunID != "run-1" {
        t.Errorf("runs: %+v", runs)
    }
}

func TestServerUnregister(t *testing.T) {
    srv := NewServer(ServerConfig{})
    ts := httptest.NewServer(srv.Handler())
    defer ts.Close()

    // Register
    reqBody := RegisterRequest{RunID: "run-1"}
    body, _ := json.Marshal(reqBody)
    resp, _ := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewReader(body))
    var regResp RegisterResponse
    json.NewDecoder(resp.Body).Decode(&regResp)
    resp.Body.Close()

    // Unregister
    req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/runs/"+regResp.AuthToken, nil)
    resp2, err := http.DefaultClient.Do(req)
    if err != nil {
        t.Fatal(err)
    }
    resp2.Body.Close()
    if resp2.StatusCode != http.StatusNoContent {
        t.Fatalf("unregister status: %d", resp2.StatusCode)
    }

    // Verify count is 0
    resp3, _ := http.Get(ts.URL + "/v1/runs")
    var runs []RunInfo
    json.NewDecoder(resp3.Body).Decode(&runs)
    resp3.Body.Close()
    if len(runs) != 0 {
        t.Errorf("runs after unregister: %+v", runs)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gatekeeper/api/ -run TestServer -v`
Expected: FAIL — `NewServer`, `ServerConfig`, `Handler()` not defined

- [ ] **Step 3: Implement the API server**

Create `internal/gatekeeper/api/server.go`. This is the Gate Keeper API server — it handles run registration, health, and lifecycle. Extract the core logic from `internal/daemon/server.go` but without moat-specific concerns (Keep policy compilation, AWS handler setup, token refresh — those are handled by callbacks or by the caller).

The server should:
- Accept a `ServerConfig` with proxy port and optional callbacks
- Expose a `Handler() http.Handler` for testability
- Support starting on Unix socket or TCP listener
- Have `OnRegister`, `OnUnregister`, `OnEmpty` callback hooks
- Route: GET /v1/health, POST /v1/runs, GET /v1/runs, PATCH /v1/runs/{token}, DELETE /v1/runs/{token}, POST /v1/shutdown

The `handleRegisterRun` handler should:
1. Decode RegisterRequest
2. Create RunContext and populate it from the request (call the Set*/Add* methods)
3. Register in registry
4. Call OnRegister callback (which is where moat plugs in Keep compilation, refresh, etc.)
5. Return RegisterResponse

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gatekeeper/api/ -run TestServer -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gatekeeper/api/server.go internal/gatekeeper/api/server_test.go
git commit -m "feat(gatekeeper): add API server with registration and health endpoints"
```

### Task 1.4: Move API Client to `internal/gatekeeper/api/`

**Files:**
- Create: `internal/gatekeeper/api/client.go`
- Create: `internal/gatekeeper/api/client_test.go`
- Reference: `internal/daemon/client.go` (191 lines)

- [ ] **Step 1: Write client tests using httptest server**

```go
// internal/gatekeeper/api/client_test.go
package api

import (
    "context"
    "net/http/httptest"
    "testing"
)

func TestClientIntegration(t *testing.T) {
    srv := NewServer(ServerConfig{ProxyPort: 19080})
    ts := httptest.NewServer(srv.Handler())
    defer ts.Close()

    client := NewHTTPClient(ts.URL)
    ctx := context.Background()

    // Health
    health, err := client.Health(ctx)
    if err != nil {
        t.Fatalf("health: %v", err)
    }
    if health.ProxyPort != 19080 {
        t.Errorf("proxy port: %d", health.ProxyPort)
    }

    // Register
    regResp, err := client.RegisterRun(ctx, RegisterRequest{
        RunID: "run-1",
        Credentials: []CredentialSpec{
            {Host: "api.github.com", Header: "Authorization", Value: "Bearer tok"},
        },
    })
    if err != nil {
        t.Fatalf("register: %v", err)
    }
    if regResp.AuthToken == "" {
        t.Error("empty token")
    }

    // List
    runs, err := client.ListRuns(ctx)
    if err != nil {
        t.Fatalf("list: %v", err)
    }
    if len(runs) != 1 {
        t.Errorf("runs: %d", len(runs))
    }

    // Unregister
    if err := client.UnregisterRun(ctx, regResp.AuthToken); err != nil {
        t.Fatalf("unregister: %v", err)
    }

    // Verify empty
    runs, _ = client.ListRuns(ctx)
    if len(runs) != 0 {
        t.Errorf("runs after unregister: %d", len(runs))
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gatekeeper/api/ -run TestClientIntegration -v`
Expected: FAIL — `NewHTTPClient` not defined

- [ ] **Step 3: Implement the client**

Create `internal/gatekeeper/api/client.go`. Extract from `internal/daemon/client.go`. The client should support both Unix socket and TCP (HTTP) connections. Key methods: `Health`, `RegisterRun`, `UpdateRun`, `UnregisterRun`, `ListRuns`, `Shutdown`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gatekeeper/api/ -run TestClientIntegration -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gatekeeper/api/client.go internal/gatekeeper/api/client_test.go
git commit -m "feat(gatekeeper): add API client with Unix socket and TCP support"
```

### Task 1.5: Rewire `internal/daemon/` to import from `gatekeeper/api`

**Files:**
- Modify: `internal/daemon/api.go` — replace type definitions with aliases
- Modify: `internal/daemon/registry.go` — replace with wrapper/alias
- Modify: `internal/daemon/server.go` — import types from gatekeeper/api
- Modify: `internal/daemon/client.go` — import types from gatekeeper/api
- Modify: `internal/daemon/runcontext.go` — embed gatekeeper/api.RunContext or delegate
- Modify: `internal/daemon/refresh.go` — update imports

- [ ] **Step 1: Run existing daemon tests as baseline**

Run: `go test ./internal/daemon/ -v -count=1`
Record which tests pass. All must still pass after this task.

- [ ] **Step 2: Replace daemon API types with aliases**

In `internal/daemon/api.go`, replace all type definitions with type aliases pointing to `gatekeeper/api`:

```go
package daemon

import api "github.com/majorcontext/moat/internal/gatekeeper/api"

// Type aliases for backwards compatibility.
type CredentialSpec = api.CredentialSpec
type ExtraHeaderSpec = api.ExtraHeaderSpec
type TokenSubstitutionSpec = api.TokenSubstitutionSpec
type RemoveHeaderSpec = api.RemoveHeaderSpec
type TransformerSpec = api.TransformerSpec
type RegisterRequest = api.RegisterRequest
type PolicyRuleSetSpec = api.PolicyRuleSetSpec
type RegisterResponse = api.RegisterResponse
type UpdateRunRequest = api.UpdateRunRequest
type HealthResponse = api.HealthResponse
type RunInfo = api.RunInfo
type RouteRegistration = api.RouteRegistration
```

Keep `ToRunContext()` as a free function in `internal/daemon/` that accepts `api.RegisterRequest` and returns `*daemon.RunContext`.

- [ ] **Step 3: Update daemon.RunContext to embed gatekeeper RunContext**

`internal/daemon/runcontext.go` should embed `api.RunContext` (for credential storage and ProxyConfigurer impl) and add daemon-specific fields: `refreshCancel`, `awsHandler`, `Close()`.

This is the trickiest part — read the actual daemon RunContext and gatekeeper RunContext carefully. The daemon RunContext adds:
- `refreshCancel context.CancelFunc`
- `awsHandler http.Handler`
- `SetRefreshCancel()`, `CancelRefresh()`, `Close()` methods
- `SetAWSHandler()`, container-specific getters

All ProxyConfigurer methods delegate to the embedded `api.RunContext`.

- [ ] **Step 4: Update daemon server/client imports**

`internal/daemon/server.go` and `internal/daemon/client.go` should import types from `gatekeeper/api` but keep their moat-specific logic (Keep compilation, token refresh, AWS handler setup, daemon lifecycle).

- [ ] **Step 5: Run daemon tests**

Run: `go test ./internal/daemon/ -v -count=1`
Expected: All previously-passing tests still pass

- [ ] **Step 6: Run full test suite**

Run: `make test-unit`
Expected: No regressions

- [ ] **Step 7: Commit**

```bash
git add internal/daemon/
git commit -m "refactor(daemon): import API types from gatekeeper/api package"
```

### Task 1.6: Phase 1 verification

- [ ] **Step 1: Run full test suite and lint**

Run: `make test-unit && make lint`
Expected: PASS

- [ ] **Step 2: Verify package imports are clean**

Run: `go list -deps ./internal/gatekeeper/api/ | grep -c daemon`
Expected: 0 (gatekeeper/api must not import daemon)

Run: `go list -deps ./internal/daemon/ | grep gatekeeper`
Expected: Shows `internal/gatekeeper/api` (daemon imports gatekeeper/api)

- [ ] **Step 3: Commit phase marker**

```bash
git commit --allow-empty -m "milestone: Phase 1 complete — API types extracted to gatekeeper/api"
```

---

## Phase 2: Gate Keeper Server

Build the core `internal/gatekeeper/` server that wires proxy + API + credential management.

### Task 2.1: Create Gate Keeper server

**Files:**
- Create: `internal/gatekeeper/gatekeeper.go`
- Create: `internal/gatekeeper/gatekeeper_test.go`

- [ ] **Step 1: Write server lifecycle test**

```go
// internal/gatekeeper/gatekeeper_test.go
package gatekeeper

import (
    "context"
    "testing"
    "time"
)

func TestServerStartStop(t *testing.T) {
    cfg := &Config{
        Proxy: ProxyConfig{
            Port: 0, // ephemeral port
            Host: "127.0.0.1",
        },
    }

    srv, err := New(cfg)
    if err != nil {
        t.Fatalf("New: %v", err)
    }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    errCh := make(chan error, 1)
    go func() { errCh <- srv.Start(ctx) }()

    // Wait for server to be ready
    time.Sleep(100 * time.Millisecond)

    if err := srv.Stop(ctx); err != nil {
        t.Fatalf("Stop: %v", err)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gatekeeper/ -run TestServerStartStop -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Implement the Gate Keeper server**

Create `internal/gatekeeper/gatekeeper.go`:

```go
package gatekeeper

import (
    "context"

    "github.com/majorcontext/moat/internal/gatekeeper/api"
    "github.com/majorcontext/moat/internal/proxy"
)

// Server is the Gate Keeper core — wires proxy, API server, and credential management.
type Server struct {
    proxy    *proxy.Proxy
    api      *api.Server
    registry *api.Registry
    cfg      *Config
}

func New(cfg *Config) (*Server, error) {
    // Create registry, proxy, API server
    // Wire context resolver from registry to proxy
}

func (s *Server) Start(ctx context.Context) error {
    // Start proxy listener
    // Start API server (if configured)
    // Block until context cancelled
}

func (s *Server) Stop(ctx context.Context) error {
    // Graceful shutdown of API + proxy
}

// Registry returns the server's run registry for external use.
func (s *Server) Registry() *api.Registry {
    return s.registry
}
```

The key wiring: proxy's `ContextResolver` is set to a function that calls `registry.Lookup(token)` and converts `api.RunContext` to `proxy.RunContextData` via `ToProxyContextData()`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gatekeeper/ -run TestServerStartStop -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gatekeeper/gatekeeper.go internal/gatekeeper/gatekeeper_test.go
git commit -m "feat(gatekeeper): add core Server with proxy and API wiring"
```

### Task 2.2: Create Gate Keeper config

**Files:**
- Create: `internal/gatekeeper/config.go`
- Create: `internal/gatekeeper/config_test.go`

- [ ] **Step 1: Write config parsing test**

Test parsing a complete `gatekeeper.yaml`:

```go
func TestParseConfig(t *testing.T) {
    yaml := `
proxy:
  port: 8080
  host: 0.0.0.0
tls:
  ca_cert: /etc/gatekeeper/ca.pem
  ca_key: /etc/gatekeeper/ca-key.pem
credentials:
  - grant: github
    source:
      type: env
      var: GITHUB_TOKEN
  - grant: anthropic
    source:
      type: static
      value: sk-test-key
network:
  policy: strict
  allow:
    - api.github.com
api:
  enabled: true
  socket: /var/run/gatekeeper.sock
log:
  level: info
  format: json
`
    cfg, err := ParseConfig([]byte(yaml))
    if err != nil {
        t.Fatal(err)
    }
    if cfg.Proxy.Port != 8080 {
        t.Errorf("port: %d", cfg.Proxy.Port)
    }
    if len(cfg.Credentials) != 2 {
        t.Errorf("credentials: %d", len(cfg.Credentials))
    }
    if cfg.Credentials[0].Source.Type != "env" {
        t.Errorf("source type: %s", cfg.Credentials[0].Source.Type)
    }
    if cfg.Network.Policy != "strict" {
        t.Errorf("network policy: %s", cfg.Network.Policy)
    }
}
```

- [ ] **Step 2: Run test, verify fails**

Run: `go test ./internal/gatekeeper/ -run TestParseConfig -v`
Expected: FAIL

- [ ] **Step 3: Implement config types and parser**

```go
// internal/gatekeeper/config.go
package gatekeeper

import "gopkg.in/yaml.v3"

type Config struct {
    Proxy       ProxyConfig            `yaml:"proxy"`
    TLS         TLSConfig              `yaml:"tls"`
    Credentials []CredentialConfig     `yaml:"credentials"`
    Network     NetworkConfig          `yaml:"network"`
    Policy      map[string]string      `yaml:"policy"`
    API         APIConfig              `yaml:"api"`
    Log         LogConfig              `yaml:"log"`
}

type ProxyConfig struct {
    Port int    `yaml:"port"`
    Host string `yaml:"host"`
}

type TLSConfig struct {
    CACert string `yaml:"ca_cert"`
    CAKey  string `yaml:"ca_key"`
}

type CredentialConfig struct {
    Grant  string       `yaml:"grant"`
    Source SourceConfig `yaml:"source"`
}

type SourceConfig struct {
    Type   string `yaml:"type"`
    Var    string `yaml:"var,omitempty"`
    Value  string `yaml:"value,omitempty"`
    Secret string `yaml:"secret,omitempty"`
    Region string `yaml:"region,omitempty"`
}

type NetworkConfig struct {
    Policy string   `yaml:"policy"`
    Allow  []string `yaml:"allow,omitempty"`
    Rules  []NetworkRule `yaml:"rules,omitempty"`
}

type NetworkRule struct {
    Host    string   `yaml:"host"`
    Methods []string `yaml:"methods,omitempty"`
}

type APIConfig struct {
    Enabled bool   `yaml:"enabled"`
    Socket  string `yaml:"socket,omitempty"`
    Listen  string `yaml:"listen,omitempty"`
}

type LogConfig struct {
    Level    string `yaml:"level"`
    Format   string `yaml:"format"`
    Output   string `yaml:"output"`
    Requests string `yaml:"requests"`
}

func ParseConfig(data []byte) (*Config, error) {
    var cfg Config
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}

func LoadConfig(path string) (*Config, error) {
    // Read file, call ParseConfig
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gatekeeper/ -run TestParseConfig -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gatekeeper/config.go internal/gatekeeper/config_test.go
git commit -m "feat(gatekeeper): add YAML config parsing"
```

### Task 2.3: Integration test — credential injection through Gate Keeper

**Files:**
- Create: `internal/gatekeeper/integration_test.go`

- [ ] **Step 1: Write integration test**

Test the full flow: create Gate Keeper server with a static credential, make an HTTPS request through the proxy, verify the credential header was injected.

```go
func TestCredentialInjection(t *testing.T) {
    // 1. Start a test HTTPS server that echoes back headers
    // 2. Create Gate Keeper config with a static credential for the test server's host
    // 3. Start Gate Keeper server on ephemeral port
    // 4. Make request through proxy to test server
    // 5. Verify Authorization header was injected
}
```

- [ ] **Step 2: Run test, verify fails**

Run: `go test ./internal/gatekeeper/ -run TestCredentialInjection -v`
Expected: FAIL

- [ ] **Step 3: Wire credential loading into Gate Keeper server**

Update `gatekeeper.go` `New()` to:
1. For each credential in config, resolve the source and fetch initial value
2. Look up the provider by grant name
3. Call `provider.ConfigureProxy(runCtx, cred)`
4. Set up the proxy with the run context

This is where the Gate Keeper server connects config → credential sources → providers → proxy.

- [ ] **Step 4: Run integration test**

Run: `go test ./internal/gatekeeper/ -run TestCredentialInjection -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gatekeeper/integration_test.go internal/gatekeeper/gatekeeper.go
git commit -m "feat(gatekeeper): wire credential loading with integration test"
```

### Task 2.4: Wire moat daemon to use Gate Keeper server

**Files:**
- Modify: `internal/daemon/server.go` — use `gatekeeper.Server` internally or import `gatekeeper/api.Server`
- Modify: `internal/daemon/daemon.go` or lifecycle files as needed

- [ ] **Step 1: Run daemon tests as baseline**

Run: `go test ./internal/daemon/ -v -count=1`

- [ ] **Step 2: Update daemon to use gatekeeper API server**

The daemon's HTTP server should delegate to `gatekeeper/api.Server` for the core routes, adding moat-specific handlers (Keep compilation, token refresh, AWS handler) via the callback hooks.

Read `internal/daemon/server.go` handleRegisterRun (lines 133-260) carefully. The moat-specific parts are:
- Keep policy compilation (lines 143-198)
- Token refresh startup (lines 217-221)
- AWS credential provider creation (lines 223-241)

These become `OnRegister` callback logic.

- [ ] **Step 3: Run daemon tests**

Run: `go test ./internal/daemon/ -v -count=1`
Expected: All previously-passing tests still pass

- [ ] **Step 4: Run full test suite**

Run: `make test-unit`
Expected: No regressions

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/
git commit -m "refactor(daemon): use gatekeeper/api server with moat-specific callbacks"
```

### Task 2.5: Phase 2 verification

- [ ] **Step 1: Run full test suite and lint**

Run: `make test-unit && make lint`
Expected: PASS

- [ ] **Step 2: Verify no circular imports**

Run: `go list -deps ./internal/gatekeeper/ | grep daemon`
Expected: 0 (gatekeeper must not import daemon)

- [ ] **Step 3: Commit phase marker**

```bash
git commit --allow-empty -m "milestone: Phase 2 complete — Gate Keeper server with proxy wiring"
```

---

## Phase 3: Split Provider Interfaces

Refactor `provider.CredentialProvider` into composable interfaces. Provider implementations don't change — they already have all methods as concrete struct methods.

### Task 3.1: Define composable interfaces

**Files:**
- Modify: `internal/provider/interfaces.go`
- Create: `internal/provider/interfaces_test.go`

- [ ] **Step 1: Write compile-time interface satisfaction tests**

```go
// internal/provider/interfaces_test.go
package provider_test

import (
    "testing"

    "github.com/majorcontext/moat/internal/provider"
    _ "github.com/majorcontext/moat/internal/providers" // register all
)

// TestProvidersSatisfyCredentialProvider verifies all registered providers
// still implement the full CredentialProvider interface after the split.
func TestProvidersSatisfyCredentialProvider(t *testing.T) {
    for _, p := range provider.All() {
        // CredentialProvider is now a composite — every registered provider must satisfy it
        var _ provider.CredentialProvider = p

        // ProxyProvider is always satisfied (embedded in CredentialProvider)
        var _ provider.ProxyProvider = p

        t.Logf("%s: satisfies CredentialProvider and ProxyProvider", p.Name())
    }
}
```

- [ ] **Step 2: Run test as baseline (should pass with current interfaces)**

Run: `go test ./internal/provider/ -run TestProvidersSatisfy -v`
Expected: PASS (current `CredentialProvider` is monolithic — all providers implement it)

- [ ] **Step 3: Split interfaces in interfaces.go**

Add the new composable interfaces (`ProxyProvider`, `GrantProvider`, `ContainerProvider`, `ImpliedDepsProvider`) and redefine `CredentialProvider` as a composite. The existing `CredentialProvider` methods don't change — they're just reorganized.

Read `internal/provider/interfaces.go` (116 lines) carefully. Add:

```go
// ProxyProvider configures proxy credential injection.
// Gate Keeper's core interface.
type ProxyProvider interface {
    Name() string
    ConfigureProxy(p ProxyConfigurer, cred *Credential)
}

// GrantProvider acquires credentials interactively. Moat CLI only.
type GrantProvider interface {
    Grant(ctx context.Context) (*Credential, error)
}

// ContainerProvider sets up container environment. Moat only.
type ContainerProvider interface {
    ContainerEnv(cred *Credential) []string
    ContainerMounts(cred *Credential, containerHome string) ([]MountConfig, string, error)
    Cleanup(cleanupPath string)
}

// ImpliedDepsProvider declares dependencies between providers. Moat CLI only.
type ImpliedDepsProvider interface {
    ImpliedDependencies() []string
}

// CredentialProvider is the composite interface — backwards compatible.
type CredentialProvider interface {
    ProxyProvider
    GrantProvider
    ContainerProvider
    ImpliedDepsProvider
}
```

`AgentProvider` and `EndpointProvider` continue to embed `CredentialProvider` — no change needed.

- [ ] **Step 4: Run interface satisfaction tests**

Run: `go test ./internal/provider/ -run TestProvidersSatisfy -v`
Expected: PASS — composite interface has same method set

- [ ] **Step 5: Run full test suite**

Run: `make test-unit`
Expected: No regressions (the composite is structurally identical)

- [ ] **Step 6: Commit**

```bash
git add internal/provider/interfaces.go internal/provider/interfaces_test.go
git commit -m "refactor(provider): split CredentialProvider into composable interfaces"
```

### Task 3.2: Update Gate Keeper to use ProxyProvider assertions

**Files:**
- Modify: `internal/gatekeeper/gatekeeper.go`

- [ ] **Step 1: Update Gate Keeper credential loading to use ProxyProvider**

Where Gate Keeper calls `provider.Get(name)` and uses `ConfigureProxy`, narrow the assertion:

```go
p := provider.Get(grantName)
if pp, ok := p.(provider.ProxyProvider); ok {
    pp.ConfigureProxy(runCtx, cred)
}
```

This makes Gate Keeper's dependency on providers explicit: it only needs `ProxyProvider`.

- [ ] **Step 2: Run Gate Keeper tests**

Run: `go test ./internal/gatekeeper/... -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/gatekeeper/gatekeeper.go
git commit -m "refactor(gatekeeper): use ProxyProvider interface assertion"
```

### Task 3.3: Phase 3 verification

- [ ] **Step 1: Run full test suite and lint**

Run: `make test-unit && make lint`
Expected: PASS

- [ ] **Step 2: Commit phase marker**

```bash
git commit --allow-empty -m "milestone: Phase 3 complete — provider interfaces split"
```

---

## Phase 4: Credential Sources and Standalone Binary

### Task 4.1: Create CredentialSource interface and env source

**Files:**
- Create: `internal/gatekeeper/credentialsource/source.go`
- Create: `internal/gatekeeper/credentialsource/env.go`
- Create: `internal/gatekeeper/credentialsource/env_test.go`

- [ ] **Step 1: Write env source test**

```go
// internal/gatekeeper/credentialsource/env_test.go
package credentialsource

import (
    "context"
    "testing"
)

func TestEnvSource(t *testing.T) {
    t.Setenv("TEST_TOKEN", "secret-value")

    src := NewEnvSource("TEST_TOKEN")
    val, err := src.Fetch(context.Background())
    if err != nil {
        t.Fatal(err)
    }
    if val != "secret-value" {
        t.Errorf("got %q", val)
    }
    if src.Type() != "env" {
        t.Errorf("type: %s", src.Type())
    }
}

func TestEnvSourceMissing(t *testing.T) {
    src := NewEnvSource("NONEXISTENT_VAR_XYZ")
    _, err := src.Fetch(context.Background())
    if err == nil {
        t.Error("expected error for missing env var")
    }
}
```

- [ ] **Step 2: Run test, verify fails**

Run: `go test ./internal/gatekeeper/credentialsource/ -run TestEnvSource -v`
Expected: FAIL

- [ ] **Step 3: Implement interface and env source**

```go
// internal/gatekeeper/credentialsource/source.go
package credentialsource

import (
    "context"
    "time"
)

// CredentialSource fetches a credential value from an external system.
type CredentialSource interface {
    Fetch(ctx context.Context) (string, error)
    Type() string
}

// RefreshableSource supports polling for rotated secrets.
type RefreshableSource interface {
    CredentialSource
    RefreshInterval() time.Duration
}
```

```go
// internal/gatekeeper/credentialsource/env.go
package credentialsource

import (
    "context"
    "fmt"
    "os"
)

type envSource struct {
    varName string
}

func NewEnvSource(varName string) CredentialSource {
    return &envSource{varName: varName}
}

func (s *envSource) Fetch(_ context.Context) (string, error) {
    val := os.Getenv(s.varName)
    if val == "" {
        return "", fmt.Errorf("environment variable %s is not set", s.varName)
    }
    return val, nil
}

func (s *envSource) Type() string { return "env" }
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gatekeeper/credentialsource/ -run TestEnvSource -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gatekeeper/credentialsource/
git commit -m "feat(gatekeeper): add CredentialSource interface and env source"
```

### Task 4.2: Add static credential source

**Files:**
- Create: `internal/gatekeeper/credentialsource/static.go`
- Create: `internal/gatekeeper/credentialsource/static_test.go`

- [ ] **Step 1: Write test**

```go
func TestStaticSource(t *testing.T) {
    src := NewStaticSource("my-api-key")
    val, err := src.Fetch(context.Background())
    if err != nil {
        t.Fatal(err)
    }
    if val != "my-api-key" {
        t.Errorf("got %q", val)
    }
    if src.Type() != "static" {
        t.Errorf("type: %s", src.Type())
    }
}
```

- [ ] **Step 2: Run test, verify fails**

Run: `go test ./internal/gatekeeper/credentialsource/ -run TestStaticSource -v`

- [ ] **Step 3: Implement**

```go
// internal/gatekeeper/credentialsource/static.go
package credentialsource

import "context"

type staticSource struct {
    value string
}

func NewStaticSource(value string) CredentialSource {
    return &staticSource{value: value}
}

func (s *staticSource) Fetch(_ context.Context) (string, error) {
    return s.value, nil
}

func (s *staticSource) Type() string { return "static" }
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gatekeeper/credentialsource/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gatekeeper/credentialsource/static.go internal/gatekeeper/credentialsource/static_test.go
git commit -m "feat(gatekeeper): add static credential source"
```

### Task 4.3: Add AWS Secrets Manager source

**Files:**
- Create: `internal/gatekeeper/credentialsource/awssecretsmanager.go`
- Create: `internal/gatekeeper/credentialsource/awssecretsmanager_test.go`

- [ ] **Step 1: Write test with mock AWS client**

```go
// internal/gatekeeper/credentialsource/awssecretsmanager_test.go
package credentialsource

import (
    "context"
    "testing"
    "time"
)

type mockSMClient struct {
    secret string
    err    error
}

func (m *mockSMClient) GetSecretValue(ctx context.Context, secretID string) (string, error) {
    return m.secret, m.err
}

func TestAWSSecretsManagerSource(t *testing.T) {
    mock := &mockSMClient{secret: "my-secret-value"}
    src := newAWSSecretsManagerSourceWithClient("prod/api-key", mock, 5*time.Minute)

    val, err := src.Fetch(context.Background())
    if err != nil {
        t.Fatal(err)
    }
    if val != "my-secret-value" {
        t.Errorf("got %q", val)
    }
    if src.Type() != "aws-secretsmanager" {
        t.Errorf("type: %s", src.Type())
    }
}

func TestAWSSecretsManagerSourceIsRefreshable(t *testing.T) {
    mock := &mockSMClient{secret: "val"}
    src := newAWSSecretsManagerSourceWithClient("secret", mock, 5*time.Minute)

    rs, ok := src.(RefreshableSource)
    if !ok {
        t.Fatal("expected RefreshableSource")
    }
    if rs.RefreshInterval() != 5*time.Minute {
        t.Errorf("interval: %v", rs.RefreshInterval())
    }
}
```

- [ ] **Step 2: Run test, verify fails**

Run: `go test ./internal/gatekeeper/credentialsource/ -run TestAWSSecretsManager -v`

- [ ] **Step 3: Implement with interface for AWS client**

Define a `SecretsManagerClient` interface so the real AWS SDK can be swapped for testing:

```go
// internal/gatekeeper/credentialsource/awssecretsmanager.go
package credentialsource

import (
    "context"
    "time"
)

// SecretsManagerClient abstracts the AWS Secrets Manager API.
type SecretsManagerClient interface {
    GetSecretValue(ctx context.Context, secretID string) (string, error)
}

type awsSMSource struct {
    secretID string
    client   SecretsManagerClient
    interval time.Duration
}

func NewAWSSecretsManagerSource(secretID, region string) (CredentialSource, error) {
    // Create real AWS SDK client
    // Return newAWSSecretsManagerSourceWithClient(secretID, client, 5*time.Minute)
}

func newAWSSecretsManagerSourceWithClient(secretID string, client SecretsManagerClient, interval time.Duration) CredentialSource {
    return &awsSMSource{secretID: secretID, client: client, interval: interval}
}

func (s *awsSMSource) Fetch(ctx context.Context) (string, error) {
    return s.client.GetSecretValue(ctx, s.secretID)
}

func (s *awsSMSource) Type() string            { return "aws-secretsmanager" }
func (s *awsSMSource) RefreshInterval() time.Duration { return s.interval }
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gatekeeper/credentialsource/ -run TestAWSSecretsManager -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gatekeeper/credentialsource/awssecretsmanager.go internal/gatekeeper/credentialsource/awssecretsmanager_test.go
git commit -m "feat(gatekeeper): add AWS Secrets Manager credential source"
```

### Task 4.4: Add credential source resolver

**Files:**
- Create: `internal/gatekeeper/config_credential.go`
- Create: `internal/gatekeeper/config_credential_test.go`

- [ ] **Step 1: Write resolver test**

```go
func TestResolveCredentialSource(t *testing.T) {
    tests := []struct {
        name   string
        cfg    SourceConfig
        setup  func()
        want   string
    }{
        {
            name:  "env source",
            cfg:   SourceConfig{Type: "env", Var: "TEST_CRED"},
            setup: func() { t.Setenv("TEST_CRED", "from-env") },
            want:  "from-env",
        },
        {
            name: "static source",
            cfg:  SourceConfig{Type: "static", Value: "inline-val"},
            want: "inline-val",
        },
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if tt.setup != nil {
                tt.setup()
            }
            src, err := ResolveSource(tt.cfg)
            if err != nil {
                t.Fatal(err)
            }
            val, err := src.Fetch(context.Background())
            if err != nil {
                t.Fatal(err)
            }
            if val != tt.want {
                t.Errorf("got %q, want %q", val, tt.want)
            }
        })
    }
}
```

- [ ] **Step 2: Run test, verify fails**

- [ ] **Step 3: Implement resolver**

```go
// internal/gatekeeper/config_credential.go
package gatekeeper

import (
    "fmt"

    "github.com/majorcontext/moat/internal/gatekeeper/credentialsource"
)

func ResolveSource(cfg SourceConfig) (credentialsource.CredentialSource, error) {
    switch cfg.Type {
    case "env":
        return credentialsource.NewEnvSource(cfg.Var), nil
    case "static":
        return credentialsource.NewStaticSource(cfg.Value), nil
    case "aws-secretsmanager":
        return credentialsource.NewAWSSecretsManagerSource(cfg.Secret, cfg.Region)
    default:
        return nil, fmt.Errorf("unknown credential source type: %q", cfg.Type)
    }
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gatekeeper/ -run TestResolveCredentialSource -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gatekeeper/config_credential.go internal/gatekeeper/config_credential_test.go
git commit -m "feat(gatekeeper): add credential source resolver"
```

### Task 4.5: Add health check endpoint

**Files:**
- Modify: `internal/gatekeeper/gatekeeper.go` or proxy setup

- [ ] **Step 1: Write health check test**

```go
func TestHealthEndpoint(t *testing.T) {
    // Start Gate Keeper on ephemeral port
    // GET http://localhost:<port>/healthz
    // Expect 200 OK
}
```

- [ ] **Step 2: Run test, verify fails**

- [ ] **Step 3: Implement health endpoint**

Add `/healthz` handler to the proxy listener. Returns 200 with `{"status": "ok"}`.

- [ ] **Step 4: Run tests**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gatekeeper/
git commit -m "feat(gatekeeper): add /healthz endpoint"
```

### Task 4.6: Create standalone binary

**Files:**
- Create: `cmd/gatekeeper/main.go`
- Create: `cmd/gatekeeper/Dockerfile`

- [ ] **Step 1: Write main.go**

```go
// cmd/gatekeeper/main.go
package main

import (
    "context"
    "flag"
    "fmt"
    "os"
    "os/signal"
    "syscall"

    "github.com/majorcontext/moat/internal/gatekeeper"
    "github.com/majorcontext/moat/internal/log"
    _ "github.com/majorcontext/moat/internal/providers" // register all providers
)

func main() {
    configPath := flag.String("config", "", "path to gatekeeper.yaml")
    flag.Parse()

    if *configPath == "" {
        *configPath = os.Getenv("GATEKEEPER_CONFIG")
    }
    if *configPath == "" {
        fmt.Fprintln(os.Stderr, "error: --config or GATEKEEPER_CONFIG required")
        os.Exit(1)
    }

    cfg, err := gatekeeper.LoadConfig(*configPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "error: loading config: %v\n", err)
        os.Exit(1)
    }

    srv, err := gatekeeper.New(cfg)
    if err != nil {
        fmt.Fprintf(os.Stderr, "error: creating server: %v\n", err)
        os.Exit(1)
    }

    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
    defer cancel()

    if err := srv.Start(ctx); err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        os.Exit(1)
    }
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./cmd/gatekeeper/`
Expected: Binary produced without errors

- [ ] **Step 3: Create Dockerfile**

```dockerfile
# cmd/gatekeeper/Dockerfile
FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /gatekeeper ./cmd/gatekeeper

FROM gcr.io/distroless/static-debian12
COPY --from=builder /gatekeeper /gatekeeper
EXPOSE 8080
ENTRYPOINT ["/gatekeeper"]
```

- [ ] **Step 4: Verify build succeeds with CGO_ENABLED=0**

Run: `CGO_ENABLED=0 go build ./cmd/gatekeeper/`
Expected: Success. If it fails, there's a CGO dependency that needs to be addressed (likely SQLite from audit — ensure Gate Keeper doesn't import it).

- [ ] **Step 5: Commit**

```bash
git add cmd/gatekeeper/
git commit -m "feat(gatekeeper): add standalone binary and Dockerfile"
```

### Task 4.7: End-to-end test with standalone binary

**Files:**
- Create: `internal/gatekeeper/e2e_test.go`

- [ ] **Step 1: Write E2E test**

Test the full standalone flow:
1. Write a temp gatekeeper.yaml with env credential source
2. Set the env var
3. Start the gatekeeper binary (or use `gatekeeper.New()` directly)
4. Make HTTPS request through the proxy
5. Verify credential was injected
6. Hit /healthz, verify 200

```go
func TestE2EStandalone(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping E2E test in short mode")
    }

    t.Setenv("TEST_GH_TOKEN", "ghp_test123")

    cfg := &Config{
        Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
        Credentials: []CredentialConfig{
            {
                Grant: "github",
                Source: SourceConfig{Type: "env", Var: "TEST_GH_TOKEN"},
            },
        },
        Network: NetworkConfig{Policy: "permissive"},
    }

    srv, err := New(cfg)
    if err != nil {
        t.Fatal(err)
    }

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    go srv.Start(ctx)
    defer srv.Stop(ctx)

    // Wait for ready
    time.Sleep(200 * time.Millisecond)

    // Test /healthz
    // Test proxy credential injection
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/gatekeeper/ -run TestE2E -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/gatekeeper/e2e_test.go
git commit -m "test(gatekeeper): add E2E standalone test"
```

### Task 4.8: Phase 4 verification

- [ ] **Step 1: Run full test suite and lint**

Run: `make test-unit && make lint`
Expected: PASS

- [ ] **Step 2: Verify Gate Keeper builds with CGO_ENABLED=0**

Run: `CGO_ENABLED=0 go build ./cmd/gatekeeper/`
Expected: Success

- [ ] **Step 3: Verify no daemon imports in gatekeeper**

Run: `go list -deps ./internal/gatekeeper/... | grep daemon`
Expected: 0

Run: `go list -deps ./cmd/gatekeeper/ | grep daemon`
Expected: 0

- [ ] **Step 4: Commit phase marker**

```bash
git commit --allow-empty -m "milestone: Phase 4 complete — Gate Keeper standalone binary ready"
```

---

## Post-Implementation

### Task 5.1: Documentation

**Files:**
- Create: `docs/content/guides/gatekeeper.md` — Gate Keeper guide
- Modify: `docs/content/reference/02-moat-yaml.md` — reference gatekeeper.yaml format
- Modify: `CHANGELOG.md` — add Gate Keeper entry

- [ ] **Step 1: Write Gate Keeper guide**

Cover: what Gate Keeper is, standalone deployment, ECS sidecar example, config reference, credential sources.

- [ ] **Step 2: Update CHANGELOG.md**

Add under next release:

```markdown
### Added
- **Gate Keeper** — standalone credential-injecting proxy for sidecar deployment. Run alongside any service on ECS, Cloud Run, or Kubernetes. Supports env, static, and AWS Secrets Manager credential sources. ([#XXX](https://github.com/majorcontext/moat/pull/XXX))
```

- [ ] **Step 3: Commit**

```bash
git add docs/ CHANGELOG.md
git commit -m "docs: add Gate Keeper guide and changelog entry"
```

### Task 5.2: Final review

- [ ] **Step 1: Run full test suite**

Run: `make test-unit && make lint`

- [ ] **Step 2: Review all new files for quality**

Verify: no TODO comments left, no unused code, consistent naming, error messages include actionable guidance.

- [ ] **Step 3: Create PR**

```bash
gh pr create --title "feat: Gate Keeper — standalone credential-injecting proxy" --body "..."
```
