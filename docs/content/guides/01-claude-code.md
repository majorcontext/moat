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

## Granting Anthropic credentials

Run `moat grant anthropic` to configure authentication. You'll see a menu with available options:

```bash
$ moat grant anthropic

Choose authentication method:

  1. Claude subscription (recommended)
     Uses 'claude setup-token' to get a long-lived OAuth token.
     Requires a Claude Pro/Max subscription.

  2. Anthropic API key
     Use an API key from console.anthropic.com
     Billed per token to your API account.

  3. Import existing Claude Code credentials
     Use OAuth tokens from your local Claude Code installation.

Enter choice [1, 2, or 3]:
```

### Option 1: Claude subscription (recommended)

If you have a Claude Pro or Max subscription and the Claude CLI installed, choose option 1. This runs `claude setup-token` to obtain a long-lived OAuth token:

```bash
Enter choice [1, 2, or 3]: 1

Running 'claude setup-token' to obtain authentication token...
This may open a browser for authentication.

Anthropic credential saved to ~/.moat/credentials/anthropic.enc

You can now run 'moat claude' to start Claude Code.
```

### Option 2: API key

If you have an Anthropic API key (from console.anthropic.com):

```bash
Enter choice [1, 2, or 3]: 2

Enter your Anthropic API key.
You can find or create one at: https://console.anthropic.com/settings/keys

API Key: sk-ant-api...

Validate API key with a test request? This makes a small API call. [Y/n]: y

Validating API key...
API key is valid.
Anthropic API key saved to ~/.moat/credentials/anthropic.enc
```

You can also set `ANTHROPIC_API_KEY` in your environment before running the command.

### Option 3: Import existing credentials

If you already have Claude Code installed and logged in locally, you can import your existing OAuth credentials:

```bash
Enter choice [1, 2, or 3]: 3

Found Claude Code credentials.
  Subscription: claude_pro
  Expires: 2026-02-15T10:30:00Z

Claude Code credentials imported to ~/.moat/credentials/anthropic.enc
```

Note: Imported tokens do not auto-refresh. When the token expires, run a Claude Code session on your host machine to refresh it, then run `moat grant anthropic` again to import the new token.

### How credentials are injected

The actual credential is never in the container environment. Moat's proxy intercepts requests to Anthropic's API and injects the real token at the network layer. See [Credential management](../concepts/02-credentials.md) for details.

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

Claude Code sessions persist across container runs. Moat mounts the Claude projects directory (`~/.claude/projects/`) between host and container, so conversation logs are always available.

**Continue the most recent conversation:**

```bash
moat resume
```

This is a shortcut for `moat claude --continue`. It starts a new container and passes `--continue` to Claude Code, which picks up the most recent conversation from the synced session logs.

**Continue from `moat claude`:**

```bash
moat claude --continue
moat claude -c
```

**Resume a specific session by ID:**

```bash
moat claude --resume ae150251-d90a-4f85-a9da-2281e8e0518d
```

Session IDs are stored in `.jsonl` files under `~/.claude/projects/`. Claude Code displays the session ID when you start a conversation.

### Named runs

Give your run a name for reference:

```bash
moat claude --name feature-auth ./my-project
```

The name appears in `moat list` and makes it easier to manage multiple runs.

### Background runs

Run Claude Code in the background:

```bash
moat claude -d ./my-project
```

Reattach later:

```bash
$ moat list
NAME          RUN ID              STATE    AGE
feature-auth  run_a1b2c3d4e5f6   running  5m

$ moat attach run_a1b2c3d4e5f6
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

Configure in `agent.yaml` for repeated use:

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

For reproducible builds or CI environments, configure plugins explicitly in `agent.yaml`:

```yaml
claude:
  plugins:
    "plugin-name@marketplace": true
```

Settings in `agent.yaml` override host settings, giving you control over exactly which plugins are available.

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

### List plugins

View which plugins are configured:

```bash
moat claude plugins list ./my-project
```

This shows plugins from all sources: host settings, project settings, and `agent.yaml`.

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

2. Create `agent.yaml`:
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
moat grant anthropic
```

### Claude Code hangs on startup

Check that you're not running in a directory without an `agent.yaml` that specifies a conflicting configuration. Try:

```bash
moat claude --name test ~/empty-dir
```

### "Failed to install Anthropic marketplace"

Claude Code needs SSH access to GitHub to clone the official Anthropic plugin marketplace. Grant SSH access:

```bash
moat grant ssh --host github.com
```

Then add the grant to your `agent.yaml`:

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
