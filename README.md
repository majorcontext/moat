# AgentOps

Run AI agents in isolated Docker containers with credential injection and full observability.

## Features

- **Isolated Execution** - Each agent runs in its own Docker container with workspace mounting
- **Credential Injection** - Transparent auth header injection via TLS-intercepting proxy
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
$ agent run --runtime node:20 --grant github -- curl -s https://api.github.com/user
{
  "login": "your-username",
  "id": 1234567,
  "name": "Your Name"
}
```

The `curl` command has no authentication flags—AgentOps injected the `Authorization` header automatically.

The `--runtime node:20` flag selects the Node.js 20 Docker image (which includes curl). You can also use `--runtime python:3.11` or `--runtime go:1.22`.

**3. Verify the token was never exposed**

```bash
$ agent run --runtime node:20 --grant github -- env | grep -i token
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

# Runtime determines the base image
runtime:
  node: 20 # Uses node:20
  # python: 3.11  # Uses python:3.11
  # go: 1.22      # Uses golang:1.22

# Credentials to inject (granted via `agent grant`)
grants:
  - github:repo

# Environment variables
env:
  NODE_ENV: development
  DEBUG: "true"

# Additional mounts (source:target[:ro])
mounts:
  - ./data:/data:ro
  - ./cache:/cache
```

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
runtime:
  node: 20
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
agent run --runtime python:3.11                # Run with Python 3.11
agent run --runtime node:20 -- npm test        # Run with Node.js and custom command
agent run --grant github                       # Run with GitHub credentials
agent run -e DEBUG=true                        # Run with environment variable
```

**Flags:**

- `--name` - Name for this agent instance (default: from agent.yaml or random)
- `--runtime` - Runtime language:version (e.g., `python:3.11`, `node:20`, `go:1.22`)
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

### Image Selection

AgentOps selects the container base image based on the `--runtime` flag or `agent.yaml` runtime config:

| Runtime                                           | Image          |
| ------------------------------------------------- | -------------- |
| `--runtime node:20` or `runtime.node: 20`         | `node:20`      |
| `--runtime python:3.11` or `runtime.python: 3.11` | `python:3.11`  |
| `--runtime go:1.22` or `runtime.go: 1.22`         | `golang:1.22`  |
| Multiple or none                                  | `ubuntu:22.04` |

The `--runtime` flag overrides `agent.yaml` settings.

### Observability

Every run captures:

- **Logs** - Timestamped container output (`~/.agentops/runs/<id>/logs.jsonl`)
- **Network** - All HTTP/HTTPS requests (`~/.agentops/runs/<id>/network.jsonl`)
- **Traces** - OpenTelemetry-compatible spans (`~/.agentops/runs/<id>/traces.jsonl`)
- **Metadata** - Run configuration (`~/.agentops/runs/<id>/metadata.json`)
- **Audit Log** - Tamper-proof audit database (`~/.agentops/runs/<id>/audit.db`)

### Tamper-Proof Audit Logging

> **Status:** Audit infrastructure is complete. Integration with `agent run` is in progress.
> See [docs/plans/audit-integration-guide.md](docs/plans/audit-integration-guide.md) for the implementation plan.

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
