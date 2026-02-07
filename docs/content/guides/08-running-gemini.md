---
title: "Running Gemini"
description: "Run Google Gemini CLI in an isolated container with credential injection."
keywords: ["moat", "gemini", "google", "ai agent", "coding assistant"]
---

# Running Gemini

This guide covers running Google Gemini CLI in a Moat container. Gemini CLI is Google's AI coding assistant that can read, write, and execute code.

## Prerequisites

- Moat installed
- A Google API key from [aistudio.google.com/apikey](https://aistudio.google.com/apikey), OR Gemini CLI installed with OAuth credentials

## Granting Gemini credentials

Run `moat grant gemini` to configure authentication. The prompts depend on what credentials are available.

### With Gemini CLI installed (OAuth)

If you have Gemini CLI installed and authenticated, you'll see a menu:

```bash
$ moat grant gemini

Choose authentication method:

  1. Import Gemini CLI credentials (recommended)
     Use OAuth tokens from your local Gemini CLI installation.
     Refresh tokens are stored for automatic access token renewal.

  2. Gemini API key
     Use an API key from aistudio.google.com/apikey

Enter choice [1 or 2]: 1

Found Gemini CLI credentials.
  Expires: 2026-02-07T12:30:00Z

Validating refresh token...
Refresh token is valid.

Gemini credential saved to ~/.moat/credentials/gemini.enc
```

OAuth credentials use Google's refresh token mechanism. Moat automatically refreshes access tokens before they expire (Google OAuth tokens last 1 hour; Moat refreshes 15 minutes before expiry).

### Without Gemini CLI (API key)

If Gemini CLI is not installed, the command prompts for an API key directly:

```bash
$ moat grant gemini

Tip: Install Gemini CLI for OAuth authentication:
  npm install -g @google/gemini-cli && gemini

Enter your Gemini API key.
You can find or create one at: https://aistudio.google.com/apikey

API Key: AI...

Validating API key...
API key is valid.

Gemini API key saved to ~/.moat/credentials/gemini.enc
```

You can also set `GEMINI_API_KEY` in your environment before running the command:

```bash
export GEMINI_API_KEY="AI..."
moat grant gemini
```

### API key vs OAuth

Gemini CLI routes to different API backends depending on authentication:

| Method | API backend | Use case |
|--------|-------------|----------|
| API key | `generativelanguage.googleapis.com` | Standard API access, billed per token |
| OAuth | `cloudcode-pa.googleapis.com` | Google account access, uses your Google subscription |

Both methods are fully supported. The proxy injects credentials at the network layer regardless of which method you use.

### How credentials are injected

For API key mode, Moat sets `GEMINI_API_KEY` in the container environment with a placeholder value. The proxy intercepts requests to `generativelanguage.googleapis.com` and injects the real key via the `x-goog-api-key` header.

For OAuth mode, Moat writes a placeholder `oauth_creds.json` to `~/.gemini/` inside the container. The proxy intercepts requests to `cloudcode-pa.googleapis.com` and `oauth2.googleapis.com`, replacing placeholder tokens with real ones at the network layer.

## Running Gemini

### Interactive mode

Start Gemini in the current directory:

```bash
moat gemini
```

Start in a specific project:

```bash
moat gemini ./my-project
```

Gemini launches in interactive mode. Use it as you would normally -- it has full access to the mounted workspace.

### Non-interactive mode

Run with a prompt:

```bash
moat gemini -p "explain this codebase"
moat gemini -p "fix the failing tests"
moat gemini -p "add input validation to the user registration form"
```

Gemini executes the prompt and exits when complete.

### Named sessions

Give your session a name for reference:

```bash
moat gemini --name feature-auth ./my-project
```

The name appears in `moat list` and makes it easier to manage multiple sessions.

### Background sessions

Run Gemini in the background:

```bash
moat gemini -d ./my-project
```

Reattach later:

```bash
$ moat list
NAME          RUN ID              STATE    SERVICES
feature-auth  run_a1b2c3d4e5f6   running

$ moat attach run_a1b2c3d4e5f6
```

## Session management

List Gemini sessions:

```bash
moat gemini sessions
```

Show only running sessions:

```bash
moat gemini sessions --active
```

The output shows session name, workspace, state, and when it was last accessed:

```
SESSION        WORKSPACE         STATE    LAST ACCESSED
feature-auth   ~/my-project      running  5 minutes ago
code-review    ~/other-project   stopped  2 hours ago
```

## Adding GitHub access

Grant GitHub access so Gemini can interact with repositories:

```bash
moat gemini --grant github ./my-project
```

This injects GitHub credentials alongside Gemini credentials. Gemini can:

- Clone repositories
- Push commits
- Create pull requests
- Access private repositories

Configure in `agent.yaml` for repeated use:

```yaml
name: my-gemini-session

grants:
  - gemini
  - github
```

Then:

```bash
moat gemini ./my-project
```

## Adding SSH access

For SSH-based git operations:

```bash
moat grant ssh --host github.com
moat gemini --grant ssh:github.com ./my-project
```

Gemini can use `git@github.com:...` URLs for cloning and pushing.

## Network access

Gemini has automatic network access to Google API endpoints:

- `generativelanguage.googleapis.com`
- `*.googleapis.com`
- `oauth2.googleapis.com`

To allow access to additional hosts:

```bash
moat gemini --allow-host example.com ./my-project
```

Or configure in `agent.yaml`:

```yaml
network:
  allow:
    - example.com
    - "*.internal.corp"
```

## Configuration via agent.yaml

For repeated use, configure Gemini in `agent.yaml`:

```yaml
name: my-gemini-agent
agent: gemini-cli

dependencies:
  - node@20
  - git
  - gemini-cli

grants:
  - gemini
  - github

gemini:
  sync_logs: true
```

Gemini-specific fields are documented in the [agent.yaml reference](../reference/02-agent-yaml.md#gemini).

### MCP servers

Configure local MCP servers for Gemini:

```yaml
gemini:
  mcp:
    my_server:
      command: /path/to/server
      args: ["--flag"]
      env:
        API_KEY: my-key
      grant: github
      cwd: /workspace
```

MCP configuration is written to `.mcp.json` in the workspace directory. See the [agent.yaml reference](../reference/02-agent-yaml.md#geminimcp) for all configuration options.

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
   moat grant gemini
   moat grant github
   ```

2. Create `agent.yaml`:
   ```yaml
   name: code-review

   grants:
     - gemini
     - github

   snapshots:
     triggers:
       disable_pre_run: false
   ```

3. Run Gemini with a review prompt:
   ```bash
   moat gemini -p "Review the changes in the last 3 commits. Focus on security issues and suggest improvements."
   ```

4. View what Gemini did:
   ```bash
   moat logs
   moat trace --network
   ```

## Troubleshooting

### "No Gemini credentials found"

Grant an API key or import Gemini CLI credentials:

```bash
# API key
export GEMINI_API_KEY="AI..."
moat grant gemini

# Or install Gemini CLI and authenticate first
npm install -g @google/gemini-cli
gemini  # Complete OAuth login
moat grant gemini
```

### Gemini hangs on startup

Check that you're not running in a directory without an `agent.yaml` that specifies a conflicting configuration. Try:

```bash
moat gemini --name test ~/empty-dir
```

### Network errors

Verify the Gemini credential is granted:

```bash
moat run --grant gemini -- curl -s "https://generativelanguage.googleapis.com/v1beta/models?key=test"
```

### OAuth token expired

OAuth tokens are automatically refreshed by the proxy. If refresh fails, re-grant:

```bash
moat grant gemini
```

## Related guides

- [SSH access](./03-ssh-access.md) -- Set up SSH for git operations
- [Snapshots](./06-snapshots.md) -- Protect your workspace with snapshots
- [Exposing ports](./05-exposing-ports.md) — Access services running inside containers
