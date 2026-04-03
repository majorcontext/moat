# Proxy Dependency Inversion Design

**Date:** 2026-04-03
**Goal:** Remove all moat-internal imports from `internal/proxy/` so gatekeeper can be extracted to a separate repo. Moat depends on gatekeeper, not vice versa.

## Current State

`internal/proxy/proxy.go` imports five moat-internal packages:

| Package | Usage | Files |
|---------|-------|-------|
| `internal/log` | 16 call sites — structured logging | proxy.go, relay.go, aws.go, mcp.go, llmpolicy.go |
| `internal/credential` | `ResponseTransformer` type, `Store` interface | proxy.go, mcp.go |
| `internal/config` | `MCPServerConfig` struct | proxy.go, mcp.go |
| `internal/netrules` | `HostRules` struct, `Check()` function | proxy.go |
| `internal/keep` | `NormalizeMCPCall`, `NormalizeHTTPCall`, `SafeEvaluate` | proxy.go, mcp.go |

Additionally:
- `internal/proxy/aws.go` imports `internal/ui` (one warning message)
- `keeplib "github.com/majorcontext/keep"` is an external dependency (acceptable for extraction)

Files with **zero** moat-internal imports (ready to extract as-is): `ca.go`, `server.go`, `hosts.go`.

## Target State

`internal/proxy/` imports only stdlib and `github.com/majorcontext/keep`. All moat-specific types are replaced with interfaces, callbacks, or local type definitions. Moat provides adapter code to satisfy them.

## Approach: Interface Injection

Define replacement types *in the proxy package* (or in `majorcontext/keep` for policy evaluation). Moat adapts its types to satisfy them. No proxy logic changes — just type swaps at the boundary.

## Phase 1: Upstream Keep Helpers

**PR to `github.com/majorcontext/keep`.**

Move internal helpers upstream so both moat and gatekeeper can use them without importing moat internals:

1. `SafeEvaluate(engine, call, scope)` → `keep.SafeEvaluate()`
2. `NormalizeMCPCall(name, args, scope)` → `keep.NormalizeMCPCall()`
3. `NormalizeHTTPCall(method, host, path)` → `keep.NormalizeHTTPCall()`

After this, `internal/keep` becomes either a re-export or gets deleted. The proxy replaces `internal/keep` import with `majorcontext/keep`.

## Phase 2: Remove `internal/log` → `log/slog`

Mechanical replacement. The moat `log` package is a thin slog wrapper with identical structured logging semantics:

```
log.Debug(msg, kvs...) → slog.Debug(msg, kvs...)
log.Warn(msg, kvs...)  → slog.Warn(msg, kvs...)
log.Error(msg, kvs...) → slog.Error(msg, kvs...)
```

Covers all files in `internal/proxy/`. Also replaces the `internal/ui` import in `aws.go` (one `ui.Warn` call → `slog.Warn`).

## Phase 3: Remove `internal/credential`

Two types to replace:

### `credential.ResponseTransformer`

Currently defined as:
```go
type ResponseTransformer func(req, resp interface{}) (interface{}, bool)
```

Move to proxy package:
```go
// in proxy.go
type ResponseTransformer func(req, resp interface{}) (interface{}, bool)
```

Moat callers that pass `credential.ResponseTransformer` switch to `proxy.ResponseTransformer`. The signature is identical.

### `credential.Store`

Used only for MCP credential injection — fetching tokens by grant name. Define a minimal interface in proxy:

```go
// in proxy.go
type CredentialStore interface {
    GetRaw(provider string) (token string, err error)
}
```

Moat wraps its `credential.Store` to satisfy this. Gatekeeper doesn't use credential stores (static credentials only).

## Phase 4: Remove `internal/config`

The proxy uses `config.MCPServerConfig` for MCP server definitions. Define a local struct in proxy with only the fields the proxy reads:

```go
// in mcp.go
type MCPServerConfig struct {
    Name string
    URL  string
    Host string // "local" or "remote"
    Auth *MCPAuthConfig
}

type MCPAuthConfig struct {
    Grant       string
    Header      string
    StubPattern string
}
```

Moat maps `config.MCPServerConfig` → `proxy.MCPServerConfig` when registering runs or setting MCP servers.

## Phase 5: Remove `internal/netrules`

The `netrules` import is used for per-host request rules (method + path restrictions). The proxy's basic host allow-list logic (`allowedHosts []hostPattern`) has no netrules dependency.

Replace with a callback:

```go
// in proxy.go
type RequestChecker func(host string, port int, method, path string) bool
```

Both `Proxy` and `RunContextData` get a `RequestChecker` field. The proxy's `checkNetworkPolicyForRequest` calls it for per-host rule evaluation. Moat provides a closure wrapping `netrules.Check()`.

When no `RequestChecker` is set (gatekeeper mode), only the built-in host allow-list applies.

## Phase 6: Remove `internal/keep`

After Phase 1 (upstream to `majorcontext/keep`), replace `internal/keep` import with `majorcontext/keep`. The proxy already imports `keeplib "github.com/majorcontext/keep"` — after upstreaming, the helpers come from the same import.

Delete `internal/keep/` if it has no remaining moat-specific logic, or reduce it to moat-specific wrappers that don't appear in proxy.

## Adapter Layer

After all phases, moat needs adapter code to bridge its types to proxy interfaces. This lives in a new `internal/proxyadapter/` package (or inline in `internal/daemon/` and `internal/run/`):

- `credential.Store` → `proxy.CredentialStore`
- `credential.ResponseTransformer` → `proxy.ResponseTransformer` (same signature, just re-typed)
- `config.MCPServerConfig` → `proxy.MCPServerConfig`
- `netrules.Check()` → `proxy.RequestChecker`

## Execution Order

Phases are ordered to minimize broken intermediate states:

1. **Phase 1** (Keep upstream) — PR to external repo, then bump go.mod
2. **Phase 2** (log → slog) — no interface changes, just import swaps
3. **Phase 3** (credential) — define local types, update callers
4. **Phase 4** (config) — define local MCP struct, update callers
5. **Phase 5** (netrules) — define RequestChecker callback, update callers
6. **Phase 6** (keep) — swap import path after Phase 1 lands

Each phase is independently committable and testable. No phase depends on another except Phase 6 depends on Phase 1.

## Verification

After all phases:
- `go list -f '{{.Imports}}' ./internal/proxy/` contains no `moat/internal/` paths
- All existing tests pass (proxy, gatekeeper, daemon, run)
- `make lint` clean
- Gatekeeper E2E test still injects credentials through HTTPS

## Out of Scope

- Moving gatekeeper to a separate repo (future step)
- Adding new gatekeeper features
- Refactoring proxy internals beyond dependency removal
