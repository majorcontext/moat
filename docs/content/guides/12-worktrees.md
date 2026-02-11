---
title: "Git worktrees"
navTitle: "Worktrees"
description: "Run agents in isolated git worktrees for parallel work on multiple branches."
keywords: ["moat", "worktree", "git", "branches", "parallel"]
---

# Git worktrees

This guide covers running agents in git worktrees — separate checkouts of your repository on different branches. Each worktree has its own working directory, so multiple agents work on separate branches simultaneously.

## Prerequisites

- Moat installed
- A git repository (`moat wt` also requires an `agent.yaml` at the repository root)

## Quick start

Run an agent on a new branch:

```bash
moat wt dark-mode
```

This creates the `dark-mode` branch from HEAD (if it doesn't exist), creates a worktree at `~/.moat/worktrees/<repo-id>/dark-mode`, and starts the agent defined in `agent.yaml`.

## How it works

`moat wt <branch>` does three things:

1. **Creates the branch** from HEAD if it doesn't already exist
2. **Creates a git worktree** — a separate checkout of the branch at `~/.moat/worktrees/<repo-id>/<branch>`
3. **Starts the agent** from `agent.yaml` with the worktree as its workspace

The repository is identified by its remote URL (or local path if no remote is configured). Each branch gets its own directory under that repo ID.

If the branch and worktree already exist, they are reused.

## Two ways to use worktrees

### The `moat wt` command

`moat wt` reads `agent.yaml` from the repository root and runs that agent in the worktree:

```bash
# Start the agent defined in agent.yaml on the dark-mode branch
moat wt dark-mode

# Run in background
moat wt dark-mode -d

# Run a specific command instead of the agent.yaml default
moat wt dark-mode -- make test
```

This requires an `agent.yaml` in the repository root.

### The `--worktree` flag

The `--worktree` flag works on `moat claude`, `moat codex`, and `moat gemini`:

```bash
# Start Claude Code on the dark-mode branch
moat claude --worktree dark-mode

# Start Codex on a feature branch with a prompt
moat codex --worktree feature/auth -p "implement OAuth login" -d

# Start Gemini on a refactor branch
moat gemini --worktree cleanup
```

The `--worktree` flag creates the branch and worktree the same way as `moat wt`, but runs the specific agent command instead of reading `agent.yaml`. `--wt` is accepted as a shorthand alias.

## Run naming

Run names are generated automatically:

- If `agent.yaml` has a `name` field (or `--name` is passed), the run name is `{name}-{branch}`
- Otherwise, the run name is `{branch}`

Override with `--name`:

```bash
moat wt dark-mode --name my-custom-name
```

## Parallel branches

Run agents on multiple branches simultaneously:

```bash
moat wt feature/auth -d
moat wt feature/dark-mode -d
moat wt fix/login-bug -d
```

Each agent gets its own worktree, its own container, and its own branch. Branch names with slashes (like `feature/auth`) are supported.

Check on all worktree runs:

```bash
$ moat wt list

BRANCH              RUN NAME               STATUS   WORKTREE
feature/auth        my-agent-feature/auth  running  ~/.moat/worktrees/github.com/my-org/my-project/feature/auth
feature/dark-mode   my-agent-feature/...   running  ~/.moat/worktrees/github.com/my-org/my-project/feature/dark-mode
fix/login-bug       my-agent-fix/login-bug stopped  ~/.moat/worktrees/github.com/my-org/my-project/fix/login-bug
```

## Active run detection

If a run is already active in a worktree, `moat wt` returns an error:

```text
Error: a run is already active in worktree for branch "dark-mode": my-agent-dark-mode (run_a1b2c3d4e5f6)
Attach with 'moat attach run_a1b2c3d4e5f6' or stop with 'moat stop run_a1b2c3d4e5f6'
```

The `--worktree` flag on agent commands behaves the same way.

## Cleaning up

Remove worktree directories for stopped runs:

```bash
# Clean all stopped worktrees for the current repo
moat wt clean

# Clean a specific branch's worktree
moat wt clean dark-mode
```

`moat wt clean` removes the worktree directory and runs `git worktree prune`. It never deletes branches — your work remains in git.

Active worktrees (with running agents) are skipped.

## Worktree storage

Worktrees are stored at:

```text
~/.moat/worktrees/<repo-id>/<branch>
```

The `<repo-id>` is derived from the repository's remote URL in `host/owner/repo` format. For example, `github.com:my-org/my-project.git` becomes `github.com/my-org/my-project`. Repositories without a remote use `_local/<directory-name>`.

Override the base path with `MOAT_WORKTREE_BASE`:

```bash
export MOAT_WORKTREE_BASE=/mnt/fast-disk/worktrees
moat wt dark-mode
```

## Configuration

The agent reads `agent.yaml` from the repository root. If the worktree directory also contains an `agent.yaml`, the worktree's copy takes precedence.

Branch-specific configuration works as follows:

1. Start with `agent.yaml` in your main branch
2. The worktree inherits it when the branch is created
3. Modify the worktree's `agent.yaml` for branch-specific settings (extra grants, different dependencies)

## Example: parallel feature development

1. Configure your agent:
   ```yaml
   name: my-agent

   grants:
     - anthropic
     - github

   dependencies:
     - node@20
     - git
     - claude-code
   ```

2. Start agents on multiple branches:
   ```bash
   moat wt feature/auth -d
   moat wt feature/dark-mode -d
   ```

3. Monitor progress:
   ```bash
   moat wt list
   moat logs run_a1b2c3d4e5f6
   moat attach run_a1b2c3d4e5f6
   ```

4. When finished, clean up worktree directories:
   ```bash
   moat wt clean
   ```

5. Branches remain in git. Merge as usual:
   ```bash
   git merge feature/auth
   git merge feature/dark-mode
   ```

## Troubleshooting

### "agent.yaml not found"

`moat wt` requires an `agent.yaml` in the repository root. Create one, or use `moat claude --worktree` / `moat codex --worktree` / `moat gemini --worktree` which work without `agent.yaml`.

### "not inside a git repository"

Run `moat wt` from within a git repository. Worktrees are a git feature and require a git repo.

### "a run is already active in worktree"

Another agent is already running on that branch. Either attach to it or stop it first:

```bash
moat attach <run-id>
moat stop <run-id>
```

### Worktree persists after stop

`moat stop` stops the container but does not remove the worktree. Use `moat wt clean` to remove stopped worktree directories.

## Related guides

- [Running Claude Code](./01-claude-code.md) — Use `--worktree` with Claude Code
- [Running Codex](./02-codex.md) — Use `--worktree` with Codex
- [Running Gemini](./03-gemini.md) — Use `--worktree` with Gemini
