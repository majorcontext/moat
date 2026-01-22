# Moat

> **Early Release:** This project is in active development. APIs and configuration formats may change. Feedback and contributions welcome.

Run AI agents locally with one command. Zero Docker knowledge. Zero secret copying. Full visibility.

## Philosophy

**Don't manage containers. Manage runs.**

A "run" is a sealed workspace: your code, dependencies, credentials, and observability—all managed as one unit. You shouldn't need to understand Docker, copy tokens around, or piece together logs from different places. Moat handles the infrastructure so you can focus on what the agent actually does.

```
moat claude
```

What happens when you run this:
- An isolated container is created with your code mounted
- Claude Code is pre-installed and ready to go
- Credentials are injected at the network layer (the agent never sees your tokens)
- Every API call, log line, and network request is captured
- When it's done, the workspace is disposable—or you can keep the artifacts

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

## Quick Start

> **First time?** If you have `gh` CLI installed and authenticated, GitHub credentials work automatically. Otherwise, you can use a Personal Access Token.

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

### 4. See exactly what happened

```bash
$ moat trace --network

[10:23:44.512] GET https://api.github.com/user 200 (89ms)
```

Every HTTP request through the proxy is logged—useful for auditing, debugging, and understanding what an agent did.

## Claude Code

The fastest way to run Claude Code in an isolated environment:

```bash
moat grant anthropic   # One-time: imports your Claude Code credentials
moat claude
```

This starts Claude Code interactively in your current directory, using your existing Claude Pro/Max subscription.

### How credentials work

If you have Claude Code installed and logged in, `moat grant anthropic` automatically imports your OAuth credentials from the macOS keychain. No API key required—it uses your existing subscription.

```bash
$ moat grant anthropic

Found Claude Code credentials.
  Subscription: claude_pro
  Expires: 2025-02-15T10:30:00Z

Use Claude Code credentials? [Y/n]: y

Claude Code credentials imported to ~/.moat/credentials/anthropic.json
```

The credentials are injected at the network layer via a TLS-intercepting proxy. Claude Code in the container authenticates normally, but the actual tokens never enter the container environment.

### Common usage

```bash
# Start Claude Code in current directory
moat claude

# Start in a specific project
moat claude ./my-project

# Run with a prompt (non-interactive)
moat claude -p "explain this codebase"
moat claude -p "fix the failing tests"

# Add GitHub access for Claude
moat claude --grant github

# Name the session for easy reference
moat claude --name my-feature

# Run in background, then attach later
moat claude -d
moat attach <run-id>
```

### With API key (pay-as-you-go)

If you have an Anthropic API key instead of a Claude Pro/Max subscription:

```bash
# When prompted, decline Claude Code import and enter your API key
moat grant anthropic

# Or set the environment variable first
export ANTHROPIC_API_KEY="sk-ant-api..."
moat grant anthropic

# Claude Code will use the key automatically
moat claude
```

The API key is injected at the network layer—Claude Code never sees it directly.

## Why This Matters

| Traditional approach | With Moat |
|---------------------|---------------|
| `GITHUB_TOKEN=xxx` in env | Token never in container |
| Agent could log/exfiltrate credentials | Token injected at network layer only |
| No visibility into API calls | Full network trace for auditing |
| Runs directly on your machine | Isolated container sandbox |
| Manage Docker, volumes, networks | Just run the agent |

## Configuration

Create `agent.yaml` when you need more control:

```yaml
name: my-agent
version: 1.0.0

# Runtime determines the base image
dependencies:
  - node@20        # Uses node:20
  # - python@3.11  # Uses python:3.11
  # - go@1.22      # Uses golang:1.22

# Credentials to inject (granted via `moat grant`)
grants:
  - github:repo

# Environment variables
env:
  NODE_ENV: development

# Secrets from external backends
secrets:
  OPENAI_API_KEY: op://Dev/OpenAI/api-key
  DATABASE_URL: ssm:///production/database/url

# Network policy
network:
  policy: strict
  allow:
    - "api.openai.com"
    - "*.amazonaws.com"

# Additional mounts
mounts:
  - ./data:/data:ro

# Expose services via hostname routing
ports:
  web: 3000
  api: 8080

# Default command to run (can be overridden with -- on CLI)
command: ["npm", "start"]

# Run in interactive mode (equivalent to -i flag)
# interactive: true
```

Then just run:

```bash
moat run ./my-project
```

### Command

The `command` field specifies the default command to run in the container.

```yaml
# Simple command
command: ["npm", "start"]

# Shell command (for pipelines, environment variable expansion, etc.)
command: ["sh", "-c", "npm install && npm start"]
```

**Security note:** When using shell commands (`sh -c`), be cautious with variable interpolation. Variables in the command string are expanded inside the container, which is generally safe. However, avoid constructing commands from untrusted input.

**Precedence:** CLI command (`-- cmd`) > `command` in agent.yaml

## Credentials

The credential broker is the security core. Agents request scoped capabilities—the broker injects authentication at the network layer.

### Granting access

```bash
moat grant github              # GitHub (from gh CLI, env var, or PAT prompt)
moat grant aws --role=ARN      # AWS IAM role (see AWS credentials below)
```

### SSH Access

Grant agents SSH access to specific hosts—your private keys stay on your machine:

```bash
# Grant access to github.com using your SSH agent's default key
$ moat grant ssh --host github.com
Using key: user@host (SHA256:...)
Granted SSH access to github.com

# Test SSH access with the example (see examples/ssh-github/)
$ moat run ./examples/ssh-github
Hi username! You've successfully authenticated, but GitHub does not provide shell access.

# Use SSH access for git operations
$ moat run --grant ssh:github.com -- git clone git@github.com:org/repo.git
```

The agent connects through moat's SSH proxy—signing requests are forwarded to your SSH agent, but only for the granted hosts. The private key never enters the container.

```yaml
# Or declare in agent.yaml
grants:
  - github            # For HTTPS API access
  - ssh:github.com    # For SSH git operations
  - ssh:gitlab.com    # Multiple hosts supported
```

**Requirements:**
- Your SSH agent must be running (`SSH_AUTH_SOCK` must be set)
- Add keys with `ssh-add ~/.ssh/id_ed25519`
- Run `moat grant ssh --host <hostname>` before using

### Using credentials in runs

```bash
moat run --grant github ./my-project
```

Or declare in `agent.yaml`:

```yaml
grants:
  - github:repo
```

### Security model

- Tokens stored encrypted on host (`~/.moat/credentials/`)
- Injected via TLS-intercepting proxy—never in container environment
- Scoped per-run—when the run ends, the capability binding is discarded
- Full audit trail of which credentials were used and when

### Encryption key storage

Moat encrypts stored credentials using AES-256-GCM. The encryption key is stored securely using your system's keychain:

| Platform | Storage |
|----------|---------|
| macOS | Keychain via Security framework |
| Linux | Secret Service (GNOME Keyring/KWallet) |
| Windows | Credential Manager |
| Headless/CI | File-based fallback at `~/.moat/encryption.key` |

The system keychain is preferred for better security. If unavailable (e.g., in CI environments, headless servers, or containers), Moat automatically falls back to file-based storage with restricted permissions (0600).

**Troubleshooting:**

If you see decryption errors after upgrading Moat, your credentials may have been encrypted with a different key. Re-authenticate with:

```bash
moat grant github  # or other provider
```

### AWS credentials

AWS credentials use IAM role assumption with short-lived sessions:

```bash
# Grant access to an IAM role
moat grant aws --role=arn:aws:iam::123456789012:role/AgentRole

# With custom options
moat grant aws --role=arn:aws:iam::123456789012:role/AgentRole \
  --region=us-west-2 \
  --session-duration=1h

# Use in a run
moat run --grant aws ./my-agent
```

**How it works:**
- Your host AWS credentials assume the specified role at runtime
- The agent receives short-lived session credentials (15 minutes by default)
- Credentials auto-refresh via a credential helper that fetches from the proxy
- Your long-lived credentials never enter the container

**Note:** Unlike GitHub and Anthropic grants where tokens are injected at the network layer and never visible to the container, AWS uses `credential_process`—a standard AWS mechanism where the SDK calls a helper script to fetch credentials on demand. This means short-lived session credentials are available to the container, but your long-lived IAM credentials remain on the host.

**Security note:** Any code in the container can access the session credentials. Use an IAM role with minimal permissions scoped to what the agent actually needs.

**Required IAM setup:**
1. Create an IAM role with appropriate permissions for the agent
2. Configure the role's trust policy to allow your IAM user/role to assume it
3. Ensure you have `sts:AssumeRole` permission

## Secrets

Pull secrets from external backends and inject as environment variables:

```yaml
secrets:
  OPENAI_API_KEY: op://Dev/OpenAI/api-key      # 1Password
  DATABASE_URL: ssm:///production/database/url  # AWS SSM
```

**Supported backends:**

| Backend | Format | CLI Required |
|---------|--------|--------------|
| 1Password | `op://vault/item/field` | `op` |
| AWS SSM | `ssm:///path` or `ssm://region/path` | `aws` |

**Important:** Secrets are environment variables—visible to all processes in the container. For credentials that should never be exposed, use credential injection (`--grant`) instead.

## Network Policy

Control outbound access with declarative policies:

```yaml
network:
  policy: strict  # Block all except allowed
  allow:
    - "api.openai.com"
    - "*.github.com"
```

**Policy modes:**
- `permissive` (default): All outbound HTTP/HTTPS allowed
- `strict`: Only allowed hosts + hosts from grants

When blocked, agents get a clear error:

```
Moat: request blocked by network policy.
Host "blocked.example.com" is not in the allow list.
Add it to network.allow in agent.yaml or use policy: permissive.
```

## Hostname Routing

Run multiple agents on the same repo simultaneously—each with isolated services on stable URLs. No port conflicts, no manual mapping.

```yaml
# agent.yaml
ports:
  web: 3000
  api: 8080
```

Run two agents working on different features:

```bash
$ moat run --name dark-mode ./my-app &
$ moat run --name checkout-flow ./my-app &

$ moat list
NAME            RUN ID           STATE     SERVICES
dark-mode       run_a1b2c3...    running   web, api
checkout-flow   run_d4e5f6...    running   web, api
```

Both agents expose the same ports internally, but each gets its own hostname namespace. Access via the proxy (default port 8080):

```
https://web.dark-mode.localhost:8080       → dark-mode container:3000
https://api.dark-mode.localhost:8080       → dark-mode container:8080
https://web.checkout-flow.localhost:8080   → checkout-flow container:3000
https://api.checkout-flow.localhost:8080   → checkout-flow container:8080
```

No port conflicts, predictable URLs for OAuth callbacks. Test both features side by side in your browser.

Inside each container, environment variables point to its own services:

```bash
# In dark-mode container:
MOAT_URL_WEB=http://web.dark-mode.localhost:8080

# In checkout-flow container:
MOAT_URL_WEB=http://web.checkout-flow.localhost:8080
```

When done, stop both agents by run ID (from `moat list`):

```bash
$ moat stop run_a1b2c3...
$ moat stop run_d4e5f6...
```

## Observability

Every run captures structured data:

- **Logs**: Container stdout/stderr with timestamps
- **Network**: All HTTP/HTTPS requests (method, URL, status, duration)
- **Audit**: Tamper-proof log with hash chain and Merkle tree verification

```bash
moat logs              # View container output
moat logs -n 50        # Last 50 lines
moat trace --network   # View HTTP requests
moat audit             # Verify audit log integrity
```

Export proof bundles for offline verification:

```bash
moat audit --export proof.json
moat verify-bundle proof.json
```

## Commands

| Command | Description |
|---------|-------------|
| `moat claude [workspace]` | Run Claude Code (easiest way to start) |
| `moat claude sessions` | List Claude Code sessions |
| `moat run [path] [-- cmd]` | Run an agent |
| `moat attach <run-id>` | Attach to a running agent |
| `moat grant <provider>` | Store credentials |
| `moat grant ssh --host <host>` | Grant SSH access for a host |
| `moat revoke <provider>` | Remove credentials |
| `moat logs [run-id]` | View logs |
| `moat trace [run-id]` | View traces/network |
| `moat audit [run-id]` | Verify audit log |
| `moat verify-bundle <file>` | Verify exported proof bundle |
| `moat list` | List runs |
| `moat status` | Show runs, images, disk usage, and health |
| `moat stop [run-id]` | Stop a run |
| `moat destroy [run-id]` | Remove a run |
| `moat proxy start/stop/status` | Manage the proxy |
| `moat clean` | Remove stopped runs and unused images |
| `moat promote <run-id>` | Promote run artifacts to persistent storage |
| `moat deps list/info` | Manage and inspect dependencies |
| `moat system images/containers` | Low-level container system commands |

### Common flags

```bash
moat run --name myapp           # Set agent name
moat run --grant github         # Inject credentials
moat run --grant ssh:github.com # Grant SSH access
moat run -e DEBUG=true          # Set env variable
moat run -- npm test            # Custom command (overrides agent.yaml)
moat run -d ./my-project        # Run detached (in background)
moat run -i -- bash             # Interactive shell
moat attach run_a1b2c3d4e5f6          # Attach (uses run's original mode)
moat attach -i run_a1b2c3d4e5f6       # Force interactive mode
moat attach -i=false run_a1b2c3d4e5f6 # Force output-only mode
moat logs -n 50                 # Last N lines
```

### Detach and attach

Runs exist independently of your terminal. By default, `moat run` attaches to the container (you see output).

**Non-interactive mode** (default):
- `Ctrl+C` → Detach (run continues in background)
- `Ctrl+C Ctrl+C` (within 500ms) → Stop the run

```bash
# Start attached (default)
moat run ./my-project

# Start detached
moat run -d ./my-project

# Reattach to a running container (uses the run's original mode)
moat attach <run-id>
```

### Interactive mode

For shells and REPLs, use `-i` to enable interactive mode (stdin + TTY):

```bash
moat run -i -- bash
moat run -i -- python
```

Or configure it in `agent.yaml`:

```yaml
# agent.yaml
command: ["bash"]
interactive: true
```

Then just `moat run` without flags—interactive mode is automatic.

When reattaching, `moat attach` defaults to the run's original mode. Use flags to override:

```bash
moat attach <run-id>        # Uses run's original mode
moat attach -i <run-id>     # Force interactive
moat attach -i=false <run-id>  # Force output-only
```

**Interactive mode escape sequences** (press `Ctrl-/` then a key):
- `Ctrl-/ d` → Detach (run continues in background)
- `Ctrl-/ k` → Stop the run

In interactive mode, `Ctrl+C` is passed through to the container process (e.g., to interrupt a Python script), so the escape sequence provides an alternative way to detach or stop.

## How It Works

**Container runtimes**: Auto-detects Apple containers (macOS 15+, Apple Silicon) or Docker.

**Credential injection**: A TLS-intercepting proxy sits between the container and the internet. It inspects requests and injects `Authorization` headers for granted services. The proxy binds to localhost (Docker) or uses per-run token auth (Apple containers).

**SSH agent proxy**: For SSH grants, moat runs a filtering SSH agent proxy that connects to your local SSH agent. Only keys mapped to granted hosts are visible to the container. Sign requests are forwarded to your real agent—private keys never enter the container.

**Image selection**: The `dependencies` field maps to base images—`node@20` → `node:20`, `python@3.11` → `python:3.11`. No dependencies defaults to `ubuntu:22.04`.

**Audit logging**: Events are hash-chained and organized into a Merkle tree. Ed25519 attestations provide cryptographic proof of authenticity.

## Setup Notes

### Trusting the CA certificate (for hostname routing)

```bash
# macOS
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ~/.moat/proxy/ca/ca.crt

# Linux
sudo cp ~/.moat/proxy/ca/ca.crt /usr/local/share/ca-certificates/moat.crt && sudo update-ca-certificates
```

### Optional CLI dependencies

- **1Password CLI**: `brew install 1password-cli` (for `op://` secrets)
- **AWS CLI**: `brew install awscli` (for `ssm://` secrets)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, testing, and architecture details.

## License

MIT
