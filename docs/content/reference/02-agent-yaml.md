---
title: "agent.yaml reference"
description: "Complete reference for agent.yaml configuration options."
keywords: ["moat", "agent.yaml", "configuration", "reference", "yaml"]
---

# agent.yaml reference

The `agent.yaml` file configures how Moat runs your agent. Place it in your workspace root directory.

## Complete example

```yaml
# Metadata
name: my-agent
agent: my-agent
version: 1.0.0

# Runtime
dependencies:
  - node@20
  - postgres@17
  - redis@7

# Service overrides
services:
  postgres:
    env:
      POSTGRES_DB: myapp

# Credentials
grants:
  - github
  - anthropic
  - ssh:github.com

# Environment
env:
  NODE_ENV: development
  DEBUG: "true"

# External secrets
secrets:
  OPENAI_API_KEY: op://Dev/OpenAI/api-key
  DATABASE_URL: ssm:///production/database/url

# Mounts
mounts:
  - ./data:/data:ro

# Endpoints
ports:
  web: 3000
  api: 8080

# Network policy
network:
  policy: strict
  allow:
    - "api.openai.com"
    - "*.amazonaws.com"

# Execution
command: ["npm", "start"]
interactive: false

# Sandbox (Docker only)
# sandbox: none  # Uncomment to disable gVisor

# Claude Code
claude:
  sync_logs: true
  plugins:
    "plugin-name@marketplace": true
  marketplaces:
    custom:
      source: github
      repo: owner/repo
      ref: main
  mcp:
    my_server:
      command: /path/to/server
      args: ["--flag"]
      env:
        VAR: value
      grant: github
      cwd: /workspace

# Remote MCP servers
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY

# Snapshots
snapshots:
  disabled: false
  triggers:
    disable_pre_run: false
    disable_git_commits: false
    disable_builds: false
    disable_idle: false
    idle_threshold_seconds: 30
  exclude:
    ignore_gitignore: false
    additional:
      - node_modules/
      - .git/
  retention:
    max_count: 10
    delete_initial: false

# Tracing
tracing:
  disable_exec: false
```

---

## Metadata

### name

Human-readable name for the run. Used in `moat list` and hostname routing.

```yaml
name: my-agent
```

- Type: `string`
- Default: Directory name
- CLI override: `--name`

### agent

Agent identifier. Used internally for tracking.

```yaml
agent: my-agent
```

- Type: `string`
- Default: Same as `name`

### version

Version number for the agent configuration.

```yaml
version: 1.0.0
```

- Type: `string`
- Default: None

---

## Runtime

### dependencies

List of runtime dependencies. The first dependency determines the base image.

```yaml
dependencies:
  - node@20
  - python@3.11
```

- Type: `array[string]`
- Default: `[]` (uses `debian:bookworm-slim`)

#### Supported dependencies

| Dependency | Base image |
|------------|------------|
| `node@18` | `node:18-slim` |
| `node@20` | `node:20-slim` |
| `node@22` | `node:22-slim` |
| `python@3.10` | `python:3.10-slim` |
| `python@3.11` | `python:3.11-slim` |
| `python@3.12` | `python:3.12-slim` |
| `go@1.21` | `golang:1.21` |
| `go@1.22` | `golang:1.22` |
| (none) | `debian:bookworm-slim` |

#### Service dependencies

Service dependencies start sidecar containers that run alongside your agent. Moat generates credentials automatically and injects connection details as environment variables.

```yaml
dependencies:
  - node@20
  - postgres@17
  - redis@7
```

| Dependency | Service | Default port |
|------------|---------|-------------|
| `postgres@16` | PostgreSQL 16 | 5432 |
| `postgres@17` | PostgreSQL 17 | 5432 |
| `mysql@8` | MySQL 8 | 3306 |
| `mysql@9` | MySQL 9 | 3306 |
| `redis@7` | Redis 7 | 6379 |

Each service injects `MOAT_*` environment variables into the main container. See [Service environment variables](#service-environment-variables) for the full list.

#### docker

The `docker` dependency provides Docker access inside the container. You must specify a mode explicitly:

| Syntax | Mode | Description |
|--------|------|-------------|
| `docker:host` | Host | Mounts the host Docker socket |
| `docker:dind` | Docker-in-Docker | Runs an isolated Docker daemon |

##### docker:host

```yaml
dependencies:
  - docker:host
```

Host mode mounts `/var/run/docker.sock` from the host:
- Fast startup (no daemon to initialize)
- Shared image cache with host
- Access to the Docker daemon's full API (ps, run, build, exec, etc.)

**Tradeoffs:**
- Containers created inside can access host network and resources
- Agent can see and interact with all host containers
- Images built inside are cached on host (may be desired or not)

Use this mode for speed and convenience when you trust the agent.

##### docker:dind (Docker-in-Docker)

```yaml
dependencies:
  - docker:dind
```

DinD mode runs an isolated Docker daemon inside the container:
- Complete isolation from host Docker
- Agent cannot see or affect host containers
- Clean slate on each run (no shared image cache)

**Tradeoffs:**
- Requires privileged mode (Moat sets this automatically)
- Slower startup (~5-10 seconds to initialize daemon)
- No image cache between runs
- Uses vfs storage driver (slower than overlay2)

Use this mode when you need isolation from the host Docker daemon or don't want agents to access host containers.

##### BuildKit sidecar (automatic with docker:dind)

When using `docker:dind`, Moat automatically deploys a BuildKit sidecar container to provide fast image builds:

- **BuildKit sidecar**: Runs `moby/buildkit:latest` in a separate container
- **Shared network**: Both containers communicate via a Docker network (`moat-<run-id>`)
- **Environment**: `BUILDKIT_HOST=tcp://buildkit:1234` routes builds to the sidecar
- **Full Docker**: Local `dockerd` in main container provides `docker ps`, `docker run`, etc.
- **Performance**: BuildKit layer caching, `RUN --mount=type=cache`, multi-stage build support

This configuration is automatic and requires no additional setup.

**How it works:**

1. BuildKit sidecar starts with `moby/buildkit:latest`
2. `BUILDKIT_HOST=tcp://buildkit:1234` environment variable set in main container
3. Image builds use BuildKit Go client (not Docker SDK)
4. Full BuildKit features available: cache mounts, multi-platform builds, etc.

**Fallback behavior:**

- If `BUILDKIT_HOST` not set: Uses Docker SDK
- If BuildKit unreachable: Clear error message with troubleshooting steps

**Example:**

```yaml
agent: builder
dependencies:
  - docker:dind  # Automatically includes BuildKit sidecar

# Your code can now use:
# - docker build (uses BuildKit for speed)
# - docker ps (uses local dockerd)
# - docker run (uses local dockerd)
```

##### Runtime requirements

Both docker modes require Docker runtime:
- **docker:host** - Apple containers cannot mount the host Docker socket
- **docker:dind** - Apple containers do not support privileged mode (required for dockerd)

```bash
# Force Docker runtime on macOS
moat run --runtime docker ./my-project
```

##### Use cases

- Integration tests with testcontainers
- Building Docker images
- Running disposable services
- CI/CD pipelines

##### BuildKit troubleshooting

If builds fail with BuildKit sidecar:

1. **Image pull failure**: Ensure Docker can access Docker Hub to pull `moby/buildkit:latest`
2. **Network issues**: Check that Docker networks are enabled (not disabled by firewall)
3. **Performance**: BuildKit sidecar typically starts in 2-5 seconds; check Docker daemon logs if slower

**Troubleshooting:**

- **"connection refused"** (with dind mode): BuildKit sidecar not running or network connectivity issue. Check BuildKit container is running and network exists
- **Slow builds**: Verify BuildKit cache is working. Build output should show "CACHED" for unchanged layers
- **"no active sessions"** or **"--mount option requires BuildKit"** (rare, older Docker versions): Install buildx (https://docs.docker.com/buildx/working-with-buildx/) or disable BuildKit: `export MOAT_DISABLE_BUILDKIT=1`

---

## Credentials

### grants

Credentials to inject into the run.

```yaml
grants:
  - github
  - anthropic
  - openai
  - ssh:github.com
```

- Type: `array[string]`
- Default: `[]`
- CLI override: `--grant` (additive)

#### Grant formats

| Format | Description |
|--------|-------------|
| `github` | GitHub API |
| `anthropic` | Anthropic API |
| `openai` | OpenAI API |
| `ssh:HOSTNAME` | SSH access to specific host |

Credentials must be stored first with `moat grant`.

---

## Environment

### env

Environment variables set in the container.

```yaml
env:
  NODE_ENV: development
  DEBUG: "true"
  PORT: "3000"
```

- Type: `map[string]string`
- Default: `{}`
- CLI override: `-e KEY=VALUE` (additive)

Values must be strings. Quote numeric values.

### secrets

Environment variables resolved from external backends.

```yaml
secrets:
  OPENAI_API_KEY: op://Dev/OpenAI/api-key
  DATABASE_URL: ssm:///production/database/url
```

- Type: `map[string]string`
- Default: `{}`

#### Secret URL formats

| Format | Backend | Example |
|--------|---------|---------|
| `op://VAULT/ITEM/FIELD` | 1Password | `op://Dev/OpenAI/api-key` |
| `ssm:///PATH` | AWS SSM (default region) | `ssm:///prod/db/url` |
| `ssm://REGION/PATH` | AWS SSM (specific region) | `ssm://us-west-2/prod/db/url` |

---

## Mounts

### mounts

Additional directories to mount in the container.

```yaml
mounts:
  - ./data:/data:ro
  - /host/path:/container/path:rw
```

- Type: `array[string]`
- Default: `[]`
- CLI override: `--mount` (additive)

#### Mount format

```text
<host-path>:<container-path>:<mode>
```

| Field | Description |
|-------|-------------|
| `host-path` | Path on host (relative or absolute) |
| `container-path` | Path inside container (absolute) |
| `mode` | `ro` (read-only) or `rw` (read-write, default) |

The workspace is always mounted at `/workspace`.

---

## Endpoints

### ports

Endpoint ports to expose via hostname routing.

```yaml
ports:
  web: 3000
  api: 8080
```

- Type: `map[string]int`
- Default: `{}`

Endpoints are accessible at `https://<endpoint>.<name>.localhost:<proxy-port>` when the routing proxy is running.

---

## Network

### network.policy

Network policy mode.

```yaml
network:
  policy: strict
```

- Type: `string`
- Values: `permissive`, `strict`
- Default: `permissive`

| Mode | Behavior |
|------|----------|
| `permissive` | All outbound HTTP/HTTPS allowed |
| `strict` | Only allowed hosts + grant hosts |

### network.allow

Hosts allowed in strict mode.

```yaml
network:
  policy: strict
  allow:
    - "api.openai.com"
    - "*.github.com"
    - "*.*.amazonaws.com"
```

- Type: `array[string]`
- Default: `[]`

Supports wildcard patterns with `*`.

Hosts from granted credentials are automatically allowed.

---

## Execution

### command

Default command to run.

```yaml
command: ["npm", "start"]
```

- Type: `array[string]`
- Default: None (uses image default)
- CLI override: `-- command` (replaces)

For shell commands:

```yaml
command: ["sh", "-c", "npm install && npm start"]
```

### interactive

Enable interactive mode (stdin + TTY).

```yaml
interactive: true
```

- Type: `boolean`
- Default: `false`
- CLI override: `-i`

Required for shells, REPLs, and interactive tools.

---

## Sandbox

### sandbox

Configures container sandboxing mode. Only affects Docker containers (Apple containers use macOS virtualization).

```yaml
sandbox: none
```

- Type: `string`
- Values: `""` (empty), `none`
- Default: `""` (gVisor sandbox enabled)
- CLI override: `--no-sandbox`

| Value | Description |
|-------|-------------|
| (empty/omitted) | gVisor sandbox enabled (default) |
| `none` | Disable gVisor sandbox |

Setting `sandbox: none` is equivalent to running with `--no-sandbox`. Use this when your agent requires syscalls that gVisor doesn't support.

**Note:** Disabling the sandbox reduces isolation. Only use when necessary for compatibility.

---

## Service dependencies

### services

Customize service behavior for dependencies declared in `dependencies:`.

```yaml
dependencies:
  - postgres@17
  - redis@7

services:
  postgres:
    env:
      POSTGRES_PASSWORD: op://vault/postgres/password
      POSTGRES_DB: myapp
    wait: false
```

- Type: `map[string]object`
- Default: `{}`

Each key matches a service name from `dependencies:` (e.g., `postgres`, `mysql`, `redis`).

#### Service fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `env` | `map[string]string` | `{}` | Environment variables for the service container. Supports secret references. |
| `image` | `string` | (auto) | Override default image (Docker runtime only) |
| `wait` | `boolean` | `true` | Block main container start until service is ready |

Setting `wait: false` starts the main container without waiting for the service health check to pass.

### Service environment variables

Moat injects `MOAT_*` environment variables into the main container for each service dependency. Credentials are auto-generated per run.

#### Postgres

| Variable | Description | Example |
|----------|-------------|---------|
| `MOAT_POSTGRES_URL` | Full connection URL | `postgresql://postgres:pass@host:5432/postgres` |
| `MOAT_POSTGRES_HOST` | Hostname | `postgres` |
| `MOAT_POSTGRES_PORT` | Port | `5432` |
| `MOAT_POSTGRES_USER` | Username | `postgres` |
| `MOAT_POSTGRES_PASSWORD` | Auto-generated password | |
| `MOAT_POSTGRES_DB` | Database name | `postgres` |

#### MySQL

| Variable | Description | Example |
|----------|-------------|---------|
| `MOAT_MYSQL_URL` | Full connection URL | `mysql://root:pass@host:3306/moat` |
| `MOAT_MYSQL_HOST` | Hostname | `mysql` |
| `MOAT_MYSQL_PORT` | Port | `3306` |
| `MOAT_MYSQL_USER` | Username | `root` |
| `MOAT_MYSQL_PASSWORD` | Auto-generated password | |
| `MOAT_MYSQL_DB` | Database name | `moat` |

#### Redis

| Variable | Description | Example |
|----------|-------------|---------|
| `MOAT_REDIS_URL` | Full connection URL | `redis://:pass@host:6379` |
| `MOAT_REDIS_HOST` | Hostname | `redis` |
| `MOAT_REDIS_PORT` | Port | `6379` |
| `MOAT_REDIS_PASSWORD` | Auto-generated password | |

---

## Claude Code

### claude.sync_logs

Mount Claude Code's log directory for observability.

```yaml
claude:
  sync_logs: true
```

- Type: `boolean`
- Default: `true` (when `anthropic` grant is used)

### claude.plugins

Enable or disable plugins. Plugins are installed during image build and cached in Docker layers, eliminating startup latency.

```yaml
claude:
  plugins:
    "plugin-name@marketplace": true
    "other-plugin@marketplace": false
```

- Type: `map[string]boolean`
- Default: `{}`

#### Host plugin inheritance

Moat automatically discovers plugins you've installed on your host machine via Claude Code:

1. **Host marketplaces**: Marketplaces registered via `claude plugin marketplace add` are read from `~/.claude/plugins/known_marketplaces.json`
2. **Host plugins**: Plugin settings from `~/.claude/settings.json` are included
3. **Moat defaults**: Settings from `~/.moat/claude/settings.json` (if present)
4. **Project settings**: Settings from your workspace's `.claude/settings.json`
5. **agent.yaml**: Explicit overrides in `claude.plugins` (highest priority)

This means plugins you've enabled on your host are automatically available in Moat containers without additional configuration.

Use `--rebuild` to update plugins after changing configuration or installing new plugins on the host.

### claude.marketplaces

Additional plugin marketplaces.

```yaml
claude:
  marketplaces:
    custom:
      source: github
      repo: owner/repo
      ref: main
```

- Type: `map[string]object`
- Default: `{}`

#### Marketplace fields

| Field | Description |
|-------|-------------|
| `source` | Source type (`github`) |
| `repo` | Repository path (`owner/repo`) |
| `ref` | Git ref (`main`, `v1.0.0`, commit SHA) |

### claude.mcp

Local MCP (Model Context Protocol) servers that run as child processes inside the container.

```yaml
claude:
  mcp:
    my_server:
      command: /path/to/server
      args: ["--flag", "value"]
      env:
        API_KEY: ${secrets.MY_KEY}
      grant: github
      cwd: /workspace
```

- Type: `map[string]object`
- Default: `{}`

#### MCP server fields

| Field | Type | Description |
|-------|------|-------------|
| `command` | `string` | Server executable path |
| `args` | `array[string]` | Command arguments |
| `env` | `map[string]string` | Environment variables |
| `grant` | `string` | Credential to inject |
| `cwd` | `string` | Working directory |

Environment variables support `${secrets.NAME}` interpolation.

**Note:** For remote HTTP-based MCP servers, use the top-level `mcp:` field instead. See [MCP servers](../guides/01-running-claude-code.md#remote-mcp-servers) in the Claude Code guide.

---

## mcp

Configures remote HTTP-based MCP (Model Context Protocol) servers accessed over HTTPS with credential injection.

```yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY
```

- Type: `array[object]`
- Default: `[]`

**Fields:**

- `name` (required): Identifier for the MCP server (must be unique)
- `url` (required): HTTPS endpoint for the MCP server (HTTP not allowed)
- `auth` (optional): Authentication configuration
  - `grant` (required if auth present): Name of grant to use (format: `mcp-<name>`)
  - `header` (required if auth present): HTTP header name for credential injection

**Credential injection:**

Credentials are stored with `moat grant mcp <name>` and injected by the proxy at runtime. The agent never sees real credentials.

**Example with multiple servers:**

```yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY

  - name: public-mcp
    url: https://public.example.com/mcp
    # No auth block = no credential injection
```

**Note:** For local process-based MCP servers running inside the container, use `claude.mcp` instead.

**See also:** [Running Claude Code guide](../guides/01-running-claude-code.md#remote-mcp-servers)

---

## Codex

### codex.sync_logs

Mount Codex's log directory for observability.

```yaml
codex:
  sync_logs: true
```

- Type: `boolean`
- Default: `true` (when `openai` grant is used)

When enabled, Codex session logs are synced to the host at `~/.moat/runs/<run-id>/codex/`.

---

## Snapshots

### snapshots.disabled

Disable snapshots entirely.

```yaml
snapshots:
  disabled: true
```

- Type: `boolean`
- Default: `false`
- CLI override: `--no-snapshots`

### snapshots.triggers

Configure automatic snapshot triggers.

```yaml
snapshots:
  triggers:
    disable_pre_run: false
    disable_git_commits: false
    disable_builds: false
    disable_idle: false
    idle_threshold_seconds: 30
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `disable_pre_run` | `boolean` | `false` | Disable pre-run snapshot |
| `disable_git_commits` | `boolean` | `false` | Disable git commit snapshots |
| `disable_builds` | `boolean` | `false` | Disable build snapshots |
| `disable_idle` | `boolean` | `false` | Disable idle snapshots |
| `idle_threshold_seconds` | `integer` | `30` | Seconds before idle snapshot |

### snapshots.exclude

Files to exclude from snapshots.

```yaml
snapshots:
  exclude:
    ignore_gitignore: false
    additional:
      - node_modules/
      - .git/
      - "*.log"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `ignore_gitignore` | `boolean` | `false` | Respect .gitignore |
| `additional` | `array[string]` | `[]` | Additional patterns |

### snapshots.retention

Snapshot retention policy.

```yaml
snapshots:
  retention:
    max_count: 10
    delete_initial: false
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_count` | `integer` | `10` | Maximum snapshots to keep |
| `delete_initial` | `boolean` | `false` | Allow deleting pre-run snapshot |

---

## Tracing

### tracing.disable_exec

Disable execution tracing.

```yaml
tracing:
  disable_exec: true
```

- Type: `boolean`
- Default: `false`

Network request logging is separate and always enabled.

---

## Precedence

When the same option is specified in multiple places:

1. CLI flags (highest priority)
2. `agent.yaml` values
3. Default values (lowest priority)

For additive options (`--grant`, `-e`, `--mount`), CLI values are merged with `agent.yaml` values.
