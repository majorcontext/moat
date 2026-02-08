---
title: "Introduction"
description: "Moat runs AI agents in isolated containers with credential injection and observability."
keywords: ["moat", "ai agents", "containers", "credentials", "observability"]
---

# Moat

Run AI agents in isolated containers with credential injection and observability.

```bash
brew tap majorcontext/moat
brew install moat
```

See [Installation](./02-installation.md) for other platforms and methods.

## What Moat does

Moat executes AI agents in containers. Each run includes:

- **Container isolation** — Code runs in Docker or Apple containers, separate from your host system
- **Credential injection** — OAuth tokens and API keys are injected at the network layer via a TLS-intercepting proxy; they never appear in the container's environment variables
- **Observability** — Every run captures container logs, HTTP request traces, and a tamper-proof audit log
- **Declarative configuration** — Define runtime, credentials, environment variables, and network policies in `agent.yaml`

## Core concept: runs

A **run** is a container execution with its associated artifacts. When you execute `moat run`, Moat:

1. Creates a container with your workspace mounted
2. Starts a TLS-intercepting proxy for credential injection
3. Routes container traffic through the proxy
4. Captures logs, network requests, and audit events
5. Stores everything in `~/.moat/runs/<run-id>/`

When the container exits, the artifacts remain. View logs with `moat logs`, inspect network requests with `moat trace --network`, or verify the audit chain with `moat audit`.

## Example

Grant GitHub access (one time):

```bash
$ moat grant github

To authorize, visit: https://github.com/login/device
Enter code: ABCD-1234

Waiting for authorization...
GitHub credential saved successfully
```

Run a command with the credential injected:

```bash
$ moat run --grant github -- curl -s https://api.github.com/user
{
  "login": "your-username",
  "id": 1234567,
  "name": "Your Name"
}
```

The GitHub token was injected into the `Authorization` header by the proxy. It never appeared in the container's environment:

```bash
$ moat run --grant github -- env | grep -i token
# (no output)
```

## Configuration

For repeated use, create an `agent.yaml` file:

```yaml
name: my-agent

dependencies:
  - node@20

grants:
  - github

env:
  NODE_ENV: development

command: ["npm", "start"]
```

Then run without flags:

```bash
moat run ./my-project
```

The `dependencies` field determines the base image (`node@20` → `node:20`). The `grants` field specifies which credentials to inject. See [agent.yaml reference](../reference/02-agent-yaml.md) for all options.

## Container runtimes

Moat supports two container runtimes:

| Platform | Runtime | Detection |
|----------|---------|-----------|
| macOS 26+ with Apple Silicon | Apple containers | Automatic |
| macOS (Intel), Linux, Windows | Docker | Automatic |

Moat detects the available runtime automatically. No configuration required.

## Use cases

Moat is designed for running AI coding agents that need:

- Access to credentials (GitHub, Anthropic, SSH keys) without exposing tokens
- Isolation from the host system
- Audit trails of what the agent did
- Network policy enforcement

The `moat claude` command provides a streamlined way to run Claude Code:

```bash
moat grant anthropic  # Import credentials from Claude Code
moat claude ./my-project
```

## What Moat is not

Moat provides container isolation, credential injection, and observability, but **does not enforce fine-grained permissions** on the actions agents can take with those credentials.

**Examples:**

- **GitHub grant** — An agent with GitHub access could delete `.git/` or force push to main. Moat injects whatever credentials you provide; the agent has the same permissions as those credentials. Protection requires GitHub-level controls (branch protection rules, repository permissions).

- **AWS grant** — An agent with a broad IAM role could delete resources. Moat assumes the role you specify and injects those temporary credentials; the agent can do anything that role allows. Protection requires IAM-level controls (scoped roles, explicit denies, resource policies).

- **File system** — Moat mounts your workspace with read-write access by default. An agent could delete or modify any file in the mounted directory.

**Fine-grained agent policies require tools like [agentsh.org](https://www.agentsh.org/)**, which provides declarative security policies for agent actions. agentsh is complementary to Moat—Moat handles container isolation and credential delivery, while agentsh enforces action-level policies. We have plans to make packaging agentsh in a Moat container straightforward.

**Moat's security model assumes:**

- The agent is semi-trusted code that should not have direct credential access
- Credentials are scoped appropriately at the service level (IAM roles, repository permissions)
- The container boundary prevents accidental credential leakage, not intentional misuse by malicious code

For high-security scenarios, combine Moat with service-level controls (branch protection, IAM policies, read-only mounts) and agent-level policy frameworks like agentsh.

## Project status

Moat is in active development. APIs and configuration formats may change. The project is open source and welcomes contributions.

## Next steps

- [Installation](./02-installation.md) — Platform-specific installation instructions
- [Quick start](./03-quick-start.md) — Guided walkthrough of your first run
- [Choosing a tool](./04-comparison.md) — Compare Moat with packnplay, Leash, and Dev Containers
