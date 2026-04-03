# Host Traffic Blocking Design

## Problem

Containers in permissive network mode can reach any service on the host machine via `host.docker.internal` (Docker) or the gateway IP (Apple containers). This is a security footgun — host services (databases, admin panels, dev servers) often have weak auth because they assume only trusted local callers. An AI agent shouldn't have implicit access to everything on your machine.

Additionally, the host gateway address differs across runtimes (`host.docker.internal` vs `192.168.64.1`), requiring users to know runtime internals to address host services.

## Solution

1. Block host-bound traffic by default, regardless of network policy (permissive or strict).
2. Add `network.host` to `moat.yaml` for per-port opt-in.
3. Expose `MOAT_HOST_GATEWAY` env var in every container to abstract the runtime-specific host address.

## Breaking Change

Permissive mode currently allows host traffic. After this change, any container relying on `host.docker.internal:<port>` will be blocked unless `network.host` includes that port. The blocked response must clearly tell users what to add to `moat.yaml`.

## Design

### moat.yaml Config

```yaml
network:
  policy: permissive          # controls internet access (unchanged)
  host:                       # controls host access (new, default: blocked)
    - 8288                    # allow Inngest dev server
    - 5432                    # allow Postgres
  rules:                      # existing, unchanged
    - api.github.com
```

`network.host` is a list of TCP ports on the host machine that the container may reach. An empty list (or omitted) means all host traffic is blocked. This is orthogonal to `network.policy` — strict vs permissive controls internet access, `host` controls host access.

### MOAT_HOST_GATEWAY env var

Set in every container:

| Runtime | Value |
|---------|-------|
| Docker on macOS/Windows | `host.docker.internal` |
| Docker on Linux | `127.0.0.1` |
| Apple containers | Gateway IP (e.g., `192.168.64.1`) |

This comes from `runtime.GetHostAddress()`, which is already called during run setup. The env var lets agents and user code address host services portably: `http://$MOAT_HOST_GATEWAY:8288`.

### Proxy Enforcement

The host gateway address is passed into `RunContextData` so the proxy can identify host-bound requests. The check happens in `checkNetworkPolicyForRequest`, before the existing permissive/strict logic:

```
checkNetworkPolicyForRequest(r, host, port, method, path):
    if isHostGateway(host):
        if port in rc.AllowedHostPorts:
            return true
        return false          # blocked, even in permissive mode
    ... existing policy logic ...
```

**What counts as "host gateway":** The proxy matches against the exact host gateway string for the run (e.g., `host.docker.internal` or `192.168.64.1`). This is a string comparison, not DNS resolution.

**Docker Linux edge case:** On Linux with host network mode, `GetHostAddress()` returns `127.0.0.1`. The container also reaches the proxy via `127.0.0.1`. To avoid blocking proxy traffic, the host-gateway check must exclude the proxy port. This is safe because the firewall already restricts which ports are reachable on loopback — only the proxy port and DNS are allowed. But within the proxy's policy logic, we need to not accidentally block requests to the proxy's own port. In practice this shouldn't be an issue because proxy-internal routes (`/mcp/*`, `/_aws/*`, `/relay/*`) arrive as direct HTTP requests (not proxied), so they bypass `checkNetworkPolicyForRequest` entirely. Proxied requests to `127.0.0.1:$PROXY_PORT` would be unusual but should be allowed — the proxy port is added to the allowed host ports list implicitly on Linux.

### Blocked Response

When host traffic is blocked, the proxy returns 407 with a clear message:

```
Moat: request blocked — host service access is not allowed by default.
To allow port 8288 on the host, add to moat.yaml:

  network:
    host:
      - 8288
```

The port number is extracted from the request so the message is actionable.

### What's Not Affected

- **MCP relay** (`/mcp/*`) — direct requests to the proxy, not proxied through. Unaffected.
- **AWS credential endpoint** (`/_aws/*`) — direct requests. Unaffected.
- **Anthropic relay** (`/relay/*`) — direct requests. Unaffected.
- **Container-to-container traffic** — uses Docker bridge or Apple container networking, not the host gateway. Unaffected.
- **DinD** — Docker-in-Docker runs inside the container on loopback. Unaffected.
- **Service dependencies** — managed by Moat's sidecar system, use their own networking. Unaffected.

### Data Flow

```
Container sends request to host.docker.internal:8288
    ↓
Firewall allows (traffic goes to proxy port)
    ↓
Proxy receives proxied HTTP/CONNECT request
    ↓
checkNetworkPolicyForRequest(r, "host.docker.internal", 8288, ...)
    ↓
isHostGateway("host.docker.internal") → true
    ↓
8288 in AllowedHostPorts? → check network.host config
    ↓
If yes → proceed with normal proxy forwarding
If no  → 407 with actionable error message
```

## Implementation Scope

### Config Layer (`internal/config/`)
- Add `Host []int` field to `NetworkConfig`
- Parse and validate `network.host` as list of port numbers (1-65535)

### Daemon Layer (`internal/daemon/`)
- Add `HostGateway string` and `AllowedHostPorts []int` to `RegisterRequest` and `RunContext`
- Pass through to `RunContextData`

### Proxy Layer (`internal/proxy/`)
- Add `HostGateway string` and `AllowedHostPorts []int` to `RunContextData`
- Add `isHostGateway(host string) bool` helper that compares against the run's host gateway address
- Modify `checkNetworkPolicyForRequest` to block host-bound traffic unless port is allowed
- Update blocked response message for host-specific blocking
- On Linux, implicitly allow the proxy port in host port list to avoid self-blocking

### Run Layer (`internal/run/`)
- Set `MOAT_HOST_GATEWAY` env var from `runtime.GetHostAddress()`
- Pass host gateway address and allowed ports when registering with daemon

### Documentation
- Update `docs/content/reference/02-moat-yaml.md` with `network.host`
- Add breaking change to `CHANGELOG.md`
- Update CLI reference if needed

## Future Work (Not In Scope)

- **Named services** with `MOAT_URL_*` env vars — sugar on top of `network.host`
- **Credential injection** for host services — extend grants to target host ports
- **Health checks** — verify host services are reachable before starting the agent
