# Keep × Moat Integration Design

**Date:** 2026-03-26
**Status:** Draft

## Problem

Moat's security controls operate at the network layer: which hosts an agent can reach and which credentials get injected. Once an agent has access to a service, Moat cannot distinguish between read and write operations, cannot inspect parameters, and cannot prevent data exfiltration through tool call payloads.

Keep is an API-level policy engine that fills this gap. It evaluates structured API calls against declarative rules and returns allow, deny, or redact decisions.

## User Stories

1. **Operation-level restrictions** — "My agent can use Linear but only read issues, not delete or reassign them."
2. **Secret and PII prevention** — "My agent can't leak API keys or customer data through tool call parameters."
3. **Rate limiting** — "Cap write operations to prevent a runaway agent from creating 500 issues."
4. **Semantic audit trail** — "Show me 'agent called create_issue with priority P0', not raw HTTP requests."
5. **LLM payload governance** — "Strip internal URLs and customer data from prompts sent to Anthropic."
6. **Starter policies** — "Drop in a `linear-readonly` policy without writing rules from scratch."

## Architecture

Two integration paths matching Keep's two enforcement points:

### Embedded policy engine in proxy

The `github.com/majorcontext/keep` package (root package, v0.2.1+) is imported as a Go library into Moat's TLS-intercepting proxy. The proxy evaluates policy after interception, before forwarding. This extends the existing firewall from host-level allow/deny to operation-level allow/deny/redact.

Applies to: MCP tool calls (via the relay) and REST API requests (via CONNECT interception).

### LLM gateway sidecar

`keep-llm-gateway` runs as a separate process inside the container. Claude Code's `base_url` points at it. The gateway evaluates LLM-specific policy (secret detection, redaction, content filtering) then forwards through Moat's proxy, which injects the Anthropic credential.

Applies to: Claude Code ↔ Anthropic API traffic. CC-only for v1.

### Request flows

```
┌──────────────── Container ──────────────────────┐
│                                                  │
│  Claude Code ──► keep-llm-gateway (localhost)    │
│       │         (LLM policy)                     │
│       │              │                           │
│       ▼              ▼                           │
│   HTTP_PROXY ──────────────────────────────►─┐   │
└──────────────────────────────────────────────┤───┘
                                               │
┌── Moat Proxy (host daemon) ──────────────────┤───┐
│  ┌─────────────┐  ┌──────────────────┐       │   │
│  │Network Policy│  │Keep Policy Engine│       │   │
│  │(host allow/ │  │(operation allow/ │       │   │
│  │ deny)       │  │ deny/redact)     │       │   │
│  └─────────────┘  └──────────────────┘       │   │
│       │                                       │   │
│       ├──► MCP upstreams (filtered)           │   │
│       ├──► REST APIs (filtered)               │   │
│       └──► Anthropic API (cred injected)      │   │
└───────────────────────────────────────────────────┘
```

**MCP:** Agent → proxy relay (`/mcp/{name}`) → Keep engine evaluates → allow/deny/redact → forward with injected credentials.

**LLM:** Agent → localhost gateway → Keep evaluates prompt/response → gateway forwards via proxy → proxy injects Anthropic credential → Anthropic API.

**REST APIs:** Agent → proxy (CONNECT/TLS intercept) → Keep engine evaluates → allow/deny → forward with injected credentials.

## Configuration

### moat.yaml

```yaml
# MCP servers with per-server policy
mcp:
  - name: linear
    url: https://mcp.linear.app/mcp
    auth:
      grant: linear
    policy: linear-readonly              # starter pack

  - name: slack
    url: https://mcp.slack.com/mcp
    auth:
      grant: slack
    policy: .keep/slack.yaml             # file reference

  - name: github-issues
    url: https://mcp.github.com/mcp
    auth:
      grant: github
    policy:                              # inline
      allow: [list_issues, get_issue, create_comment]
      deny: [delete_issue, close_issue]

# Network-level policy for REST API requests
# Note: network.rules is an existing field ([]netrules.NetworkRuleEntry) for
# host-level firewall rules. Keep policy uses a separate field to avoid conflict.
network:
  policy: strict
  rules:
    - host: api.linear.app
      allow: true
    - host: api.slack.com
      allow: true
  keep_policy: .keep/api-policy.yaml    # Keep rules for REST API filtering

# LLM gateway under agent config
claude:
  llm-gateway:
    policy: .keep/llm-rules.yaml
    version: v0.2.0                      # optional, defaults to latest
```

### Policy field resolution

The `policy` field accepts three shapes:

| Shape | Detection | Example |
|-------|-----------|---------|
| Starter pack name | Plain string, no `/` or `.yaml` | `linear-readonly` |
| File path | Contains `/` or ends in `.yaml` | `.keep/linear.yaml` |
| Inline rules | YAML object | `{allow: [...], deny: [...]}` |

### Go type for policy field

The polymorphic `policy` field requires a custom YAML unmarshaler:

```go
// PolicyConfig represents a Keep policy, parsed from one of three shapes:
// a starter pack name (string), a file path (string), or inline rules (object).
type PolicyConfig struct {
    Pack   string   // starter pack name (e.g., "linear-readonly")
    File   string   // file path (e.g., ".keep/linear.yaml")
    Allow  []string // inline allowlist
    Deny   []string // inline denylist
    Mode   string   // "enforce" or "audit" (default: "enforce")
}

func (p *PolicyConfig) UnmarshalYAML(node *yaml.Node) error {
    // String → detect file path vs starter pack name
    // Mapping → parse as inline rules
}
```

Detection: string containing `/` or ending in `.yaml` → file path. String otherwise → starter pack name. YAML mapping → inline rules.

### Inline rules schema

For simple cases:

```yaml
policy:
  allow: [tool_name, ...]        # allowlist (only these pass)
  deny: [tool_name, ...]         # denylist (always blocked)
  mode: enforce                  # enforce | audit (default: enforce)
```

When both `allow` and `deny` are specified, `allow` is evaluated first (request must match), then `deny` is checked (matching requests are blocked). This means `deny` takes precedence over `allow` for overlapping entries.

Moat converts inline rules to Keep's full rule YAML before passing to `keep.LoadFromBytes()`. The translation generates Keep-native rule format:

```yaml
# Moat generates this from: policy: {allow: [get_issue], deny: [delete_issue]}
scope: mcp-tools
mode: enforce
rules:
  - name: allow-get_issue
    match:
      operation: "get_issue"
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

Full Keep syntax (CEL expressions, rate limits, redaction patterns) requires a file.

### Starter pack resolution

Starter packs are curated Keep rule sets (e.g., `linear-readonly`, `github-safe`) **embedded in the Moat binary** as YAML files. Keep does not provide a starter pack registry — this is Moat's responsibility.

Resolution:
1. Moat embeds starter pack YAML files via Go `embed` (e.g., `internal/keep/packs/`)
2. When a policy field contains a pack name, Moat looks it up in the embedded registry
3. If the pack name is unknown, config validation fails (hard error at run start)
4. The embedded YAML is passed to `keep.LoadFromBytes()` like any other rule file
5. Packs are versioned with Moat releases

This keeps the convenience in Moat where it belongs. Users can also author and share `.keep/` rule files for the same effect.

### Policy file validation

Moat validates that referenced policy files exist at config parse time (`moat run` start), not at evaluation time. Missing files produce a clear error before the container is created.

### .keep/ directory convention

```
project/
├── moat.yaml
├── .keep/
│   ├── linear.yaml
│   ├── slack.yaml
│   ├── api-policy.yaml
│   └── llm-rules.yaml
```

## Keep API (v0.2.1)

Reference for the actual Keep types used in the integration:

```go
import "github.com/majorcontext/keep"

// Construction — from raw YAML bytes, no filesystem access.
eng, err := keep.LoadFromBytes(policyYAML,
    keep.WithMode("enforce"),
    keep.WithAuditHook(func(e keep.AuditEntry) { /* route to Moat logs */ }),
)
defer eng.Close()

// Evaluation — goroutine-safe, no I/O, no panics for well-formed input.
result, err := eng.Evaluate(keep.Call{
    Operation: "delete_issue",
    Params:    map[string]any{"id": "123"},
    Context:   keep.CallContext{Timestamp: time.Now(), Scope: "mcp-tools"},
}, "mcp-tools")

// Result handling.
switch result.Decision {
case keep.Deny:   // block, return error to agent
case keep.Redact: // apply mutations, then forward
    params = keep.ApplyMutations(params, result.Mutations)
case keep.Allow:  // forward as-is
}
```

### Moat's normalization responsibilities

Keep is protocol-agnostic — it sees `Call{Operation, Params}`, not HTTP or MCP specifics. Moat normalizes at two points:

**MCP tool calls → Keep Call:**
```go
call := keep.Call{
    Operation: toolName,                // e.g., "delete_issue"
    Params:    toolParams,              // map[string]any from MCP request body
    Context:   keep.CallContext{Scope: mcpServerName},
}
```

**HTTP requests → Keep Call:**
```go
call := keep.Call{
    Operation: req.Method + " " + req.URL.Path,  // e.g., "DELETE /issues/123"
    Params: map[string]any{
        "method": req.Method,
        "host":   req.Host,
        "path":   req.URL.Path,
    },
    Context: keep.CallContext{Scope: "http-" + req.Host},
}
```

## Embedded Policy Engine

### Integration points

1. **MCP relay** (`internal/proxy/mcp.go` → `handleMCPRelay()`) — after receiving the MCP request, before forwarding to upstream. MCP requests have structured tool-call format (tool name + parameters), which maps directly to Keep's `Call{Operation, Params}` model.
2. **CONNECT handler** (`internal/proxy/proxy.go`) — after TLS interception, before forwarding REST API requests. Moat normalizes HTTP requests into Keep calls: `Call{Operation: "DELETE /issues/123", Params: {"method": "DELETE", "host": "api.linear.app", "path": "/issues/123"}}`. Body inspection for REST is deferred to v2 — v1 covers method+path rules.

### Evaluation flow

```
Request arrives at proxy
    │
    ▼
Resolve RunContext (existing)
    │
    ▼
Network policy check (existing: host allow/deny)
    │
    ▼
Keep policy check (NEW)
    ├── Load *keep.Engine from RunContext
    ├── Normalize request → keep.Call{Operation, Params, Context}
    ├── engine.Evaluate(call, scope) → EvalResult
    ├── If Deny → return error, log to audit + debug
    ├── If Redact → keep.ApplyMutations(params, result.Mutations), log, continue
    └── If Allow → continue
    │
    ▼
Credential injection (existing)
    │
    ▼
Forward to upstream
```

### RunContext extension

```go
import "github.com/majorcontext/keep"

type RunContextData struct {
    // ... existing fields
    KeepEngine *keep.Engine  // compiled Keep rules, nil if no policy
}
```

The engine is compiled once at run registration time via `keep.LoadFromBytes()`. The daemon's `RegisterRequest` carries the raw policy YAML; the daemon compiles it into an engine instance when building the `RunContext` (specifically `daemon.RunContext`, which is converted to `proxy.RunContextData` via `ToProxyContextData()`). The engine must be closed via `Engine.Close()` when the run is unregistered.

### Daemon API backwards compatibility

Adding policy fields to `RegisterRequest` is additive and safe per the daemon API contract. However, a newer CLI sending policy config to an older daemon (without Keep support) would silently drop the policy — a security-critical silent failure.

To detect version mismatch:
- Add a `Capabilities []string` field to `HealthResponse` (additive, backwards-compatible)
- The CLI checks for `"keep-policy"` in capabilities before registering a run with policy
- If the daemon lacks the capability, the CLI fails with a clear error: "Policy enforcement requires a newer proxy daemon. Run `moat proxy restart` to upgrade."

### Error handling for policy compilation

If the policy config has syntax errors, references a nonexistent file, or names an unknown starter pack, compilation fails at run registration time. This is a hard error — the run does not start. The `RegisterResponse` gains an optional `Error string` field (additive) to carry the compilation error back to the CLI. Users see a clear message: "Policy compilation failed: .keep/linear.yaml: line 12: unknown field 'denys'."

### Audit and logging

Every evaluation produces a decision event routed to two sinks:

- **Audit log** — the proxy emits policy decision events via a callback (same pattern as `storage.NetworkRequest`). The run manager on the CLI side routes these to `audit.Store.Append()`. Queryable via `moat audit <run-id>`.
- **Debug log** (`log.Info` / `log.Warn`) — same data, written to `~/.moat/debug/` by the proxy/daemon process directly.

Denied requests get an error response with a human-readable reason and a `log.Warn` entry.

### Audit-only mode

When `mode: audit` (mapped to Keep's `"audit_only"` mode via `keep.WithMode("audit_only")`), the engine evaluates but does not block. Decisions are logged as "would have denied" via the audit hook. The request proceeds. This lets users profile agent behavior before enforcing.

## LLM Gateway Sidecar

### Binary distribution

- `moat-init.sh` downloads `keep-llm-gateway` from GitHub releases (goreleaser artifacts) at container startup
- Selects correct binary for container's `GOARCH`/`GOOS`
- Cached in a mounted volume (`~/.moat/cache/keep/`) so repeated runs skip the download
- Version optionally pinned in moat.yaml, defaults to latest

### Startup sequence

1. `moat-init.sh` downloads the gateway binary (if not cached)
2. Starts `keep-llm-gateway` in background, listening on `localhost:PORT`
3. Waits for health check (`/health`) before proceeding
4. Claude Code launches with `base_url` set to `http://localhost:PORT`

### Credential flow

The gateway does not hold the Anthropic API key:

```
Claude Code → localhost gateway (no auth, local)
    → gateway evaluates policy
    → forwards to api.anthropic.com via HTTP_PROXY (Moat's proxy)
    → proxy injects Anthropic credential
    → Anthropic API
```

### Configuration generation

When `claude.llm-gateway` is present, Moat's Claude provider:
- Allocates a fixed well-known port (e.g., 18080) for the gateway — configurable via `claude.llm-gateway.port` if needed
- Sets `base_url` in Claude config to `http://localhost:PORT`
- Writes gateway config (policy rules, listen address) to the container
- Adds gateway startup to `moat-init.sh`

**Conflict with `claude.base_url`:** If both `claude.base_url` and `claude.llm-gateway` are set, config validation fails with an error. They are mutually exclusive — `base_url` routes to an external LLM proxy, while `llm-gateway` routes to a local Keep sidecar.

### Failure handling

- Gateway fails to start → run fails with clear error
- Gateway crashes mid-run → Claude Code gets connection errors, no silent degradation
- No restart logic for v1

## Implementation Scope

### Changes in Moat

| Component | Change |
|-----------|--------|
| `internal/config/` | Parse `policy` field on MCP servers, `network.keep_policy`, `claude.llm-gateway`; add `PolicyConfig` type with custom `UnmarshalYAML`; add `KeepPolicy *PolicyConfig` to `NetworkConfig` |
| `internal/proxy/` | Import Keep engine, add evaluation step in MCP relay and CONNECT handler |
| `internal/daemon/` | Carry policy config in `RegisterRequest`, compile engine in `RunContext`, propagate via `ToProxyContextData()`, add `Capabilities` to `HealthResponse` |
| `internal/audit/` | New event type for policy decisions |
| `internal/run/manager.go` | LLM gateway binary download, startup, health check, port allocation |
| `internal/providers/claude/` | Generate `base_url` when `llm-gateway` is configured |
| `moat-init.sh` template | Gateway download + background launch |

### Changes in Keep (upstream) — SHIPPED in v0.2.1

All required upstream changes have been shipped:

| Feature | API |
|---------|-----|
| Bytes-based construction | `keep.LoadFromBytes(data []byte, opts ...Option) (*Engine, error)` |
| Audit hook | `keep.WithAuditHook(func(AuditEntry)) Option` |
| Mode override | `keep.WithMode("enforce" \| "audit_only") Option` |
| Concurrent evaluation | `Engine.Evaluate(Call, scope) (EvalResult, error)` — goroutine-safe |
| Mutation application | `keep.ApplyMutations(params, mutations) map[string]any` |

**Not shipped (Moat's responsibility):**
- Inline `allow`/`deny` shorthand → Moat translates to Keep rule YAML
- Starter packs → Moat embeds its own YAML files
- HTTP request normalization → Moat converts HTTP to `keep.Call`

### Build order

1. ~~**Keep engine API cleanup**~~ — Done (v0.2.1)
2. **Config parsing** — `policy` field on MCP servers, `network.keep_policy`, `claude.llm-gateway`
3. **Inline rule translation** — Moat converts `{allow, deny, mode}` to Keep rule YAML
4. **Proxy integration** — embed engine, add evaluation to MCP relay and CONNECT handler, HTTP normalization layer
5. **Audit integration** — route Keep's `AuditEntry` events to Moat's audit and debug logs via `WithAuditHook`
6. **LLM gateway sidecar** — binary download, init script, Claude provider config generation
7. **Starter packs** — embed curated YAML files in Moat, resolve pack names at config time
8. **Documentation** — config reference and policy guide

Steps 2–5 deliver core value (MCP + API policy). Steps 6–7 are additive.

### Go module dependency

```
require github.com/majorcontext/keep v0.2.1
```

The root package (`github.com/majorcontext/keep`) is the public API surface. It pulls in CEL (`cel-go`), YAML (`yaml.v3`), and gitleaks patterns for secret detection. It does **not** pull in relay, gateway, CLI, or HTTP server dependencies — those are in `internal/` and `cmd/` packages that are not transitively imported.

### Out of scope for v1

- Real-time UI alerts for policy violations (separate feature)
- Gateway process supervision / restart
- Keep rule hot-reload during a run
- LLM gateway for non-Claude agents (Codex, Gemini)
- REST API request body inspection (v1 covers method + path rules only)
- Rate limiting (Keep supports this, but stateful counters in the proxy need design work)
