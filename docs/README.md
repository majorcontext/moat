# Moat Documentation

## Contents

### Getting Started

- [Introduction](./content/getting-started/01-introduction.md) — What Moat does, core concepts, basic usage
- [Installation](./content/getting-started/02-installation.md) — Install on macOS, Linux, or Windows
- [Quick Start](./content/getting-started/03-quick-start.md) — Run your first agent in 5 minutes
- [Choosing a Tool](./content/getting-started/04-comparison.md) — Compare Moat with packnplay, Leash, and Dev Containers

### Concepts

- [Sandboxing](./content/concepts/01-sandboxing.md) — Container isolation with Docker and Apple containers
- [Credential Management](./content/concepts/02-credentials.md) — Secure credential storage and network-layer injection
- [Observability](./content/concepts/03-observability.md) — Audit logging, network traces, and execution spans
- [Networking](./content/concepts/05-networking.md) — Network policies and hostname routing
- [Runtimes](./content/concepts/07-runtimes.md) — Container runtimes, sandbox modes, and platform differences
- [Security Model](./content/concepts/08-security.md) — Threat model, trust boundaries, and defense in depth
- [Proxy Architecture](./content/concepts/09-proxy.md) — TLS interception, credential injection, and MCP relay

### Guides

- [Running Claude Code](./content/guides/01-claude-code.md) — Use Claude Code in isolated containers
- [Running Codex](./content/guides/02-codex.md) — Use OpenAI Codex CLI in isolated containers
- [Running Gemini](./content/guides/03-gemini.md) — Use Google Gemini CLI in isolated containers
- [Running Pi](./content/guides/16-pi.md) — Use the Pi coding agent with an existing Anthropic or OpenAI grant
- [Running GitHub Copilot CLI](./content/guides/17-copilot.md) — Use Copilot CLI with the GitHub grant
- [SSH Access](./content/guides/04-ssh.md) — Grant SSH access without exposing private keys
- [Secrets Management](./content/guides/05-secrets.md) — Pull secrets from 1Password and AWS SSM
- [Exposing Ports](./content/guides/06-ports.md) — Access services running inside agent containers
- [Workspace Snapshots](./content/guides/07-snapshots.md) — Point-in-time recovery for workspaces
- [Service Dependencies](./content/guides/08-services.md) — Run databases and caches alongside agents
- [MCP Servers](./content/guides/09-mcp.md) — Configure remote and local MCP servers
- [Lifecycle Hooks](./content/guides/10-hooks.md) — Run commands during image build and container startup
- [Observability](./content/guides/11-observability.md) — View logs, traces, and audit data
- [Git Worktrees](./content/guides/12-worktrees.md) — Parallel work on multiple branches, each in its own container
- [Workspace Sharing](./content/guides/13-workspace-sharing.md) — Isolate platform-specific dependency directories with mount excludes
- [Volume-Mode Workspaces](./content/guides/15-volume-workspaces.md) — Run against an isolated copy of the workspace in an ephemeral Docker volume
- [Multi-Agent Sessions](./content/guides/14-multi-agent.md) — Run a second agent inside a running container with `moat join`
- [Recipes](./content/guides/13-recipes.md) — Complete `moat.yaml` examples for common project types

### Reference

- [CLI Reference](./content/reference/01-cli.md) — Complete command and flag reference
- [moat.yaml Reference](./content/reference/02-moat-yaml.md) — Configuration file options
- [Environment Variables](./content/reference/03-environment.md) — Moat and container environment variables
- [Grants Reference](./content/reference/04-grants.md) — All grant types, credential sources, and injection details
- [Mount Syntax](./content/reference/05-mounts.md) — Mount format, path resolution, and access modes
- [Dependencies Reference](./content/reference/06-dependencies.md) — Dependency types, version resolution, and base images
- [Provider YAML Reference](./content/reference/07-provider-yaml.md) — Schema for YAML-defined credential providers
- [Troubleshooting](./content/reference/08-troubleshooting.md) — Error-to-fix lookup for proxy, auth, credential, and runtime errors

---

## Directory Structure

```
docs/
  README.md                     # This file
  STYLE-GUIDE.md                # Writing guidelines
  content/                      # User-facing documentation
    getting-started/
    concepts/
    guides/
    reference/
  plans/                        # Internal design documents (not published)
```

## Frontmatter Schema

Each documentation file includes YAML frontmatter:

```yaml
---
title: "Page Title"
navTitle: "Short Title"  # optional, for navigation menus
description: "Brief description for SEO and previews"
keywords: ["keyword1", "keyword2"]
---
```

The following are inferred from the file path:
- **slug** — From filename (e.g., `01-introduction.md` → `introduction`)
- **section** — From parent directory (e.g., `getting-started/`)
- **order** — From numeric prefix (e.g., `01-`, `02-`)
- **prev/next** — From adjacent files in the same directory

## Writing Guidelines

See [STYLE-GUIDE.md](./STYLE-GUIDE.md) for voice, tone, and formatting conventions.

Summary:
1. **Be objective** — State facts, avoid hyperbole
2. **Be respectful** — Don't disparage other tools
3. **Be factual** — Make specific, verifiable claims
4. **Be practical** — Lead with examples, explain after
5. **Test examples** — All code examples should work as written
