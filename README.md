# Moat

> **Early Release:** This project is in active development. APIs and configuration formats may change.

Run agents in containers with credential injection and full observability.

```bash
moat claude ./workspace
```

This starts Claude Code in an isolated container with your workspace mounted. Credentials are injected at runtime—Claude authenticates normally but never sees your tokens. Every API call, log line, and network request is captured.

For design rationale and principles, see [VISION.md](VISION.md).

## Installation

```bash
go install github.com/andybons/moat/cmd/moat@latest
```

Or build from source:

```bash
git clone https://github.com/andybons/moat.git
cd moat
go build -o moat ./cmd/moat
```

**Requirements:** Docker or Apple containers (macOS 15+ with Apple Silicon—auto-detected).

## Quick start

### 1. Grant credentials (one time)

```bash
$ moat grant github

Found gh CLI authentication
Use token from gh CLI? [Y/n]: y
Validating token...
Authenticated as: your-username
GitHub credential saved to ~/.moat/credentials/github.enc
```

### 2. Run a command with injected credentials

```bash
$ moat run --grant github -- curl -s https://api.github.com/user
{
  "login": "your-username",
  "id": 1234567,
  "name": "Your Name"
}
```

No `GITHUB_TOKEN` in the command. No secrets in environment variables. The token is injected at the network layer by a TLS-intercepting proxy.

### 3. Verify the agent never saw your token

```bash
$ moat run --grant github -- env | grep -i token
# (nothing)
```

### 4. See what happened

```bash
$ moat trace --network

[10:23:44.512] GET https://api.github.com/user 200 (89ms)
```

## Why this matters

| Traditional approach | With Moat |
|---------------------|-----------|
| `GITHUB_TOKEN=xxx` in env | Token never in container |
| Agent could log/exfiltrate credentials | Token injected at network layer only |
| No visibility into API calls | Full network trace for auditing |
| Runs directly on your machine | Isolated container sandbox |
| Manage Docker, volumes, networks | Just run the agent |

## Running AI coding agents

### Claude Code

```bash
moat grant anthropic   # One-time: imports your Claude Code credentials
moat claude            # Interactive mode
moat claude -p "fix the failing tests"  # Non-interactive
```

### Codex

```bash
moat grant openai      # One-time: imports your Codex credentials
moat codex             # Interactive mode
moat codex -p "explain this codebase"   # Non-interactive
```

Both agents run in isolated containers with credentials injected at the network layer. See the [Running Claude Code](docs/content/guides/01-running-claude-code.md) and [Running Codex](docs/content/guides/02-running-codex.md) guides for details.

## Configuration

Create `agent.yaml` when you need more control:

```yaml
name: my-agent

dependencies:
  - node@20
  - git

grants:
  - github
  - anthropic

network:
  policy: strict
  allow:
    - "api.github.com"
    - "api.anthropic.com"

command: ["npm", "test"]
```

Then run:

```bash
moat run ./my-project
```

See the [agent.yaml reference](docs/content/reference/02-agent-yaml.md) for all options.

## Commands

| Command | Description |
|---------|-------------|
| `moat claude [workspace]` | Run Claude Code |
| `moat codex [workspace]` | Run Codex |
| `moat run [path] [-- cmd]` | Run an agent |
| `moat attach <run-id>` | Attach to a running agent |
| `moat grant <provider>` | Store credentials (github, anthropic, openai, aws, ssh) |
| `moat revoke <provider>` | Remove credentials |
| `moat logs [run-id]` | View logs |
| `moat trace [run-id]` | View network requests |
| `moat audit [run-id]` | Verify audit log integrity |
| `moat list` | List runs |
| `moat stop [run-id]` | Stop a run |
| `moat destroy [run-id]` | Remove a run and its artifacts |
| `moat deps list/info` | Browse available dependencies |

Common flags:

```bash
moat run --grant github         # Inject credentials
moat run -d ./my-project        # Run detached (background)
moat run -i -- bash             # Interactive shell
moat run -p "prompt"            # Run with prompt (Claude/Codex)
```

See the [CLI reference](docs/content/reference/01-cli.md) for all commands and flags.

## How it works

**Container runtimes**: Auto-detects Apple containers (macOS 15+, Apple Silicon) or Docker.

**Credential injection**: A TLS-intercepting proxy sits between the container and the internet. It inspects requests and injects `Authorization` headers for granted services. The proxy binds to localhost (Docker) or uses per-run token auth (Apple containers).

**SSH agent proxy**: For SSH grants, moat runs a filtering SSH agent proxy. Sign requests are forwarded to your SSH agent, but only for granted hosts. Private keys never enter the container.

**Image selection**: The `dependencies` field determines the base image—`node@20` uses `node:20-slim`, `python@3.11` uses `python:3.11-slim`. No dependencies defaults to `ubuntu:22.04`.

**Audit logging**: Events are hash-chained for tamper evidence. Ed25519 attestations provide cryptographic proof of authenticity.

## Setup notes

### Trusting the CA certificate (for HTTPS inspection)

```bash
# macOS
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ~/.moat/proxy/ca/ca.crt

# Linux
sudo cp ~/.moat/proxy/ca/ca.crt /usr/local/share/ca-certificates/moat.crt && sudo update-ca-certificates
```

### Optional CLI dependencies

- **1Password CLI**: `brew install 1password-cli` (for `op://` secrets)
- **AWS CLI**: `brew install awscli` (for `ssm://` secrets)

## Documentation

- [Getting started](docs/content/getting-started/) — Installation, quick start, tool comparison
- [Concepts](docs/content/concepts/) — Sandboxing, credentials, audit logs, networking, dependencies
- [Guides](docs/content/guides/) — Claude Code, Codex, SSH, secrets, multi-agent, snapshots
- [Reference](docs/content/reference/) — CLI, agent.yaml, environment variables

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, testing, and architecture details.

## License

MIT
