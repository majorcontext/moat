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

## Generating moat.yaml

Use `moat init` to auto-generate a `moat.yaml` for your project:

```bash
moat init ./my-project
```

This scans the project, detects its dependencies and tools, and generates a configuration file using AI. Requires at least one credential granted (e.g., `moat grant codex`).

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

Codex runs the prompt with `codex exec` and exits when complete.

### Permission handling

Codex has its own approval prompts and its own sandbox. Inside a moat container both are turned off by default, via the generated `~/.codex/config.toml`:

```toml
approval_policy = "never"
sandbox_mode = "danger-full-access"
```

Two reasons. The container is already the isolation boundary, so a second sandbox inside it adds prompts without adding protection. And Codex's sandbox blocks network access for the commands it runs, which breaks anything routed through the moat proxy — `npm install`, `pip`, `git push`, `gh`.

**Security properties:**

The container runs as a non-root user with filesystem access limited to the mounted workspace. Credentials are injected at the network layer and never appear in the container environment. See [Security model](../concepts/08-security.md) for the full threat model.

**Restoring manual approval:**

Use `--noyolo` to run with Codex's own defaults instead (`approval_policy = "on-request"`, `sandbox_mode = "workspace-write"`):

```bash
moat codex -p "refactor the API layer" --noyolo
```

Codex then prompts for confirmation before each potentially destructive operation, and confines the commands it runs to the workspace with the network disabled.

The older `--full-auto` flag is deprecated and hidden. `--full-auto=false` still maps to `--noyolo`; the Codex CLI itself removed the flag from interactive mode and deprecated it on `codex exec`.

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

Configure in `moat.yaml` for repeated use:

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

Or configure in `moat.yaml`:

```yaml
network:
  rules:
    - example.com
    - "*.internal.corp"
```

## Session transcripts

When the `openai` grant is configured, Codex session transcripts written inside the container appear on the host at:

```
~/.moat/codex/sessions/<workspace>-<id>/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl
```

The directory name renders the workspace path for readability and appends a short digest of it, so two projects whose paths differ only in punctuation (`my-project` and `my_project`) get separate directories rather than sharing one.

The directory is per-workspace and separate from your own `~/.codex/sessions`, so a container working on one project cannot read transcripts from another. That matters because transcripts contain whatever the agent saw -- source, data, credentials in error messages -- and one project's data should not reach another project's agent.

Opt out of syncing entirely with `sync_logs: false`.

To use your own Codex history instead, so container sessions show up alongside host ones:

```yaml
codex:
  shared_sessions: true
```

This is all-or-nothing. Codex partitions sessions by date rather than by workspace and cannot be told to write elsewhere, so sharing exposes your transcripts from **every** project to the container. Prefer the default unless you specifically need one combined history.

The SQLite databases in `~/.codex` are never shared in either mode -- concurrent runs writing one shared SQLite file risks corrupting it. A consequence is that `codex resume` on the host may not list sessions created inside a container, since resume reads that index. The transcripts themselves are complete.

## MCP servers

Both kinds of MCP server work with Codex, and both are written to the `[mcp_servers]` table of the generated `~/.codex/config.toml` -- Codex reads MCP servers from `config.toml` only, so nothing is written to `.mcp.json`.

Remote servers declared at the top level of `moat.yaml` become streamable HTTP entries pointing at the proxy relay, which injects the real credential:

```yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
```

Sandbox-local servers declared under `codex.mcp:` become stdio entries:

```yaml
codex:
  mcp:
    filesystem:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
      cwd: /workspace
```

See [MCP servers](./09-mcp.md) for the full configuration reference.

## Workspace snapshots

Moat captures workspace snapshots for recovery and rollback. See [Snapshots](./07-snapshots.md) for configuration and usage.

## Example: Code review workflow

1. Grant credentials:
   ```bash
   moat grant openai
   moat grant github
   ```

2. Create `moat.yaml`:
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

Check that you're not running in a directory without a `moat.yaml` that specifies a conflicting configuration. Try:

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
