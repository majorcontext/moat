---
title: "Running Claude Code"
navTitle: "Claude Code"
description: "Run Claude Code in an isolated container with credential injection."
keywords: ["moat", "claude code", "anthropic", "ai agent", "coding assistant"]
---

# Running Claude Code

This guide covers running Claude Code in a Moat container.

## Prerequisites

- Moat installed
- Claude Code installed on your host machine with an active subscription (Claude Pro or Max), OR an Anthropic API key

## Granting credentials

Moat uses two separate credential providers for Claude Code:

- `moat grant claude` -- OAuth tokens for Claude Pro/Max subscribers
- `moat grant anthropic` -- API keys from console.anthropic.com

### Claude subscription (OAuth)

Run `moat grant claude` to authenticate with a Claude subscription. If the Claude CLI is installed, you see a menu:

```bash
$ moat grant claude

Choose authentication method:

  1. Claude subscription (OAuth token)
     Runs 'claude setup-token' to get a long-lived token.

  2. Existing OAuth token
     Paste a token from a previous 'claude setup-token' run.

  3. Import existing Claude Code credentials
     Import OAuth tokens from your local Claude Code installation.

Enter choice [1-3]:
```

Option 3 only appears if Claude Code credentials are found on your machine. Option 1 only appears if the Claude CLI is installed.

**Option 1** runs `claude setup-token`, which may open a browser:

```bash
Running 'claude setup-token' to obtain authentication token...
This may open a browser for authentication.

Validating OAuth token...
OAuth token is valid.

Claude credential acquired via setup-token.
You can now run 'moat claude' to start Claude Code.
```

**Option 2** prompts you to paste an OAuth token you already obtained.

**Option 3** imports credentials from your local Claude Code installation:

```bash
Found Claude Code credentials.
  Subscription: claude_pro
  Expires: 2026-02-15T10:30:00Z

Validating OAuth token...
OAuth token is valid.

Claude Code credentials imported.
```

Note: Imported tokens do not auto-refresh. When the token expires, run a Claude Code session on your host machine to refresh it, then run `moat grant claude` again to import the new token.

### Anthropic API key

Run `moat grant anthropic` to use an API key. This goes straight to the API key prompt:

```bash
$ moat grant anthropic

Enter your Anthropic API key.
You can find or create one at: https://console.anthropic.com/settings/keys

API Key: sk-ant-api...

Validating API key...
API key is valid.
```

You can also set `ANTHROPIC_API_KEY` in your environment before running the command.

### How credentials are injected

The actual credential is never in the container environment. Moat's proxy intercepts requests to Anthropic's API and injects the real token at the network layer. See [Credential management](../concepts/02-credentials.md) for details.

## Generating moat.yaml

Use `moat init` to auto-generate a `moat.yaml` for your project:

```bash
moat init ./my-project
```

This scans the project, detects its dependencies and tools, and generates a configuration file using AI. Requires at least one credential granted (e.g., `moat grant claude` or `moat grant anthropic`).

## Running Claude Code

### Interactive mode

Start Claude Code in the current directory:

```bash
moat claude
```

Start in a specific project:

```bash
moat claude ./my-project
```

Claude Code launches in interactive mode with full access to the mounted workspace.

### Non-interactive mode

Run with a prompt:

```bash
moat claude -p "explain this codebase"
moat claude -p "fix the failing tests"
moat claude -p "add input validation to the user registration form"
```

Claude Code executes the prompt and exits when complete.

### Permission handling

By default, `moat claude` runs with `--dangerously-skip-permissions` enabled. This skips Claude Code's per-tool confirmation prompts that normally ask before each file edit, command execution, or network request.

**Security properties:**

The container runs as a non-root user with filesystem access limited to the mounted workspace. Credentials are injected at the network layer and never appear in the container environment. See [Security model](../concepts/08-security.md) for the full threat model.

**Restoring manual approval:**

If you prefer Claude Code's default confirmation behavior, use the `--noyolo` flag:

```bash
moat claude --noyolo ./my-project
```

With `--noyolo`, Claude Code prompts for confirmation before each potentially destructive operation, just as it would when running directly on your host machine.

### Resuming sessions

When Claude Code exits, Moat captures the session ID from the Claude projects directory on the host filesystem. You can resume a previous session by run name:

```bash
moat claude --resume my-feature
```

This looks up the stored Claude session UUID for the run named `my-feature` and passes it to Claude Code. You can also pass a raw Claude session UUID directly:

```bash
moat claude --resume ae150251-d90a-4f85-a9da-2281e8e0518d
```

To continue the most recent conversation without specifying a session:

```bash
moat claude --continue
```

### Named runs

Give your run a name for reference:

```bash
moat claude --name feature-auth ./my-project
```

The name appears in `moat list` and makes it easier to manage multiple runs.

### Non-interactive runs

Run Claude Code non-interactively with a prompt:

```bash
moat claude -p "fix the failing tests" ./my-project
```

Monitor progress:

```bash
$ moat list
NAME          RUN ID              STATE    AGE
feature-auth  run_a1b2c3d4e5f6   running  5m ago

$ moat logs -f run_a1b2c3d4e5f6
```

## Adding GitHub access

Grant GitHub access so Claude Code can interact with repositories:

```bash
moat claude --grant github ./my-project
```

This injects GitHub credentials alongside Anthropic credentials. Claude Code can:

- Clone repositories
- Push commits
- Create pull requests
- Access private repositories

Configure in `moat.yaml` for repeated use:

```yaml
name: my-claude-project

grants:
  - anthropic
  - github
```

Then:

```bash
moat claude ./my-project
```

## Using an LLM proxy

Route Claude Code API traffic through a host-side proxy for caching, logging, or policy enforcement. Tools like [Headroom](https://github.com/chopratejas/headroom) sit between Claude Code and the Anthropic API.

Install and start Headroom:

```bash
pip install "headroom-ai[all]"
headroom proxy --port 8787
```

Configure `claude.base_url` in `moat.yaml`:

```yaml
grants:
  - claude

claude:
  base_url: http://localhost:8787
```

Moat handles the details:

- Sets `ANTHROPIC_BASE_URL` inside the container
- Routes traffic through a relay on the Moat proxy (`localhost` works because the relay runs on the host)
- Injects credentials for both `api.anthropic.com` and the proxy host

Start the proxy on your host, then run as usual:

```bash
moat claude ./my-project
```

## Adding SSH access

For SSH-based git operations:

```bash
moat grant ssh --host github.com
moat claude --grant ssh:github.com ./my-project
```

Claude Code can use `git@github.com:...` URLs for cloning and pushing.

## Plugin management

Moat supports Claude Code plugins, with automatic discovery of plugins installed on your host machine.

### Host plugin inheritance

Plugins you install via Claude Code on your host machine are automatically available in Moat containers:

```bash
# Install a plugin on your host
claude plugin marketplace add owner/repo
claude plugin enable plugin-name@repo
```

The next time you run `moat claude`, the plugin is available inside the container. Moat reads:

- `~/.claude/plugins/known_marketplaces.json` — Marketplaces registered via `claude plugin marketplace add`
- `~/.claude/settings.json` — Plugin enable/disable settings

No additional configuration required. Use `--rebuild` to update the container image after installing new plugins.

### Explicit plugin configuration

For reproducible builds or CI environments, configure plugins explicitly in `moat.yaml`:

```yaml
claude:
  plugins:
    "plugin-name@marketplace": true
```

Settings in `moat.yaml` override host settings, giving you control over exactly which plugins are available.

### Marketplaces

Configure additional plugin marketplaces:

```yaml
claude:
  marketplaces:
    custom:
      source: github
      repo: owner/repo
      ref: main
```

Marketplaces are cloned during image build. Use `--rebuild` to update after changing marketplace configuration.

## Language servers

Moat includes prepackaged language server configurations that give Claude Code access to code intelligence features like go-to-definition, find-references, and diagnostics. Language servers are installed as Claude Code plugins during image build.

Add `language_servers` to your `moat.yaml`:

```yaml
agent: claude
language_servers:
  - go
grants:
  - anthropic
```

Moat installs the language server binary and its runtime dependencies during image build, then enables the corresponding Claude Code plugin. No additional setup is needed.

### Available language servers

| Name | Language | Description | Dependencies installed |
|------|----------|-------------|----------------------|
| `go` | Go | Code intelligence, refactoring, diagnostics | `go`, `gopls` |
| `typescript` | TypeScript/JavaScript | Code intelligence, diagnostics | `node`, `typescript`, `typescript-language-server` |
| `python` | Python | Code intelligence, type checking, diagnostics | `python`, `pyright` |

### How it works

When you add a language server to `language_servers`:

1. Moat adds required dependencies to the image build (e.g., `go` and `gopls` for the Go language server)
2. The corresponding Claude Code plugin is enabled and baked into the container image
3. Claude Code discovers and manages the language server through its plugin system

Runtime dependencies are added automatically -- listing them in `dependencies:` is not required. Use `--rebuild` to update the container image after changing language server configuration.

### Multiple language servers

You can enable multiple language servers for polyglot projects:

```yaml
language_servers:
  - go
  - typescript
  - python
```

## MCP servers

Moat supports both remote and local MCP servers with credential injection. See [MCP servers](./09-mcp.md) for configuration and usage.

## Workspace snapshots

Moat captures workspace snapshots for recovery and rollback. See [Snapshots](./07-snapshots.md) for configuration and usage.

## Example: Code review workflow

1. Grant credentials:
   ```bash
   moat grant anthropic
   moat grant github
   ```

2. Create `moat.yaml`:
   ```yaml
   name: code-review

   grants:
     - anthropic
     - github

   snapshots:
     triggers:
       disable_pre_run: false
   ```

3. Run Claude Code with a review prompt:
   ```bash
   moat claude -p "Review the changes in the last 3 commits. Focus on security issues and suggest improvements."
   ```

4. View what Claude Code did:
   ```bash
   moat logs
   moat trace --network
   ```

## Troubleshooting

### "No Claude Code credentials found"

Claude Code is not installed or not logged in on your host machine. Either:

1. Install Claude Code and log in, then run `moat grant anthropic` again
2. Use an API key: `export ANTHROPIC_API_KEY="sk-ant-..." && moat grant anthropic`

### "Credential expired"

OAuth credentials have an expiration time. Re-grant:

```bash
moat grant claude
```

### Claude Code hangs on startup

Check that you're not running in a directory without a `moat.yaml` that specifies a conflicting configuration. Try:

```bash
moat claude --name test ~/empty-dir
```

### "Failed to install Anthropic marketplace"

Claude Code needs SSH access to GitHub to clone the official Anthropic plugin marketplace. Grant SSH access:

```bash
moat grant ssh --host github.com
```

Then add the grant to your `moat.yaml`:

```yaml
grants:
  - anthropic
  - ssh:github.com
```

Or pass it on the command line:

```bash
moat claude --grant ssh:github.com ./my-project
```

### Network errors

Verify the Anthropic credential is granted:

```bash
moat run --grant anthropic -- curl -s https://api.anthropic.com/v1/models
```

## Related guides

- [SSH access](./04-ssh.md) — Set up SSH for git operations
- [Snapshots](./07-snapshots.md) — Protect your workspace with snapshots
- [MCP servers](./09-mcp.md) — Extend Claude Code with remote and local MCP servers
- [Exposing ports](./06-ports.md) — Access services running inside containers
- [Security model](../concepts/08-security.md) — Container isolation and credential injection
