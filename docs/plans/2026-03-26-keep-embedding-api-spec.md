# Keep Embedding API Spec

**Date:** 2026-03-26
**Status:** Shipped (v0.2.1)
**Consumer:** Moat (github.com/majorcontext/moat)

## Context

Moat embeds Keep's policy engine directly in its TLS-intercepting proxy to evaluate MCP tool calls and REST API requests inline. This document records the API surface shipped by Keep and the integration contract.

## Package

```
require github.com/majorcontext/keep v0.2.1
```

Import the root package: `github.com/majorcontext/keep`. No `pkg/engine` — Keep uses the idiomatic Go pattern of one public surface at the root. Transitive deps: CEL (`cel-go`), YAML (`yaml.v3`), gitleaks patterns. Does **not** pull in relay, gateway, CLI, or HTTP server code.

## Core API

### Engine Construction

```go
// LoadFromBytes compiles policy rules from raw YAML bytes.
// No filesystem access. All validation happens here.
// Pack references (packs:) are rejected — all rules must be inline.
func LoadFromBytes(data []byte, opts ...Option) (*Engine, error)
```

### Evaluation

```go
// Evaluate checks a call against compiled rules for a given scope.
// Goroutine-safe. No file I/O, no network, no panics for well-formed input.
// Exception: rateCount() increments mutex-protected in-memory counters.
func (e *Engine) Evaluate(call Call, scope string) (EvalResult, error)
```

### Cleanup

```go
// Close releases engine resources. Call when the run is unregistered.
func (e *Engine) Close()
```

### Introspection

```go
// Scopes returns the list of scopes defined in the loaded rules.
func (e *Engine) Scopes() []string
```

## Types

### Input

```go
type Call struct {
    Operation string         // tool name or "METHOD /path"
    Params    map[string]any // tool params or HTTP context
    Context   CallContext
}

type CallContext struct {
    AgentID   string
    UserID    string
    Timestamp time.Time
    Scope     string
    Direction string            // "request" or "response"
    Labels    map[string]string
}
```

### Output

```go
type Decision string

const (
    Allow  Decision = "allow"
    Deny   Decision = "deny"
    Redact Decision = "redact"
)

type EvalResult struct {
    Decision  Decision
    Rule      string      // matched rule name
    Message   string      // human-readable reason
    Mutations []Mutation  // field mutations (when Decision == Redact)
    Audit     AuditEntry  // structured audit data
}

type Mutation struct {
    Path     string // dot-path to field
    Original string // not serialized to JSON (security)
    Replaced string
}
```

### Mutations helper

```go
// ApplyMutations applies redaction mutations to a params map.
func ApplyMutations(params map[string]any, mutations []Mutation) map[string]any
```

## Options

```go
// WithMode overrides mode for all scopes. "enforce" (default) or "audit_only".
func WithMode(mode string) Option

// WithAuditHook registers a synchronous callback on every Evaluate.
// Not called when Evaluate returns an error.
func WithAuditHook(hook func(AuditEntry)) Option
```

## Moat's Responsibilities

Keep is protocol-agnostic — it sees `Call{Operation, Params}`, not HTTP or MCP. Moat owns:

### 1. Protocol normalization

**MCP tool calls:**
```go
call := keep.Call{
    Operation: toolName,
    Params:    toolParams,
    Context:   keep.CallContext{Scope: mcpServerName, Timestamp: time.Now()},
}
result, err := eng.Evaluate(call, mcpServerName)
```

**HTTP requests:**
```go
call := keep.Call{
    Operation: req.Method + " " + req.URL.Path,
    Params: map[string]any{
        "method": req.Method,
        "host":   req.Host,
        "path":   req.URL.Path,
    },
    Context: keep.CallContext{Scope: "http-" + req.Host, Timestamp: time.Now()},
}
result, err := eng.Evaluate(call, "http-"+req.Host)
```

### 2. Inline rule translation

Keep does not support the `{allow: [...], deny: [...]}` shorthand. Moat translates before calling `LoadFromBytes`:

```yaml
# moat.yaml inline:
policy:
  allow: [get_issue, list_issues]
  deny: [delete_issue]

# Moat generates:
scope: mcp-tools
mode: enforce
rules:
  - name: allow-get_issue
    match:
      operation: "get_issue"
    action: allow
  - name: allow-list_issues
    match:
      operation: "list_issues"
    action: allow
  - name: deny-delete_issue
    match:
      operation: "delete_issue"
    action: deny
    message: "Operation blocked by policy."
  - name: default-deny
    match:
      operation: "*"
    action: deny
    message: "Operation not in allowlist."
```

### 3. Starter packs

Keep does not embed starter packs. Moat embeds its own curated YAML files via Go `embed` and passes them to `LoadFromBytes`.

### 4. Audit routing

Moat registers a `WithAuditHook` callback that routes `AuditEntry` events to:
- Moat's audit log (`audit.Store.Append()`) via a callback pattern
- Moat's debug log (`log.Info` / `log.Warn`) directly

## Concurrency and Safety Guarantees

- `Engine.Evaluate` is goroutine-safe
- No file I/O or network during evaluation
- No global state mutation (exception: rate limit counters, mutex-protected)
- No panics for well-formed `Call` input
- `*Engine` is immutable after construction — no `Reload` for bytes-based engines
- Construct a new engine to change rules

## Rule Format

Keep uses its standard rule YAML (not the Moat inline shorthand):

```yaml
scope: mcp-tools
mode: enforce
rules:
  - name: no-delete
    match:
      operation: "delete_*"
    action: deny
    message: "Destructive operations are blocked."

  - name: no-deletes-on-linear
    match:
      operation: "DELETE *"
      when: "params.host == 'api.linear.app'"
    action: deny
    message: "DELETE requests to Linear are blocked."
```

The `when` field supports CEL expressions evaluated against `params`.
