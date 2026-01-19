# Moat Design Document

**Date:** 2026-01-10
**Status:** Draft

## Overview

Moat is local execution infrastructure for AI agents. The core abstraction is a **run** — a sealed, ephemeral workspace containing code, dependencies, tools, credentials, and observability.

**Core promise:** `moat run claude-code .` just works — zero Docker knowledge, zero secret copying, full visibility.

### Mental Model

- **Runs, not containers** — Users never think about Docker. They think about "I want to run claude-code on this repo with GitHub access."
- **Zero-config start, progressive complexity** — `moat run claude-code .` works immediately. Add `agent.yaml` only when you need customization.
- **Credentials as capabilities** — Agents request scoped capabilities (`github:repo`, `aws:s3.read`). The broker mints short-lived, run-scoped tokens. No secrets copied into containers.
- **Everything is observable** — Every run emits structured traces (OpenTelemetry). Logs, tool invocations, file changes, network calls — all captured, all replayable.

### What a Run Contains

| Layer | Description |
|-------|-------------|
| **Workspace** | Mounted code, read-write or read-only |
| **Runtime** | Node, Python, system packages — resolved from curated base images |
| **Tools** | Agent binaries (claude-code, cursor, etc.) |
| **Credentials** | Injected via proxy, scoped to granted capabilities |
| **Network** | Outbound via auth proxy, inbound via reverse proxy |
| **Traces** | OTLP spans for every significant event |

### Run Lifecycle

```
create → start → (running) → stop → [promote | destroy]
```

Runs are disposable by default. "Promote" preserves artifacts or graduates a workspace to a persistent state.

---

## CLI UX & Commands

### Primary Commands

```bash
# Run an agent (the 90% case)
agent run <agent> [path]
agent run claude-code .
agent run claude-code ./my-repo --grant github,aws:s3.read

# Manage credentials (grant before or during run)
agent grant github                    # GitHub device flow, broad access
agent grant github:repo               # Scoped to repo operations only
agent grant aws --scope=s3.read       # AWS SSO, scoped to S3 read
agent grant aws --role=my_agent_role  # Assume specific IAM role
agent revoke github                   # Remove cached credential

# Observability
agent logs                      # Tail current/latest run
agent logs run-abc123           # Specific run
agent trace                     # Show trace spans (TUI or JSON)
agent trace --step 14           # Jump to specific span
agent replay run-abc123         # Replay with full context

# Run management
agent list                      # Show runs (running, stopped, recent)
agent stop [run-id]             # Stop a run
agent destroy [run-id]          # Remove run and artifacts
agent promote [run-id]          # Graduate workspace/artifacts to persistent storage
```

### Flags That Work Everywhere

```bash
--dry-run          # Show what would happen
--verbose, -v      # More output
--json             # Machine-readable output
--config, -c       # Explicit config file (default: agent.yaml)
```

### Implicit Behavior

- `moat run` with no agent specified looks for `agent.yaml`, falls back to interactive prompt
- Running in a git repo auto-mounts the repo as the workspace
- If credentials are needed but not granted, prompts interactively (no silent failures)

---

## Credential Broker Architecture

The broker is the security core. It mediates all authenticated requests — the container never sees real credentials.

### Components

```
┌─────────────────────────────────────────────────────────────┐
│  Host                                                       │
│  ┌─────────────────┐    ┌─────────────────────────────────┐ │
│  │  Credential     │    │  Auth Proxy                     │ │
│  │  Store          │◄───│  (localhost:9999)               │ │
│  │                 │    │                                 │ │
│  │  - github:token │    │  1. Receives HTTP request       │ │
│  │  - aws:session  │    │  2. Checks run's granted caps   │ │
│  │                 │    │  3. Injects auth headers        │ │
│  └─────────────────┘    │  4. Forwards to real endpoint   │ │
│                         └─────────────────────────────────┘ │
│                                    ▲                        │
│  ┌─────────────────────────────────┼───────────────────────┐│
│  │  Container (run-abc123)         │                       ││
│  │                                 │                       ││
│  │   HTTP_PROXY=localhost:9999     │                       ││
│  │   HTTPS_PROXY=localhost:9999    │                       ││
│  │                                 │                       ││
│  │   Agent → HTTP request ─────────┘                       ││
│  └─────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────┘
```

### Credential Lifecycle

1. **Grant** — User runs `moat grant github`. Moat initiates device flow, receives token, stores encrypted on host.
2. **Bind** — When a run starts with `--grant github:repo`, the broker notes this run can use `github:repo` scope.
3. **Inject** — Container makes request to `api.github.com`. Proxy sees the run-id, checks granted caps, injects `Authorization: Bearer <token>`.
4. **Scope enforcement** — If run has `github:repo` but requests `/gists`, proxy rejects with clear error.
5. **Expiry** — Short-lived tokens are refreshed transparently. When a run ends, its capability bindings are discarded.

### Storage

Credentials stored in `~/.moat/credentials/` — encrypted at rest, keyed by provider. Runs reference capabilities, never raw tokens.

---

## Runtime Resolution & Container Setup

### Resolution Strategy

```
1. Check for agent.yaml → explicit runtime config
2. No manifest? → Use agent-specific defaults
3. Detect project (package.json, pyproject.toml, go.mod)
4. Select curated base image
5. Layer system packages at container start (cached)
```

### Curated Base Images

Moat maintains a small set of base images optimized for common cases:

| Image | Contents |
|-------|----------|
| `moat/node20` | Node 20, npm, common build tools |
| `moat/python311` | Python 3.11, pip, venv |
| `moat/polyglot` | Node 20 + Python 3.11 + Go 1.22 |
| `moat/minimal` | Just the essentials, for custom setups |

### System Packages

When `agent.yaml` specifies system packages, these are installed at first run and cached. Cache key = `hash(base_image + sorted(packages))`.

### Agent Binaries

Agents like `claude-code` are fetched and cached in `~/.moat/agents/`. Version pinned in `agent.yaml` or defaults to latest stable.

---

## Networking

### Outbound: Auth Proxy

All outbound HTTP/HTTPS traffic flows through the auth proxy:

```
Container                    Host                         Internet
┌─────────┐    HTTP_PROXY    ┌─────────────┐              ┌─────────┐
│  Agent  │ ───────────────► │ Auth Proxy  │ ───────────► │ GitHub  │
│         │                  │ :9999       │  + Auth hdr  │ API     │
└─────────┘                  └─────────────┘              └─────────┘
```

**Proxy responsibilities:**
- Inject credentials based on run's granted capabilities
- Enforce scope (block requests outside granted scope)
- Log all requests (metadata only, not bodies) for observability
- Handle HTTPS via CONNECT tunneling

### Inbound: Reverse Proxy

The reverse proxy (`localhost:9999`) routes inbound traffic to container ports:

**URL structure:**
```
http://localhost:9999/run-abc123/web/         → container :3000
http://localhost:9999/run-abc123/api/         → container :8000
http://localhost:9999/callback                → active run's OAuth handler
```

### OAuth Flow

1. User registers `http://localhost:9999/callback` with OAuth provider (once)
2. Run starts, Moat notes it expects OAuth callbacks
3. OAuth redirect hits `/callback`, proxy routes to active run's `/auth/callback`
4. If multiple runs, proxy uses state parameter or prompts user

### Firewall (Optional)

Default: allow all. Explicit allowlist available for security-conscious users.

---

## Observability

### What Gets Captured

| Event Type | Data Captured |
|------------|---------------|
| **stdout/stderr** | Full streams, timestamped |
| **Structured logs** | JSON logs from agent, parsed and indexed |
| **Tool invocations** | Tool name, args, result, duration |
| **File operations** | Path, operation (read/write/delete), diff for writes |
| **Network calls** | Method, URL, status, duration (no bodies) |
| **Credential use** | Which capability, when, for what request |

### Storage Format

OpenTelemetry spans, stored locally in `~/.moat/runs/<run-id>/`.

### CLI Commands

```bash
agent logs                              # Tail logs
agent trace                             # Interactive span explorer (TUI)
agent replay run-abc123                 # Full replay
agent trace --export=jaeger             # Push to Jaeger
```

---

## Configuration

### The `agent.yaml` Manifest (Optional)

```yaml
agent: claude-code
version: 1.0.46

runtime:
  node: 20
  python: 3.11
  system:
    - chromium
    - ffmpeg

grants:
  - github:repo
  - aws:s3.read

ports:
  web: 3000
  api: 8000

oauth:
  redirect: web:/auth/callback

network:
  allow:
    - "*.github.com"
    - "*.npmjs.org"

mounts:
  - ./data:/data:ro

env:
  NODE_ENV: development

ai:
  provider: claude
  plugins:
    - org/private-plugin
```

### Host Directory Structure

```
~/.moat/
  config.yaml              # Global settings
  credentials/             # Encrypted tokens
  agents/                  # Cached agent binaries
  images/                  # Cached base image layers
  runs/                    # Run data (traces, logs, artifacts)
  cache/                   # Package cache
```

---

## Key Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Sandbox backend | Docker-first | Ubiquitous, one backend keeps focus |
| Credential model | Proxy/intercept | Strongest isolation, natural audit point |
| Traffic routing | Env var proxying | Simple, covers 90% of tools |
| Runtime resolution | Curated base images | Predictable, fast, controlled quality |
| Port virtualization | Host reverse proxy | No DNS magic, single OAuth callback |
| Observability | OpenTelemetry | Industry standard, future-proof |
| Config approach | Zero-config default | `agent.yaml` only when needed |

---

## Component Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  CLI (agent)                                                    │
│  - run, grant, logs, trace, replay, list, stop, destroy        │
└─────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────┐
│  Core Engine                                                    │
│  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐ ┌────────────┐│
│  │ Run Manager │ │ Credential  │ │ Image       │ │ Trace      ││
│  │             │ │ Broker      │ │ Resolver    │ │ Collector  ││
│  └─────────────┘ └─────────────┘ └─────────────┘ └────────────┘│
└─────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────┐
│  Proxy Layer                                                    │
│  ┌──────────────────────────┐ ┌────────────────────────────────┐│
│  │ Auth Proxy (outbound)    │ │ Reverse Proxy (inbound)       ││
│  │ - Credential injection   │ │ - Port routing                ││
│  │ - Scope enforcement      │ │ - OAuth callback dispatch     ││
│  │ - Request logging        │ │                               ││
│  └──────────────────────────┘ └────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────┐
│  Docker Backend                                                 │
│  - Container lifecycle                                          │
│  - Volume mounts                                                │
│  - Network configuration                                        │
└─────────────────────────────────────────────────────────────────┘
```

---

## Out of Scope (v1)

- Remote execution / multi-machine
- Kubernetes integration
- Non-Docker backends (MicroVMs, Nix)
- Non-HTTP credential injection (SSH, databases)
- GUI / web dashboard

---

## Implementation Priority

1. **Core loop** — `moat run` with Docker, basic workspace mounting
2. **Credential broker** — GitHub and AWS, proxy injection
3. **Observability** — OTLP capture, `moat logs` and `moat trace`
4. **Polish** — `agent.yaml` parsing, curated images, cleanup
