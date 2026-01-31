---
title: "Running Claude Code"
description: "Run Claude Code in an isolated container with credential injection."
keywords: ["moat", "claude code", "anthropic", "ai agent", "coding assistant"]
---

# Running Claude Code

This guide covers running Claude Code in a Moat container. Claude Code is Anthropic's AI coding assistant that can read, write, and execute code.

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

Moat sets `CLAUDE_CODE_OAUTH_TOKEN` (for OAuth) or `ANTHROPIC_API_KEY` (for API keys) in the container environment. These variables contain a placeholder value—the actual credential is never in the container environment. The proxy intercepts requests to Anthropic's API and injects the real token at the network layer.

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

Claude Code launches in interactive mode. Use it as you would normally—it has full access to the mounted workspace.

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

The container provides these isolation boundaries:

- Runs as a non-root user (`moatuser`, UID 5000) inside the container
- Filesystem access is limited to the mounted workspace, plus read-only mounts for credential helper configs (e.g., `~/.config/gh/config.yml`, AWS credential process scripts)
- SSH private keys remain on the host—the container can request signatures via an SSH agent proxy but cannot extract key material
- Credentials are injected at the network layer via proxy and do not appear in the container environment (see [Credential management](../concepts/02-credentials.md) for details on AWS `credential_process`)
- Standard container isolation separates the run from other containers and host processes

Per-operation prompts require user confirmation for each action. The container already limits access to the mounted workspace and routes credentials through the proxy.

**Restoring manual approval:**

If you prefer Claude Code's default confirmation behavior, use the `--noyolo` flag:

```bash
moat claude --noyolo ./my-project
```

With `--noyolo`, Claude Code prompts for confirmation before each potentially destructive operation, just as it would when running directly on your host machine.

### Named sessions

Give your session a name for reference:

```bash
moat claude --name feature-auth ./my-project
```

The name appears in `moat list` and makes it easier to manage multiple sessions.

### Background sessions

Run Claude Code in the background:

```bash
moat claude -d ./my-project
```

Reattach later:

```bash
$ moat list
NAME          RUN ID              STATE    SERVICES
feature-auth  run_a1b2c3d4e5f6   running

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
name: my-claude-session

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

MCP (Model Context Protocol) servers extend Claude Code with additional tools and context. Moat supports two types:

1. **Local MCP servers** - Child processes running inside the container
2. **Remote MCP servers** - External HTTPS services with credential injection

### Local MCP servers

Configure local MCP servers that run as child processes:

```yaml
claude:
  mcp:
    my_server:
      command: /path/to/server
      args: ["--flag"]
      env:
        API_KEY: ${secrets.MY_API_KEY}
      grant: github
      cwd: /workspace
```

See the [agent.yaml reference](../reference/02-agent-yaml.md#claudemcp) for all configuration options.

### Remote MCP servers

Remote MCP servers are external HTTPS services that require credential injection.

#### 1. Grant credentials

Store credentials for the MCP server:

```bash
$ moat grant mcp context7
Enter credential for MCP server 'context7': ***************
MCP credential 'mcp-context7' saved to ~/.moat/credentials/mcp-context7.enc
```

#### 2. Configure in agent.yaml

Declare the MCP server at the top level (not under `claude:`):

```yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY
```

**Required fields:**
- `name`: Identifier for the server (must be unique)
- `url`: HTTPS endpoint (HTTP not allowed for security)

**Optional fields:**
- `auth.grant`: Name of grant to use (must exist via `moat grant mcp <name>`)
- `auth.header`: Header name where credential should be injected

For public MCP servers without authentication, omit the `auth` block.

#### 3. Run Claude Code

```bash
moat claude ./workspace
```

Behind the scenes:
- Container starts with `.claude.json` containing MCP configuration
- Claude Code discovers MCP servers with stub credentials
- When Claude Code makes MCP requests, the proxy replaces stubs with real credentials
- All MCP traffic is logged for audit

#### How credential injection works

Moat uses the same credential injection model for MCP as it does for API calls:

1. Credentials never enter the container environment
2. MCP configuration is written to `.claude.json` with stub credentials (`moat-stub-{grant}`)
3. When Claude Code makes an MCP request with a stub, the proxy detects it
4. Proxy replaces stub with real credential based on URL + header matching
5. MCP server receives the real credential

This ensures:
- Credentials are not in environment variables or config files
- Container code cannot access raw credentials
- Credential usage is fully auditable

#### Multiple MCP servers

Configure multiple MCP servers:

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

Grant all required credentials:

```bash
moat grant mcp context7
moat grant mcp notion
```

#### Observability

MCP connections appear in network logs:

```bash
$ moat trace --network

[10:23:44.512] GET https://mcp.context7.com/mcp 200 (89ms)
```

Credential injection is logged in audit logs:

```bash
$ moat audit

[10:23:44.500] credential.injected grant=mcp-context7 host=mcp.context7.com header=CONTEXT7_API_KEY
```

#### Troubleshooting

**MCP server not appearing:**
- Verify MCP server is declared in `agent.yaml`
- Check grant exists: `moat grants list` should show `mcp-{name}`
- Check container logs: `moat logs`

**Authentication failures (401):**
- Verify grant exists and credential is correct
- Try revoking and re-granting: `moat revoke mcp-{name}` then `moat grant mcp {name}`
- Verify URL in `agent.yaml` matches the actual MCP server endpoint

**Stub credential in errors:**
If you see `moat-stub-{grant}` in errors:
- Proxy didn't recognize the request as MCP traffic
- Verify URL in `agent.yaml` exactly matches the host being accessed
- Verify header name matches what Claude Code is sending

## Workspace snapshots

Moat can create point-in-time snapshots of your workspace. This is useful for recovering from unwanted changes.

Enable automatic snapshots:

```yaml
snapshots:
  triggers:
    disable_pre_run: false    # Snapshot before run starts
    disable_git_commits: false # Snapshot on git commits
    disable_idle: false        # Snapshot when idle
    idle_threshold_seconds: 30
```

List snapshots:

```bash
moat snapshot list run_a1b2c3d4e5f6
```

Restore a snapshot:

```bash
moat snapshot restore run_a1b2c3d4e5f6 snap_xyz123
```

See [Snapshots guide](./06-snapshots.md) for details.

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

- [SSH access](./03-ssh-access.md) — Set up SSH for git operations
- [Snapshots](./06-snapshots.md) — Protect your workspace with snapshots
- [Multi-agent](./05-multi-agent.md) — Run multiple Claude Code sessions
