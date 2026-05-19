---
title: "Running Kiro"
navTitle: "Kiro"
description: "Run the Kiro CLI in an isolated container with credential injection."
keywords: ["moat", "kiro", "ai agent", "coding assistant"]
---

# Running Kiro

This guide covers running the Kiro CLI in a Moat container.

## Prerequisites

- Moat installed
- A Kiro API token

## Granting Kiro credentials

Run `moat grant kiro` to configure authentication:

```bash
$ moat grant kiro

Enter your Kiro token: ...

Kiro token saved to ~/.moat/credentials/kiro.enc
```

You can also set `KIRO_API_KEY` in your environment before running the command:

```bash
export KIRO_API_KEY="..."
moat grant kiro
```

Kiro tokens are static credentials — there is no automatic refresh. When a token expires, re-run `moat grant kiro` to replace it.

### How credentials are injected

The actual credential is never in the container environment. Moat's proxy intercepts requests to the Kiro API and injects the real token at the network layer. The container receives only a placeholder value for `KIRO_API_KEY`. See [Credential management](../concepts/02-credentials.md) for details.

## Running Kiro

### Interactive mode

Start Kiro in the current directory:

```bash
moat kiro
```

Start in a specific project:

```bash
moat kiro ./my-project
```

Kiro launches in interactive mode with full access to the mounted workspace.

### Non-interactive mode

Run with a prompt:

```bash
moat kiro -p "explain this codebase"
moat kiro -p "fix the failing tests"
moat kiro -p "add input validation to the user registration form"
```

Kiro executes the prompt and exits when complete.

### Named runs

Give your run a name for reference:

```bash
moat kiro --name feature-auth ./my-project
```

The name appears in `moat list` and makes it easier to manage multiple runs.

## Adding GitHub access

Grant GitHub access so Kiro can interact with repositories:

```bash
moat kiro --grant github ./my-project
```

Configure in `moat.yaml` for repeated use:

```yaml
name: my-kiro-project
agent: kiro

grants:
  - kiro
  - github
```

Then:

```bash
moat kiro ./my-project
```

## Local MCP servers

Run MCP servers as processes inside the container using the `kiro.mcp` section in `moat.yaml`:

```yaml
name: my-kiro-project
agent: kiro

grants:
  - kiro

kiro:
  mcp:
    filesystem:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
```

The MCP server configuration is written to `~/.kiro/settings/mcp.json` inside the container.

## Remote MCP servers

Remote HTTP MCP servers are configured with the top-level `mcp:` field and relayed through the Moat proxy:

```yaml
name: my-kiro-project
agent: kiro

grants:
  - kiro

mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY
```

Store the MCP server credential before running:

```bash
moat grant mcp context7
```

## Network hosts

Kiro uses the following hosts. Moat allows them automatically when the `kiro` grant is configured:

| Host | Purpose |
|------|---------|
| `q.us-east-1.amazonaws.com` | Kiro API (credential injection applies to this host) |
| `cognito-identity.*.amazonaws.com` | Cognito identity (allowlisted, no credential injection) |
| `cli.kiro.dev` | Kiro CLI installer |

**Note on regions:** Credential injection is scoped to `q.us-east-1.amazonaws.com`. This is the only region currently in use. If Kiro adds support for additional regions in the future, their hostnames must be listed explicitly — the credential injection layer does not wildcard-match hostnames.

## Allowing additional hosts

To allow access to additional hosts:

```bash
moat kiro --allow-host example.com ./my-project
```

Or configure in `moat.yaml`:

```yaml
network:
  rules:
    - example.com
    - "*.internal.corp"
```

## Workspace snapshots

Moat captures workspace snapshots for recovery and rollback. See [Snapshots](./07-snapshots.md) for configuration and usage.

## Related guides

- [SSH access](./04-ssh.md) — Set up SSH for git operations
- [Snapshots](./07-snapshots.md) — Protect your workspace with snapshots
- [MCP servers](./09-mcp.md) — Configure remote and local MCP servers
- [Exposing ports](./06-ports.md) — Access services running inside containers
