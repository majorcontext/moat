# LLM Policy Evaluation in Proxy — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move LLM gateway policy evaluation from an in-container sidecar binary into the Moat proxy, eliminating the need for a separate binary, container image changes, and init script orchestration.

**Architecture:** The proxy already compiles Keep engines per-run and evaluates them for HTTP and MCP requests. This plan adds a third evaluation point: after receiving a response from `api.anthropic.com`, the proxy buffers the response body and calls Keep's new `llm.EvaluateResponse` / `llm.EvaluateStream` library API. If any tool call is denied, the proxy returns a `400 policy_denied` error instead of forwarding the response. SSE streaming responses are handled natively by `llm.EvaluateStream`.

**Tech Stack:** Go, `github.com/majorcontext/keep` v0.2.3 (`llm` and `llm/anthropic` packages), Anthropic Messages API

**Scope:** Full `llm.tool_use` evaluation on responses (JSON and SSE). Request-side evaluation (`llm.EvaluateRequest`) can be added later with the same pattern.

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/proxy/llmpolicy.go` | **Create** | Evaluate Anthropic API responses against Keep LLM policy using `llm.EvaluateResponse`/`EvaluateStream` |
| `internal/proxy/llmpolicy_test.go` | **Create** | Unit tests for LLM policy evaluation |
| `internal/proxy/proxy.go` | **Modify** (~1380-1410) | Integrate LLM policy evaluation into CONNECT handler after RoundTrip |
| `internal/run/manager.go` | **Modify** (~563, ~706-767, ~892-948, ~1485) | Route LLM gateway policy through PolicyYAML; remove sidecar env vars |
| `internal/config/config.go` | **Modify** (~236-240) | Remove `Version` and `Port` from `LLMGatewayConfig` |
| `internal/deps/dockerfile.go` | **Modify** (~209, ~465-480) | Remove `writeLLMGateway()` function and call |
| `internal/deps/imagespec.go` | **Modify** (~44-47, ~73) | Remove `LLMGatewayVersion` field |
| `internal/deps/builder.go` | **Modify** (~65-67) | Remove LLMGateway hash input |
| `internal/deps/scripts/moat-init.sh` | **Modify** (~365-409) | Remove Keep LLM Gateway block |
| `internal/keep/evaluate.go` | **No change** | Existing `SafeEvaluate` stays; `NormalizeLLMToolCall` is NOT needed (Keep's `llm` package handles decomposition internally) |
| `go.mod` | **Modify** | Bump `github.com/majorcontext/keep` to v0.2.3 |
| `examples/policy/moat.yaml` | **Modify** | Update comments |

---

## Context: Proxy CONNECT Handler Flow

The proxy intercepts HTTPS requests via TLS interception in `handleConnectWithInterception` (`proxy.go:1210`). For each inner HTTP request on the TLS connection:

1. Read request from TLS stream (`proxy.go:1267`)
2. `captureBody` for logging (`proxy.go:1278`)
3. Network policy check → may block (`proxy.go:1287`)
4. Keep HTTP policy check → may block (`proxy.go:1307`)
5. Credential injection (`proxy.go:1351`)
6. Forward request: `transport.RoundTrip(req)` (`proxy.go:1381`)
7. Response transformers (`proxy.go:1392`)
8. Capture response body for logging (`proxy.go:1408`)
9. Write response to TLS connection (`proxy.go:1418`)

LLM policy evaluation inserts between steps 6 and 7 (after upstream response, before forwarding).

## Context: Keep v0.2.3 LLM Library API

**Non-streaming** (JSON response — `application/json`):

```go
import (
    "github.com/majorcontext/keep/llm"
    "github.com/majorcontext/keep/llm/anthropic"
)

codec := anthropic.NewCodec()
result, err := llm.EvaluateResponse(engine, codec, body, scope, llm.DecomposeConfig{})
// result.Decision, result.Rule, result.Message
```

**Streaming** (SSE response — `text/event-stream`) requires the `sse` package:

```go
import (
    "github.com/majorcontext/keep/llm"
    "github.com/majorcontext/keep/llm/anthropic"
    "github.com/majorcontext/keep/sse"
)

// Parse SSE events from upstream response body
reader := sse.NewReader(resp.Body)
var events []sse.Event
for {
    ev, err := reader.Next()
    if err == io.EOF { break }
    events = append(events, ev)
    if ev.Type == "message_stop" { break }
}

// Evaluate stream against policy
codec := anthropic.NewCodec()
result, err := llm.EvaluateStream(engine, codec, events, scope, llm.DecomposeConfig{})

// Write (possibly redacted) events to client
writer, _ := sse.NewWriter(w)
for _, ev := range result.Events {
    writer.WriteEvent(ev)
}
```

**Key types:**
- `llm.Result` — has `.Decision` (keeplib.Decision), `.Rule`, `.Message`
- `llm.StreamResult` — has `.Decision`, `.Rule`, `.Message`, `.Events` ([]sse.Event — possibly redacted/modified)
- `sse.Event` — parsed SSE event with `.Type` and `.Data` fields
- `sse.Reader` — reads SSE events from `io.Reader`
- `sse.NewWriter` — writes SSE events to `http.ResponseWriter`
- Keep handles case normalization internally — rule `when` clauses use lowercase literals.

## Context: How Keep Engines Are Already Wired

- `daemon/server.go:142-198` compiles engines from `PolicyYAML`/`PolicyRuleSets` → `RunContext.KeepEngines` keyed by scope.
- `daemon/runcontext.go` copies engines to `RunContextData.KeepEngines` for proxy use.
- HTTP Keep policy uses `rc.KeepEngines["http-{host}"]` or `rc.KeepEngines["http"]`.
- MCP Keep policy uses `rc.KeepEngines[serverName]`.
- LLM gateway policy will use scope `"llm-gateway"`.

---

### Task 1: Bump Keep to v0.2.3 and create LLM policy module

**Files:**
- Modify: `go.mod`
- Create: `internal/proxy/llmpolicy.go`
- Create: `internal/proxy/llmpolicy_test.go`

**Design decisions:**
- **Fail-closed on errors:** If Keep returns an evaluation error, the response is denied. Safe default for security enforcement.
- **Response body size limit:** 10MB max buffer. Responses exceeding this are forwarded without evaluation (with warning log).
- **SSE detection:** Check `Content-Type` header for `text/event-stream` to choose `EvaluateStream` vs `EvaluateResponse`.

- [ ] **Step 1: Bump Keep dependency**

```bash
cd /workspace
go get github.com/majorcontext/keep@v0.2.3
go mod tidy
```

- [ ] **Step 2: Write tests**

```go
// internal/proxy/llmpolicy_test.go
package proxy

import (
	"net/http"
	"testing"

	keeplib "github.com/majorcontext/keep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateLLMResponse_AllowsReadTool(t *testing.T) {
	eng, err := keeplib.LoadFromBytes([]byte(`
scope: llm-gateway
mode: enforce
rules:
  - name: deny-edit
    match:
      operation: "llm.tool_use"
      when: "params.name == 'edit'"
    action: deny
    message: "Editing blocked."
`))
	require.NoError(t, err)
	defer eng.Close()

	// Response with Read tool — should be allowed.
	body := []byte(`{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/foo"}}],"stop_reason":"tool_use"}`)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	denied, rule, msg := evaluateLLMResponse(eng, body, resp)
	assert.False(t, denied)
	assert.Empty(t, rule)
	assert.Empty(t, msg)
}

func TestEvaluateLLMResponse_DeniesEditTool(t *testing.T) {
	eng, err := keeplib.LoadFromBytes([]byte(`
scope: llm-gateway
mode: enforce
rules:
  - name: deny-edit
    match:
      operation: "llm.tool_use"
      when: "params.name == 'edit'"
    action: deny
    message: "Editing blocked."
`))
	require.NoError(t, err)
	defer eng.Close()

	body := []byte(`{"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/foo"}}],"stop_reason":"tool_use"}`)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	denied, rule, msg := evaluateLLMResponse(eng, body, resp)
	assert.True(t, denied)
	assert.Equal(t, "deny-edit", rule)
	assert.Contains(t, msg, "Editing blocked")
}

func TestEvaluateLLMResponse_NoToolsAllowed(t *testing.T) {
	eng, err := keeplib.LoadFromBytes([]byte(`
scope: llm-gateway
mode: enforce
rules:
  - name: deny-edit
    match:
      operation: "llm.tool_use"
      when: "params.name == 'edit'"
    action: deny
    message: "Editing blocked."
`))
	require.NoError(t, err)
	defer eng.Close()

	// Response with no tool_use — should be allowed.
	body := []byte(`{"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn"}`)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	denied, _, _ := evaluateLLMResponse(eng, body, resp)
	assert.False(t, denied)
}

func TestBuildPolicyDeniedResponse(t *testing.T) {
	body := buildPolicyDeniedResponse("deny-edit", "Editing blocked.")
	assert.Contains(t, string(body), "policy_denied")
	assert.Contains(t, string(body), "deny-edit")
	assert.Contains(t, string(body), "Editing blocked")
}

func TestBuildPolicyDeniedResponse_EmptyMessage(t *testing.T) {
	body := buildPolicyDeniedResponse("deny-edit", "")
	assert.Contains(t, string(body), "deny-edit")
	assert.NotContains(t, string(body), ". .")
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/proxy/ -run "TestEvaluateLLMResponse|TestBuildPolicyDenied" -v`
Expected: FAIL — functions undefined

- [ ] **Step 4: Implement `llmpolicy.go`**

```go
// internal/proxy/llmpolicy.go
package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	keeplib "github.com/majorcontext/keep"
	"github.com/majorcontext/keep/llm"
	"github.com/majorcontext/keep/llm/anthropic"
	"github.com/majorcontext/keep/sse"

	"github.com/majorcontext/moat/internal/log"
)

// maxLLMResponseSize is the maximum response body size (10MB) the proxy will
// buffer for LLM policy evaluation. Responses exceeding this are forwarded
// without evaluation (with a warning log).
const maxLLMResponseSize = 10 << 20

// llmCodec is the shared Anthropic codec instance. Codecs are stateless and
// safe for concurrent use.
var llmCodec = anthropic.NewCodec()

// llmPolicyResult holds the outcome of an LLM policy evaluation.
type llmPolicyResult struct {
	Denied  bool
	Rule    string
	Message string
	// For SSE responses: the (possibly redacted) events to forward.
	// nil for JSON responses or when denied.
	Events []sse.Event
}

// evaluateLLMResponse evaluates an Anthropic API response against a Keep
// engine. Handles both JSON and SSE (streaming) responses based on Content-Type.
// Fail-closed: evaluation errors cause denial.
func evaluateLLMResponse(eng *keeplib.Engine, body []byte, resp *http.Response) llmPolicyResult {
	ct := resp.Header.Get("Content-Type")
	isSSE := strings.Contains(ct, "text/event-stream")

	if isSSE {
		return evaluateLLMStream(eng, body)
	}
	return evaluateLLMJSON(eng, body)
}

// evaluateLLMJSON evaluates a non-streaming JSON response.
func evaluateLLMJSON(eng *keeplib.Engine, body []byte) llmPolicyResult {
	r, err := llm.EvaluateResponse(eng, llmCodec, body, "llm-gateway", llm.DecomposeConfig{})
	if err != nil {
		log.Warn("Keep LLM response evaluation error, denying (fail-closed)", "error", err)
		return llmPolicyResult{
			Denied:  true,
			Rule:    "evaluation-error",
			Message: fmt.Sprintf("LLM policy evaluation failed: %v", err),
		}
	}
	if r.Decision == keeplib.Deny {
		return llmPolicyResult{Denied: true, Rule: r.Rule, Message: r.Message}
	}
	return llmPolicyResult{}
}

// evaluateLLMStream parses SSE events from the buffered body, evaluates them,
// and returns the (possibly redacted) event list for forwarding.
func evaluateLLMStream(eng *keeplib.Engine, body []byte) llmPolicyResult {
	// Parse SSE events from buffered body.
	reader := sse.NewReader(bytes.NewReader(body))
	var events []sse.Event
	for {
		ev, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Warn("Keep LLM SSE parse error, denying (fail-closed)", "error", err)
			return llmPolicyResult{
				Denied:  true,
				Rule:    "evaluation-error",
				Message: fmt.Sprintf("Failed to parse SSE stream: %v", err),
			}
		}
		events = append(events, ev)
		if ev.Type == "message_stop" {
			break
		}
	}

	// Evaluate parsed events against policy.
	sr, err := llm.EvaluateStream(eng, llmCodec, events, "llm-gateway", llm.DecomposeConfig{})
	if err != nil {
		log.Warn("Keep LLM stream evaluation error, denying (fail-closed)", "error", err)
		return llmPolicyResult{
			Denied:  true,
			Rule:    "evaluation-error",
			Message: fmt.Sprintf("LLM policy evaluation failed: %v", err),
		}
	}
	if sr.Decision == keeplib.Deny {
		return llmPolicyResult{Denied: true, Rule: sr.Rule, Message: sr.Message}
	}
	// Allowed — return events (possibly redacted) for forwarding.
	return llmPolicyResult{Events: sr.Events}
}

// buildPolicyDeniedResponse returns a JSON error body matching the format
// the Keep LLM gateway uses, so Claude Code handles it consistently.
func buildPolicyDeniedResponse(rule, message string) []byte {
	msg := "Policy denied: " + rule
	if message != "" {
		msg += ". " + message
	}
	resp := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "policy_denied",
			"message": msg,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}
```

**Note:** The exact field names on `llm.Result` and `llm.StreamResult` (`.Decision`, `.Rule`, `.Message`, `.Events`) should be verified against the v0.2.3 source. Adjust if the `llm` package uses different field names.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/proxy/ -run "TestEvaluateLLMResponse|TestBuildPolicyDenied" -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/proxy/llmpolicy.go internal/proxy/llmpolicy_test.go
git commit -m "feat(proxy): add LLM policy evaluation using Keep v0.2.3 llm library"
```

---

### Task 2: Integrate LLM policy into the CONNECT handler

Insert evaluation after `transport.RoundTrip(req)` returns a response from `api.anthropic.com`. Buffer the response body, evaluate with Keep, and replace with a 400 error if denied.

**Files:**
- Modify: `internal/proxy/proxy.go` (~1380-1410)

**Important context:**
- `r` is the CONNECT request (carries RunContext); `req` is the inner HTTP request.
- The response body is a streaming `io.ReadCloser`. We must read it fully to evaluate, then restore it for downstream code (transformers, logging, writing) via `io.NopCloser(bytes.NewReader(...))`.
- Only evaluate 200 OK responses from `api.anthropic.com` — other hosts and error responses pass through unchanged.

- [ ] **Step 1: Add LLM policy evaluation block**

In `proxy.go`, after the `transport.RoundTrip(req)` call and `duration` capture (~line 1382), and BEFORE response transformers (~line 1392), add:

```go
		// Evaluate LLM gateway policy on Anthropic API responses.
		// Only applies to api.anthropic.com when an "llm-gateway" engine exists.
		// Buffers the response body, evaluates tool_use blocks via Keep's llm
		// library (handles both JSON and SSE), and replaces with a 400 error
		// if any tool call is denied. For SSE, the evaluated events (from
		// StreamResult.Events) are re-serialized as the response body.
		if resp != nil && resp.StatusCode == http.StatusOK && host == "api.anthropic.com" {
			if rc := getRunContext(r); rc != nil && rc.KeepEngines != nil {
				if eng, ok := rc.KeepEngines["llm-gateway"]; ok {
					respBodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
					resp.Body.Close()
					if readErr != nil {
						log.Warn("failed to read response body for LLM policy",
							"host", host, "error", readErr)
						resp.Body = io.NopCloser(bytes.NewReader(respBodyBytes))
					} else {
						result := evaluateLLMResponse(eng, respBodyBytes, resp)
						if result.Denied {
							log.Info("LLM tool_use denied by policy",
								"rule", result.Rule, "message", result.Message)
							errorBody := buildPolicyDeniedResponse(result.Rule, result.Message)
							resp = &http.Response{
								StatusCode:    http.StatusBadRequest,
								ProtoMajor:    1,
								ProtoMinor:    1,
								Header:        make(http.Header),
								ContentLength: int64(len(errorBody)),
								Body:          io.NopCloser(bytes.NewReader(errorBody)),
							}
							resp.Header.Set("Content-Type", "application/json")
							resp.Header.Set("X-Moat-Blocked", "llm-policy")
						} else if result.Events != nil {
							// SSE response allowed — re-serialize evaluated events.
							// Events may have been redacted by Keep.
							// sse.NewWriter requires http.ResponseWriter+Flusher, so
							// we write SSE wire format directly: "event: {type}\ndata: {data}\n\n"
							var buf bytes.Buffer
							for _, ev := range result.Events {
								if ev.Type != "" {
									fmt.Fprintf(&buf, "event: %s\n", ev.Type)
								}
								fmt.Fprintf(&buf, "data: %s\n\n", ev.Data)
							}
							resp.Body = io.NopCloser(&buf)
							resp.ContentLength = int64(buf.Len())
						} else {
							// JSON response allowed — restore original body.
							resp.Body = io.NopCloser(bytes.NewReader(respBodyBytes))
							resp.ContentLength = int64(len(respBodyBytes))
						}
					}
				}
			}
		}
```

**Verify:** `bytes`, `fmt`, and `io` are already imported in `proxy.go`. No new imports needed — SSE re-serialization uses `fmt.Fprintf` with the wire format directly (`event: {type}\ndata: {data}\n\n`). The `sse` package import is only in `llmpolicy.go` (for `sse.NewReader` and `sse.Event`).

**Note:** `sse.NewWriter` requires `http.ResponseWriter` with `http.Flusher` — designed for HTTP streaming, not buffer serialization. The SSE wire protocol is simple enough to write directly.

- [ ] **Step 2: Run proxy tests**

Run: `go test ./internal/proxy/ -v -count=1`
Expected: PASS (existing tests unaffected; evaluation only triggers when `"llm-gateway"` engine exists)

- [ ] **Step 3: Run full build**

Run: `go build ./...`
Expected: Clean

- [ ] **Step 4: Commit**

```bash
git add internal/proxy/proxy.go
git commit -m "feat(proxy): integrate LLM policy evaluation into CONNECT handler"
```

---

### Task 3: Route LLM gateway policy through daemon registration

Instead of base64-encoding rules as an env var for the sidecar, send them through `PolicyYAML` (for file/pack policies) or `PolicyRuleSets` (for inline deny lists) — the same path MCP and network policies already use. The daemon compiles them into a Keep engine at scope `"llm-gateway"`.

**Files:**
- Modify: `internal/run/manager.go` (~563, ~706-767, ~892-948, ~1485)

- [ ] **Step 1: Add LLM gateway policy to the existing policy resolution block**

In `manager.go`, the policy resolution block is at lines ~706-767 (where MCP and network policies are resolved). Add LLM gateway policy resolution at the end of this block, before `regReq` is built (~line 770).

Add after the network `keep_policy` resolution (after line ~766):

```go
			// Resolve LLM gateway policy if configured.
			if opts.Config.Claude.LLMGateway != nil && opts.Config.Claude.LLMGateway.Policy != nil {
				gwPolicy := opts.Config.Claude.LLMGateway.Policy
				if gwPolicy.IsInline() {
					mode := gwPolicy.Mode
					if mode == "" {
						mode = "enforce"
					}
					policyRuleSets = append(policyRuleSets, daemon.PolicyRuleSetSpec{
						Scope: "llm-gateway",
						Mode:  mode,
						Deny:  gwPolicy.Deny,
					})
				} else {
					if policyYAML == nil {
						policyYAML = make(map[string][]byte)
					}
					yamlBytes, err := internalkeep.ResolvePolicyYAML(gwPolicy, "llm-gateway", opts.Workspace)
					if err != nil {
						return nil, fmt.Errorf("llm-gateway policy: %w", err)
					}
					if err := keeplib.ValidateRuleBytes(yamlBytes); err != nil {
						return nil, fmt.Errorf("llm-gateway policy validation: %w", err)
					}
					policyYAML["llm-gateway"] = yamlBytes
				}
			}
```

**Note:** `internalkeep.ResolvePolicyYAML` and `keeplib.ValidateRuleBytes` are already imported in manager.go (used by MCP/network policy resolution).

- [ ] **Step 2: Remove the old sidecar setup block**

Delete the entire LLM gateway sidecar block at lines ~892-948 (the block starting with `if opts.Config != nil && opts.Config.Claude.LLMGateway != nil` that sets `KEEP_LLM_GATEWAY_PORT`, `ANTHROPIC_BASE_URL`, `KEEP_LLM_GATEWAY_RULES_B64`).

- [ ] **Step 3: Remove `llmGatewayVersion` variable and its usage**

- Delete `var llmGatewayVersion string` at line ~563.
- Delete `LLMGatewayVersion: llmGatewayVersion,` at line ~1485 in the `ImageSpec` construction.

- [ ] **Step 4: Remove `encoding/base64` import if no longer used**

Check if `base64` is used elsewhere in manager.go. If the only usage was the sidecar env var encoding, remove the import.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/run/ -v -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/run/manager.go
git commit -m "refactor(run): route LLM gateway policy through daemon PolicyYAML registration"
```

---

### Task 4: Remove sidecar infrastructure

Remove all code that existed solely to support the in-container gateway binary.

**Files:**
- Modify: `internal/deps/dockerfile.go` (~209, ~465-480)
- Modify: `internal/deps/imagespec.go` (~44-47, ~66-73)
- Modify: `internal/deps/builder.go` (~65-67)
- Modify: `internal/deps/scripts/moat-init.sh` (~365-409)
- Modify: `internal/config/config.go` (~236-240)

- [ ] **Step 1: Remove `writeLLMGateway` from dockerfile.go**

Delete the call at line ~209:
```go
writeLLMGateway(&b, opts.LLMGatewayVersion)
```

Delete the function at lines ~465-480:
```go
func writeLLMGateway(b *strings.Builder, version string) { ... }
```

- [ ] **Step 2: Remove `LLMGatewayVersion` from imagespec.go**

Delete the `LLMGatewayVersion` field from the `ImageSpec` struct (lines ~44-47).

Remove `s.LLMGatewayVersion != ""` from the `NeedsCustomImage` check at line ~73. The line becomes:
```go
return hasDeps || s.NeedsSSH || len(s.InitProviders) > 0 ||
    s.NeedsFirewall || s.NeedsInitFiles || s.NeedsClipboard ||
    len(s.ClaudePlugins) > 0 || hasHooks
```

- [ ] **Step 3: Remove LLMGateway hash from builder.go**

Delete lines ~65-67:
```go
if opts.LLMGatewayVersion != "" {
    hashInput += ",llm-gateway:" + opts.LLMGatewayVersion
}
```

- [ ] **Step 4: Remove Keep LLM Gateway block from moat-init.sh**

Delete lines ~365-409 (the entire `# ── Keep LLM Gateway ──` section through the closing `fi`).

- [ ] **Step 5: Simplify `LLMGatewayConfig`**

In `internal/config/config.go`, remove `Version` and `Port` fields:

```go
// LLMGatewayConfig configures Keep LLM policy evaluation in the proxy.
// When configured, the proxy evaluates tool_use blocks in Anthropic API
// responses against Keep rules before forwarding to the container.
type LLMGatewayConfig struct {
    Policy *keep.PolicyConfig `yaml:"policy,omitempty"`
}
```

Update the doc comment on the `LLMGateway` field in `ClaudeConfig` (~line 266):
```go
// LLMGateway configures Keep LLM policy evaluation in the Moat proxy.
// Mutually exclusive with BaseURL.
LLMGateway *LLMGatewayConfig `yaml:"llm-gateway,omitempty"`
```

- [ ] **Step 6: Run all affected tests and build**

```bash
go test ./internal/deps/ -v -count=1
go test ./internal/config/ -v -count=1
go build ./...
```
Expected: PASS, builds clean

- [ ] **Step 7: Commit**

```bash
git add internal/deps/dockerfile.go internal/deps/imagespec.go internal/deps/builder.go internal/deps/scripts/moat-init.sh internal/config/config.go
git commit -m "refactor(deps): remove LLM gateway sidecar infrastructure"
```

---

### Task 5: Update tests, example, and lint

**Files:**
- Modify: `internal/config/keep_test.go`
- Modify: `examples/policy/moat.yaml`
- Verify: `examples/policy/.keep/read-only.yaml` (lowercase literals)

- [ ] **Step 1: Update config tests**

In `internal/config/keep_test.go`, verify `TestClaudeLLMGateway` (line ~76) still passes — it only uses `policy:` which is unchanged. Verify `TestClaudeLLMGateway_ConflictsWithBaseURL` (line ~90) still passes.

If any test references `Version` or `Port`, remove those fields from the test YAML.

- [ ] **Step 2: Update example moat.yaml**

```yaml
# Example: Keep LLM gateway policy
#
# Evaluates Keep policy rules on Claude's API responses. The Moat proxy
# inspects tool_use blocks returned by the Anthropic API and denies any
# that match the policy rules.
#
# Run with:
#   moat run examples/policy
#
# What happens:
#
#   Claude tries to edit moat.yaml but the proxy evaluates the tool_use
#   against the Keep policy and returns a 400 policy_denied error:
#
#     Policy denied: deny-edit. File editing is blocked by policy.
#
#   Claude reports that it cannot make the change.
#
# Override the prompt to try a read (allowed):
#   moat run examples/policy -- claude -p "read moat.yaml and summarize it"
#
# Prerequisites:
#   moat grant claude

name: policy-demo

dependencies:
  - node@20
  - claude-code

grants:
  - claude

# Ask Claude to edit moat.yaml — blocked by the read-only policy.
command: ["claude", "-p", "add a yaml comment to the top of moat.yaml that says 'edited by claude'"]

claude:
  llm-gateway:
    policy: .keep/read-only.yaml
```

- [ ] **Step 3: Verify example rules file uses lowercase literals**

Confirm `examples/policy/.keep/read-only.yaml` has lowercase string literals in `when` clauses (`'edit'`, `'write'`, `'bash'`, `'notebookedit'`).

- [ ] **Step 4: Run full test suite and lint**

```bash
make test-unit
make lint
```
Expected: PASS, clean

- [ ] **Step 5: Commit**

```bash
git add internal/config/keep_test.go examples/policy/moat.yaml examples/policy/.keep/read-only.yaml
git commit -m "docs(policy): update example and tests for proxy-based LLM policy evaluation"
```

---

## Future Work (not in this plan)

- **Request-side evaluation:** Call `llm.EvaluateRequest` before forwarding to Anthropic, enabling rules that target `llm.request` operations (model restrictions, system prompt inspection).
- **Streaming pass-through:** Currently the proxy buffers the full SSE response before forwarding (including for allowed streams). A future optimization could stream-through text events in real-time and only buffer/evaluate tool_use blocks, preserving the "tokens appearing one by one" experience while still enforcing policy on tool calls.
- **Audit log integration:** Policy decisions should flow into Moat's audit log (`audit.Store`) for compliance. Currently logged via `log.Info`/`log.Warn` to debug logs.
- **Network request logging:** Tag policy-denied responses in `network.jsonl` with policy metadata so `moat logs` shows which requests were blocked by LLM policy.
- **`base_url` coexistence:** LLM policy evaluation is currently host-gated to `api.anthropic.com` and mutually exclusive with `base_url`. If `base_url` support is added in the future, the host check must be updated to match the configured upstream host.
