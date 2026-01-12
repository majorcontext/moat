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

**Requirements:** Docker must be installed and running.

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
$ agent run my-agent . --runtime node:20 --grant github -- curl -s https://api.github.com/user
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
$ agent run my-agent . --runtime node:20 --grant github -- env | grep -i token
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

## Commands

### `agent run`

Run an agent in an isolated container.

```bash
agent run <agent> [path] [flags] [-- command]

# Examples
agent run my-agent . --runtime python:3.11     # Run with Python 3.11
agent run my-agent . --runtime node:20         # Run with Node.js 20
agent run test . --grant github                # Run with GitHub credentials
agent run test . -e DEBUG=true                 # Run with environment variable
agent run test . -- pytest -v                  # Run custom command
```

**Flags:**

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

## How It Works

### Credential Injection

When you run `agent grant github`, your GitHub token is stored securely. During runs with `--grant github`, AgentOps:

1. Starts a TLS-intercepting proxy
2. Routes container traffic through the proxy
3. Automatically adds `Authorization: Bearer <token>` headers to GitHub API requests
4. The agent never sees the raw token

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
