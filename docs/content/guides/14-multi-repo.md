---
title: "Multi-repo workspaces"
description: "Run agents across multiple git repositories under a single root directory."
keywords: ["moat", "multi-repo", "monorepo", "worktree", "workspace", "submodules"]
---

# Multi-repo workspaces

This guide covers running agents when your workspace contains multiple git repositories. Two common layouts exist, and they require different approaches.

## Identify your layout

Check whether your root directory is a git repository:

```bash
$ cd ~/dev/my-project && git rev-parse --git-dir
.git
```

If this succeeds, you have **Layout A** (root is a git repo). If it fails with `fatal: not a git repository`, you have **Layout B** (root is a plain directory).

| Layout | Structure | `moat wt` works? |
|--------|-----------|-------------------|
| A: Git root | Root is a git repo containing sub-repos (submodules or subtrees) | Yes |
| B: Plain root | Root is a plain directory containing independent git repos | No |

## Layout A: root is a git repo

Your root directory is itself a git repository. Sub-repos are typically git submodules or subtrees:

```text
my-project/          # git repo (root)
  moat.yaml
  api/               # submodule or subtree
  web/               # submodule or subtree
```

### Using worktrees

`moat wt` works because the root is a git repo:

```bash
moat wt feature/new-endpoint
```

This creates a worktree of the root repo on the `feature/new-endpoint` branch. Place `moat.yaml` at the repository root so it is included in every worktree.

### Submodule behavior

Git worktrees do not automatically initialize submodules. When `moat wt` creates a worktree, submodules appear as empty directories. Your agent's startup command should initialize them:

```yaml
name: my-agent

grants:
  - anthropic
  - github

command: |
  git submodule update --init --recursive
  # ... rest of your workflow
```

Submodule HEAD pointers in a worktree are frozen at the commit recorded in the parent repo at branch creation time. If the main branch updates submodule pointers after the worktree is created, the worktree does not pick up those changes automatically. To update:

```bash
cd ~/.moat/worktrees/<repo-id>/feature/new-endpoint
git submodule update --remote
```

### Configuration

Place `moat.yaml` at the repository root. It is checked out with the worktree, so each branch gets its own copy:

```yaml
name: my-agent

grants:
  - anthropic
  - github

dependencies:
  - node@20
  - python@3.12
  - git
  - claude-code
```

## Layout B: root is not a git repo

Your root directory is a plain directory containing independent repos:

```text
dev/                 # plain directory, not a git repo
  api/               # independent git repo
  web/               # independent git repo
  shared-lib/        # independent git repo
```

`moat wt` does not work here because the root directory is not a git repository. Two approaches handle this layout.

### Option 1: run per repo

Run separate agents in each repository. Place a `moat.yaml` in each repo:

```bash
# Terminal 1
moat wt feature/auth -C ~/dev/api

# Terminal 2
moat wt feature/auth -C ~/dev/web
```

Each repo gets its own worktree and container. This works well when repos are loosely coupled.

### Option 2: mount the root directory

If the agent needs access to multiple repos simultaneously, mount the parent directory as the workspace:

```bash
moat run --mount ~/dev:/workspace ./dev
```

Or with a `moat.yaml` inside one of the repos:

```yaml
mounts:
  - ../:/workspace

command: |
  cd /workspace
  # agent has access to api/, web/, shared-lib/
```

> **Warning:** Without worktree isolation, concurrent runs on the same directory risk conflicts. Use this approach for sequential runs, or make sure agents work on separate files.

### Trade-offs

| Approach | Isolation | Disk usage | Concurrent runs |
|----------|-----------|------------|-----------------|
| Run per repo | Each repo isolated | Low (worktrees share git objects) | Safe with `moat wt` |
| Mount root | All repos accessible | No duplication | Risk of conflicts |

## General advice

### Keep moat.yaml at the workspace root

Moat looks for `moat.yaml` in the workspace root directory. Placing it elsewhere requires extra flags and makes worktree-based workflows harder.

### Avoid hardcoded absolute paths

Absolute paths break when worktrees or different machines use different directory structures. Use relative paths in `moat.yaml` and scripts:

```yaml
# Avoid
command: /home/user/dev/api/run.sh

# Prefer
command: ./run.sh
```

### Pin submodule updates in CI

If using Layout A with submodules, pin submodule commits in your CI pipeline. Worktrees inherit the submodule pointer from the branch, so an unpinned submodule may produce different results across worktrees created at different times.

## Related guides

- [Git worktrees](./12-worktrees.md) -- Using `moat wt` for parallel branch development
- [Running Claude Code](./01-claude-code.md) -- Use `--worktree` with Claude Code
