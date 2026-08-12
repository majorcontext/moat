---
title: "Multi-agent sessions"
navTitle: "Multi-agent"
description: "Run several agents in one container and join them with moat join."
keywords: ["moat", "join", "multi-agent", "claude", "codex", "parallel", "session"]
---

# Multi-agent sessions

`moat join` launches a second agent inside an already-running container, reusing its workspace, grants, and credential context. No new container is created and no new proxy registration is issued.

## How it works

A `moat join` session is an exec child of the existing container. The original agent owns the container lifecycle: stopping the run (via `moat stop`) tears down the container and terminates any joined agents. Joined agents share the run's proxy token, so their network requests are logged to the same run's `network.jsonl` and attributed to the same audit chain.

Console output is split: the primary agent writes to `logs.jsonl`; each joined agent writes to `logs.<index>.jsonl`.

The agent you join must be one moat actually provisioned into the container: the agent the run was started with, or any agent listed in that project's [`agents:`](../reference/02-moat-yaml.md#agents). Joining an agent moat never provisioned fails with a clear error naming what the run can host.

## Quick start: same agent, second session

In a first terminal, start a claude run:

```bash
moat claude
# note the run ID printed in the status footer, e.g. run_a1b2c3d4e5f6
```

In a second terminal, join it:

```bash
moat join run_a1b2c3d4e5f6 claude
```

The second terminal opens an interactive claude session in the same workspace. The status footer on the join shows `joined · 1`; the primary's footer shows `primary +1`.

## Cross-agent joins with `agents:`

By default, a run only provisions the agent you started it with — `moat claude` provisions claude, and only claude can join it. To make a second agent joinable, list it in `moat.yaml`'s [`agents:`](../reference/02-moat-yaml.md#agents):

```yaml
agents: [claude, codex]
```

This provisions both agents' dependencies, credential grants, and network rules into the container. With no `agent:` field set, `agents[0]` (`claude`) becomes the run's recorded **primary agent** — used for agent-specific defaults like container memory, implied dependencies, and language-server support. It does not change what the run executes: `moat run` with no `-- command` and no `command:` in `moat.yaml` still starts a shell, never an agent. To run an agent in the foreground, use its own verb:

```bash
moat claude
# agents[0] (claude) would be recorded as the primary agent even if you ran
# `moat run` instead — but moat run itself still starts your configured
# command, or a shell, not an agent. moat claude runs it directly.

# from a second terminal
moat join run_a1b2c3d4e5f6 codex
```

Order in `agents:` matters only for picking the primary agent — every entry after the first is equally joinable.

## Joining without a run ID

`moat join <agent>` infers the run instead of requiring one:

```bash
moat join claude
```

Resolution narrows candidates in this order:

1. Only `running` runs are considered.
2. Runs are filtered to ones that can host the named agent (started with it, or provisioning it via `agents:`).
3. If any qualifying run's workspace matches the current directory, only those are offered; otherwise every qualifying run is offered, and moat says so, because attaching to another workspace's run means using that run's grants and network policy.
4. A single candidate in the current workspace attaches immediately. Multiple candidates print a numbered table, newest run first, and prompt for a selection:

```
Multiple running runs can host claude:

    NAME        RUN ID              AGENTS         AGE
  1 my-feature  run_a1b2c3d4e5f6    claude, codex  2m
  2 my-feature  run_9f8e7d6c5b4a    claude         14m

Select [1-2]:
```

The table and prompt are written to **stderr**, not stdout — `moat join claude | tee log` still shows the picker even though stdin is a TTY. A non-interactive caller (no TTY, or more than one candidate arriving over a pipe) gets an error listing the candidate run IDs instead of a prompt.

If no run anywhere is running, the error says so directly. If runs are running but none can host the agent, the error says that instead and points at `agents:`.

If the single argument you pass names both a known agent and a run (for example, a project whose runs happen to be named `claude`), moat interprets it as the agent and warns, showing the two-argument form to target the run explicitly.

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
| Primary, N joins active | `primary +N` before the run ID |
| Joined session (index N) | `joined · N` before the run ID |

## Lifecycle

The original run owns the container. Joined agents are exec children:

- `moat stop <run>` stops the container, which terminates all joined agents.
- A joined agent exiting (or being interrupted) does not affect the primary or other joins.
- `moat destroy` removes the run after it is stopped; joined log files (`logs.N.jsonl`) are removed with the run.

## What agents in one container share

Every agent and process in a container shares the run's single proxy token, so
**every credential granted to the run is reachable by every agent in it**. A
container with `agents: [claude, codex]` lets either agent reach both the
Anthropic and the OpenAI credential. Use separate runs when the agents should
not share credentials.

Joined agents also share the primary's git working tree and can contend over it.
Container-side worktree isolation is not implemented.

## Relationship to moat exec

`moat join` and `moat exec` both exec a command into an existing container:

- `moat exec` runs an arbitrary command you supply.
- `moat join` resolves an agent provider by name and constructs its standard invocation (equivalent to what `moat claude` would run inside the container).

Use `moat exec` for shell commands and scripts; use `moat join` to start a full interactive agent session.
