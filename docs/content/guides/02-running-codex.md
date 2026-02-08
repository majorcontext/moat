---
title: "Running Codex"
description: "Run OpenAI Codex CLI in an isolated container with credential injection."
keywords: ["moat", "codex", "openai", "ai agent", "coding assistant"]
---

# Running Codex

This guide covers running OpenAI Codex CLI in a Moat container. Codex is OpenAI's AI coding assistant that can read, write, and execute code.

## Prerequisites

- Moat installed
- An OpenAI API key from [platform.openai.com](https://platform.openai.com/api-keys)

## Granting OpenAI credentials

Run `moat grant openai` to configure authentication:

```bash
$ moat grant openai

Enter your OpenAI API key.
You can find or create one at: https://platform.openai.com/api-keys

API Key: sk-...

Validating API key...
API key is valid.

OpenAI API key saved to ~/.moat/credentials/openai.enc
```

You can also set `OPENAI_API_KEY` in your environment before running the command:

```bash
export OPENAI_API_KEY="sk-..."
moat grant openai
```

### How credentials are injected

Moat sets `OPENAI_API_KEY` in the container environment. This variable contains a placeholder value (`moat-proxy-injected`)—the actual credential is never in the container environment. The proxy intercepts requests to OpenAI's API and injects the real token at the network layer.

## Running Codex

### Interactive mode

Start Codex in the current directory:

```bash
moat codex
```

Start in a specific project:

```bash
moat codex ./my-project
```

Codex launches in interactive TUI mode. Use it as you would normally—it has full access to the mounted workspace.

### Non-interactive mode

Run with a prompt:

```bash
moat codex -p "explain this codebase"
moat codex -p "fix the failing tests"
moat codex -p "add input validation to the user registration form"
```

Codex executes the prompt with `--full-auto` mode enabled and exits when complete.

### Permission handling

By default, `moat codex -p` runs with `--full-auto` enabled. This auto-approves tool use (file edits, command execution, etc.) without per-operation confirmation prompts.

**Security properties:**

The container provides these isolation boundaries:

- Runs as a non-root user (`moatuser`, UID 5000) inside the container
- Filesystem access is limited to the mounted workspace, plus read-only mounts for credential helper configs (e.g., `~/.config/gh/config.yml`, AWS credential process scripts)
- SSH private keys remain on the host—the container can request signatures via an SSH agent proxy but cannot extract key material
- Credentials are injected at the network layer via proxy and do not appear in the container environment (see [Credential management](../concepts/02-credentials.md) for details on AWS `credential_process`)
- Standard container isolation separates the run from other containers and host processes

Per-operation prompts require user confirmation for each action. The container already limits access to the mounted workspace and routes credentials through the proxy.

**Restoring manual approval:**

If you prefer Codex's default confirmation behavior, use the `--full-auto=false` flag (or `--noyolo` alias):

```bash
moat codex -p "refactor the API layer" --full-auto=false
moat codex -p "refactor the API layer" --noyolo
```

With manual approval enabled, Codex prompts for confirmation before each potentially destructive operation.

### Named sessions

Give your session a name for reference:

```bash
moat codex --name feature-auth ./my-project
```

The name appears in `moat list` and makes it easier to manage multiple sessions.

### Background sessions

Run Codex in the background:

```bash
moat codex -d ./my-project
```

Reattach later:

```bash
$ moat list
NAME          RUN ID              STATE    SERVICES
feature-auth  run_a1b2c3d4e5f6   running

$ moat attach run_a1b2c3d4e5f6
```

## Adding GitHub access

Grant GitHub access so Codex can interact with repositories:

```bash
moat codex --grant github ./my-project
```

This injects GitHub credentials alongside OpenAI credentials. Codex can:

- Clone repositories
- Push commits
- Create pull requests
- Access private repositories

Configure in `agent.yaml` for repeated use:

```yaml
name: my-codex-session

grants:
  - openai
  - github
```

Then:

```bash
moat codex ./my-project
```

## Adding SSH access

For SSH-based git operations:

```bash
moat grant ssh --host github.com
moat codex --grant ssh:github.com ./my-project
```

Codex can use `git@github.com:...` URLs for cloning and pushing.

## Allowing additional hosts

By default, Codex has network access to OpenAI endpoints (`api.openai.com`, `chatgpt.com`, etc.). To allow access to additional hosts:

```bash
moat codex --allow-host example.com ./my-project
```

Or configure in `agent.yaml`:

```yaml
network:
  allow:
    - example.com
    - "*.internal.corp"
```

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
   moat grant openai
   moat grant github
   ```

2. Create `agent.yaml`:
   ```yaml
   name: code-review

   grants:
     - openai
     - github

   snapshots:
     triggers:
       disable_pre_run: false
   ```

3. Run Codex with a review prompt:
   ```bash
   moat codex -p "Review the changes in the last 3 commits. Focus on security issues and suggest improvements."
   ```

4. View what Codex did:
   ```bash
   moat logs
   moat trace --network
   ```

## Troubleshooting

### "No OpenAI credentials found"

Create an API key from [platform.openai.com/api-keys](https://platform.openai.com/api-keys) and grant it:

```bash
export OPENAI_API_KEY="sk-..."
moat grant openai
```

### Codex hangs on startup

Check that you're not running in a directory without an `agent.yaml` that specifies a conflicting configuration. Try:

```bash
moat codex --name test ~/empty-dir
```

### Network errors

Verify the OpenAI credential is granted:

```bash
moat run --grant openai -- curl -s https://api.openai.com/v1/models -H "Authorization: Bearer test"
```

## Related guides

- [SSH access](./03-ssh-access.md) — Set up SSH for git operations
- [Snapshots](./06-snapshots.md) — Protect your workspace with snapshots
- [Exposing ports](./05-exposing-ports.md) — Access services running inside containers
