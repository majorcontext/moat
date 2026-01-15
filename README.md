# AgentOps

Run AI agents in isolated Docker containers with credential injection and full observability.

## Features

- **Isolated Execution** - Each agent runs in its own Docker container with workspace mounting
- **Credential Injection** - Transparent auth header injection via TLS-intercepting proxy
- **Secrets Injection** - Pull secrets from 1Password and inject as environment variables
- **Smart Image Selection** - Automatically selects container images based on runtime requirements
- **Full Observability** - Capture logs, network requests, and traces for every run
- **Declarative Config** - Configure agents via `agent.yaml` manifests

## Installation

```bash
go install github.com/andybons/agentops/cmd/agent@latest
```

Or build from source:

```bash
git clone https://github.com/andybons/agentops.git
cd agentops
go build -o agent ./cmd/agent
```

**Requirements:** A container runtime must be available:
- **macOS:** Apple containers (macOS 15+, Apple Silicon) or Docker Desktop
- **Linux:** Docker

**Optional dependencies:**
- **1Password CLI** - Required for secrets injection from 1Password (`brew install 1password-cli`)

## Setup

### GitHub OAuth App (for credential injection)

To use `agent grant github`, you need a GitHub OAuth App configured for device flow:

1. Go to [GitHub Developer Settings](https://github.com/settings/developers)
2. Click **New OAuth App**
3. Fill in the details:
   - **Application name:** AgentOps (or your preferred name)
   - **Homepage URL:** `https://github.com/andybons/agentops`
   - **Authorization callback URL:** `https://github.com/andybons/agentops` (not used for device flow)
4. Click **Register application**
5. Copy the **Client ID**
6. Enable device flow: Check **Enable Device Flow** in the app settings

Set the environment variable:

```bash
export AGENTOPS_GITHUB_CLIENT_ID="your-client-id-here"
```

Add this to your shell profile (`~/.bashrc`, `~/.zshrc`, etc.) to persist it.

## Quick Start

### Example: Call the GitHub API without exposing your token

This example runs a container that calls the GitHub API. The container has no access to your token—AgentOps injects it at the network layer.

**1. Grant GitHub access (one-time setup)**

```bash
$ agent grant github

To authorize, visit: https://github.com/login/device
Enter code: ABCD-1234

Waiting for authorization...
GitHub credential saved successfully
```

**2. Run a command that calls the GitHub API**

```bash
$ agent run --grant github -- curl -s https://api.github.com/user
{
  "login": "your-username",
  "id": 1234567,
  "name": "Your Name"
}
```

The `curl` command has no authentication flags—AgentOps injected the `Authorization` header automatically.

By default, containers use `ubuntu:22.04`. To use a different runtime, add `dependencies` to your `agent.yaml` (e.g., `node@20`, `python@3.11`, `go@1.22`).

**3. Verify the token was never exposed**

```bash
$ agent run --grant github -- env | grep -i token
```

Output: _(nothing)_

The token isn't in the environment. It's injected at the network layer by a TLS-intercepting proxy.

**4. See every API call that was made**

```bash
$ agent trace --network
```

Output:

```
[10:23:44.512] GET https://api.github.com/user 200 (89ms)
```

Every HTTP request through the proxy is logged—useful for auditing what an agent did.

### Why this matters

| Traditional approach             | With AgentOps                          |
| -------------------------------- | -------------------------------------- |
| `GITHUB_TOKEN=xxx` in env        | Token never in container's environment |
| Agent could log/exfiltrate token | Token injected at network layer only   |
| No visibility into API calls     | Full network trace for auditing        |
| Runs directly on your machine    | Isolated Docker container              |

## Configuration

Create an `agent.yaml` in your workspace to configure runs:

```yaml
agent: my-agent
version: 1.0.0

# Dependencies determine the base image and installed tools
dependencies:
  - node@20      # Uses node:20 base image
  # - python@3.11  # Uses python:3.11
  # - go@1.22      # Uses golang:1.22

# Credentials to inject (granted via `agent grant`)
grants:
  - github:repo

# Environment variables
env:
  NODE_ENV: development
  DEBUG: "true"

# Secrets from external backends (injected as environment variables)
secrets:
  OPENAI_API_KEY: op://Dev/OpenAI/api-key
  DATABASE_URL: op://Prod/Database/connection-string

# Additional mounts (source:target[:ro])
mounts:
  - ./data:/data:ro
  - ./cache:/cache
```

## Network Policy

Control outbound HTTP/HTTPS access from your agents with declarative network policies. By default, agents can make requests to any host. Enable strict mode to allow only specific hosts.

### How It Works

The network firewall is enforced via the credential-injecting proxy that containers already use for authentication. When a request is blocked, the agent receives a `407 Proxy Authentication Required` response with an `X-AgentOps-Blocked: network-policy` header and an actionable error message.

### Configuration

In `agent.yaml`, configure the network policy:

```yaml
agent: my-agent
dependencies:
  - node@20

# Network policy (optional)
network:
  policy: strict  # "permissive" (default) or "strict"
  allow:
    - "api.openai.com"        # Allows ports 80 and 443
    - "*.amazonaws.com"       # Wildcard matching
    - "api.example.com:8080"  # Specific port only
```

**Policy modes:**

- `permissive` (default) - All outbound HTTP/HTTPS is allowed
- `strict` - All outbound HTTP/HTTPS is blocked except for:
  - Hosts in the `allow` list
  - Hosts derived from grants (e.g., `--grant github` automatically allows `github.com`, `api.github.com`, `*.githubusercontent.com`)

**Allow list format:**

- `api.example.com` - Allows HTTP (port 80) and HTTPS (port 443)
- `api.example.com:8080` - Allows only the specified port (8080)
- `*.example.com` - Wildcard matching for subdomains (ports 80 and 443)
- `*.example.com:8080` - Wildcard with specific port

### Example: OpenAI API with strict networking

```yaml
agent: openai-agent
dependencies:
  - python@3.11

network:
  policy: strict
  allow:
    - "api.openai.com"

grants:
  - github:repo
```

This configuration:
- Blocks all outbound HTTP/HTTPS by default
- Allows access to `api.openai.com` (ports 80 and 443)
- Automatically allows GitHub hosts (`github.com`, `api.github.com`, etc.) because of the `github:repo` grant

### What Happens When Blocked

When an agent tries to access a blocked host:

```bash
$ agent run -- curl -v https://blocked.example.com

< HTTP/1.1 407 Proxy Authentication Required
< X-AgentOps-Blocked: network-policy
< Content-Type: text/plain

AgentOps: request blocked by network policy.
Host "blocked.example.com" is not in the allow list.
Add it to network.allow in agent.yaml or use policy: permissive.
```

### Limitations

1. **Proxy-aware tools only** - HTTP/HTTPS filtering relies on the `HTTP_PROXY` and `HTTPS_PROXY` environment variables. Tools that ignore these variables will have their connections blocked by iptables rather than receiving a helpful error message.

2. **DNS not filtered** - DNS queries (UDP 53) are allowed so tools can resolve hostnames. An agent can resolve any hostname, even if HTTP access to it would be blocked.

3. **Wildcards don't match base domain** - The pattern `*.example.com` matches `api.example.com` and `foo.bar.example.com`, but does NOT match `example.com` itself. To allow both, include both patterns: `*.example.com` and `example.com`.

4. **Brief startup window** - The iptables firewall is configured immediately after container start. There is a brief window (milliseconds) before rules are applied. For most use cases this is not a concern.

### Use Cases

Network policies are useful for:

- **Cost control** - Prevent accidental API calls to expensive services
- **Compliance guardrails** - Ensure agents only access approved endpoints
- **Testing isolation** - Verify agents work with specific API dependencies
- **Debugging** - Quickly identify which services an agent is trying to reach

## Hostname-Based Service Routing

Expose container services with predictable, OAuth-friendly hostnames. This is essential for local development with OAuth flows, webhooks, and multi-service architectures where you need stable URLs that work with callback configurations.

Multiple agents can run simultaneously, each with their own namespace.

### Agent Names

Each running agent has a name used in its hostname:

| Priority | Source | Example |
|----------|--------|---------|
| 1 | `--name` flag | `agent run --name myapp` |
| 2 | `name` in agent.yaml | `name: myapp` |
| 3 | Auto-generated | `fluffy-chicken` |

Names must be unique among running agents. If a name collision occurs, you'll be prompted to choose a different name.

### Configuration

In `agent.yaml`, define the agent name and ports to expose:

```yaml
name: myapp
dependencies:
  - node@20
ports:
  web: 3000
  api: 8080
```

### Usage

```bash
# Start with configured name from agent.yaml
agent run ./project

# Override name with --name flag
agent run --name myapp ./project
```

### Accessing Services

Services are available via HTTPS hostname routing:

```
https://web.myapp.localhost:8080  → container port 3000
https://api.myapp.localhost:8080  → container port 8080
https://myapp.localhost:8080      → default service (first in config)
```

The proxy supports both HTTP and HTTPS on the same port, with HTTPS enabled by default using a locally-generated CA certificate.

**Trusting the CA certificate** (to avoid browser/curl warnings):

```bash
# macOS
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ~/.agentops/proxy/ca/ca.crt

# Linux
sudo cp ~/.agentops/proxy/ca/ca.crt /usr/local/share/ca-certificates/agentops.crt && sudo update-ca-certificates

# Or use curl with --cacert
curl --cacert ~/.agentops/proxy/ca/ca.crt https://web.myapp.localhost:8080
```

The proxy binds to port 8080 by default. Configure globally in `~/.agentops/config.yaml`:

```yaml
proxy:
  port: 8080
```

Or via environment variable: `AGENTOPS_PROXY_PORT=9000`

### Environment Variables

Inside the container, these environment variables are automatically set for OAuth callbacks and CORS configuration:

```bash
AGENTOPS_HOST=myapp.localhost:8080
AGENTOPS_URL=http://myapp.localhost:8080
AGENTOPS_HOST_WEB=web.myapp.localhost:8080
AGENTOPS_URL_WEB=http://web.myapp.localhost:8080
AGENTOPS_HOST_API=api.myapp.localhost:8080
AGENTOPS_URL_API=http://api.myapp.localhost:8080
```

### Listing Running Agents

```bash
$ agent list
NAME            RUN ID           STATE     SERVICES
myapp           run-a1b2c3d4     running   web, api
fluffy-chicken  run-e5f6a1b2     running   web
```

## Commands

### `agent run`

Run an agent in an isolated container.

```bash
agent run [path] [flags] [-- command]

# Examples
agent run                                      # Run from current directory
agent run ./my-project                         # Run from specific directory
agent run --name myapp                         # Run with specific name
agent run --grant github                       # Run with GitHub credentials
agent run -e DEBUG=true                        # Run with environment variable
agent run -- npm test                          # Run with custom command
```

**Flags:**

- `--name` - Name for this agent instance (default: from agent.yaml or random)
- `--grant, -g` - Grant credential access (e.g., `github`, `github:repo,user`)
- `--env, -e` - Set environment variable (can be repeated)

### `agent grant`

Store credentials for injection into agent runs.

```bash
agent grant <provider>[:<scopes>]

# Examples
agent grant github              # Grant with default scopes
agent grant github:repo         # Grant with specific scope
agent grant github:repo,user    # Grant with multiple scopes
```

### `agent revoke`

Remove stored credentials.

```bash
agent revoke <provider>
```

### `agent logs`

View logs from a run.

```bash
agent logs [run-id] [flags]

# Examples
agent logs                      # Logs from most recent run
agent logs run-abc123           # Logs from specific run
agent logs -f                   # Follow logs (like tail -f)
agent logs -n 50                # Show last 50 lines

# Flags
--follow, -f    Follow log output
--lines, -n     Number of lines to show (default: 100)
```

### `agent trace`

View trace spans and network requests from a run.

```bash
agent trace [run-id] [flags]

# Examples
agent trace                     # Traces from most recent run
agent trace run-abc123          # Traces from specific run
agent trace --network           # Show network requests

# Flags
--network       Show network requests instead of traces
```

### `agent list`

List all runs.

```bash
agent list
```

### `agent stop`

Stop a running agent.

```bash
agent stop [run-id]

# If no run-id provided, stops the most recent running run
```

### `agent destroy`

Remove a stopped run and its resources.

```bash
agent destroy [run-id]

# The run must be stopped before it can be destroyed
```

### `agent proxy`

Manage the hostname-based routing proxy. The proxy supports both HTTP and HTTPS on a single port, using auto-generated certificates signed by a local CA. By default, the proxy starts automatically when needed and stops when the last agent exits. Use these commands to run it as a standalone daemon.

```bash
# Start proxy on default port (8080)
agent proxy start

# Start on privileged port (requires sudo)
sudo agent proxy start --port=80

# Check proxy status
agent proxy status

# Stop proxy
agent proxy stop
```

On first start, the proxy generates a CA certificate at `~/.agentops/proxy/ca/ca.crt`. Trust this certificate to avoid browser warnings (see "Accessing Services" above).

Running the proxy separately is useful when you need privileged ports (like 80) since agents can then run without sudo.

### `agent audit`

View the tamper-proof audit log for a run with cryptographic verification.

```bash
agent audit [run-id] [flags]

# Examples
agent audit                           # Audit most recent run
agent audit run-abc123                # Audit specific run
agent audit --export proof.json       # Export proof bundle for offline verification
agent audit run-abc123 -e audit.json  # Export specific run's proof bundle

# Flags
--export, -e    Export proof bundle to JSON file
```

**Output:**

```
Auditing run: run-abc123
===============================================================

Log Integrity
  [ok] Hash chain: 5 entries, no gaps, all hashes valid
  [ok] Merkle tree: root matches computed root

Local Signatures
  [ok] 1 attestations, all signatures valid

External Attestations (Sigstore/Rekor)
  - No Rekor proofs found

===============================================================
VERDICT: [ok] INTACT - No tampering detected
```

### `agent verify-bundle`

Verify an exported proof bundle offline.

```bash
agent verify-bundle <file>

# Example
agent verify-bundle proof.json
```

**Output:**

```
Proof Bundle Verification
===============================================================
Bundle Version: 1
Created: 2026-01-13 10:24:00 UTC
Entries: 5

Log Integrity
  [ok] Hash chain: 5 entries verified
  [ok] Merkle root: 9c8d7e6fa1b2...

Local Signatures
  [ok] 1 attestation(s) verified

External Attestations (Sigstore/Rekor)
  - No Rekor proofs in bundle

===============================================================
VERDICT: [ok] VALID
```

## How It Works

### Container Runtimes

AgentOps automatically detects and uses the best available container runtime:

| Platform | Runtime | Notes |
| -------- | ------- | ----- |
| macOS (Apple Silicon, 15+) | Apple containers | Native virtualization, fastest startup |
| macOS (Intel or older) | Docker Desktop | Requires Docker Desktop installed |
| Linux | Docker | Native Docker, supports host network mode |

The runtime is selected automatically—no configuration needed.

### Credential Injection

When you run `agent grant github`, your GitHub token is stored securely. During runs with `--grant github`, AgentOps:

1. Starts a TLS-intercepting proxy on the host
2. Routes container traffic through the proxy via `HTTP_PROXY`/`HTTPS_PROXY` environment variables
3. Automatically adds `Authorization: Bearer <token>` headers to GitHub API requests
4. The agent never sees the raw token—it's injected at the network layer

**Security:** The proxy binds to localhost only (Docker) or uses authenticated access with a per-run cryptographic token (Apple containers). Credentials are never exposed to the network.

### Secrets Injection

AgentOps can pull secrets from external backends and inject them as environment variables. Unlike credential injection (which happens at the network layer), secrets are resolved before the container starts and passed as environment variables.

**1Password Setup:**

```bash
# Install the 1Password CLI
brew install 1password-cli

# Sign in to your account
eval $(op signin)
```

**Configure secrets in agent.yaml:**

```yaml
secrets:
  OPENAI_API_KEY: op://Development/OpenAI/api-key
  DATABASE_URL: op://Production/Database/connection-string
```

The format is `op://vault/item/field`. When you run `agent run`, AgentOps resolves each secret reference and injects the value as an environment variable.

**Security considerations:**
- Secret values are passed as environment variables (visible to processes in the container)
- Secret names and backends are logged for audit purposes, but values are never logged
- For credentials that should never be visible to the agent, use credential injection (`--grant`) instead

### Image Selection

AgentOps selects the container base image based on `dependencies` in `agent.yaml`:

| Dependencies in agent.yaml | Image          |
| -------------------------- | -------------- |
| `node@20`                  | `node:20`      |
| `python@3.11`              | `python:3.11`  |
| `go@1.22`                  | `golang:1.22`  |
| None specified             | `ubuntu:22.04` |

### Observability

Every run captures:

- **Logs** - Timestamped container output (`~/.agentops/runs/<id>/logs.jsonl`)
- **Network** - All HTTP/HTTPS requests (`~/.agentops/runs/<id>/network.jsonl`)
- **Traces** - OpenTelemetry-compatible spans (`~/.agentops/runs/<id>/traces.jsonl`)
- **Secrets** - Which secrets were resolved (names/backends only, never values) (`~/.agentops/runs/<id>/secrets.jsonl`)
- **Metadata** - Run configuration (`~/.agentops/runs/<id>/metadata.json`)
- **Audit Log** - Tamper-proof audit database (`~/.agentops/runs/<id>/audit.db`)

### Tamper-Proof Audit Logging

AgentOps provides cryptographic verification that audit logs haven't been tampered with:

1. **Hash Chain** - Each log entry includes a SHA-256 hash of its contents plus the previous entry's hash, creating an immutable chain
2. **Merkle Tree** - All entries are organized into a Merkle tree for efficient verification and inclusion proofs
3. **Ed25519 Attestations** - The Merkle root can be signed with a per-run Ed25519 key for cryptographic proof of authenticity
4. **Transparency Log Integration** - Supports Sigstore/Rekor for public timestamping and independent verification

**Export proof bundles** for offline verification or sharing with auditors:

```bash
agent audit run-abc123 --export proof.json
agent verify-bundle proof.json
```

The proof bundle contains entries, attestations, and inclusion proofs that can be verified without access to the original database.

## Development

```bash
# Build
go build ./...

# Test
go test ./...

# Run with verbose output
go test -v ./...
```

## License

MIT
