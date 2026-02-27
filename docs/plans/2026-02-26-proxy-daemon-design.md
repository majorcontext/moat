# Proxy Daemon Design

## Problem

The credential-injecting proxy and routing reverse proxy are both tied to the `moat` CLI process lifecycle. When the CLI exits (non-interactive runs, process detachment, crashes), the proxies die and containers lose credential injection and routing. Related to #159.

## Decision

Merge both proxies into a single long-lived daemon process that outlives the CLI. The CLI becomes a client that registers/unregisters runs via a Unix socket API.

### Alternatives considered

- **Thin wrapper daemon** (per-run proxy servers managed by daemon): Smaller refactor but multiple ports, doesn't unify routing proxy.
- **Self-exec fork model** (one daemon per run): Smallest change but fragile process management, doesn't consolidate.

## Architecture

### Daemon process

New package `internal/daemon/` containing a long-lived process that:

- Listens on Unix socket at `~/.moat/proxy/daemon.sock` for management API
- Runs the credential-injecting proxy on a single fixed port (persisted in lock file)
- Runs the routing reverse proxy on its own port
- Writes lock file at `~/.moat/proxy/daemon.lock` with PID and ports

### Startup

1. `moat run` (or `moat proxy start`) calls `daemon.EnsureRunning()`
2. If lock file exists and daemon is alive: reuse (return socket path)
3. If not: exec `moat _daemon --proxy-port <port> --dir ~/.moat/proxy` as a detached background process
4. Wait for socket file to appear (with timeout)
5. Return socket path for subsequent API calls

`moat _daemon` is a hidden subcommand, not user-facing. It detaches from the terminal, writes the lock file, and starts serving.

### Shutdown

- `moat proxy stop` sends shutdown request over Unix socket
- Daemon drains active connections, unregisters all runs, removes lock and socket files, exits
- Auto-shutdown after 5 minutes idle (no active runs); timer resets on new registration

### Unix socket API

HTTP API over `~/.moat/proxy/daemon.sock`:

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/v1/runs` | Register a run (phase 1: get token and port) |
| `PATCH` | `/v1/runs/{token}` | Update run (phase 2: set container ID) |
| `DELETE` | `/v1/runs/{token}` | Unregister a run |
| `GET` | `/v1/runs` | List active runs |
| `GET` | `/v1/health` | Health check (PID, ports, uptime, run count) |
| `POST` | `/v1/shutdown` | Graceful shutdown |
| `POST` | `/v1/routes/{agent}` | Register routing proxy routes |
| `DELETE` | `/v1/routes/{agent}` | Unregister routing proxy routes |

**Register run request** (`POST /v1/runs`):

```json
{
  "run_id": "run_abc123",
  "credentials": [
    {"host": "api.github.com", "header": "Authorization", "value": "token ghp_..."}
  ],
  "extra_headers": {
    "api.anthropic.com": [{"key": "anthropic-version", "value": "2023-06-01"}]
  },
  "mcp_servers": [...],
  "network_policy": {"policy": "strict", "allow": ["api.github.com"]},
  "aws_config": {...}
}
```

**Register run response**:

```json
{
  "auth_token": "hex-encoded-32-bytes",
  "proxy_port": 9100
}
```

The daemon generates auth tokens (single source of truth). Credentials pass over the Unix socket, which is protected by filesystem permissions.

### Per-run credential scoping

New type `RunContext` holds per-run state:

```go
type RunContext struct {
    RunID              string
    ContainerID        string
    AuthToken          string
    Credentials        map[string]CredentialEntry
    ExtraHeaders       map[string][]HeaderEntry
    MCPServers         []config.MCPServerConfig
    NetworkPolicy      string
    NetworkAllow       []string
    AWSProvider        *AWSCredentialProvider
    TokenRefreshCancel context.CancelFunc
    RegisteredAt       time.Time
}
```

Proxy request flow:

1. Request arrives at proxy
2. Extract auth token from `Proxy-Authorization` header
3. Look up `RunContext` by token in `sync.Map`
4. Apply that run's credentials, headers, network policy, MCP config
5. No valid token: reject with 401

All runtimes now use token-based auth (extending current Apple container model to Docker).

### Token refresh

The daemon runs token refresh goroutines per run (same pattern as current `runTokenRefreshLoop`). When a token refreshes, it updates the `RunContext` in place. The daemon holds a read-only handle to the credential store for refresh operations.

### Request logging

The daemon writes network request logs to the run's storage directory (`~/.moat/runs/<id>/network.jsonl`), using the `RunID` from the resolved `RunContext`. Same `storage.NetworkRequest` format as today.

### Container liveness & cleanup

Background goroutine runs every 30 seconds:

- For each registered `RunContext`, checks if `ContainerID` is still running via Docker/Apple container API
- If container is gone: unregister the run (remove `RunContext`, cancel token refresh, log event)
- Container runtime auto-detected on daemon startup (same as `container.NewRuntime()`)

Two-phase registration handles the chicken-and-egg problem (container needs proxy URL, but daemon needs container ID):

1. `POST /v1/runs` — register credentials, get auth token and proxy port (no container ID yet)
2. `PATCH /v1/runs/{token}` — update with container ID after `runtime.Create()`

### Idle auto-shutdown

- After last run unregisters (explicit or liveness cleanup), start 5-minute timer
- New registration cancels the timer
- On timer expiry: daemon exits cleanly, removes lock file and socket

## CLI changes

### `moat run` (manager.Create)

The proxy setup block (~lines 426-640 of `manager.go`) is replaced with:

```go
daemonClient, err := daemon.EnsureRunning(daemonDir, proxyPort)

creds := loadCredentials(opts.Grants, store)

// Phase 1: register run, get token and port
reg, err := daemonClient.RegisterRun(ctx, daemon.RegisterRequest{
    RunID:         r.ID,
    Credentials:   creds,
    MCPServers:    opts.Config.MCP,
    NetworkPolicy: opts.Config.Network,
})

// Use token and port in container env
proxyURL := fmt.Sprintf("http://moat:%s@%s:%d", reg.AuthToken, hostAddr, reg.ProxyPort)

// ... create container ...

// Phase 2: update with container ID
daemonClient.UpdateRun(ctx, reg.AuthToken, containerID)
```

All ~40 `cleanupProxy(proxyServer)` calls in error paths become a single deferred `daemonClient.UnregisterRun(ctx, authToken)`.

### `moat destroy`

```go
daemonClient.UnregisterRun(ctx, r.ProxyAuthToken)
```

Replaces `r.stopProxyServer(ctx)` calls.

### `moat proxy` commands

- `moat proxy start` — starts daemon (foreground or background)
- `moat proxy stop` — sends shutdown via Unix socket
- `moat proxy status` — queries `/v1/health` and `/v1/runs`, shows PID, ports, active runs, registered routes

### Run struct changes

Remove `ProxyServer *proxy.Server` field. Keep:
- `ProxyAuthToken string` (already exists)
- `ProxyPort int` (already exists)

Remove `proxyStopOnce sync.Once` (no longer needed).

## Routing proxy integration

The existing `routing.Lifecycle` merges into the daemon:

- Route registration/unregistration goes through the daemon API instead of direct `RouteTable` access
- The daemon owns the `routing.ProxyServer` instance
- `routes.json` remains for persistence across daemon restarts
- Lock file coordination simplified (daemon owns both proxies)

## Security

- Unix socket protected by filesystem permissions (only the owning user can connect)
- All runtimes use token-based proxy auth (extending Apple container model to Docker)
- Credentials only transmitted over Unix socket, never over network
- Daemon runs as the same user as the CLI (no privilege escalation)
- Auth tokens are 32 bytes from `crypto/rand`, hex-encoded

## Backward compatibility

None needed. This is an internal architecture change. Users see the same `moat run --grant github` behavior. The only visible difference is `moat proxy status` showing richer information about the daemon.
