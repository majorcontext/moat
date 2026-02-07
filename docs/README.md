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
- [Audit Logs](./content/concepts/03-audit-logs.md) — Tamper-proof logging with cryptographic hash chains
- [Observability](./content/concepts/04-observability.md) — Logs, network traces, and execution spans
- [Networking](./content/concepts/05-networking.md) — Network policies and hostname routing
- [Dependencies](./content/concepts/06-dependencies.md) — Runtime environments, tools, and the dependency registry

### Guides

- [Running Claude Code](./content/guides/01-running-claude-code.md) — Use Claude Code in isolated containers
- [Running Codex](./content/guides/02-running-codex.md) — Use OpenAI Codex CLI in isolated containers
- [SSH Access](./content/guides/03-ssh-access.md) — Grant SSH access without exposing private keys
- [Secrets Management](./content/guides/04-secrets-management.md) — Pull secrets from 1Password and AWS SSM
- [Exposing Ports](./content/guides/05-exposing-ports.md) — Access services running inside agent containers
- [Workspace Snapshots](./content/guides/06-snapshots.md) — Point-in-time recovery for workspaces

### Reference

- [CLI Reference](./content/reference/01-cli.md) — Complete command and flag reference
- [agent.yaml Reference](./content/reference/02-agent-yaml.md) — Configuration file options
- [Environment Variables](./content/reference/03-environment.md) — Moat and container environment variables

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
