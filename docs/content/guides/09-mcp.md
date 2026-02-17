---
title: "MCP servers"
navTitle: "MCP"
description: "Configure remote, host-local, and sandbox-local MCP (Model Context Protocol) servers in Moat."
keywords: ["moat", "mcp", "model context protocol", "mcp servers", "credential injection", "sandbox-local", "language servers"]
---

# MCP servers

MCP (Model Context Protocol) servers extend AI agents with additional tools and context. An MCP server exposes capabilities -- file search, database queries, API integrations -- that an agent can invoke during a run.

Moat supports three types of MCP servers:

- **Remote MCP servers** -- External HTTPS services accessed through Moat's credential-injecting proxy
- **Host-local MCP servers** -- Services running on the host machine, accessed via `localhost`, `127.0.0.1`, or `[::1]`
- **Sandbox-local MCP servers** -- Child processes running inside the container

For common language servers, Moat provides [prepackaged configurations](#prepackaged-language-servers) that handle installation and setup automatically.

This guide covers configuring all three types, granting credentials, and troubleshooting common issues.

## Prerequisites

- Moat installed
- An agent configured (Claude Code, Codex, or Gemini)
- For remote MCP servers: the server URL and any required credentials
- For sandbox-local MCP servers: the server package name or executable

## Remote MCP servers

Remote MCP servers are external HTTPS services declared at the top level of `agent.yaml`. Moat injects credentials at the network layer -- the agent never sees raw tokens.

### 1. Grant credentials

Store credentials for the MCP server with `moat grant mcp`:

```bash
$ moat grant mcp context7
Enter credential for MCP server 'context7': ***************
MCP credential 'mcp-context7' saved to ~/.moat/credentials/mcp-context7.enc
```

The credential is encrypted and stored locally. The grant name follows the pattern `mcp-<name>`.

For public MCP servers that do not require authentication, skip this step.

### 2. Configure in agent.yaml

Declare the MCP server at the top level of `agent.yaml` (not under `claude:`, `codex:`, or `gemini:`):

```yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY
```

**Required fields:**

| Field | Description |
|-------|-------------|
| `name` | Unique identifier for the server |
| `url` | HTTPS endpoint (HTTP is not allowed) |

**Optional fields:**

| Field | Description |
|-------|-------------|
| `auth.grant` | Grant name to use (format: `mcp-<name>`) |
| `auth.header` | HTTP header name for credential injection |

Omit the `auth` block for public MCP servers that do not require authentication:

```yaml
mcp:
  - name: public-tools
    url: https://public.example.com/mcp
```

### 3. Run the agent

```bash
moat claude ./my-project
```

No additional flags are needed. Moat reads the `mcp:` section from `agent.yaml` and configures the proxy relay automatically.

### Multiple remote servers

Configure multiple MCP servers in a single `agent.yaml`:

```yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY

  - name: notion
    url: https://notion-mcp.example.com
    auth:
      grant: mcp-notion
      header: Notion-Token
```

Grant all required credentials before running:

```bash
moat grant mcp context7
moat grant mcp notion
```

## Host-local MCP servers

Host-local MCP servers run on the host machine and are accessed from the container via `localhost`, `127.0.0.1`, or `[::1]`. Configure them the same way as remote servers, using the top-level `mcp:` section. HTTP URLs are allowed for host-local addresses:

```yaml
mcp:
  - name: my-local-server
    url: http://localhost:8080/mcp
```

Authentication is optional for host-local servers. Add an `auth` block if the server requires credentials.

## How the relay works

Some agent HTTP clients do not respect `HTTP_PROXY` for MCP connections, so Moat routes MCP traffic through a local relay endpoint on the proxy. The agent connects to the relay, the relay injects credentials from the grant store, and forwards requests to the real MCP server. The agent never has access to raw credentials, and all MCP traffic appears in network traces and audit logs.

See [Proxy architecture](../concepts/09-proxy.md) for details on how the relay works.

## Sandbox-local MCP servers

Sandbox-local MCP servers run as child processes inside the container. The agent starts them directly -- no proxy is involved. Configure them under the agent-specific section of `agent.yaml`.

### 1. Install the MCP server

The server executable must be available inside the container. Use a `pre_run` hook to install it:

```yaml
hooks:
  pre_run: npm install -g @modelcontextprotocol/server-filesystem
```

Alternatively, include the server in your project's dependencies so it is installed alongside your code.

### 2. Configure in agent.yaml

Declare the MCP server under the agent section (`claude:`, `codex:`, or `gemini:`):

#### Claude Code

```yaml
claude:
  mcp:
    filesystem:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
      cwd: /workspace
```

Moat writes the server configuration to `.claude.json` inside the container. Claude Code discovers it automatically.

#### Codex

```yaml
codex:
  mcp:
    filesystem:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
      cwd: /workspace
```

Moat writes the server configuration to `.mcp.json` in the workspace directory.

#### Gemini

```yaml
gemini:
  mcp:
    filesystem:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
      cwd: /workspace
```

Moat writes the server configuration to `.mcp.json` in the workspace directory.

### 3. Run the agent

```bash
moat claude ./my-project
```

The agent starts the MCP server process inside the container and connects to it over stdio.

### Injecting credentials

Use the `grant` field to inject a credential as an environment variable:

```yaml
claude:
  mcp:
    github_server:
      command: /path/to/github-mcp-server
      grant: github
      cwd: /workspace
```

When `grant` is specified, Moat sets the corresponding environment variable automatically:

| Grant | Environment variable |
|-------|---------------------|
| `github` | `GITHUB_TOKEN` |
| `anthropic` | `ANTHROPIC_API_KEY` |
| `openai` | `OPENAI_API_KEY` |
| `gemini` | `GEMINI_API_KEY` |

The grant must also appear in the top-level `grants:` list.

### Environment variables and secrets

Pass environment variables directly or interpolate from the `secrets:` section:

```yaml
secrets:
  MY_API_KEY: op://Dev/MyService/api-key

claude:
  mcp:
    my_server:
      command: /path/to/server
      env:
        API_KEY: ${secrets.MY_API_KEY}
        DEBUG: "true"
      cwd: /workspace
```

### Sandbox-local MCP server fields

| Field | Type | Description |
|-------|------|-------------|
| `command` | `string` | Server executable path (required) |
| `args` | `array[string]` | Command arguments |
| `env` | `map[string]string` | Environment variables (supports `${secrets.NAME}` interpolation) |
| `grant` | `string` | Credential to inject as an environment variable |
| `cwd` | `string` | Working directory for the server process |

## Observability

All remote MCP traffic flows through the proxy, so it appears in network traces:

```bash
$ moat trace --network

[10:23:44.512] GET https://mcp.context7.com/mcp 200 (89ms)
```

Credential injection events are recorded in audit logs:

```bash
$ moat audit

[10:23:44.500] credential.injected grant=mcp-context7 host=mcp.context7.com header=CONTEXT7_API_KEY
```

Sandbox-local MCP server output appears in container logs:

```bash
moat logs
```

## Prepackaged language servers

Language servers provide code intelligence (go-to-definition, find-references, diagnostics) to AI agents through MCP. Moat includes prepackaged configurations that handle installation and setup automatically.

Add `language_servers` to your `agent.yaml`:

```yaml
language_servers:
  - gopls
grants:
  - anthropic
```

Moat installs the language server and its runtime dependencies (e.g., `go` and `gopls`) during image build, then configures it as a stdio MCP server in `.claude.json`. No additional setup is needed.

### Available language servers

| Name | Language | Description | Dependencies installed |
|------|----------|-------------|----------------------|
| `gopls` | Go | Code intelligence, refactoring, diagnostics via `gopls mcp` | `go`, `gopls` |

### How it works

When you add a language server to `language_servers`:

1. Moat adds required dependencies to the image build (e.g., `go` and `gopls` for the gopls server)
2. The server is registered as an MCP server in `.claude.json` with `type: stdio`
3. The agent starts the server process and communicates with it over stdin/stdout

No proxy or network configuration is needed -- the language server runs inside the container alongside the agent.

> **Note:** Prepackaged language servers are currently supported with Claude Code only.

## Troubleshooting

### MCP server not appearing in agent

- Verify the MCP server is declared in `agent.yaml` (remote servers at the top level, sandbox-local servers under the agent section)
- For remote servers, check that the grant exists: `moat grant list` should show `mcp-{name}`
- Check container logs for configuration errors: `moat logs`

### Authentication failures (401 or 403)

- Verify the grant exists and the credential is correct
- Revoke and re-grant: `moat revoke mcp-{name}` then `moat grant mcp {name}`
- Verify the `url` in `agent.yaml` matches the actual MCP server endpoint
- Verify the `header` name matches what the MCP server expects

### Stub credential in error messages

If you see `moat-stub-{grant}` in error output, the proxy did not replace the stub with a real credential. Check:

- The `url` in `agent.yaml` matches the host the agent is connecting to
- The `header` name matches what the agent sends
- The grant name in `auth.grant` matches the stored credential (`mcp-{name}`)

### Connection refused

- The proxy may not be running. Check `moat list` to verify the run is active
- For remote servers, verify the URL is reachable from your network
- For sandbox-local servers, verify the `command` path exists inside the container

### Sandbox-local server not starting

- Verify the server executable is installed inside the container. Use a `pre_run` hook or include it in your project dependencies.
- Check that `command` is an absolute path or available on `$PATH` inside the container
- Review container logs with `moat logs` for startup errors

### SSE streaming issues

Remote MCP servers that use SSE (Server-Sent Events) for streaming responses are supported through the proxy relay. If streaming appears stalled:

- Check that the MCP server URL is correct and the server supports SSE
- Review network traces with `moat trace --network` to see if requests reach the server
- Verify no intermediate firewalls or proxies are buffering SSE responses

## Agent-specific notes

### Claude Code

Remote MCP servers, sandbox-local MCP servers, and prepackaged language servers are all configured in the generated `.claude.json`. Remote servers use `type: http` with relay URLs pointing to the proxy. Sandbox-local and language servers use `type: stdio`. Claude Code discovers all types through this config file automatically. See [Running Claude Code](./01-claude-code.md) for other Claude Code configuration options.

### Codex

Sandbox-local MCP servers are configured under `codex.mcp:` in `agent.yaml`. Configuration is written to `.mcp.json` in the workspace. See [Running Codex](./02-codex.md) for Codex-specific configuration.

### Gemini

Sandbox-local MCP servers are configured under `gemini.mcp:` in `agent.yaml`. Configuration is written to `.mcp.json` in the workspace. See [Running Gemini](./03-gemini.md) for Gemini-specific configuration.

## Related guides

- [Credential management](../concepts/02-credentials.md) -- How credential injection works
- [Observability](../concepts/03-observability.md) -- Network traces and audit logs
- [agent.yaml reference](../reference/02-agent-yaml.md) -- Full field reference for `mcp:`, `claude.mcp:`, `codex.mcp:`, `gemini.mcp:`, and `language_servers:`
- [CLI reference](../reference/01-cli.md) -- `moat grant mcp` command details
- [Running Claude Code](./01-claude-code.md) -- Claude Code agent guide
- [Running Codex](./02-codex.md) -- Codex agent guide
- [Running Gemini](./03-gemini.md) -- Gemini agent guide
