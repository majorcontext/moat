---
title: "Choosing a tool"
navTitle: "Comparison"
description: "Compare Moat with other tools for running AI coding agents in isolated environments."
keywords: ["moat", "comparison", "packnplay", "leash", "devcontainer", "sandbox"]
---

# Choosing a tool

Several tools help you run AI coding agents in isolated environments. This guide compares Moat with packnplay, Leash, and VS Code Dev Containers to help you choose the right one for your needs.

All of these are good tools solving related problems. The right choice depends on what you're optimizing for.

## Quick comparison

| Feature | Moat | packnplay | Leash | Dev Containers |
|---------|------|-----------|-------|----------------|
| **Primary goal** | Credential security + observability | Simple containerization | Policy enforcement | Development environments |
| **Credential handling** | Network-layer injection | Mount into container | Network-layer injection (Linux) | Environment variables |
| **Agent sees tokens** | No | Yes | No (Linux only) | Yes |
| **Network monitoring** | Yes | No | Yes | No |
| **Audit logging** | Tamper-proof with hash chain | No | Event streaming | No |
| **Policy language** | YAML (allow list) | None | Cedar | None |
| **Container runtime** | Docker, Apple containers | Docker | Docker, Podman, OrbStack | Docker |
| **Setup complexity** | Low | Low | Medium | Low-Medium |
| **macOS native** | Yes (Apple containers) | No | Experimental | No |

## When to use each tool

### Use Moat when

You want credentials injected at the network layer—the proxy adds authentication headers to requests, so tokens never appear in the container environment. Even if an agent logs all environment variables or gets compromised, your credentials remain safe.

You also want observability. Every HTTP request the agent makes is logged with method, URL, status, and timing. Audit logs are hash-chained and can be cryptographically verified.

```bash
# Credential never enters container
moat grant github
moat run --grant github -- curl https://api.github.com/user
```

**Best for:** Teams running agents with access to sensitive credentials (production APIs, private repos) who need audit trails.

### Use packnplay when

You want the simplest path to running coding agents in containers. packnplay focuses on ease of use with automatic worktree management and pre-configured support for multiple agents (Claude Code, Codex, Gemini CLI, and others).

The trade-off is explicit: packnplay provides no introspection or access control beyond container isolation. Credentials are mounted into containers, so agents can see them.

```bash
# Simple, but agent sees credentials
packnplay run --all-creds claude
```

**Best for:** Individual developers who want container isolation without the overhead of policy management or credential proxying.

### Use Leash when

You need fine-grained policy enforcement with Cedar policies. Leash uses eBPF on Linux to enforce policies at the kernel level—controlling which files agents can access, which processes they can run, and which network connections they can make.

```cedar
// Example Cedar policy
permit (principal, action == Action::"FileOpenReadOnly", resource)
when { resource in [ Dir::"/workspace/" ] };

forbid (principal, action == Action::"NetworkConnect", resource)
when { resource in [ Host::"*.facebook.com" ] };
```

Leash also provides a Control UI for real-time inspection of agent activity.

**Best for:** Organizations that need granular, auditable access control with custom policies.

### Use Dev Containers when

You're already using VS Code or GitHub Codespaces and want a consistent development environment. Dev Containers weren't designed specifically for AI agents, but they provide familiar tooling and work well for developers who want to run agents in the same environment they use for manual development.

```json
// .devcontainer/devcontainer.json
{
  "image": "mcr.microsoft.com/devcontainers/base:ubuntu",
  "containerEnv": {
    "ANTHROPIC_API_KEY": "${localEnv:ANTHROPIC_API_KEY}"
  }
}
```

The limitation is that credentials must be passed as environment variables or mounted files—the agent can access them directly.

**Best for:** Developers who want to use the same containerized environment for both manual development and AI agents.

## Feature details

### Credential security

How each tool handles sensitive credentials:

| Tool | Approach | Agent access |
|------|----------|--------------|
| **Moat** | TLS proxy injects headers at network layer | Agent sees placeholder value |
| **packnplay** | Mounts credentials read-only into container | Agent can read mounted files |
| **Leash** | TLS proxy injects headers (Linux only) | Agent sees placeholder (Linux); full access (macOS) |
| **Dev Containers** | Environment variables or mounted files | Agent has full access |

If you need to prevent tokens from appearing in the container environment, Moat and Leash (on Linux) inject credentials at the network layer, keeping real tokens outside the container entirely.

### Observability

What you can see about agent activity:

| Tool | Network requests | File access | Audit trail |
|------|-----------------|-------------|-------------|
| **Moat** | Full HTTP logging | No | Hash-chained, verifiable |
| **packnplay** | None | No | None |
| **Leash** | Full HTTP logging | Yes (eBPF) | Event streaming |
| **Dev Containers** | None | No | None |

Leash provides the most comprehensive observability with kernel-level visibility into file and process activity. Moat focuses on network-layer observability with tamper-proof audit logs.

### Policy enforcement

How each tool controls what agents can do:

| Tool | Network policy | File policy | Process policy |
|------|---------------|-------------|----------------|
| **Moat** | Allow list in YAML | Container boundary | Container boundary |
| **packnplay** | None | Container boundary | Container boundary |
| **Leash** | Cedar policies + eBPF | Cedar policies + eBPF | Cedar policies + eBPF |
| **Dev Containers** | None | Container boundary | Container boundary |

Leash offers the most granular control. Moat provides network-level policies. packnplay and Dev Containers rely on container isolation alone.

### Platform support

| Tool | macOS (Intel) | macOS (Apple Silicon) | Linux |
|------|--------------|----------------------|-------|
| **Moat** | Docker | Docker or Apple containers | Docker |
| **packnplay** | Docker | Docker | Docker |
| **Leash** | Docker | Docker or native (experimental) | Docker + eBPF |
| **Dev Containers** | Docker | Docker | Docker |

Moat's Apple container support on macOS 26+ provides native virtualization without Docker Desktop. Leash's macOS native mode is experimental and lacks some features (no credential injection).

## Migration paths

### From Dev Containers to Moat

If you're using Dev Containers and want better credential security:

1. Your existing `devcontainer.json` can inform your `agent.yaml` (similar concepts: features → dependencies, containerEnv → env)
2. Replace environment variable credentials with `moat grant` + `--grant` flags
3. Network requests are automatically logged

### From packnplay to Moat

If you want credential injection and observability:

1. Replace `packnplay run --all-creds claude` with `moat grant anthropic && moat claude`
2. Credentials are now injected at network layer instead of mounted
3. Use `moat trace --network` to see what the agent did

### From Moat to Leash

If you need file-level and process-level policies:

1. Leash's Cedar policies provide finer control than Moat's YAML
2. eBPF enforcement on Linux gives kernel-level visibility
3. Trade-off: more complex setup and policy authoring

## Summary

- **Moat**: Credential security via network-layer injection, plus observability with tamper-proof audit logs.
- **packnplay**: Simplest setup for container isolation. No credential protection or observability.
- **Leash**: Most comprehensive policy enforcement and observability. Requires more setup.
- **Dev Containers**: Familiar tooling for VS Code users. No agent-specific security features.

Choose based on your priorities:
- **Simplicity**: packnplay or Dev Containers
- **Credential security**: Moat or Leash
- **Fine-grained policies**: Leash
- **Audit trails**: Moat or Leash

## Links

- [packnplay](https://github.com/obra/packnplay) — Jesse Vincent's containerization wrapper
- [Leash](https://github.com/strongdm/leash) — StrongDM's policy enforcement tool
- [Dev Containers](https://containers.dev/) — Development container specification
