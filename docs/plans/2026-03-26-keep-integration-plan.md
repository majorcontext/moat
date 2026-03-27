# Keep × Moat Integration Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Embed Keep's policy engine in Moat's proxy to enforce operation-level allow/deny/redact on MCP tool calls and REST API requests, and run Keep's LLM gateway as a container sidecar for prompt/response policy.

**Architecture:** Two integration paths — (1) Keep's Go library (`github.com/majorcontext/keep` v0.2.1) embedded in Moat's TLS-intercepting proxy daemon for MCP and HTTP policy evaluation, (2) `keep-llm-gateway` binary run as a sidecar inside the container for LLM traffic policy. Policy config lives in `moat.yaml` (inline rules, file references, or starter pack names) and is compiled into Keep engines at run registration time.

**Tech Stack:** Go 1.25, `github.com/majorcontext/keep` v0.2.1 (CEL, YAML, gitleaks), Moat proxy daemon, `moat-init.sh` templating.

**Spec:** `docs/plans/2026-03-26-keep-integration-design.md`
**Keep API:** `docs/plans/2026-03-26-keep-embedding-api-spec.md`

---

## File Structure

### New files

| File | Responsibility |
|------|---------------|
| `internal/keep/policy.go` | `PolicyConfig` type, `UnmarshalYAML`, inline-to-YAML translation, file resolution |
| `internal/keep/policy_test.go` | Unit tests for policy parsing and translation |
| `internal/keep/packs.go` | Starter pack registry (Go `embed`), pack lookup |
| `internal/keep/packs_test.go` | Tests for pack resolution |
| `internal/keep/packs/linear-readonly.yaml` | Embedded starter pack: Linear read-only |
| `internal/keep/evaluate.go` | Proxy-facing evaluation helpers: MCP normalization, HTTP normalization, engine construction |
| `internal/keep/evaluate_test.go` | Tests for normalization and evaluation |
| `internal/audit/policy.go` | `EntryPolicy` type and `PolicyDecisionData` struct |

### Modified files

| File | Change |
|------|--------|
| `go.mod` / `go.sum` | Add `github.com/majorcontext/keep v0.2.1` dependency |
| `internal/config/config.go` | Add `Policy *keep.PolicyConfig` to `MCPServerConfig`, `KeepPolicy *keep.PolicyConfig` to `NetworkConfig`, `LLMGateway *LLMGatewayConfig` to `ClaudeConfig`; validation |
| `internal/config/config_test.go` | Tests for new config fields |
| `internal/daemon/api.go` | Add `PolicyYAML map[string][]byte` to `RegisterRequest`, `Error string` to `RegisterResponse`, `Capabilities []string` to `HealthResponse` |
| `internal/daemon/runcontext.go` | Store `*keep.Engine`, propagate to `RunContextData` via `ToProxyContextData()`, `Close()` cleanup |
| `internal/daemon/server.go` | Compile Keep engine on registration, populate `Capabilities` in health endpoint |
| `internal/proxy/proxy.go` | Add `KeepEngine *keep.Engine` to `RunContextData`, call Keep evaluation in CONNECT handler |
| `internal/proxy/mcp.go` | Call Keep evaluation in `handleMCPRelay()` before forwarding |
| `internal/run/manager.go` | Resolve policy files, build `PolicyYAML` for `RegisterRequest`, LLM gateway setup |

---

## Task 1: Add Keep Go Module Dependency

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add keep dependency**

```bash
cd /workspace && go get github.com/majorcontext/keep@v0.2.1
```

- [ ] **Step 2: Tidy modules**

```bash
go mod tidy
```

- [ ] **Step 3: Verify import works**

Create a temporary test to verify the import resolves:

```bash
go build ./...
```

Expected: builds successfully.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "build: add github.com/majorcontext/keep v0.2.1 dependency"
```

---

## Task 2: PolicyConfig Type and Inline Rule Translation

**Files:**
- Create: `internal/keep/policy.go`
- Create: `internal/keep/policy_test.go`

- [ ] **Step 1: Write tests for PolicyConfig UnmarshalYAML**

```go
// internal/keep/policy_test.go
package keep

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPolicyConfigUnmarshalYAML_StarterPack(t *testing.T) {
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(`"linear-readonly"`), &pc)
	require.NoError(t, err)
	assert.Equal(t, "linear-readonly", pc.Pack)
	assert.Empty(t, pc.File)
	assert.Nil(t, pc.Allow)
}

func TestPolicyConfigUnmarshalYAML_FilePath(t *testing.T) {
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(`".keep/linear.yaml"`), &pc)
	require.NoError(t, err)
	assert.Equal(t, ".keep/linear.yaml", pc.File)
	assert.Empty(t, pc.Pack)
}

func TestPolicyConfigUnmarshalYAML_FilePathNoSlash(t *testing.T) {
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(`"rules.yaml"`), &pc)
	require.NoError(t, err)
	assert.Equal(t, "rules.yaml", pc.File)
	assert.Empty(t, pc.Pack)
}

func TestPolicyConfigUnmarshalYAML_Inline(t *testing.T) {
	input := `
allow:
  - get_issue
  - list_issues
deny:
  - delete_issue
mode: enforce
`
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(input), &pc)
	require.NoError(t, err)
	assert.Equal(t, []string{"get_issue", "list_issues"}, pc.Allow)
	assert.Equal(t, []string{"delete_issue"}, pc.Deny)
	assert.Equal(t, "enforce", pc.Mode)
	assert.Empty(t, pc.Pack)
	assert.Empty(t, pc.File)
}

func TestPolicyConfigUnmarshalYAML_InvalidNode(t *testing.T) {
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(`[1, 2, 3]`), &pc)
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /workspace && go test ./internal/keep/ -run TestPolicyConfig -v
```

Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write tests for inline rule translation**

Add to `internal/keep/policy_test.go`:

```go
func TestToKeepYAML_AllowOnly(t *testing.T) {
	pc := PolicyConfig{
		Allow: []string{"get_issue", "list_issues"},
		Mode:  "enforce",
	}
	yamlBytes, err := pc.ToKeepYAML("mcp-tools")
	require.NoError(t, err)

	yamlStr := string(yamlBytes)
	assert.Contains(t, yamlStr, "scope: mcp-tools")
	assert.Contains(t, yamlStr, "mode: enforce")
	assert.Contains(t, yamlStr, "operation: get_issue")
	assert.Contains(t, yamlStr, "operation: list_issues")
	// Should have a default-deny rule
	assert.Contains(t, yamlStr, `operation: "*"`)
}

func TestToKeepYAML_DenyOnly(t *testing.T) {
	pc := PolicyConfig{
		Deny: []string{"delete_issue"},
	}
	yamlBytes, err := pc.ToKeepYAML("mcp-tools")
	require.NoError(t, err)

	yamlStr := string(yamlBytes)
	assert.Contains(t, yamlStr, "operation: delete_issue")
	assert.Contains(t, yamlStr, "action: deny")
	// No default-deny when there's no allowlist
	assert.NotContains(t, yamlStr, `operation: "*"`)
}

func TestToKeepYAML_AllowAndDeny(t *testing.T) {
	pc := PolicyConfig{
		Allow: []string{"get_issue", "delete_issue"},
		Deny:  []string{"delete_issue"},
		Mode:  "audit",
	}
	yamlBytes, err := pc.ToKeepYAML("mcp-tools")
	require.NoError(t, err)

	yamlStr := string(yamlBytes)
	// "audit" in moat.yaml is translated to "audit_only" for Keep
	assert.Contains(t, yamlStr, "mode: audit_only")
	// deny rule should be present for delete_issue
	assert.Contains(t, yamlStr, "action: deny")
}

func TestToKeepYAML_DefaultMode(t *testing.T) {
	pc := PolicyConfig{
		Deny: []string{"delete_issue"},
	}
	yamlBytes, err := pc.ToKeepYAML("test-scope")
	require.NoError(t, err)
	assert.Contains(t, string(yamlBytes), "mode: enforce")
}
```

- [ ] **Step 4: Implement PolicyConfig**

```go
// internal/keep/policy.go
package keep

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// PolicyConfig represents a Keep policy parsed from moat.yaml.
// It accepts three shapes:
//   - Starter pack name: plain string without "/" or ".yaml" suffix
//   - File path: string containing "/" or ending in ".yaml"
//   - Inline rules: YAML mapping with allow/deny/mode fields
type PolicyConfig struct {
	Pack  string   `yaml:"-"` // starter pack name
	File  string   `yaml:"-"` // file path
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
	Mode  string   `yaml:"mode,omitempty"`
}

func (p *PolicyConfig) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		s := node.Value
		if strings.Contains(s, "/") || strings.HasSuffix(s, ".yaml") || strings.HasSuffix(s, ".yml") {
			p.File = s
		} else {
			p.Pack = s
		}
		return nil
	case yaml.MappingNode:
		type policyAlias PolicyConfig
		var alias policyAlias
		if err := node.Decode(&alias); err != nil {
			return fmt.Errorf("invalid inline policy: %w", err)
		}
		*p = PolicyConfig(alias)
		return nil
	default:
		return fmt.Errorf("policy must be a string (file path or pack name) or mapping (inline rules), got %v", node.Kind)
	}
}

// IsInline returns true if this policy uses inline allow/deny rules.
func (p *PolicyConfig) IsInline() bool {
	return p.Pack == "" && p.File == "" && (len(p.Allow) > 0 || len(p.Deny) > 0)
}

// IsFile returns true if this policy references an external file.
func (p *PolicyConfig) IsFile() bool {
	return p.File != ""
}

// IsPack returns true if this policy references a starter pack.
func (p *PolicyConfig) IsPack() bool {
	return p.Pack != ""
}

// ToKeepYAML converts inline rules to Keep's native YAML rule format.
// scope is the Keep scope name (e.g., MCP server name).
// Returns an error if the policy is not inline.
func (p *PolicyConfig) ToKeepYAML(scope string) ([]byte, error) {
	if !p.IsInline() {
		return nil, fmt.Errorf("ToKeepYAML called on non-inline policy")
	}

	mode := p.Mode
	if mode == "" {
		mode = "enforce"
	}
	// Keep uses "audit_only", moat.yaml uses "audit" for brevity.
	if mode == "audit" {
		mode = "audit_only"
	}

	var rules []keepRule

	// Deny rules first (highest priority).
	for _, op := range p.Deny {
		rules = append(rules, keepRule{
			Name:    "deny-" + op,
			Match:   keepMatch{Operation: op},
			Action:  "deny",
			Message: "Operation blocked by policy.",
		})
	}

	// Allow rules.
	for _, op := range p.Allow {
		// Skip if also in deny list (deny takes precedence).
		if containsString(p.Deny, op) {
			continue
		}
		rules = append(rules, keepRule{
			Name:   "allow-" + op,
			Match:  keepMatch{Operation: op},
			Action: "allow",
		})
	}

	// Default deny if allowlist is set.
	if len(p.Allow) > 0 {
		rules = append(rules, keepRule{
			Name:    "default-deny",
			Match:   keepMatch{Operation: "*"},
			Action:  "deny",
			Message: "Operation not in allowlist.",
		})
	}

	doc := keepRuleDoc{
		Scope: scope,
		Mode:  mode,
		Rules: rules,
	}

	return yaml.Marshal(doc)
}

type keepRuleDoc struct {
	Scope string     `yaml:"scope"`
	Mode  string     `yaml:"mode"`
	Rules []keepRule `yaml:"rules"`
}

type keepRule struct {
	Name    string    `yaml:"name"`
	Match   keepMatch `yaml:"match"`
	Action  string    `yaml:"action"`
	Message string    `yaml:"message,omitempty"`
}

type keepMatch struct {
	Operation string `yaml:"operation"`
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
cd /workspace && go test ./internal/keep/ -run "TestPolicyConfig|TestToKeepYAML" -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/keep/policy.go internal/keep/policy_test.go
git commit -m "feat(keep): add PolicyConfig type with YAML unmarshaling and inline rule translation"
```

---

## Task 3: Config Parsing — Add Policy to MCP, Network, and Claude Configs

**Files:**
- Modify: `internal/config/config.go` (lines 136-140, 225-230, 232-260)
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write config parsing tests**

Config tests use `t.TempDir()` + write file + `Load(dir)` (there is no `LoadFromBytes`).

Add to `internal/config/config_test.go`:

```go
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(content), 0o644)
	return dir
}

func TestMCPServerPolicy_StarterPack(t *testing.T) {
	dir := writeConfig(t, `
name: test
mcp:
  - name: linear
    url: https://mcp.linear.app/mcp
    policy: linear-readonly
`)
	cfg, err := Load(dir)
	require.NoError(t, err)
	require.Len(t, cfg.MCP, 1)
	require.NotNil(t, cfg.MCP[0].Policy)
	assert.Equal(t, "linear-readonly", cfg.MCP[0].Policy.Pack)
}

func TestMCPServerPolicy_FilePath(t *testing.T) {
	dir := writeConfig(t, `
name: test
mcp:
  - name: linear
    url: https://mcp.linear.app/mcp
    policy: .keep/linear.yaml
`)
	cfg, err := Load(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg.MCP[0].Policy)
	assert.Equal(t, ".keep/linear.yaml", cfg.MCP[0].Policy.File)
}

func TestMCPServerPolicy_Inline(t *testing.T) {
	dir := writeConfig(t, `
name: test
mcp:
  - name: linear
    url: https://mcp.linear.app/mcp
    policy:
      allow: [get_issue, list_issues]
      deny: [delete_issue]
`)
	cfg, err := Load(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg.MCP[0].Policy)
	assert.Equal(t, []string{"get_issue", "list_issues"}, cfg.MCP[0].Policy.Allow)
	assert.Equal(t, []string{"delete_issue"}, cfg.MCP[0].Policy.Deny)
}

func TestNetworkKeepPolicy(t *testing.T) {
	dir := writeConfig(t, `
name: test
network:
  policy: strict
  keep_policy: .keep/api-policy.yaml
`)
	cfg, err := Load(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg.Network.KeepPolicy)
	assert.Equal(t, ".keep/api-policy.yaml", cfg.Network.KeepPolicy.File)
}

func TestClaudeLLMGateway(t *testing.T) {
	dir := writeConfig(t, `
name: test
claude:
  llm-gateway:
    policy: .keep/llm-rules.yaml
`)
	cfg, err := Load(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg.Claude.LLMGateway)
	require.NotNil(t, cfg.Claude.LLMGateway.Policy)
	assert.Equal(t, ".keep/llm-rules.yaml", cfg.Claude.LLMGateway.Policy.File)
}

func TestClaudeLLMGateway_ConflictsWithBaseURL(t *testing.T) {
	dir := writeConfig(t, `
name: test
claude:
  base_url: http://localhost:8080
  llm-gateway:
    policy: .keep/llm-rules.yaml
`)
	_, err := Load(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /workspace && go test ./internal/config/ -run "TestMCPServerPolicy|TestNetworkKeepPolicy|TestClaudeLLMGateway" -v
```

Expected: FAIL — fields don't exist yet.

- [ ] **Step 3: Add Policy field to MCPServerConfig**

In `internal/config/config.go`, add to `MCPServerConfig` struct (around line 140):

```go
type MCPServerConfig struct {
	Name   string          `yaml:"name"   json:"name"`
	URL    string          `yaml:"url"    json:"url"`
	Auth   *MCPAuthConfig  `yaml:"auth,omitempty" json:"auth,omitempty"`
	Policy *keep.PolicyConfig `yaml:"policy,omitempty" json:"policy,omitempty"`
}
```

Add import: `"github.com/majorcontext/moat/internal/keep"`

- [ ] **Step 4: Add KeepPolicy to NetworkConfig**

In `internal/config/config.go`, update `NetworkConfig` struct (around line 225-230):

```go
type NetworkConfig struct {
	Policy     string                      `yaml:"policy,omitempty"`
	Allow      []string                    `yaml:"allow,omitempty"`
	Rules      []netrules.NetworkRuleEntry `yaml:"rules,omitempty"`
	KeepPolicy *keep.PolicyConfig          `yaml:"keep_policy,omitempty"`
}
```

- [ ] **Step 5: Add LLMGateway to ClaudeConfig**

In `internal/config/config.go`, add after existing `ClaudeConfig` fields:

```go
type LLMGatewayConfig struct {
	Policy  *keep.PolicyConfig `yaml:"policy,omitempty"`
	Version string             `yaml:"version,omitempty"`
	Port    int                `yaml:"port,omitempty"`
}
```

Add `LLMGateway *LLMGatewayConfig` to `ClaudeConfig`:

```go
// LLMGateway configures a Keep LLM gateway sidecar inside the container.
// Mutually exclusive with BaseURL.
LLMGateway *LLMGatewayConfig `yaml:"llm-gateway,omitempty"`
```

- [ ] **Step 6: Add validation for base_url/llm-gateway mutual exclusion**

In the config validation section (inside `Load()` or `Validate()`), add:

```go
if cfg.Claude.BaseURL != "" && cfg.Claude.LLMGateway != nil {
	return nil, fmt.Errorf("claude: base_url and llm-gateway are mutually exclusive — base_url routes to an external LLM proxy, llm-gateway routes to a local Keep sidecar")
}
```

- [ ] **Step 7: Run tests to verify they pass**

```bash
cd /workspace && go test ./internal/config/ -run "TestMCPServerPolicy|TestNetworkKeepPolicy|TestClaudeLLMGateway" -v
```

Expected: all PASS.

- [ ] **Step 8: Run full config test suite**

```bash
cd /workspace && go test ./internal/config/ -v
```

Expected: all existing tests still pass.

- [ ] **Step 9: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add policy field to MCP servers, network keep_policy, and claude llm-gateway"
```

---

## Task 4: Daemon API Extensions

**Files:**
- Modify: `internal/daemon/api.go` (lines 64-84, 92-98)
- Modify: `internal/daemon/runcontext.go` (lines 47-72, 239-329)
- Modify: `internal/daemon/server.go`

- [ ] **Step 1: Write test for capabilities in health response**

```go
// Add to existing daemon test file or create internal/daemon/api_test.go
func TestHealthResponseCapabilities(t *testing.T) {
	resp := HealthResponse{
		PID:          1234,
		ProxyPort:    8080,
		Capabilities: []string{"keep-policy"},
	}
	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded HealthResponse
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Contains(t, decoded.Capabilities, "keep-policy")
}

// Old client ignores unknown capabilities field.
func TestHealthResponseBackwardsCompat(t *testing.T) {
	data := `{"pid":1234,"proxy_port":8080,"capabilities":["keep-policy"]}`
	type OldHealthResponse struct {
		PID       int    `json:"pid"`
		ProxyPort int    `json:"proxy_port"`
	}
	var old OldHealthResponse
	require.NoError(t, json.Unmarshal([]byte(data), &old))
	assert.Equal(t, 1234, old.PID)
}
```

- [ ] **Step 2: Add fields to daemon API structs**

In `internal/daemon/api.go`, add to `RegisterRequest` (after line 77):

```go
// PolicyYAML contains compiled Keep policy YAML keyed by scope name.
// Each entry is raw YAML bytes ready to pass to keep.LoadFromBytes().
// Additive field — old daemons ignore it.
PolicyYAML map[string][]byte `json:"policy_yaml,omitempty"`
```

Add to `RegisterResponse` (after line 83):

```go
// Error is set when registration fails (e.g., policy compilation error).
// Additive field — old CLIs ignore it.
Error string `json:"error,omitempty"`
```

Add to `HealthResponse` (after line 97):

```go
// Capabilities lists features supported by this daemon build.
// Additive field — old CLIs ignore it.
Capabilities []string `json:"capabilities,omitempty"`
```

- [ ] **Step 3: Update RunContext to store Keep engine**

In `internal/daemon/runcontext.go`, add to the `RunContext` struct (around line 72):

```go
// KeepEngines holds compiled Keep policy engines for this run, keyed by scope.
// nil if no policy is configured. Must be closed when the run is unregistered.
KeepEngines map[string]*keep.Engine
```

Add import: `keep "github.com/majorcontext/keep"` (note: alias to avoid conflict with the internal `keep` package — or use the internal package's full path).

**Important:** The import is `github.com/majorcontext/keep` for the upstream library. Use an alias if there's a naming conflict with `internal/keep`:

```go
import (
	keeplib "github.com/majorcontext/keep"
)
```

- [ ] **Step 4: Update ToProxyContextData to propagate engine**

In `internal/daemon/runcontext.go`, in the `ToProxyContextData()` function (around line 239-329), add the engine to the returned struct:

```go
// Near the end of ToProxyContextData(), before the return:
data.KeepEngines = rc.KeepEngines
```

- [ ] **Step 5: Add Close method to RunContext for cleanup**

```go
// Close releases resources held by this RunContext.
// Must be called when the run is unregistered.
func (rc *RunContext) Close() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for _, eng := range rc.KeepEngines {
		eng.Close()
	}
	rc.KeepEngines = nil
}
```

- [ ] **Step 6: Compile Keep engine on registration**

In `internal/daemon/server.go`, in the `handleRegisterRun()` handler, after `req.ToRunContext()`, add engine compilation:

```go
rc := req.ToRunContext()

// Compile Keep policy engines if provided.
if len(req.PolicyYAML) > 0 {
	rc.KeepEngines = make(map[string]*keeplib.Engine, len(req.PolicyYAML))
	for scope, policyBytes := range req.PolicyYAML {
		scopeCopy := scope // capture for closure
		eng, err := keeplib.LoadFromBytes(policyBytes,
			keeplib.WithAuditHook(func(entry keeplib.AuditEntry) {
				log.Info("keep policy", "scope", scopeCopy, "op", entry.Call.Operation, "decision", entry.Decision)
			}),
		)
		if err != nil {
			// Clean up already-compiled engines.
			for _, e := range rc.KeepEngines {
				e.Close()
			}
			writeJSON(w, http.StatusBadRequest, RegisterResponse{
				Error: fmt.Sprintf("policy compilation failed for scope %q: %v", scope, err),
			})
			return
		}
		rc.KeepEngines[scope] = eng
	}
}
```

**Note:** This is a simplification for v1 — one engine per run. The policy YAML should be pre-merged by the CLI into a single document before registration. Revisit for multi-scope support.

- [ ] **Step 7: Add capabilities to health endpoint**

In `internal/daemon/server.go`, in the health handler, populate capabilities:

```go
resp := HealthResponse{
	// ... existing fields ...
	Capabilities: []string{"keep-policy"},
}
```

- [ ] **Step 8: Clean up engine on unregister**

In `internal/daemon/server.go`, in the `handleUnregisterRun()` handler, call `Close()`:

```go
// Before removing the run context from the map:
if rc := s.runs[token]; rc != nil {
	rc.Close() // closes all KeepEngines
}
```

- [ ] **Step 9: Run daemon tests**

```bash
cd /workspace && go test ./internal/daemon/ -v
```

Expected: all pass.

- [ ] **Step 10: Commit**

```bash
git add internal/daemon/api.go internal/daemon/runcontext.go internal/daemon/server.go
git commit -m "feat(daemon): add Keep policy compilation on run registration with capabilities check"
```

---

## Task 5: Proxy Integration — Keep Evaluation in MCP Relay and CONNECT Handler

**Files:**
- Modify: `internal/proxy/proxy.go` (lines 232-245 RunContextData, lines 1116-1149 CONNECT)
- Modify: `internal/proxy/mcp.go` (lines 151-274 handleMCPRelay)
- Create: `internal/keep/evaluate.go`
- Create: `internal/keep/evaluate_test.go`

- [ ] **Step 1: Write tests for MCP call normalization**

```go
// internal/keep/evaluate_test.go
package keep

import (
	"testing"

	keeplib "github.com/majorcontext/keep"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeMCPCall(t *testing.T) {
	call := NormalizeMCPCall("delete_issue", map[string]any{"id": "123"}, "linear")
	assert.Equal(t, "delete_issue", call.Operation)
	assert.Equal(t, map[string]any{"id": "123"}, call.Params)
	assert.Equal(t, "linear", call.Context.Scope)
}

func TestNormalizeHTTPCall(t *testing.T) {
	call := NormalizeHTTPCall("DELETE", "api.linear.app", "/issues/123")
	assert.Equal(t, "DELETE /issues/123", call.Operation)
	assert.Equal(t, "DELETE", call.Params["method"])
	assert.Equal(t, "api.linear.app", call.Params["host"])
	assert.Equal(t, "/issues/123", call.Params["path"])
	assert.Equal(t, "http-api.linear.app", call.Context.Scope)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /workspace && go test ./internal/keep/ -run "TestNormalize" -v
```

Expected: FAIL.

- [ ] **Step 3: Implement normalization helpers**

```go
// internal/keep/evaluate.go
package keep

import (
	"time"

	keeplib "github.com/majorcontext/keep"
)

// NormalizeMCPCall converts an MCP tool call into a Keep Call.
func NormalizeMCPCall(toolName string, params map[string]any, scope string) keeplib.Call {
	return keeplib.Call{
		Operation: toolName,
		Params:    params,
		Context: keeplib.CallContext{
			Scope:     scope,
			Timestamp: time.Now(),
		},
	}
}

// NormalizeHTTPCall converts an HTTP request into a Keep Call.
func NormalizeHTTPCall(method, host, path string) keeplib.Call {
	return keeplib.Call{
		Operation: method + " " + path,
		Params: map[string]any{
			"method": method,
			"host":   host,
			"path":   path,
		},
		Context: keeplib.CallContext{
			Scope:     "http-" + host,
			Timestamp: time.Now(),
		},
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /workspace && go test ./internal/keep/ -run "TestNormalize" -v
```

Expected: PASS.

- [ ] **Step 5: Add KeepEngines to RunContextData**

In `internal/proxy/proxy.go`, add to `RunContextData` struct (around line 245):

```go
KeepEngines map[string]*keeplib.Engine // compiled Keep rules keyed by scope, nil if no policy
```

Add import: `keeplib "github.com/majorcontext/keep"`

- [ ] **Step 6: Add Keep evaluation to handleMCPRelay**

In `internal/proxy/mcp.go`, after the MCP server is found and **before** the proxy request is created from `r.Body` (before line 196 where `http.NewRequestWithContext` consumes the body). This must be before body consumption, not at the credential injection point (line 220).

```go
// Evaluate Keep policy if engine is available for this MCP server's scope.
if rc := getRunContext(r); rc != nil && rc.KeepEngines[serverName] != nil {
	// Parse MCP request body to extract tool name and params.
	// The body has already been read for relay; we need to peek at it.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var mcpReq struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(bodyBytes, &mcpReq); err == nil && mcpReq.Method != "" {
		// Extract tool name from MCP method (e.g., "tools/call" has params.name)
		toolName := ""
		toolParams := mcpReq.Params
		if name, ok := mcpReq.Params["name"].(string); ok {
			toolName = name
		}
		if args, ok := mcpReq.Params["arguments"].(map[string]any); ok {
			toolParams = args
		}

		if toolName != "" {
			call := internalkeep.NormalizeMCPCall(toolName, toolParams, serverName)
			result, evalErr := rc.KeepEngines[serverName].Evaluate(call, serverName)
			if evalErr != nil {
				log.Warn("keep evaluation error", "scope", serverName, "error", evalErr)
			} else if result.Decision == keeplib.Deny {
				http.Error(w, fmt.Sprintf("policy denied: %s — %s", toolName, result.Message), http.StatusForbidden)
				return
			} else if result.Decision == keeplib.Redact {
				// Apply mutations to params and rewrite body.
				mutated := keeplib.ApplyMutations(toolParams, result.Mutations)
				mcpReq.Params["arguments"] = mutated
				newBody, _ := json.Marshal(mcpReq)
				r.Body = io.NopCloser(bytes.NewReader(newBody))
				r.ContentLength = int64(len(newBody))
			}
		}
	}
}
```

Add imports: `internalkeep "github.com/majorcontext/moat/internal/keep"` and `keeplib "github.com/majorcontext/keep"`

- [ ] **Step 7: Add Keep evaluation to CONNECT handler**

In `internal/proxy/proxy.go`, inside the TLS interception loop (around line 1262) where `req` is read from `clientReader`. The outer CONNECT request `r` carries the `RunContextData` (via request context), while the inner `req` has the HTTP method/path. Place this after network policy check, before `transport.RoundTrip()`:

```go
// Evaluate Keep policy for HTTP requests.
// r = outer CONNECT request (carries RunContextData), req = inner HTTP request
if rc := getRunContext(r); rc != nil {
	scope := "http-" + req.Host
	if eng := rc.KeepEngines[scope]; eng != nil {
		call := internalkeep.NormalizeHTTPCall(req.Method, req.Host, req.URL.Path)
		result, evalErr := eng.Evaluate(call, scope)
	if evalErr != nil {
		log.Warn("keep evaluation error", "scope", scope, "error", evalErr)
	} else if result.Decision == keeplib.Deny {
		resp := &http.Response{
			StatusCode: http.StatusForbidden,
			Status:     "403 Forbidden",
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(fmt.Sprintf("policy denied: %s %s — %s", req.Method, req.URL.Path, result.Message))),
		}
		resp.Write(clientConn)
		return
	}
}
```

**Note:** The exact insertion point depends on the interception flow. Look for where the proxy reads the HTTP/1.1 request from the intercepted TLS connection and before it forwards via `transport.RoundTrip()`. This is in the `handleConnect` flow after the MITM TLS handshake.

- [ ] **Step 8: Add recover() around Keep evaluation**

Wrap both evaluation points with panic recovery to protect the proxy:

```go
func safeEvaluate(eng *keeplib.Engine, call keeplib.Call, scope string) (keeplib.EvalResult, error) {
	var result keeplib.EvalResult
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("keep engine panic: %v", r)
				log.Error("keep engine panic during evaluation", "scope", scope, "panic", r)
			}
		}()
		result, err = eng.Evaluate(call, scope)
	}()
	return result, err
}
```

Add this to `internal/keep/evaluate.go` and use it from both proxy integration points.

- [ ] **Step 9: Build and test**

```bash
cd /workspace && go build ./...
cd /workspace && go test ./internal/proxy/ -v
cd /workspace && go test ./internal/keep/ -v
```

Expected: all pass, no build errors.

- [ ] **Step 10: Commit**

```bash
git add internal/keep/evaluate.go internal/keep/evaluate_test.go internal/proxy/proxy.go internal/proxy/mcp.go
git commit -m "feat(proxy): embed Keep policy evaluation in MCP relay and CONNECT handler"
```

---

## Task 6: Audit Integration

**Files:**
- Create: `internal/audit/policy.go`
- Modify: `internal/daemon/server.go` (audit hook wiring)

- [ ] **Step 1: Write test for policy audit entry**

```go
// internal/audit/policy_test.go
package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPolicyDecisionData(t *testing.T) {
	data := PolicyDecisionData{
		Scope:     "linear",
		Operation: "delete_issue",
		Decision:  "deny",
		Rule:      "no-deletes",
		Message:   "Destructive operations blocked",
	}
	assert.Equal(t, "linear", data.Scope)
	assert.Equal(t, "deny", data.Decision)
}
```

- [ ] **Step 2: Define policy audit entry type**

```go
// internal/audit/policy.go
package audit

// EntryPolicy is the audit entry type for Keep policy decisions.
const EntryPolicy EntryType = "policy"

// PolicyDecisionData records a Keep policy evaluation result.
type PolicyDecisionData struct {
	Scope     string `json:"scope"`
	Operation string `json:"operation"`
	Decision  string `json:"decision"` // "allow", "deny", "redact"
	Rule      string `json:"rule,omitempty"`
	Message   string `json:"message,omitempty"`
}
```

- [ ] **Step 3: Wire audit hook in daemon server**

Update the engine compilation in `internal/daemon/server.go` to use an audit-aware hook. The hook should log to both debug log and emit a policy event that the CLI can collect:

```go
eng, err := keeplib.LoadFromBytes(policyBytes,
	keeplib.WithAuditHook(func(entry keeplib.AuditEntry) {
		decision := string(entry.Decision)
		if entry.Decision == keeplib.Deny {
			log.Warn("keep policy deny",
				"scope", entry.Scope,
				"op", entry.Call.Operation,
				"rule", entry.Rule,
				"message", entry.Message,
			)
		} else {
			log.Info("keep policy",
				"scope", entry.Scope,
				"op", entry.Call.Operation,
				"decision", decision,
			)
		}
		// Emit to the proxy's request logger callback if available.
		// The CLI-side run manager routes these to audit.Store.Append().
		if s.policyLogger != nil {
			s.policyLogger(audit.PolicyDecisionData{
				Scope:     entry.Scope,
				Operation: entry.Call.Operation,
				Decision:  decision,
				Rule:      entry.Rule,
				Message:   entry.Message,
			})
		}
	}),
)
```

**Note:** The exact callback mechanism depends on how the daemon server exposes logging. Follow the existing `RequestLogger` pattern from `internal/proxy/proxy.go:76-77`. Add a `PolicyLogger func(PolicyDecisionData)` field to the server or proxy struct.

- [ ] **Step 4: Run tests**

```bash
cd /workspace && go test ./internal/audit/ -v
cd /workspace && go test ./internal/daemon/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/audit/policy.go internal/audit/policy_test.go internal/daemon/server.go
git commit -m "feat(audit): add policy decision audit entry type and wire Keep audit hook"
```

---

## Task 7: Run Manager — Policy Resolution and RegisterRequest Building

**Files:**
- Modify: `internal/run/manager.go` (lines 580-699 create flow, lines 3610-3674 buildRegisterRequest)

- [ ] **Step 1: Write test for policy YAML resolution**

```go
// internal/keep/policy_test.go — add
func TestResolvePolicyYAML_InlineRules(t *testing.T) {
	pc := &PolicyConfig{
		Allow: []string{"get_issue"},
		Deny:  []string{"delete_issue"},
	}
	yamlBytes, err := ResolvePolicyYAML(pc, "linear", "")
	require.NoError(t, err)
	assert.Contains(t, string(yamlBytes), "scope: linear")
}

func TestResolvePolicyYAML_FileReference(t *testing.T) {
	// Create a temp file
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.yaml")
	os.WriteFile(path, []byte("scope: test\nrules: []\n"), 0o644)

	pc := &PolicyConfig{File: path}
	yamlBytes, err := ResolvePolicyYAML(pc, "test", "")
	require.NoError(t, err)
	assert.Contains(t, string(yamlBytes), "scope: test")
}

func TestResolvePolicyYAML_MissingFile(t *testing.T) {
	pc := &PolicyConfig{File: "/nonexistent/rules.yaml"}
	_, err := ResolvePolicyYAML(pc, "test", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "policy file not found")
}
```

- [ ] **Step 2: Implement ResolvePolicyYAML**

Add to `internal/keep/policy.go`:

```go
// ResolvePolicyYAML resolves a PolicyConfig into raw YAML bytes
// suitable for keep.LoadFromBytes(). baseDir is used to resolve
// relative file paths; if empty, paths are used as-is.
func ResolvePolicyYAML(pc *PolicyConfig, scope, baseDir string) ([]byte, error) {
	switch {
	case pc.IsInline():
		return pc.ToKeepYAML(scope)
	case pc.IsFile():
		path := pc.File
		if baseDir != "" && !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("policy file not found: %s: %w", pc.File, err)
		}
		return data, nil
	case pc.IsPack():
		return GetStarterPack(pc.Pack)
	default:
		return nil, fmt.Errorf("empty policy config")
	}
}
```

Add imports: `"os"`, `"path/filepath"`.

- [ ] **Step 3: Run tests**

```bash
cd /workspace && go test ./internal/keep/ -run TestResolvePolicy -v
```

Expected: PASS (GetStarterPack will be implemented in Task 8; for now the file and inline cases pass).

- [ ] **Step 4: Update buildRegisterRequest in run/manager.go**

In `internal/run/manager.go`, in the run creation flow, after MCP servers are set (around line 699), add policy resolution:

```go
// Resolve Keep policy YAML for each MCP server with a policy.
policyYAML := make(map[string][]byte)
configDir := filepath.Dir(opts.ConfigPath) // base dir for relative paths

for _, mcp := range opts.Config.MCP {
	if mcp.Policy != nil {
		yamlBytes, err := internalkeep.ResolvePolicyYAML(mcp.Policy, mcp.Name, configDir)
		if err != nil {
			return fmt.Errorf("MCP server %q policy: %w", mcp.Name, err)
		}
		policyYAML[mcp.Name] = yamlBytes
	}
}

// Resolve network keep_policy.
if opts.Config.Network.KeepPolicy != nil {
	yamlBytes, err := internalkeep.ResolvePolicyYAML(opts.Config.Network.KeepPolicy, "http", configDir)
	if err != nil {
		return fmt.Errorf("network keep_policy: %w", err)
	}
	policyYAML["http"] = yamlBytes
}
```

Then set `policyYAML` on the `RegisterRequest` **after** `buildRegisterRequest` returns (around line 702, not inside the function):

```go
// After: regReq := buildRegisterRequest(runCtx, grants)
regReq.PolicyYAML = policyYAML
```

- [ ] **Step 5: Check daemon capabilities before registration**

Before registering, if policy is configured, check daemon capabilities:

```go
if len(policyYAML) > 0 {
	health, err := daemonClient.Health(ctx)
	if err != nil {
		return fmt.Errorf("failed to check proxy daemon: %w", err)
	}
	hasKeep := false
	for _, cap := range health.Capabilities {
		if cap == "keep-policy" {
			hasKeep = true
			break
		}
	}
	if !hasKeep {
		return fmt.Errorf("policy enforcement requires a newer proxy daemon — run 'moat proxy restart' to upgrade")
	}
}
```

- [ ] **Step 6: Check registration error response**

After registration, check for compilation errors:

```go
if regResp.Error != "" {
	return fmt.Errorf("policy compilation failed: %s", regResp.Error)
}
```

- [ ] **Step 7: Build and test**

```bash
cd /workspace && go build ./...
```

Expected: builds successfully.

- [ ] **Step 8: Commit**

```bash
git add internal/keep/policy.go internal/keep/policy_test.go internal/run/manager.go
git commit -m "feat(run): resolve Keep policy files and pass to daemon on registration"
```

---

## Task 8: Starter Packs

**Files:**
- Create: `internal/keep/packs.go`
- Create: `internal/keep/packs_test.go`
- Create: `internal/keep/packs/linear-readonly.yaml`

- [ ] **Step 1: Write test for starter pack lookup**

```go
// internal/keep/packs_test.go
package keep

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetStarterPack_Known(t *testing.T) {
	data, err := GetStarterPack("linear-readonly")
	require.NoError(t, err)
	assert.Contains(t, string(data), "scope:")
	assert.Contains(t, string(data), "rules:")
}

func TestGetStarterPack_Unknown(t *testing.T) {
	_, err := GetStarterPack("nonexistent-pack")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown starter pack")
}

func TestListStarterPacks(t *testing.T) {
	packs := ListStarterPacks()
	assert.Contains(t, packs, "linear-readonly")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /workspace && go test ./internal/keep/ -run "TestGetStarterPack|TestListStarterPacks" -v
```

Expected: FAIL.

- [ ] **Step 3: Create linear-readonly starter pack**

```yaml
# internal/keep/packs/linear-readonly.yaml
scope: linear
mode: enforce
rules:
  - name: allow-read-operations
    match:
      operation: "list_*"
    action: allow

  - name: allow-get-operations
    match:
      operation: "get_*"
    action: allow

  - name: allow-search
    match:
      operation: "search_*"
    action: allow

  - name: default-deny
    match:
      operation: "*"
    action: deny
    message: "Only read operations are allowed by the linear-readonly policy."
```

- [ ] **Step 4: Implement starter pack registry**

```go
// internal/keep/packs.go
package keep

import (
	"embed"
	"fmt"
	"path/filepath"
	"strings"
)

//go:embed packs/*.yaml
var packsFS embed.FS

// GetStarterPack returns the YAML bytes for a named starter pack.
func GetStarterPack(name string) ([]byte, error) {
	filename := name + ".yaml"
	data, err := packsFS.ReadFile(filepath.Join("packs", filename))
	if err != nil {
		return nil, fmt.Errorf("unknown starter pack %q — available packs: %s", name, strings.Join(ListStarterPacks(), ", "))
	}
	return data, nil
}

// ListStarterPacks returns the names of all available starter packs.
func ListStarterPacks() []string {
	entries, err := packsFS.ReadDir("packs")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
		}
	}
	return names
}
```

- [ ] **Step 5: Run tests**

```bash
cd /workspace && go test ./internal/keep/ -run "TestGetStarterPack|TestListStarterPacks" -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/keep/packs.go internal/keep/packs_test.go internal/keep/packs/
git commit -m "feat(keep): add starter pack registry with linear-readonly pack"
```

---

## Task 9: LLM Gateway Sidecar

**Files:**
- Modify: `internal/run/manager.go`
- Modify: `internal/providers/claude/` (config generation)

- [ ] **Step 1: Write test for LLM gateway config validation**

Add to config test file:

```go
func TestClaudeLLMGateway_DefaultPort(t *testing.T) {
	input := `
name: test
claude:
  llm-gateway:
    policy: .keep/llm-rules.yaml
`
	cfg, err := LoadFromBytes([]byte(input))
	require.NoError(t, err)
	assert.NotNil(t, cfg.Claude.LLMGateway)
	// Port defaults to 0 (will be set by runtime to 18080)
}
```

- [ ] **Step 2: Add LLM gateway binary download to init script**

In the init script template (look for the moat-init.sh template in `internal/run/` or `internal/claude/`), add a section that downloads the gateway binary when `claude.llm-gateway` is configured:

```bash
# Keep LLM Gateway setup
if [ -n "${KEEP_LLM_GATEWAY_VERSION}" ]; then
    KEEP_ARCH=$(uname -m)
    case "$KEEP_ARCH" in
        x86_64) KEEP_ARCH="amd64" ;;
        aarch64|arm64) KEEP_ARCH="arm64" ;;
    esac
    KEEP_OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    KEEP_BIN="/usr/local/bin/keep-llm-gateway"

    if [ ! -f "$KEEP_BIN" ]; then
        KEEP_URL="https://github.com/majorcontext/keep/releases/download/${KEEP_LLM_GATEWAY_VERSION}/keep-llm-gateway_${KEEP_OS}_${KEEP_ARCH}"
        curl -fsSL -o "$KEEP_BIN" "$KEEP_URL" || {
            echo "Failed to download keep-llm-gateway from $KEEP_URL" >&2
            exit 1
        }
        chmod +x "$KEEP_BIN"
    fi

    "$KEEP_BIN" \
        --listen "127.0.0.1:${KEEP_LLM_GATEWAY_PORT}" \
        --rules "${KEEP_LLM_GATEWAY_RULES}" \
        &

    # Wait for gateway to be healthy.
    for i in $(seq 1 30); do
        if curl -sf "http://127.0.0.1:${KEEP_LLM_GATEWAY_PORT}/health" >/dev/null 2>&1; then
            break
        fi
        sleep 0.5
    done
fi
```

- [ ] **Step 3: Set environment variables for gateway in container**

In `internal/run/manager.go`, when building the container environment, add:

```go
if opts.Config.Claude.LLMGateway != nil {
	gw := opts.Config.Claude.LLMGateway
	port := gw.Port
	if port == 0 {
		port = 18080
	}
	version := gw.Version
	if version == "" {
		version = "latest"
	}

	containerEnv = append(containerEnv,
		fmt.Sprintf("KEEP_LLM_GATEWAY_VERSION=%s", version),
		fmt.Sprintf("KEEP_LLM_GATEWAY_PORT=%d", port),
	)

	// Resolve policy file path for mounting into container.
	if gw.Policy != nil && gw.Policy.IsFile() {
		containerEnv = append(containerEnv,
			fmt.Sprintf("KEEP_LLM_GATEWAY_RULES=%s", "/etc/keep/llm-rules.yaml"),
		)
		// Mount the rules file — add to mounts list.
	}
}
```

- [ ] **Step 4: Set base_url in Claude provider config**

In the Claude provider's config generation (likely `internal/providers/claude/config.go`), when `LLMGateway` is configured, override `base_url`:

```go
if cfg.Claude.LLMGateway != nil {
	port := cfg.Claude.LLMGateway.Port
	if port == 0 {
		port = 18080
	}
	// Set ANTHROPIC_BASE_URL to point at local gateway.
	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
}
```

- [ ] **Step 5: Build and verify**

```bash
cd /workspace && go build ./...
```

Expected: builds successfully.

- [ ] **Step 6: Commit**

```bash
git add internal/run/manager.go internal/providers/claude/
git commit -m "feat(run): add Keep LLM gateway sidecar setup with binary download and health check"
```

---

## Task 10: Lint and Final Verification

- [ ] **Step 1: Run linter**

```bash
cd /workspace && make lint
```

Fix any issues found.

- [ ] **Step 2: Run full unit test suite**

```bash
cd /workspace && make test-unit
```

Expected: all pass.

- [ ] **Step 3: Run build**

```bash
cd /workspace && go build ./...
```

Expected: clean build.

- [ ] **Step 4: Commit any lint fixes**

```bash
git add -A
git commit -m "style: fix lint issues from Keep integration"
```

---

## Task 11: Documentation

**Files:**
- Modify: `docs/content/reference/02-moat-yaml.md`
- Modify: `docs/content/guides/` (new guide or update existing)

- [ ] **Step 1: Update moat.yaml reference**

Add documentation for the new fields:

- `mcp[].policy` — starter pack name, file path, or inline rules
- `network.keep_policy` — Keep rules for REST API filtering
- `claude.llm-gateway` — LLM gateway sidecar config
- `claude.llm-gateway.policy` — LLM policy rules
- `claude.llm-gateway.version` — pinned Keep version
- `claude.llm-gateway.port` — gateway listen port

- [ ] **Step 2: Add policy guide**

Create a guide explaining:
- What Keep policy does and when to use it
- Inline rules for simple cases
- `.keep/` directory convention
- Available starter packs
- Audit-only mode for profiling
- How to write custom Keep rule files

- [ ] **Step 3: Update CHANGELOG.md**

Add entry under the next release:

```markdown
### Added

- **Keep policy integration** — enforce operation-level allow/deny/redact on MCP tool calls and REST API requests via the `policy` field on MCP servers and `network.keep_policy` ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
- **LLM gateway sidecar** — run Keep's LLM gateway inside containers for prompt/response policy via `claude.llm-gateway` ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
- **Starter packs** — built-in policy packs like `linear-readonly` for quick setup ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
```

- [ ] **Step 4: Commit**

```bash
git add docs/ CHANGELOG.md
git commit -m "docs: add Keep policy integration documentation and changelog"
```
