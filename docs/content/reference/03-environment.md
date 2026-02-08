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

If set, `moat grant anthropic` uses this key instead of prompting.

### SSH_AUTH_SOCK

Path to SSH agent socket. Required for `moat grant ssh`.

```bash
# Usually set automatically when SSH agent starts
echo $SSH_AUTH_SOCK
```

If not set, start the SSH agent:

```bash
eval "$(ssh-agent -s)"
ssh-add ~/.ssh/id_ed25519
```

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
export MOAT_RUNTIME=apple   # Force Apple containers runtime
```

- Default: Auto-detect (Apple containers on macOS 15+ with Apple Silicon, Docker otherwise)
- If the requested runtime is unavailable, moat returns an error

See [Runtimes](../concepts/07-runtimes.md) for details on runtime selection.

### MOAT_DISABLE_BUILDKIT

Disable BuildKit for image builds, falling back to the legacy Docker builder.

```bash
export MOAT_DISABLE_BUILDKIT=1
```

- Default: BuildKit enabled (faster builds, better caching)
- Use this if BuildKit is unavailable or causes issues in your environment
- **Warning:** Legacy builder is significantly slower and doesn't cache build layers

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
# agent.yaml
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

Variables from `env` in agent.yaml or `-e` CLI flag:

```yaml
# agent.yaml
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

Variables from `secrets` in agent.yaml:

```yaml
# agent.yaml
secrets:
  OPENAI_API_KEY: op://Dev/OpenAI/api-key
```

```bash
# Inside container:
echo $OPENAI_API_KEY
# sk-... (resolved value)
```

---

## Variable precedence

When the same variable is defined in multiple places:

1. CLI `-e` flag (highest priority)
2. `secrets` in agent.yaml
3. `env` in agent.yaml
4. Moat-injected variables (HTTP_PROXY, etc.)
5. Base image defaults (lowest priority)

---

## Security notes

### Visible to all processes

Environment variables are visible to all processes in the container. Any process can read them via:

- `env` command
- `/proc/*/environ`
- Language-specific environment APIs

### Not suitable for sensitive credentials

For sensitive credentials like OAuth tokens, use grants instead of environment variables. Grants inject credentials at the network layer where they're not visible in the environment.

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

---

## Troubleshooting

### Variable not set in container

1. Check agent.yaml syntax:
   ```yaml
   env:
     MY_VAR: "value"  # Correct
     MY_VAR: value    # May cause issues with special characters
   ```

2. Check for typos in variable name

3. Verify with verbose mode:
   ```bash
   moat run -v -- env | grep MY_VAR
   ```

### Secret resolution failed

Check the error message for the specific backend:

```bash
# 1Password
op read "op://Dev/OpenAI/api-key"

# AWS SSM
aws ssm get-parameter --name /path/to/secret
```

### Proxy variables interfering

Some tools ignore proxy settings. Check tool-specific proxy configuration:

```bash
# curl respects HTTP_PROXY by default
curl https://example.com

# git may need explicit config
git config --global http.proxy $HTTP_PROXY
```

### Variable expansion

Variables in agent.yaml are not expanded on the host. They're set literally:

```yaml
env:
  # Sets PATH to literal string "$PATH:/custom"
  # Does NOT expand $PATH
  PATH: "$PATH:/custom"
```

For variable expansion, use shell commands:

```yaml
command: ["sh", "-c", "export PATH=$PATH:/custom && my-command"]
```
