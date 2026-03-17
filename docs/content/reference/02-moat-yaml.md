---
title: "moat.yaml reference"
navTitle: "moat.yaml"
description: "Complete reference for moat.yaml configuration options."
keywords: ["moat", "moat.yaml", "configuration", "reference", "yaml"]
---

# moat.yaml reference

The `moat.yaml` file configures how Moat runs your agent. Place it in your workspace root directory.

> **Backwards compatibility:** `agent.yaml` is still supported as a fallback. If `moat.yaml` is not found, Moat looks for `agent.yaml` in the same directory. New projects should use `moat.yaml`.

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

# Persistent volumes
volumes:
  - name: state
    target: /home/moatuser/.myapp

# Endpoints
ports:
  web: 3000
  api: 8080

# Network policy
network:
  policy: strict
  rules:
    - "api.openai.com"
    - "*.amazonaws.com"

# Execution
command: ["npm", "start"]
interactive: false

# Hooks
hooks:
  post_build_root: apt-get update -qq && apt-get install -y -qq figlet
  post_build: git config --global core.autocrlf input
  pre_run: npm install

# Sandbox (Docker only)
# sandbox: none  # Uncomment to disable gVisor

# Runtime (optional - auto-detects if not specified)
# runtime: docker  # Force Docker runtime (useful for docker:dind on macOS)

# Container resources (applies to both Docker and Apple)
container:
  memory: 16384                   # 16 GB (default: 8192 for AI agents on Apple, 4096 otherwise)
  cpus: 8                         # CPU count (default: 4 for Apple, no limit for Docker)
  dns: ["8.8.8.8", "8.8.4.4"]    # DNS servers (default: Google DNS)

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

# Gemini CLI
gemini:
  sync_logs: true
  mcp:
    my_server:
      command: /path/to/server
      args: ["--flag"]
      env:
        VAR: value
      grant: github
      cwd: /workspace

# Language servers
language_servers:
  - go

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

When using `moat wt` or `--worktree`, the `name` field is used to generate the run name as `{name}-{branch}`. If `name` is not set, the run is named after the branch.

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

## Container runtime

### runtime

Force a specific container runtime (Docker or Apple containers).

```yaml
runtime: docker  # Force Docker runtime
```

- Type: `string`
- Values: `docker` | `apple`
- Default: Auto-detected (Apple containers on macOS 26+ with Apple Silicon, Docker otherwise)
- CLI override: `--runtime`

Force Docker when dependencies require privileged mode (e.g., `docker:dind`).

---

## Runtime dependencies

### dependencies

List of runtime dependencies. The first dependency determines the base image.

```yaml
dependencies:
  - node@20
  - python@3.11
```

- Type: `array[string]`
- Default: `[]` (uses `debian:bookworm-slim`)

When `git` is listed as a dependency, the host's git identity (`user.name` and `user.email`) is automatically imported into the container. This can be overridden with a [`post_build` hook](/moat/guides/hooks).

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
| `ollama@0.18.1` | Ollama | 11434 |

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

Host mode mounts `/var/run/docker.sock` from the host. Fast startup, shared image cache, full Docker API access. The agent can see and interact with all host containers.

##### docker:dind (Docker-in-Docker)

```yaml
dependencies:
  - docker:dind
```

DinD mode runs an isolated Docker daemon inside the container. Complete isolation from host Docker, clean slate on each run. Requires privileged mode (set automatically), ~5-10 second startup, vfs storage driver.

##### BuildKit sidecar (automatic with docker:dind)

When using `docker:dind`, Moat automatically deploys a BuildKit sidecar container to provide fast image builds:

- **BuildKit sidecar**: Runs `moby/buildkit:latest` in a separate container
- **Shared network**: Both containers communicate via a Docker network (`moat-<run-id>`)
- **Environment**: `BUILDKIT_HOST=tcp://buildkit:1234` routes builds to the sidecar
- **Full Docker**: Local `dockerd` in main container provides `docker ps`, `docker run`, etc.
- **Performance**: BuildKit layer caching, `RUN --mount=type=cache`, multi-stage build support

This configuration is automatic and requires no additional setup. The main container receives `BUILDKIT_HOST=tcp://buildkit:1234`; when unset or unreachable, builds fall back to the Docker SDK.

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
| `gemini` | Google Gemini API |
| `npm` | npm registries |
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
  CUSTOM_API_KEY: env://CUSTOM_API_KEY
```

- Type: `map[string]string`
- Default: `{}`

#### Secret URL formats

| Format | Backend | Example |
|--------|---------|---------|
| `op://VAULT/ITEM/FIELD` | 1Password | `op://Dev/OpenAI/api-key` |
| `ssm:///PATH` | AWS SSM (default region) | `ssm:///prod/db/url` |
| `ssm://REGION/PATH` | AWS SSM (specific region) | `ssm://us-west-2/prod/db/url` |
| `env://VAR_NAME` | Host environment | `env://MY_API_KEY` |

---

## Mounts

### mounts

Additional directories to mount in the container. Accepts a mixed array of strings and objects.

```yaml
mounts:
  - ./data:/data:ro
  - source: .
    target: /workspace
    exclude:
      - node_modules
      - .venv
```

- Type: `array[string | object]`
- Default: `[]`
- CLI override: `--mount` (additive, string form only)

#### String format

```text
<host-path>:<container-path>:<mode>
```

| Field | Description |
|-------|-------------|
| `host-path` | Path on host (relative or absolute) |
| `container-path` | Path inside container (absolute) |
| `mode` | `ro` (read-only) or `rw` (read-write, default) |

#### Object format

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `source` | `string` | yes | | Host path (relative or absolute) |
| `target` | `string` | yes | | Container path (absolute) |
| `mode` | `string` | no | `rw` | `ro` or `rw` |
| `exclude` | `[]string` | no | `[]` | Paths relative to `target` to overlay with tmpfs |

Excluded paths are overlaid with tmpfs (in-memory) mounts inside the container. The host files at those paths are hidden. This prevents VirtioFS file descriptor accumulation from large dependency trees on Apple Containers. See [Excluding directories](./05-mounts.md#excluding-directories) for details.

The workspace is always mounted at `/workspace` unless an explicit mount targets `/workspace`, in which case it replaces the automatic mount.

---

## Volumes

### volumes

Named volumes that persist data across runs for the same agent name.

```yaml
name: my-agent

volumes:
  - name: state
    target: /home/moatuser/.myapp
  - name: cache
    target: /var/cache/myapp
    readonly: true
```

- Type: `array[object]`
- Default: `[]`
- Requires: `name` field must be set (volumes are scoped by agent name)

Unlike `mounts:` (bind mounts with a host-side source path), volumes are managed by moat. They have no host-side source — moat handles the storage.

#### Volume fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | yes | Volume name, scoped to agent. Must match `[a-z0-9][a-z0-9_-]*`. |
| `target` | `string` | yes | Absolute path inside the container. |
| `readonly` | `bool` | no | Mount as read-only. Default: `false`. |

#### Storage

Volumes are stored on the host at `~/.moat/volumes/<agent-name>/<volume-name>/` and bind-mounted into the container. This works identically across Docker and Apple container runtimes.

#### Volume lifecycle

| Event | Behavior |
|-------|----------|
| First run | Volume created automatically |
| Stop/Destroy | Volume persists |
| Next run (same agent name) | Volume reattached |
| `moat volumes rm <agent>` | Volume deleted |
| `moat clean` | Volumes **not** deleted |

#### Managing volumes

```bash
moat volumes ls                  # List managed volumes
moat volumes rm <agent-name>     # Remove volumes for an agent
moat volumes prune               # Remove all managed volumes
```

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

### network.rules

Per-host access rules. Each entry is either a plain hostname string or a map of hostname to a list of method+path rules.

```yaml
network:
  policy: strict
  rules:
    - "api.openai.com"
    - "*.github.com"
    - "*.*.amazonaws.com"
```

- Type: `array[string | map[string]array[string]]`
- Default: `[]`

Hostname patterns support `*` (matches any single segment).

Hosts from granted credentials are automatically allowed regardless of this list.

#### Per-host request rules

Each host entry can include a list of method+path rules that filter requests to that host:

```yaml
network:
  policy: strict
  rules:
    - "api.github.com":
        - "allow GET /repos/**"
        - "deny * /**"
    - "api.openai.com"
```

Rule format: `"<allow|deny> <method> <path-pattern>"`

- `method`: HTTP method (`GET`, `POST`, `PUT`, `DELETE`, `PATCH`, etc.) or `*` for any method
- `path-pattern`: URL path pattern where `*` matches a single path segment and `**` matches zero or more segments

Rules are evaluated in order — the first matching rule wins. If no rule matches, the request falls through to the policy default (`permissive` allows it, `strict` blocks it).

#### Examples

Read-only access to a REST API:

```yaml
network:
  policy: strict
  rules:
    - "api.example.com":
        - "allow GET /**"
        - "deny * /**"
```

Block administrative endpoints while allowing everything else:

```yaml
network:
  policy: permissive
  rules:
    - "api.example.com":
        - "deny * /admin/**"
        - "deny DELETE /**"
```

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

Enable interactive mode.

```yaml
interactive: true
```

- Type: `boolean`
- Default: `false`
- CLI override: `-i`

When `true`, allocates a TTY and connects stdin. The session owns the terminal. Press `Ctrl-/ k` to stop the run; `Ctrl+C` is forwarded to the container process.

When `false` (default), output streams to the terminal. Press `Ctrl+C` to stop. Use `moat logs <id>` to review output after the run.

Required for shells, REPLs, and interactive tools.

### clipboard

Enable host clipboard bridging.

```yaml
# Disable clipboard bridging
clipboard: false
```

- Type: `bool`
- Default: `true`
- CLI override: `--no-clipboard`

When enabled, moat intercepts `Ctrl+V` during interactive sessions, reads the host clipboard, and makes the data available inside the container via a headless X server. This allows coding agents to paste images and text from the host clipboard.

Requires `xvfb` and `xclip` in the container image (added automatically to moat-built images).

---

## Hooks

Lifecycle hooks that run at different stages of the container lifecycle.

### hooks.post_build_root

Command to run as `root` during image build, after dependencies are installed. Baked into image layers and cached.

```yaml
hooks:
  post_build_root: apt-get update -qq && apt-get install -y -qq figlet
```

- Type: `string`
- Default: None

Use for system-level setup: installing system packages, kernel tuning, modifying `/etc` files.

### hooks.post_build

Command to run as the container user (`moatuser`) during image build, after dependencies are installed. Baked into image layers and cached.

```yaml
hooks:
  post_build: git config --global core.autocrlf input
```

- Type: `string`
- Default: None

Use for user-level image setup: configuring tools, setting defaults.

Build hooks run during image build, **before** your workspace is mounted. They can only use commands available in the image — not files from your project directory. For multi-step setup, chain commands with `&&`:

```yaml
hooks:
  post_build: git config --global core.autocrlf input && git config --global pull.rebase true
```

### hooks.pre_run

Command to run as the container user (`moatuser`) in `/workspace` on every container start, before the main command.

```yaml
hooks:
  pre_run: npm install
```

- Type: `string`
- Default: None

Use for workspace-level setup that needs your project files: installing dependencies, running codegen, building assets. This runs on every start, but workspace-aware package managers like `npm install` and `pip install` are fast no-ops when dependencies are current.

`pre_run` runs before any command, including when `moat claude` or `moat codex` overrides `command`.

### Execution order

Build hooks (`post_build_root`, `post_build`) run during image build and are cached as Docker layers -- they cannot access workspace files. `pre_run` runs at container start after the workspace is mounted and is not cached.

Order: `dependencies` installed -> `post_build_root` (root) -> `post_build` (moatuser) -> container start -> `pre_run` (moatuser) -> `command`. Use `--rebuild` to force re-running build hooks.

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

## Container

Container resource limits and settings that apply to both Docker and Apple container runtimes.

### container.memory

Memory limit in megabytes.

```yaml
container:
  memory: 8192  # 8 GB
```

- Type: `integer`
- Default: `8192` MB (8 GB) for `moat claude`, `moat codex`, and `moat gemini` on Apple containers; `4096` MB (4 GB) for other Apple container workloads; no limit for Docker

Apple containers have a system default of 1024 MB which is insufficient for AI coding agents. Moat defaults to 8 GB for agent runs on Apple containers. Docker containers have no default memory limit regardless of the agent. Setting `container.memory` explicitly always takes precedence.

### container.cpus

Number of CPUs available to the container.

```yaml
container:
  cpus: 8
```

- Type: `integer`
- Default: System default (Apple: typically 4, Docker: no limit)

### container.dns

DNS servers for both runtime containers and builders.

```yaml
container:
  dns: ["192.168.1.1", "1.1.1.1"]
```

- Type: `array[string]`
- Default: `["8.8.8.8", "8.8.4.4"]` (Google DNS)

Applies to both Docker and Apple containers. Used for both build-time dependency installation and runtime name resolution.

### container.ulimits

Resource limits (ulimits) for the container process. Applies to both Docker and Apple containers.

```yaml
container:
  ulimits:
    nofile:
      soft: 1024
      hard: 65536
    nproc:
      soft: 4096
      hard: 4096
    memlock:
      soft: -1
      hard: -1
```

- Type: `map[string, {soft: integer, hard: integer}]`
- Default: Runtime defaults (inherited from host/daemon)

Each key is a ulimit name. Values must include both `soft` and `hard` limits. Use `-1` for unlimited. The soft limit must not exceed the hard limit.

Supported ulimit names: `core`, `cpu`, `data`, `fsize`, `locks`, `memlock`, `msgqueue`, `nice`, `nofile`, `nproc`, `rss`, `rtprio`, `rttime`, `sigpending`, `stack`.

Apple containers require CLI version 0.9.0 or later for ulimit support.

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
| `memory` | `integer` | (runtime default) | Memory limit for the service container in MB. Useful for memory-intensive services like Ollama. |
| `wait` | `boolean` | `true` | Block main container start until service is ready |

Setting `wait: false` starts the main container without waiting for the service health check to pass.

`memory` sets the limit for the service sidecar container, independent of `container.memory` (which limits the main agent container).

### Service-specific lists

Some services accept additional list configuration beyond `env` and `wait`. These keys are defined by the service's registry entry:

| Service | Key | Purpose |
|---------|-----|---------|
| `ollama` | `models` | Models to pull during startup |

Example:

```yaml
services:
  ollama:
    memory: 4096  # 4 GB — size to match your largest model
    models:
      - qwen2.5-coder:7b
      - nomic-embed-text
```

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

#### Ollama

| Variable | Description | Example |
|----------|-------------|---------|
| `MOAT_OLLAMA_HOST` | Service hostname | `ollama` |
| `MOAT_OLLAMA_PORT` | Service port | `11434` |
| `MOAT_OLLAMA_URL` | Base URL for the Ollama API | `http://ollama:11434` |

---

## Claude Code

### claude.base_url

Redirect Claude Code API traffic through a host-side LLM proxy. Sets `ANTHROPIC_BASE_URL` inside the container and registers credential injection for the proxy host.

```yaml
claude:
  base_url: http://localhost:8787
```

- Type: `string` (URL)
- Default: none (Claude Code connects to `api.anthropic.com` directly)
- Scheme must be `http` or `https`

Moat routes traffic through a relay endpoint on the Moat proxy, which forwards requests to the configured URL with credentials injected. This works transparently with `localhost` URLs because the relay runs on the host where `localhost` resolves correctly. Credentials from the `anthropic` or `claude` grant are injected for the base URL host in addition to the standard `api.anthropic.com` injection.

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
5. **moat.yaml**: Explicit overrides in `claude.plugins` (highest priority)

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

Local MCP servers that run as child processes inside the container. Configuration is written to `.claude.json` with `type: stdio`.

```yaml
claude:
  mcp:
    filesystem:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
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
| `command` | `string` | Server executable path (required) |
| `args` | `array[string]` | Command arguments |
| `env` | `map[string]string` | Environment variables (supports `${secrets.NAME}` interpolation) |
| `grant` | `string` | Credential to inject as an environment variable |
| `cwd` | `string` | Working directory for the server process |

When `grant` is specified, the corresponding environment variable is set automatically:

| Grant | Environment variable |
|-------|---------------------|
| `github` | `GITHUB_TOKEN` |
| `anthropic` | `ANTHROPIC_API_KEY` |
| `openai` | `OPENAI_API_KEY` |

**Note:** For remote HTTP-based MCP servers, use the top-level `mcp:` field instead. See [MCP servers guide](../guides/09-mcp.md#remote-mcp-servers).

---

## Language servers

### language_servers

Prepackaged language servers that provide code intelligence inside the container via Claude Code plugins. Each entry installs the server binary and its runtime dependencies during image build, then enables the corresponding Claude Code plugin.

```yaml
language_servers:
  - go
  - typescript
  - python
```

- Type: `array[string]`
- Default: `[]`

Adding a language server automatically:
- Installs the server binary and its runtime dependencies during image build
- Enables the corresponding Claude Code plugin (from the `claude-plugins-official` marketplace)

**Available language servers:**

| Name | Description | Dependencies installed |
|------|-------------|----------------------|
| `go` | Go language server (code intelligence, refactoring, diagnostics) | `go`, `gopls` |
| `typescript` | TypeScript/JavaScript language server (code intelligence, diagnostics) | `node`, `typescript`, `typescript-language-server` |
| `python` | Python language server (code intelligence, type checking, diagnostics) | `python`, `pyright` |

**Example:**

```yaml
agent: claude
language_servers:
  - go
grants:
  - anthropic
```

Runtime dependencies are added automatically -- listing them in `dependencies:` is not required.

> **Note:** Prepackaged language servers are currently supported with Claude Code only.

---

## mcp

Configures MCP (Model Context Protocol) servers accessed through Moat's proxy relay. Supports both remote HTTPS servers and host-local HTTP servers.

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
- `url` (required): Endpoint for the MCP server. HTTPS is required for remote servers. HTTP is allowed for host-local servers (`localhost`, `127.0.0.1`, or `[::1]`)
- `auth` (optional): Authentication configuration
  - `grant` (required if auth present): Name of grant to use (format: `mcp-<name>`)
  - `header` (required if auth present): HTTP header name for credential injection

**Credential injection:**

Credentials are stored with `moat grant mcp <name>` and injected by the proxy at runtime. The agent never sees real credentials.

**Example with remote and host-local servers:**

```yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY

  - name: local-tools
    url: http://localhost:3000/mcp
    # Host-local server: auth optional, proxy bridges container to host

  - name: public-mcp
    url: https://public.example.com/mcp
    # No auth block = no credential injection
```

**Host-local MCP servers:**

MCP servers running on the host machine (e.g., `http://localhost:3000`) are not accessible from inside the container. Moat's proxy relay bridges this gap -- the relay runs on the host and forwards container requests to the host-local server.

**Note:** For local MCP servers running inside the container, use `claude.mcp`, `codex.mcp`, or `gemini.mcp` instead.

**See also:** [MCP servers guide](../guides/09-mcp.md#remote-mcp-servers)

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

## Gemini

### gemini.sync_logs

Mount Gemini's session logs directory for observability.

```yaml
gemini:
  sync_logs: true
```

- Type: `boolean`
- Default: `true` (when `gemini` grant is used)

When enabled, Gemini session logs are synced to the host at `~/.moat/runs/<run-id>/gemini/`.

### gemini.mcp

Local MCP servers that run as child processes inside the container. Configuration is written to `.mcp.json` in the workspace directory.

```yaml
gemini:
  mcp:
    filesystem:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
      env:
        API_KEY: my-key
      grant: github
      cwd: /workspace
```

- Type: `map[string]object`
- Default: `{}`

#### MCP server fields

| Field | Type | Description |
|-------|------|-------------|
| `command` | `string` | Server executable path (required) |
| `args` | `array[string]` | Command arguments |
| `env` | `map[string]string` | Environment variables (supports `${secrets.NAME}` interpolation) |
| `grant` | `string` | Credential to inject as an environment variable |
| `cwd` | `string` | Working directory for the server process |

When `grant` is specified, the corresponding environment variable is set automatically:

| Grant | Environment variable |
|-------|---------------------|
| `github` | `GITHUB_TOKEN` |
| `gemini` | `GEMINI_API_KEY` |
| `anthropic` | `ANTHROPIC_API_KEY` |

**Note:** For remote HTTP-based MCP servers, use the top-level `mcp:` field instead. See [MCP servers guide](../guides/09-mcp.md#remote-mcp-servers).

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
- CLI override: none (config-only)

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
2. `moat.yaml` values
3. Default values (lowest priority)

For additive options (`--grant`, `-e`, `--mount`), CLI values are merged with `moat.yaml` values.
