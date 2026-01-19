# Hostname-Based Service Routing

## Overview

Expose container services via hostname-based routing so multiple agent instances can run simultaneously with predictable, OAuth-friendly URLs.

## URL Structure

```
http://<service>.<agent-name>.localhost:<proxy-port>
```

Examples with proxy on port 8080:
- `http://web.myapp.localhost:8080` → container port 3000
- `http://api.myapp.localhost:8080` → container port 8080
- `http://myapp.localhost:8080` → default service (first in config)

## Agent Naming

**Precedence:**
1. `--name myapp` CLI flag (highest priority)
2. `name:` field in agent.yaml
3. Random two-word phrase: `adjective-animal` (e.g., `fluffy-chicken`)

**Collision Handling:**
If an agent with that name is already running, exit with error:
```
Error: agent "myapp" is already running (run-a1b2c3d4)
Use --name to specify a different name, or stop the existing agent first.
```

## Configuration

**agent.yaml:**
```yaml
name: myapp
ports:
  web: 3000
  api: 8080
```

**Global config (~/.moat/config.yaml):**
```yaml
proxy:
  port: 8080
```

## Reverse Proxy Architecture

### Single Shared Proxy
- One reverse proxy handles routing for all running agents
- Binds to `127.0.0.1:<port>` (default 8080, configurable)
- Routes based on `Host` header parsing

### Lifecycle
- **Start:** Automatically launched when the first agent with exposed ports starts
- **Stop:** Automatically shuts down when the last agent with exposed ports is destroyed
- **Coordination:** Lock file at `~/.moat/proxy/proxy.lock` with PID

### Host Header Routing
```
Host: web.myapp.localhost:8080
        │    │
        │    └── agent name → lookup agent's port mappings
        └─────── service name → lookup container host port
```

### Proxy State Storage (~/.moat/proxy/)
- `proxy.lock` - PID, port, started_at
- `routes.json` - Current routing table:
  ```json
  {
    "myapp": {
      "web": "127.0.0.1:49152",
      "api": "127.0.0.1:49153"
    }
  }
  ```

### Registration Flow
1. Agent starts → container created with port bindings (container:3000 → host:random)
2. Agent process writes entry to `routes.json` (file-locked)
3. Agent process signals proxy to reload routes (or proxy watches file)
4. On agent destroy → remove entry from `routes.json`

## Host Injection into Container

Inject bound hostnames as environment variables:

```bash
MOAT_HOST=myapp.localhost:8080
MOAT_HOST_WEB=web.myapp.localhost:8080
MOAT_HOST_API=api.myapp.localhost:8080
MOAT_URL=http://myapp.localhost:8080
MOAT_URL_WEB=http://web.myapp.localhost:8080
MOAT_URL_API=http://api.myapp.localhost:8080
```

- `MOAT_HOST_*` for host:port (e.g., CORS allowed origins)
- `MOAT_URL_*` for full URLs (e.g., OAuth redirect URIs)
- `MOAT_HOST` / `MOAT_URL` as the default service entry point

## Port Binding Implementation

### Docker
Add `PortBindings` to `HostConfig`:
```go
PortBindings: nat.PortMap{
    "3000/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}},
    "8080/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}},
}
```
After container starts, inspect to get assigned host ports.

### Apple Containers
Use `--publish` flag:
```bash
container run --publish 127.0.0.1::3000 --publish 127.0.0.1::8080 ...
```
If not supported, fall back to connecting directly to container IP.

### Interface Addition
```go
// GetPortBindings returns the host ports mapped to container ports.
GetPortBindings(ctx context.Context, containerID string) (map[int]int, error)
```

## CLI Changes

**New Flag:**
```
agent run --name <name> [existing flags...]
```

**Startup Output:**
```
$ agent run --name myapp ./project

Starting agent "myapp"...
Container: run-a1b2c3d4e5f6
Runtime: docker

Services:
  web: http://web.myapp.localhost:8080 → :3000
  api: http://api.myapp.localhost:8080 → :8080

Proxy listening on :8080
```

**Updated `moat list`:**
```
NAME            RUN ID           STATE     SERVICES
myapp           run-a1b2c3d4     running   web, api
fluffy-chicken  run-e5f6a1b2     running   web
```

## Random Name Generation

Format: `adjective-animal`

Small curated word lists (~50 each). If generated name collides with running agent, regenerate (up to 3 attempts, then append random suffix).

## Proxy Configuration

**Precedence:**
1. `MOAT_PROXY_PORT` env var
2. `~/.moat/config.yaml` proxy.port
3. Default: 8080

**Startup Behavior:**
1. Determine desired port from config precedence
2. Check if proxy already running (via lock file + PID check)
3. If running on same port → use it
4. If running on different port → error:
   ```
   Error: proxy port mismatch
   Proxy is already running on port 8080, but MOAT_PROXY_PORT is set to 9000.
   Either unset MOAT_PROXY_PORT, or stop all agents to restart the proxy on the new port.
   ```
5. If not running → start proxy on desired port

## Error Handling

**Container Port Not Listening:**
```
HTTP 502 Bad Gateway
{"error": "service unavailable", "service": "web", "agent": "myapp"}
```

**Unknown Agent/Service:**
```
HTTP 404 Not Found
{"error": "unknown agent", "agent": "foobar"}
```

**Proxy Crashes:**
Next `moat run` detects stale lock, cleans up, starts fresh proxy.

**Apple Container `--publish` Not Supported:**
Fall back to direct container IP access with warning.

## File & Package Structure

**New Packages:**
```
internal/
  name/           # Random name generation
    name.go

  routing/        # Reverse proxy and route management
    proxy.go      # HTTP reverse proxy server
    routes.go     # Route table (load/save/update)
    lock.go       # Proxy lock file management
```

**Modified Files:**
```
internal/
  config/
    config.go     # Add Name field
    global.go     # New: ~/.moat/config.yaml parsing

  container/
    runtime.go    # Add GetPortBindings() to interface
    docker.go     # Implement port binding
    apple.go      # Implement port binding (or fallback)

  run/
    run.go        # Add Name, Ports fields
    manager.go    # Proxy startup, route registration, env injection

  storage/
    storage.go    # Add Name, Ports to Metadata

cmd/agent/cli/
  run.go          # Add --name flag
  list.go         # Show name and services
```

**Storage Layout:**
```
~/.moat/
  config.yaml
  proxy/
    proxy.lock
    routes.json
  runs/
    run-*/
      metadata.json  # includes name, ports
```
