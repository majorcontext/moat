# HTTP-Level Request Rules

**Date:** 2026-03-10
**Status:** Approved

## Problem

Moat allows agents to act using credentials without worrying about exfiltration. But the agent can still do whatever the credential allows. Users want more fine-grained control over what an agent can do at the HTTP level — blocking DELETE requests, restricting access to specific API endpoints, denying admin paths.

The current network policy operates at host level only (strict = deny all except allowed hosts, permissive = allow all). This adds one notch of granularity below that: method + path rules per host.

## Design

### Config Syntax

The `network.allow` field is replaced by `network.rules`. Using the old `allow` key produces a hard parse error: `"network.allow" is no longer supported, use "network.rules" instead`.

```yaml
network:
  policy: strict  # or permissive
  rules:
    # Host with no sub-rules: allow the host entirely (strict) or no-op (permissive)
    - "registry.npmjs.org"

    # Host with ordered rules: first match wins, unmatched falls through to policy default
    - "api.github.com":
        - "allow GET /repos/*"
        - "allow POST /repos/*/pulls"
        - "deny DELETE /*"

    # Block admin endpoints on an internal service
    - "internal.myco.com":
        - "deny * /admin/*"
        - "allow * /*"
```

#### Rule Format

`<action> <method> <path-pattern>`

- **action:** `allow` or `deny`
- **method:** HTTP method (GET, POST, PUT, DELETE, PATCH, etc.) or `*` for any
- **path pattern:** glob-style with `*` (single segment) and `**` (multiple segments)

### Evaluation

1. Find the matching host entry (using existing host/wildcard matching from `hosts.go`)
2. If host has no sub-rules: host is allowed (strict mode context) or no-op (permissive)
3. If host has sub-rules: evaluate rules in order, first match wins
4. No rule matches: fall through to policy default (strict = deny, permissive = allow)
5. Host not listed at all: policy default applies

### Policy Modes

- **strict** — default deny. Rules whitelist what's allowed.
- **permissive** — default allow. Rules blacklist what's blocked.

```yaml
# strict: default deny, rules open things up
network:
  policy: strict
  rules:
    - "api.github.com":
        - "allow GET /repos/*"

# permissive: default allow, rules lock things down
network:
  policy: permissive
  rules:
    - "api.github.com":
        - "deny DELETE /*"
```

### Enforcement

All enforcement happens in the proxy's existing request handling path — `handleHTTP()` and `handleConnect()` (after TLS interception).

- **HTTP requests:** Method and path are directly available. Check rules after host matching, before forwarding.
- **HTTPS CONNECT tunnels:** The proxy intercepts TLS and reads the inner HTTP request. Method and path are available at that point.

**Blocked response:** HTTP 407 with `X-Moat-Blocked: request-rule` header and a descriptive message:

```
Request blocked by rule: "deny DELETE /*" for api.github.com
To allow this request, update network.rules in moat.yaml
```

**Per-run context:** Rules are scoped per-run through `RunContextData` in daemon mode, same path as current network policy.

### Path Matching

- `*` — matches a single path segment (`/repos/*` matches `/repos/foo` but not `/repos/foo/bar`)
- `**` — matches zero or more segments (`/repos/**` matches `/repos/foo/bar/baz`)

Query strings are stripped before matching. Paths are normalized before matching: double slashes collapsed, dot segments resolved, no trailing slash sensitivity.

## Implementation Scope

### Config layer (`internal/config/`)

- New `rules` field on `NetworkConfig`, replacing `allow`
- Parse rule strings into structs: action, method, path pattern
- Validation at parse time: reject malformed rules, unknown actions, bad patterns
- Hard error if `allow` key is present

### Proxy layer (`internal/proxy/`)

- New `matchRule()` function: takes method + path, evaluates against a host's rule list
- Hook into existing `checkNetworkPolicy()` — after host is matched, before forwarding
- Path matching utility with `*`/`**` glob support
- Blocked response with rule attribution

### Daemon layer (`internal/daemon/`)

- Extend `RunContextData` to carry parsed rules (replacing current allow list)
- API remains backwards-compatible (additive field)

### Not in Scope

- Header matching, query string matching, body inspection
- Rate limiting
- Preset profiles ("github-readonly")
- Rule composition (AND/OR logic)

These are reasonable future extensions but not needed for v1.

## Testing

- Unit tests for rule parsing and validation
- Unit tests for path matching (`*`, `**`, edge cases)
- Unit tests for rule evaluation (first-match-wins, fall-through, both policy modes)
- Integration test through the proxy: request blocked/allowed based on rules
- Config migration: verify `allow` key produces clear error

## Documentation Updates

- `docs/content/reference/02-moat-yaml.md` — new `rules` syntax
- `docs/content/concepts/05-networking.md` — rule evaluation explanation
- `examples/firewall/` — updated example
