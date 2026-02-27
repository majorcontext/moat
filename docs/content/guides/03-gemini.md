---
title: "Running Gemini"
navTitle: "Gemini"
description: "Run Google Gemini CLI in an isolated container with credential injection."
keywords: ["moat", "gemini", "google", "ai agent", "coding assistant"]
---

# Running Gemini

This guide covers running Google Gemini CLI in a Moat container.

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

The actual credential is never in the container environment. Moat's proxy intercepts requests to Google's API endpoints and injects the real token at the network layer, for both API key and OAuth modes. See [Credential management](../concepts/02-credentials.md) for details.

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

Gemini launches in interactive mode with full access to the mounted workspace.

### Non-interactive mode

Run with a prompt:

```bash
moat gemini -p "explain this codebase"
moat gemini -p "fix the failing tests"
moat gemini -p "add input validation to the user registration form"
```

Gemini executes the prompt and exits when complete.

### Permission handling

Gemini CLI does not have a per-tool confirmation prompt. File edits, command execution, and other operations run without individual approval steps.

**Security properties:**

The container runs as a non-root user with filesystem access limited to the mounted workspace. Credentials are injected at the network layer and never appear in the container environment. See [Security model](../concepts/08-security.md) for the full threat model.

### Named runs

Give your run a name for reference:

```bash
moat gemini --name feature-auth ./my-project
```

The name appears in `moat list` and makes it easier to manage multiple runs.

### Non-interactive runs

Run Gemini non-interactively with a prompt:

```bash
moat gemini -p "fix the failing tests" ./my-project
```

Monitor progress:

```bash
$ moat list
NAME          RUN ID              STATE    AGE
feature-auth  run_a1b2c3d4e5f6   running  5m

$ moat logs -f run_a1b2c3d4e5f6
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
name: my-gemini-project

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

Moat captures workspace snapshots for recovery and rollback. See [Snapshots](./07-snapshots.md) for configuration and usage.

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

- [SSH access](./04-ssh.md) -- Set up SSH for git operations
- [Snapshots](./07-snapshots.md) -- Protect your workspace with snapshots
- [Exposing ports](./06-ports.md) -- Access services running inside containers
