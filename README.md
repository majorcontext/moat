# Moat

Run AI agents locally with one command. Zero Docker knowledge. Zero secret copying. Full visibility.

## Philosophy

**Don't manage containers. Manage runs.**

A "run" is a sealed workspace: your code, dependencies, credentials, and observability—all managed as one unit. You shouldn't need to understand Docker, copy tokens around, or piece together logs from different places. Moat handles the infrastructure so you can focus on what the agent actually does.

```
moat run --grant github -- npx claude-code
```

What happens when you run this:
- An isolated container is created with your code mounted
- GitHub credentials are injected at the network layer (the agent never sees your token)
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

> **First time?** You'll need a GitHub OAuth App with device flow enabled. See [Setup Notes](#setup-notes) for a 2-minute setup.

### 1. Grant credentials (one time)

```bash
$ moat grant github

To authorize, visit: https://github.com/login/device
Enter code: ABCD-1234

Waiting for authorization...
GitHub credential saved successfully
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
moat grant github              # GitHub device flow
moat grant github:repo         # Specific scope
moat grant github:repo,user    # Multiple scopes
```

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
NAME            RUN ID       STATE     SERVICES
dark-mode       run-a1b2...  running   web, api
checkout-flow   run-c3d4...  running   web, api
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
$ moat stop run-a1b2...
$ moat stop run-c3d4...
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
| `moat run [path] [-- cmd]` | Run an agent |
| `moat grant <provider>` | Store credentials |
| `moat revoke <provider>` | Remove credentials |
| `moat logs [run-id]` | View logs |
| `moat trace [run-id]` | View traces/network |
| `moat audit [run-id]` | Verify audit log |
| `moat verify-bundle <file>` | Verify exported proof bundle |
| `moat list` | List runs |
| `moat stop [run-id]` | Stop a run |
| `moat destroy [run-id]` | Remove a run |
| `moat proxy start/stop/status` | Manage the proxy |

### Common flags

```bash
moat run --name myapp           # Set agent name
moat run --grant github         # Inject credentials
moat run -e DEBUG=true          # Set env variable
moat run -- npm test            # Custom command (overrides agent.yaml)
moat logs -n 50                 # Last N lines
```

## How It Works

**Container runtimes**: Auto-detects Apple containers (macOS 15+, Apple Silicon) or Docker.

**Credential injection**: A TLS-intercepting proxy sits between the container and the internet. It inspects requests and injects `Authorization` headers for granted services. The proxy binds to localhost (Docker) or uses per-run token auth (Apple containers).

**Image selection**: The `dependencies` field maps to base images—`node@20` → `node:20`, `python@3.11` → `python:3.11`. No dependencies defaults to `ubuntu:22.04`.

**Audit logging**: Events are hash-chained and organized into a Merkle tree. Ed25519 attestations provide cryptographic proof of authenticity.

## Setup Notes

### GitHub OAuth App (for `moat grant github`)

1. Go to [GitHub Developer Settings](https://github.com/settings/developers) → New OAuth App
2. Enable **Device Flow** in the app settings
3. Set `MOAT_GITHUB_CLIENT_ID` in your shell profile

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
