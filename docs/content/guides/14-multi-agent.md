---
title: "Multi-agent sessions"
navTitle: "Multi-agent"
description: "Run a second agent inside an already-running container using moat join."
keywords: ["moat", "join", "multi-agent", "claude", "parallel", "session"]
---

# Multi-agent sessions

`moat join` launches a second agent inside an already-running container, reusing its workspace, grants, and credential context. No new container is created and no new proxy registration is issued.

## How it works

A `moat join` session is an exec child of the existing container. The original agent owns the container lifecycle: stopping the run (via `moat stop`) tears down the container and terminates any joined agents. Joined agents share the run's proxy token, so their network requests are logged to the same run's `network.jsonl` and attributed to the same audit chain.

Console output is split: the primary agent writes to `logs.jsonl`; each joined agent writes to `logs.<index>.jsonl`.

## Quick start

In a first terminal, start a claude run:

```bash
moat claude
# note the run ID printed in the status footer, e.g. run_a1b2c3d4e5f6
```

In a second terminal, join it:

```bash
moat join run_a1b2c3d4e5f6 claude
```

The second terminal opens an interactive claude session in the same workspace. The status footer on the join shows `joined · 1`; the primary's footer shows `+1`.

## Headless join

Use `--prompt` / `-p` to run a join non-interactively and exit when done:

```bash
moat join run_a1b2c3d4e5f6 claude -p "print the current git branch"
```

The output streams to the terminal and the command exits with claude's exit code.

## Session flags

| Flag | Effect |
|------|--------|
| `-c`, `--continue` | Continue the most recent conversation |
| `-r`, `--resume ID` | Resume a specific session by ID |
| `-p`, `--prompt TEXT` | Run non-interactively with this prompt |

`--continue` and `--resume` are mutually exclusive.

## Status footer

| Session | Footer display |
|---------|----------------|
| Primary, no joins | (no session segment) |
| Primary, N joins active | `+N` before the run ID |
| Joined session (index N) | `joined · N` before the run ID |

## Lifecycle

The original run owns the container. Joined agents are exec children:

- `moat stop <run>` stops the container, which terminates all joined agents.
- A joined agent exiting (or being interrupted) does not affect the primary or other joins.
- `moat destroy` removes the run after it is stopped; joined log files (`logs.N.jsonl`) are removed with the run.

## v1 constraints

v1 supports same-agent joins only. The agent argument must match the agent the run was created with:

```bash
# Works: joining claude into a moat claude run
moat join run_a1b2c3d4e5f6 claude

# Error: run has no codex configuration
moat join run_a1b2c3d4e5f6 codex
```

Cross-provider join (e.g. running codex inside a claude run) and container-side worktree joins (`moat join … --wt`) are not supported in v1.

## Relationship to moat exec

`moat join` and `moat exec` both exec a command into an existing container:

- `moat exec` runs an arbitrary command you supply.
- `moat join` resolves an agent provider by name and constructs its standard invocation (equivalent to what `moat claude` would run inside the container).

Use `moat exec` for shell commands and scripts; use `moat join` to start a full interactive agent session.
