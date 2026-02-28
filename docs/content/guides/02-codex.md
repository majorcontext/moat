---
title: "Running Codex"
navTitle: "Codex"
description: "Run OpenAI Codex CLI in an isolated container with credential injection."
keywords: ["moat", "codex", "openai", "ai agent", "coding assistant"]
---

# Running Codex

This guide covers running OpenAI Codex CLI in a Moat container.

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

The actual credential is never in the container environment. Moat's proxy intercepts requests to OpenAI's API and injects the real token at the network layer. See [Credential management](../concepts/02-credentials.md) for details.

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

Codex launches in interactive TUI mode with full access to the mounted workspace.

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

The container runs as a non-root user with filesystem access limited to the mounted workspace. Credentials are injected at the network layer and never appear in the container environment. See [Security model](../concepts/08-security.md) for the full threat model.

**Restoring manual approval:**

If you prefer Codex's default confirmation behavior, use the `--full-auto=false` flag (or `--noyolo` alias):

```bash
moat codex -p "refactor the API layer" --full-auto=false
moat codex -p "refactor the API layer" --noyolo
```

With manual approval enabled, Codex prompts for confirmation before each potentially destructive operation.

### Named runs

Give your run a name for reference:

```bash
moat codex --name feature-auth ./my-project
```

The name appears in `moat list` and makes it easier to manage multiple runs.

### Non-interactive runs

Run Codex non-interactively with a prompt:

```bash
moat codex -p "fix the failing tests" ./my-project
```

Monitor progress:

```bash
$ moat list
NAME          RUN ID              STATE    AGE
feature-auth  run_a1b2c3d4e5f6   running  5m

$ moat logs -f run_a1b2c3d4e5f6
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
name: my-codex-project

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

Moat captures workspace snapshots for recovery and rollback. See [Snapshots](./07-snapshots.md) for configuration and usage.

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

- [SSH access](./04-ssh.md) — Set up SSH for git operations
- [Snapshots](./07-snapshots.md) — Protect your workspace with snapshots
- [Exposing ports](./06-ports.md) — Access services running inside containers
