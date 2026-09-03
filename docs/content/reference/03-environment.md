---
title: "Environment variables"
navTitle: "Environment"
description: "Environment variables used by Moat and injected into containers."
keywords: ["moat", "environment variables", "configuration", "reference"]
---

# Environment variables

This page documents environment variables used to configure Moat and variables injected into containers.

## Moat configuration

These variables configure Moat itself. Set them in your shell profile or before running Moat commands.

### ANTHROPIC_API_KEY

Anthropic API key. Used by `moat grant anthropic` as an alternative to Claude Code OAuth.

```bash
export ANTHROPIC_API_KEY="sk-ant-api..."
```

When set, `moat grant anthropic` uses this key instead of prompting.

### SSH_AUTH_SOCK

Path to SSH agent socket. Required for `moat grant ssh`.

Set automatically by SSH agent. Start one with `eval "$(ssh-agent -s)"` and `ssh-add` if not running.

### MOAT_PROXY_PORT

Override default routing proxy port.

```bash
export MOAT_PROXY_PORT="9000"
```

- Default: `8080`
- Ports below 1024 require elevated privileges on macOS/Linux (e.g., `sudo moat run` for port 80)

### MOAT_RUNTIME

Force a specific container runtime instead of auto-detection.

```bash
export MOAT_RUNTIME=docker  # Force Docker runtime
export MOAT_RUNTIME=podman  # Force Docker runtime over a Podman socket
export MOAT_RUNTIME=apple   # Force Apple containers runtime
```

- Default: Auto-detect (Apple containers on macOS 26+ with Apple Silicon, Docker otherwise -- including Docker-API-compatible sockets like Podman machine or Rancher Desktop)
- `podman` is not a separate runtime implementation; it points the Docker runtime at a Podman socket
- When the requested runtime is unavailable, Moat returns an error

See [Runtimes](../concepts/07-runtimes.md) for details on runtime selection.

### BUILDKIT_HOST

Enable BuildKit for image builds. When set, Moat generates Dockerfiles with BuildKit-specific features like `--mount=type=cache` for faster apt installs.

```bash
export BUILDKIT_HOST=docker-container://buildkitd
```

- Default: BuildKit disabled (legacy builder compatibility)
- Set this to a BuildKit daemon address to enable BuildKit features
- BuildKit builds are faster and support layer caching

### MOAT_DISABLE_BUILDKIT

Force-disable BuildKit even when `BUILDKIT_HOST` is set.

```bash
export MOAT_DISABLE_BUILDKIT=1
```

- Only meaningful when `BUILDKIT_HOST` is also set
- Use this to temporarily fall back to legacy-compatible Dockerfiles without unsetting `BUILDKIT_HOST`

See [Runtimes](../concepts/07-runtimes.md#buildkit) for BuildKit configuration.

### MOAT_NO_SANDBOX

Disable gVisor sandbox for Docker containers on Linux.

```bash
export MOAT_NO_SANDBOX=1
```

- Default: gVisor sandbox enabled on Linux, disabled on macOS/Windows (not supported)
- gVisor provides additional isolation by intercepting syscalls
- Disable if gVisor is unavailable or incompatible with your workload

See [Sandboxing](../concepts/01-sandboxing.md) for security implications.

### MOAT_PROFILE

Selects the credential profile for all grant and run commands. The `--profile` flag overrides this variable when both are set.

```bash
export MOAT_PROFILE=work
moat grant github        # Stored in work profile
moat run --grant github  # Uses work profile credential
```

- Default: empty (uses default credential store at `~/.moat/credentials/`)
- Profile credentials are stored in `~/.moat/credentials/profiles/<name>/`

See [Credential profiles](./04-grants.md#credential-profiles) for details.

### MOAT_WORKTREE_BASE

Override the default worktree base path (`~/.moat/worktrees/`).

### MOAT_HOME

Override the Moat configuration directory. By default, Moat stores runs, credentials, and daemon state under `~/.moat/`. Set `MOAT_HOME` to an absolute path to relocate everything — the value is used as the complete directory (no `.moat` suffix is appended).

```bash
export MOAT_HOME=/tmp/moat-test
```

Primarily useful for:

- Hermetic test runs that must not share a daemon or credential store with the developer's live install.
- Running two versions of Moat side-by-side, where each version must see only its own state.

`MOAT_HOME` is inherited by spawned daemon processes, so the daemon socket, lock file, and logs all land under the override path. The real `$HOME` is still used for reading third-party state like `~/.claude/` and `~/.config/gh/`.

### MOAT_KEYRING_BACKEND

Control where Moat stores the encryption key that protects the credential store. By default Moat uses the system keychain (macOS Keychain, Windows Credential Manager, or a Linux secret service) and silently falls back to a file at `~/.moat/encryption.key` when no keychain is available.

```bash
export MOAT_KEYRING_BACKEND=file  # Skip the system keychain; use file storage only
```

Set this to `file` on headless or locked-down macOS where touching the keychain pops a blocking GUI authorization prompt. It also disables the macOS Keychain lookup for Claude Code OAuth credentials (falling back to `~/.claude/.credentials.json`). Any other value (or leaving it unset) keeps the default keychain-first behavior. The Moat test suite sets it so tests never touch the real keychain.

Set it consistently across every Moat process for a given `MOAT_HOME`, including the proxy daemon (which inherits the variable from the CLI that spawns it). The credential store is encrypted with a key resolved from the selected backend, so mixing backends across processes — for example, switching this variable on while a daemon started without it is still running — makes them resolve different keys and credentials fail to decrypt. After changing the value on a host with a running daemon, run `moat proxy restart` so the daemon picks up the new backend.

### MOAT_TTY_TRACE

Record the terminal I/O of every interactive run to a file, for debugging TUI and input problems.

```bash
export MOAT_TTY_TRACE=session.tty
moat codex
moat tty-trace analyze session.tty --decode
```

Equivalent to passing `--tty-trace <path>`, which takes precedence when both are set. Use the variable when the sessions worth tracing are the broken ones: if input handling misbehaves, the in-session `ctrl+/ d` dump may itself be unreachable, and exporting a variable captures every subsequent run without editing each command line.

The trace records terminal output, terminal input, and resize events with timestamps. Input is recorded **after** moat's escape-sequence handling, so a `ctrl+/` that moat consumed does not appear — its absence is the signal that moat recognized it, and its presence means moat passed it through untouched.

Each run overwrites the file, so give concurrent runs distinct paths. Traces are JSON regardless of the name; the `.tty` suffix used here is what moat's `.gitignore` excludes, so captures taken inside a repo are not committed by accident.

### AWS credentials

For AWS SSM secrets, standard AWS environment variables are used:

```bash
export AWS_ACCESS_KEY_ID="..."
export AWS_SECRET_ACCESS_KEY="..."
export AWS_REGION="us-east-1"
```

Or configure via `aws configure`.

---

## Container environment

These variables are injected into containers by Moat.

### HTTP_PROXY / HTTPS_PROXY

Proxy URL for credential injection.

```bash
# Inside container:
echo $HTTP_PROXY
# http://127.0.0.1:54321

echo $HTTPS_PROXY
# http://127.0.0.1:54321
```

All HTTP/HTTPS traffic routes through this proxy for credential injection and network policy enforcement.

> **Note:** On Apple containers (macOS 26+), the proxy URL includes a per-run authentication token: `http://moat:<token>@<host>:<port>`. The token is generated automatically and is different for each run. See [Proxy architecture](../concepts/09-proxy.md) for details on the security model.

### NO_PROXY

Hosts that bypass the proxy.

```bash
# Inside container:
echo $NO_PROXY
# localhost,127.0.0.1
```

Local addresses are excluded from proxying.

### MOAT_URL_*

Endpoint URLs for hostname routing. One variable per endpoint defined in `ports`.

```yaml
# moat.yaml
ports:
  web: 3000
  api: 8080
```

```bash
# Inside container:
echo $MOAT_URL_WEB
# http://web.my-agent.localhost:8080

echo $MOAT_URL_API
# http://api.my-agent.localhost:8080
```

Use these for inter-endpoint communication or OAuth callback URLs.

### MOAT_RUN_ID

Unique identifier for the current run.

```bash
# Inside container:
echo $MOAT_RUN_ID
# run_a1b2c3d4e5f6
```

### MOAT_RUN_NAME

Name of the current run.

```bash
# Inside container:
echo $MOAT_RUN_NAME
# my-agent
```

### User-defined environment

Variables from `env` in moat.yaml or `-e` CLI flag:

```yaml
# moat.yaml
env:
  NODE_ENV: development
  DEBUG: "true"
```

```bash
# Inside container:
echo $NODE_ENV
# development

echo $DEBUG
# true
```

### Resolved secrets

Variables from `secrets` in moat.yaml:

```yaml
# moat.yaml
secrets:
  OPENAI_API_KEY: op://Dev/OpenAI/api-key
```

```bash
# Inside container:
echo $OPENAI_API_KEY
# sk-... (resolved value)
```

---

## Profile environment variables

A credential profile can carry environment variables that apply to every run using it. Configure them under `profiles` in `~/.moat/config.yaml`:

```yaml
profiles:
  lunaroute:
    env:
      ANTHROPIC_MODEL: glm-5.3
      ANTHROPIC_DEFAULT_HAIKU_MODEL: glm-5.3-flash
      CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY: "1"
```

```bash
moat run --profile lunaroute -- claude    # in any project
```

A profile is an identity — a set of credentials plus the configuration that goes with them. Settings that belong to the identity rather than to a project live here, so they do not have to be repeated in every `moat.yaml`. The models an LLM gateway serves are the motivating case: they go with the gateway's key, not with the checkout.

- Profile names follow the same rules as `--profile`: start with a letter or digit, then letters, digits, hyphens, and underscores.
- `moat.yaml` `env` and the `-e` flag both override profile env.
- Proxy variables Moat owns (`HTTP_PROXY`, `NO_PROXY`, `ALL_PROXY`, …) are ignored with a warning when the run has a proxy, exactly as they are in `moat.yaml` `env`.
- Runs with no `--profile` (the default credential store) get no profile env — the default store is not a named profile.

## Variable precedence

When the same variable is defined in multiple places:

1. CLI `-e` flag (highest priority)
2. `secrets` in moat.yaml
3. `env` in moat.yaml
4. `env` for the active `--profile` in `~/.moat/config.yaml`
5. Moat-injected variables (HTTP_PROXY, etc.)
6. Base image defaults (lowest priority)

---

## Security notes

### Visible to all processes

Environment variables are visible to all processes in the container. Any process can read them via:

- `env` command
- `/proc/*/environ`
- Language-specific environment APIs

### Do not use for sensitive credentials

For sensitive credentials like OAuth tokens, use [grants](04-grants.md) instead of environment variables. Grants inject credentials at the network layer where they're not visible in the environment. See [Security model](../concepts/08-security.md) for a full discussion of credential safety.

```yaml
# Prefer: Network-layer injection
grants:
  - github

# Avoid for sensitive data: Environment variable
secrets:
  GITHUB_TOKEN: op://Dev/GitHub/token
```

### Audit logging

Secret resolution is logged in the audit trail (which secrets were resolved, not their values). Environment variable usage is not logged.

