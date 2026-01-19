# Network Firewall Design

**Date:** 2026-01-15
**Status:** Approved

## Overview

Add outgoing firewall capability to Moat containers. When activated, only explicitly allowed HTTP/HTTPS destinations are permitted.

## Goals

- Prevent data exfiltration to unauthorized services
- Restrict agents to known APIs (grant-derived + explicit)
- Provide defense in depth for untrusted code execution
- Maintain ease of use with opt-in strict mode

## Configuration

The `agent.yaml` gains a `network` section:

```yaml
name: my-agent
grants:
  - github

network:
  policy: strict    # "permissive" (default) or "strict"
  allow:
    - "api.openai.com"
    - "*.amazonaws.com"
    - "internal.company.com:8080"
```

### Policy Modes

| Policy | Behavior |
|--------|----------|
| `permissive` (default) | All outbound HTTP/HTTPS allowed. No filtering. Current behavior. |
| `strict` | Only grant-derived hosts + explicit `allow` list permitted. All other requests blocked with 407. |

### Allow List Format

- `api.example.com` - allows ports 80 and 443
- `api.example.com:8080` - allows only that port
- `*.example.com` - wildcard suffix matching

### Grant-Derived Hosts

Grants automatically allow their associated hosts:

| Grant | Allowed Hosts |
|-------|---------------|
| `github` | `github.com`, `api.github.com`, `*.githubusercontent.com`, `*.github.com` |

Future grants will define their own host mappings.

## Enforcement Architecture

Enforcement happens at the existing credential-injecting proxy:

```
Container makes HTTP/HTTPS request
    ↓
HTTP_PROXY / HTTPS_PROXY routes to Moat proxy
    ↓
Proxy checks: is network.policy == "strict"?
    ↓ yes
Check host against allowed list (grants + explicit)
    ↓
Match? → Forward request
No match? → Return 407, log blocked request
```

### Blocked Response

```http
HTTP/1.1 407 Proxy Authentication Required
X-Moat-Blocked: network-policy
Proxy-Authenticate: Moat-Policy
Content-Type: text/plain

Moat: request blocked by network policy.
Host "api.example.com" is not in the allow list.
Add it to network.allow in agent.yaml or use policy: permissive.
```

The `407` status code:
- Is proxy-specific (won't be confused with application-level rejections)
- Won't trigger automatic retries in most HTTP clients
- The `X-Moat-Blocked: network-policy` header enables programmatic detection

### Host Matching Logic

1. **Exact match**: `api.github.com` matches only `api.github.com`
2. **Wildcard prefix**: `*.github.com` matches `api.github.com`, `foo.bar.github.com`
3. **Port-specific**: `api.example.com:8080` only matches that port; without port, allows 80 and 443
4. **Grant-derived hosts** merged into allow list automatically

Matching order:
1. Check explicit allow list
2. Check grant-derived hosts
3. No match → block with 407

### Request Logging

All requests logged regardless of policy:

```json
{"ts": "...", "method": "GET", "host": "api.github.com", "path": "/user", "status": 200, "allowed": true}
{"ts": "...", "method": "POST", "host": "evil.com", "path": "/exfil", "status": 407, "allowed": false, "reason": "network-policy"}
```

## Implementation

### Files to Modify

1. **`internal/config/config.go`** - Add network config parsing
   ```go
   type Config struct {
       // ... existing fields
       Network NetworkConfig
   }

   type NetworkConfig struct {
       Policy string   // "permissive" or "strict", default "permissive"
       Allow  []string // allowed host patterns
   }
   ```

2. **`internal/proxy/proxy.go`** - Add filtering logic
   - New fields: `policy string`, `allowedHosts []hostPattern`
   - New method: `SetNetworkPolicy(policy string, allows []string, grants []string)`
   - Modify request handler: check policy before forwarding
   - Return 407 with `X-Moat-Blocked` header on deny

3. **`internal/proxy/hosts.go`** (new file) - Host matching logic
   - `hostPattern` struct (pattern string, port int, isWildcard bool)
   - `parseHostPattern(s string) hostPattern`
   - `matchHost(patterns []hostPattern, host string, port int) bool`
   - `grantHosts` map with built-in grant→hosts mappings

4. **`internal/run/manager.go`** - Wire up network config
   - Pass `config.Network` to proxy setup
   - Merge grant-derived hosts with explicit allows

5. **`README.md`** - Document the feature and limitations

### No Changes Needed

- Container runtime code (Docker/Apple)
- Routing proxy (inbound traffic)
- Storage/audit logging (existing request logger handles it)

## Limitations

**Documented limitations (HTTP/HTTPS only):**

- Direct TCP/UDP connections are not filtered (databases, SSH, gRPC, WebSockets)
- Tools that ignore `HTTP_PROXY` environment variable can bypass the firewall
- DNS queries themselves are not filtered

These limitations should be documented in the README. Future work may add iptables-based enforcement for full network control.

## Examples

### Basic strict mode with GitHub grant

```yaml
name: code-reviewer
grants:
  - github

network:
  policy: strict
```

Result: Only `github.com`, `api.github.com`, `*.githubusercontent.com`, `*.github.com` allowed.

### Strict mode with additional APIs

```yaml
name: ai-assistant
grants:
  - github

network:
  policy: strict
  allow:
    - "api.openai.com"
    - "api.anthropic.com"
    - "*.sentry.io"
```

Result: GitHub hosts + OpenAI + Anthropic + Sentry allowed.

### Permissive mode (default)

```yaml
name: dev-sandbox

network:
  policy: permissive
```

Result: All outbound HTTP/HTTPS allowed. Same as omitting `network` entirely.
