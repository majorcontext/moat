---
title: "CLI reference"
description: "Complete reference for all Moat CLI commands and flags."
keywords: ["moat", "cli", "commands", "reference", "flags"]
---

# CLI reference

Complete reference for Moat CLI commands.

## Global flags

These flags apply to all commands:

| Flag | Description |
|------|-------------|
| `-v`, `--verbose` | Enable verbose output (debug logs) |
| `--dry-run` | Show what would happen without executing |
| `--json` | Output in JSON format |
| `-h`, `--help` | Show help for command |

## moat run

Run an agent in a container.

```
moat run [flags] [path] [-- command]
```

### Arguments

| Argument | Description |
|----------|-------------|
| `path` | Workspace directory (default: current directory) |
| `command` | Command to run (overrides agent.yaml) |

### Flags

| Flag | Description |
|------|-------------|
| `--name NAME` | Set run name (used for hostname routing) |
| `--grant PROVIDER` | Inject credential (repeatable) |
| `-e KEY=VALUE` | Set environment variable (repeatable) |
| `-d`, `--detach` | Run in background |
| `-i`, `--interactive` | Enable interactive mode (stdin + TTY) |
| `--rebuild` | Force rebuild of container image |
| `--runtime RUNTIME` | Container runtime to use (apple, docker) |
| `--keep` | Keep container after run completes |
| `--mount SRC:DST:MODE` | Additional mount (repeatable) |
| `--no-snapshots` | Disable snapshots for this run |
| `--no-sandbox` | Disable gVisor sandboxing (Docker only) |

### Examples

```bash
# Run in current directory
moat run

# Run in specific directory
moat run ./my-project

# Run with credentials
moat run --grant github ./my-project

# Run with custom command
moat run -- npm test

# Run shell command
moat run -- sh -c "npm install && npm test"

# Interactive shell
moat run -i -- bash

# Background run
moat run -d ./my-project

# Multiple credentials
moat run --grant github --grant anthropic ./my-project

# Environment variable
moat run -e DEBUG=true ./my-project

# Named run for hostname routing
moat run --name my-feature ./my-project

# Disable gVisor sandbox (when needed for compatibility)
moat run --no-sandbox ./my-project
```

### --no-sandbox

Disables gVisor sandboxing for Docker containers. By default, Moat runs Docker containers with gVisor (`runsc`) for additional isolation. This flag disables gVisor and uses the standard Docker runtime (`runc`).

**When to use:** Some workloads use syscalls that gVisor doesn't support. If your agent fails with syscall-related errors, try `--no-sandbox`.

**Note:** This flag only affects Docker containers. Apple containers use macOS virtualization and are unaffected.

```bash
moat run --no-sandbox ./my-project
```

---

## moat claude

Run Claude Code in a container.

```
moat claude [flags] [path]
```

### Arguments

| Argument | Description |
|----------|-------------|
| `path` | Workspace directory (default: current directory) |

### Flags

| Flag | Description |
|------|-------------|
| `-p`, `--prompt TEXT` | Run non-interactive with prompt |
| `--grant PROVIDER` | Add credentials (repeatable) |
| `-e KEY=VALUE` | Set environment variable (repeatable) |
| `--name NAME` | Session name |
| `-d`, `--detach` | Run in background |
| `--rebuild` | Force rebuild of container image |
| `--allow-host HOST` | Additional hosts to allow network access to (repeatable) |
| `--runtime RUNTIME` | Container runtime to use (apple, docker) |
| `--keep` | Keep container after run completes |
| `--no-sandbox` | Disable gVisor sandbox (Docker only) |
| `--noyolo` | Restore Claude Code's confirmation prompts (by default, prompts are skipped) |

### Examples

```bash
# Interactive Claude Code
moat claude

# In specific directory
moat claude ./my-project

# Non-interactive with prompt
moat claude -p "fix the failing tests"

# With GitHub access
moat claude --grant github

# Named session
moat claude --name feature-auth ./my-project

# Background session
moat claude -d ./my-project
```

### Subcommands

#### moat claude plugins list

List configured plugins for a workspace.

```bash
moat claude plugins list [path]
```

---

## moat codex

Run OpenAI Codex CLI in a container.

```
moat codex [workspace] [flags]
```

### Arguments

| Argument | Description |
|----------|-------------|
| `workspace` | Workspace directory (default: current directory) |

### Flags

| Flag | Description |
|------|-------------|
| `-p`, `--prompt TEXT` | Run non-interactive with prompt |
| `--grant PROVIDER` | Add credentials (repeatable) |
| `-e KEY=VALUE` | Set environment variable (repeatable) |
| `--name NAME` | Session name |
| `-d`, `--detach` | Run in background |
| `--rebuild` | Force rebuild of container image |
| `--allow-host HOST` | Additional hosts to allow network access to (repeatable) |
| `--full-auto` | Enable full-auto mode (auto-approve tool use) (default: true) |
| `--runtime RUNTIME` | Container runtime to use (apple, docker) |
| `--keep` | Keep container after run completes |
| `--no-sandbox` | Disable gVisor sandbox (Docker only) |

### Examples

```bash
# Interactive Codex CLI
moat codex

# In specific directory
moat codex ./my-project

# Non-interactive with prompt
moat codex -p "explain this codebase"
moat codex -p "fix the bug in main.py"

# With GitHub access
moat codex --grant github

# Named session
moat codex --name my-feature

# Run in background
moat codex -d

# Force rebuild
moat codex --rebuild

# Disable full-auto mode (require manual approval)
moat codex --full-auto=false
```

### Subcommands

#### moat codex sessions

List Codex sessions.

```bash
moat codex sessions [flags]
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--active` | Show only running sessions |

**Examples:**

```bash
# List all sessions
moat codex sessions

# Show only active sessions
moat codex sessions --active
```

---

## moat attach

Attach to a running container.

```
moat attach [flags] <run-id>
```

### Arguments

| Argument | Description |
|----------|-------------|
| `run-id` | Run ID to attach to |

### Flags

| Flag | Description |
|------|-------------|
| `-i`, `--interactive` | Force interactive mode |
| `-i=false` | Force output-only mode |

By default, uses the run's original mode (interactive or not).

### Examples

```bash
# Attach with original mode
moat attach run_a1b2c3d4e5f6

# Force interactive
moat attach -i run_a1b2c3d4e5f6

# Force output-only
moat attach -i=false run_a1b2c3d4e5f6
```

### Detach sequences

**Non-interactive mode:**
- `Ctrl+C` — Detach (run continues)
- `Ctrl+C Ctrl+C` (within 500ms) — Stop the run

**Interactive mode:**
- `Ctrl-/ d` — Detach (run continues)
- `Ctrl-/ k` — Stop the run

---

## moat grant

Store credentials for injection into runs.

```
moat grant <provider>[:<scopes>]
```

### Providers

| Provider | Description |
|----------|-------------|
| `github` | GitHub (gh CLI, env var, or PAT) |
| `anthropic` | Anthropic (Claude Code OAuth or API key) |
| `openai` | OpenAI (API key) |
| `aws` | AWS (IAM role assumption) |

GitHub credentials are obtained from multiple sources, in order of preference:

1. **gh CLI** — Uses token from `gh auth token` if available
2. **Environment variable** — Falls back to `GITHUB_TOKEN` or `GH_TOKEN`
3. **Personal Access Token** — Interactive prompt for manual entry

### Examples

```bash
# GitHub (uses gh CLI token if available)
moat grant github

# Anthropic
moat grant anthropic

# OpenAI
moat grant openai
```

### moat grant mcp <name>

Store a credential for an MCP server.

```bash
moat grant mcp context7
```

The credential is stored as `mcp-<name>` (e.g., `mcp-context7`) and can be referenced in agent.yaml.

**Interactive prompts:**
- Credential (hidden input)

**Storage:**
- `~/.moat/credentials/mcp-<name>.enc`

### moat grant ssh

Grant SSH access to a specific host.

```
moat grant ssh --host <hostname>
```

### Flags

| Flag | Description |
|------|-------------|
| `--host HOSTNAME` | Host to grant access to (required) |

### Examples

```bash
moat grant ssh --host github.com
moat grant ssh --host gitlab.com
```

### moat grant aws

Grant AWS credentials via IAM role assumption.

```
moat grant aws --role=<ARN> [flags]
```

### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--role ARN` | IAM role ARN to assume (required) | — |
| `--region REGION` | AWS region for API calls | From AWS config |
| `--session-duration DURATION` | Session duration (e.g., `1h`, `30m`, `15m`) | `15m` |
| `--external-id ID` | External ID for cross-account role assumption | — |

### Examples

```bash
# Basic role assumption
moat grant aws --role arn:aws:iam::123456789012:role/AgentRole

# With explicit region
moat grant aws --role arn:aws:iam::123456789012:role/AgentRole --region us-west-2

# With custom session duration
moat grant aws --role arn:aws:iam::123456789012:role/AgentRole --session-duration 2h

# Cross-account with external ID
moat grant aws --role arn:aws:iam::987654321098:role/CrossAccountRole --external-id abc123

# Full example
moat grant aws \
    --role arn:aws:iam::123456789012:role/AgentRole \
    --region eu-west-1 \
    --session-duration 30m
```

### moat grant list

List all stored credentials.

```
moat grant list
```

#### Examples

```bash
moat grant list
moat grant list --json
```

---

## moat sessions

List sessions across all agent types.

```
moat sessions [flags]
```

### Flags

| Flag | Description |
|------|-------------|
| `--agent TYPE` | Filter by agent type (claude, codex) |
| `--active` | Only show running sessions |

### Examples

```bash
moat sessions
moat sessions --active
moat sessions --agent claude
```

---

## moat revoke

Remove stored credentials.

```
moat revoke <provider>
```

### Examples

```bash
moat revoke github
moat revoke anthropic
moat revoke ssh:github.com
```

---

## moat logs

View container logs.

```
moat logs [flags] [run-id]
```

### Arguments

| Argument | Description |
|----------|-------------|
| `run-id` | Run ID (default: most recent) |

### Flags

| Flag | Description |
|------|-------------|
| `-n`, `--lines N` | Show last N lines (default: 100) |

### Examples

```bash
# Most recent run
moat logs

# Specific run
moat logs run_a1b2c3d4e5f6

# Last 50 lines
moat logs -n 50
```

---

## moat trace

View execution traces and network requests.

```
moat trace [flags] [run-id]
```

### Arguments

| Argument | Description |
|----------|-------------|
| `run-id` | Run ID (default: most recent) |

### Flags

| Flag | Description |
|------|-------------|
| `--network` | Show network requests instead of spans |
| `-v`, `--verbose` | Show headers and bodies (requires `--network`) |

### Examples

```bash
# Execution spans
moat trace

# Network requests
moat trace --network

# Network with details
moat trace --network -v

# Specific run
moat trace --network run_a1b2c3d4e5f6
```

---

## moat audit

Verify audit log integrity.

```
moat audit [flags] <run-id>
```

### Arguments

| Argument | Description |
|----------|-------------|
| `run-id` | Run ID to audit |

### Flags

| Flag | Description |
|------|-------------|
| `-e`, `--export FILE` | Export proof bundle |

### Examples

```bash
# Verify audit log
moat audit run_a1b2c3d4e5f6

# Export proof bundle
moat audit run_a1b2c3d4e5f6 --export proof.json
```

---

### moat audit verify

Verify an exported proof bundle.

```
moat audit verify <file>
```

### Examples

```bash
moat audit verify proof.json
```

---

## moat list

List all runs.

```
moat list
```

### Output columns

| Column | Description |
|--------|-------------|
| NAME | Run name |
| RUN ID | Unique identifier |
| STATE | running, stopped, failed |
| SERVICES | Exposed services (from ports) |

---

## moat status

Show high-level system status summary.

```
moat status
```

### Output sections

- **Runtime**: Docker or Apple containers
- **Active Runs**: Currently running containers with age, disk usage, and endpoints
- **Summary**: Counts and disk usage for stopped runs and cached images
- **Health**: Warnings about stopped runs and orphaned containers

For detailed information about all runs, use `moat list`.
For image details, use `moat system images`

---

## moat stop

Stop a running container.

```
moat stop [run-id]
```

### Arguments

| Argument | Description |
|----------|-------------|
| `run-id` | Run ID (default: most recent running) |

### Flags

| Flag | Description |
|------|-------------|
| `--all` | Stop all running containers |

### Examples

```bash
# Stop most recent
moat stop

# Stop specific run
moat stop run_a1b2c3d4e5f6

# Stop all
moat stop --all
```

---

## moat destroy

Remove a stopped run and its artifacts.

```
moat destroy [run-id]
```

### Examples

```bash
moat destroy run_a1b2c3d4e5f6
```

---

## moat clean

Clean up stopped runs and unused images.

```
moat clean [flags]
```

### Flags

| Flag | Description |
|------|-------------|
| `-f`, `--force` | Skip confirmation prompt |
| `--dry-run` | Show what would be removed |

### Examples

```bash
# Interactive cleanup
moat clean

# Force cleanup
moat clean -f

# Preview cleanup
moat clean --dry-run
```

---

## moat snapshot

Create and manage workspace snapshots.

When called with a run ID, creates a manual snapshot. Use subcommands to list, prune, or restore snapshots.

```
moat snapshot <run-id> [flags]
```

### Flags

| Flag | Description |
|------|-------------|
| `--label TEXT` | Optional label for the snapshot |

### Examples

```bash
moat snapshot run_a1b2c3d4e5f6
moat snapshot run_a1b2c3d4e5f6 --label "before refactor"
```

### moat snapshot list

List snapshots for a run.

```
moat snapshot list <run-id>
```

#### Examples

```bash
moat snapshot list run_a1b2c3d4e5f6
moat snapshot list run_a1b2c3d4e5f6 --json
```

### moat snapshot prune

Remove old snapshots, keeping the newest N. The pre-run snapshot is always preserved.

```
moat snapshot prune <run-id> [flags]
```

#### Flags

| Flag | Description |
|------|-------------|
| `--keep N` | Keep N most recent (default: 5) |
| `--dry-run` | Preview what would be deleted |

#### Examples

```bash
moat snapshot prune run_a1b2c3d4e5f6 --keep 3
moat snapshot prune run_a1b2c3d4e5f6 --dry-run
```

### moat snapshot restore

Restore workspace from a snapshot. If no snapshot ID is given, restores the most recent. A safety snapshot is created before in-place restores.

```
moat snapshot restore <run-id> [snapshot-id] [flags]
```

#### Flags

| Flag | Description |
|------|-------------|
| `--to DIR` | Extract to a different directory instead of restoring in-place |

#### Examples

```bash
moat snapshot restore run_a1b2c3d4e5f6
moat snapshot restore run_a1b2c3d4e5f6 snap_abc123
moat snapshot restore run_a1b2c3d4e5f6 --to /tmp/recovery
```

---

## moat proxy

Manage the hostname routing proxy. When called without a subcommand, shows the current proxy status.

### moat proxy start

Start the routing proxy.

```
moat proxy start [flags]
```

### Flags

| Flag | Description |
|------|-------------|
| `-p`, `--port N` | Listen port (default: 8080) |

### Examples

```bash
moat proxy start
moat proxy start --port 9000
```

### moat proxy stop

Stop the routing proxy.

```
moat proxy stop
```

### moat proxy status

Show proxy status and registered agents.

```
moat proxy status
```

---

---

## moat deps

Manage dependencies. See [Dependencies](../concepts/06-dependencies.md) for details on the dependency system.

### moat deps list

List available dependencies from the registry.

```
moat deps list [flags]
```

### Flags

| Flag | Description |
|------|-------------|
| `--type TYPE` | Filter by dependency type (runtime, npm, apt, github-binary, go-install, uv-tool, custom, meta) |

### moat deps info

Show detailed information about a dependency.

```
moat deps info <name>
```

### Examples

```bash
# List all dependencies
moat deps list

# List only runtimes
moat deps list --type runtime

# List npm packages
moat deps list --type npm

# Show details for node
moat deps info node

# Show details for a meta dependency
moat deps info go-extras
```

---

## moat system

Low-level system commands.

### moat system images

List moat-managed container images.

```
moat system images
```

### moat system containers

List moat containers.

```
moat system containers
```

### moat system clean-temp

Clean up orphaned temporary directories.

```
moat system clean-temp [flags]
```

Moat creates temporary directories in `/tmp` for AWS credentials, Claude configuration, and Codex configuration. These are normally cleaned up when a run completes, but may accumulate if moat crashes.

This command scans for and removes temporary directories matching these patterns:
- `agentops-aws-*` - AWS credential helper directories
- `moat-claude-staging-*` - Claude configuration staging directories
- `moat-codex-staging-*` - Codex configuration staging directories

Only directories older than `--min-age` are removed.

#### Flags

| Flag | Description |
|------|-------------|
| `--min-age DURATION` | Minimum age of temp directories to clean (default: 1h) |
| `--dry-run` | Show what would be cleaned without removing anything |
| `-f`, `--force` | Skip confirmation prompt |

#### Examples

```bash
# Show orphaned temp directories (dry run)
moat system clean-temp --dry-run

# Clean directories older than 24 hours
moat system clean-temp --min-age=24h

# Clean with automatic confirmation
moat system clean-temp --force

# Clean directories older than 1 week
moat system clean-temp --min-age=168h
```

---

## moat doctor

Diagnostic information about the Moat environment.

```
moat doctor [flags]
```

Shows version, container runtime status, credential status, Claude Code configuration, and recent runs. All sensitive information is automatically redacted.

### Flags

| Flag | Description |
|------|-------------|
| `-v`, `--verbose` | Show verbose output including JWT claims |

### Examples

```bash
moat doctor
moat doctor --verbose
```

### Subcommands

#### moat doctor claude

Diagnose Claude Code authentication and configuration issues in moat containers.

```
moat doctor claude [flags]
```

Compares your host Claude Code configuration against what's available in moat containers to identify authentication problems. Checks host `~/.claude.json` fields, credential status (OAuth vs API key, expiration), and field mapping via the host config allowlist.

**Flags:**

| Flag | Description |
|------|-------------|
| `--verbose` | Show full configuration diff and all checked fields |
| `--json` | Output results as JSON for scripting |
| `--test-container` | Launch a real container to test authentication end-to-end (~$0.0001 cost) |

**Exit codes:**

| Code | Meaning |
|------|---------|
| 0 | All checks passed |
| 1 | Configuration issues detected |
| 2 | Container authentication test failed (`--test-container` only) |

**Examples:**

```bash
# Basic diagnostics
moat doctor claude

# Full field-level diff
moat doctor claude --verbose

# JSON output for scripting
moat doctor claude --json

# End-to-end container auth test
moat doctor claude --test-container

# Combine flags
moat doctor claude --test-container --verbose
```
