# Multi-agent join — design

**Date:** 2026-06-16
**Status:** Draft for review
**Topic:** Run a second agent inside an already-running container via `moat join`

## Problem

Today moat is strictly **1 run = 1 container = 1 foreground agent**. The container
exists for exactly one agent and exits when that agent exits. Running a second
`claude` against the same workspace means paying the full container setup cost
again — image resolve, container create, mounts, proxy registration, dependency
provisioning, agent config materialization — for what is conceptually "another
agent in the same place."

We want a way to launch an additional agent into an existing run's container,
reusing all of that setup.

## Goal (v1)

```
moat join <run> <agent> [agent flags…]

moat join run_123 claude
moat join run_123 claude --continue
moat join happy-otter claude --prompt "..."
```

A second `claude` agent starts inside the container of an existing `claude` run,
sharing its workspace, grants, config, and proxy credential context — with no
new container, image build, or proxy registration.

### Non-goals (deferred — see Future)

- Cross-provider join (`codex` into a `claude` run).
- Joining into a generic `moat run` container that has no agent configured.
- A persistent / "session" container whose lifecycle is independent of any agent.
- Reference-counted teardown across agents.

## Decisions

### 1. Command shape: a new top-level verb, `moat join`

`join` is the run-targeting sibling of `exec`, mirroring how `claude`/`codex`
relate to `run`:

|                    | arbitrary command        | configured agent                 |
| ------------------ | ------------------------ | -------------------------------- |
| **fresh container**   | `moat run -- …`        | `moat claude` / `moat codex`     |
| **existing container**| `moat exec <run> -- …` | `moat join <run> <agent>`        |

`join : exec :: claude : run`. Same axis (raw vs. provider-configured), applied
to a different lifecycle (fresh vs. existing).

**Why a verb, not a `--attach` flag on `moat claude`.** `moat claude` *is* the
create-a-run path — it threads through `Create` → image resolve → container
create → mounts → proxy register → start. Adding `--attach <run>` would fork
that entire flow with a "skip all of the above and divert into exec" branch
inside the one command that most assumes it is building something new. A
dedicated verb is a clean, purpose-built path straight to the exec backend that
never touches `Create` — **less** plumbing, not more.

**Why run-first.** All run-targeting commands lead with the run: `moat exec
<run>`, `moat logs <run>`, `moat stop <run>`. `moat join <run> <agent>` is
consistent with that family. A `--attach <run>` flag would bury the target.

**Agent flags are shared.** `moat join <run> claude --continue` takes the *exact
same* flags as `moat claude --continue`. Same provider flag surface, two entry
points (fresh vs. join). Nothing to relearn.

**`exec` is unaffected.** `moat exec <run> -- claude` still works — it launches
the raw `claude` binary with no provider config, no TTY-before-process, no
observability wiring. `exec` stays the honest "run literally what I typed"
escape hatch; `join` is the curated, provider-configured path. Exactly the
relationship `run` and `claude` already have. We do not block exec; we just do
not pretend it does the setup.

### 2. Lifecycle: the original agent owns the container

Simplicity first. Joined agents are `exec` children of the container, so they
already die when the container stops. The container's lifecycle is unchanged
from today:

- The container stays up exactly as long as the **original** foreground agent
  runs (or until `moat stop <run>`).
- When the original agent exits, the container tears down and any joined agents
  go with it.
- `moat stop <run>` while joined agents are live stops everything. This is
  acceptable and consistent with today's behavior.

No reference counting, no new persistence, no daemon lifecycle changes. A
persistent / "last one out" / session-owned container is a natural later
extension (see Future) but is explicitly **not** built now.

### 3. Scope: same-provider join only, inheriting the run's context

`moat join <run> claude` works **only when the target run already has claude
configured** — practically, a run started by `moat claude`. The joined agent
inherits the run's existing grants, config, and proxy token.

This is cheap because of how a `moat claude` run is already built:

- The agent config (`~/.claude.json`, credentials, MCP relay URLs) was
  materialized into the container home at container start (via the
  `/moat/claude-init` staging mount + `moat-init.sh`). A joined claude agent
  finds it already in place — **no new config writing**.
- The proxy scopes credentials per-run by a single auth token baked into the
  container's `HTTP_PROXY`. Every process in the container — including a joined
  agent — shares that token, so the joined agent gets the same Anthropic
  credential injection automatically. **No daemon/proxy changes.**

Anything outside this — a *different* provider, or a generic `moat run`
container with no agent — **errors clearly** and is deferred:

```
$ moat join run_123 codex
error: run_123 has no codex configuration.
       v1 join only attaches an agent that the run was started with (claude).
       To run codex here, start the run with codex configured.
```

### 4. Observability: hybrid — split console, interleaved network/audit

The parts that attribute by proxy token are unified at the run level; the
console, which is per-agent TUI noise, is split.

- **Console (stdout/stderr): split per agent.** The original agent's TUI output
  is already messy in `logs.jsonl` because of terminal control codes; forcing a
  joined TUI into the same stream would just be two TUIs fighting over one file.
  Each agent captures to its own console stream — original → `logs.jsonl`,
  joins → their own, addressed by a per-join index. The only new bookkeeping is
  that index; there is **no child run**. `moat list` shows the run with an agent
  count.
- **Network tracing: interleaved.** One `network.jsonl` per run. Automatic and
  correct by construction — every agent shares the run's proxy token, so the
  proxy already attributes all traffic to this run.
- **Audit chain: interleaved.** One hash-chained audit log per run; a joined
  agent's credential/network events append to the same chain. Same reason —
  shared token.

`moat list` shows the run with an agent count; `moat logs <run>` defaults to the
original agent's console, with joined consoles addressable by index.

### 5. Status footer reflects session role and attached count

Each interactive session — the primary and every join — already renders its own
status footer. Its current layout is two clusters with padding between them:

```
 moat  <runtime>  <grants…>  <warning> ··········· <run-id> · <name> (ctrl+/)
└── left ───────────────────────────┘              └──────── right ─────────┘
```

The session info is **prepended to the right cluster, just before the run-id**.
Color carries the role distinction, and the primary stays implicit:

- **Primary is implicit.** The primary (owner) session shows **no role word**.
  With zero joins its footer is identical to today's. When joins are live it
  shows only a color-accented `+N` count, so the owner sees at a glance that
  others are in the workspace.
- **Joined is explicit and color-accented.** A joined session renders a colored
  `joined` indicator so the terminal is unmistakably a join, with its index,
  e.g. `joined · 2`.

```
primary, solo:     moat  apple  github ····················· run_123 · happy-otter (ctrl+/)
primary, +2 joins: moat  apple  github ··············· +2 · run_123 · happy-otter (ctrl+/)
joined (agent 2):  moat  apple  github ········· joined · 2 · run_123 · happy-otter (ctrl+/)
```

`+2` and `joined` are color-accented (the bar already colors the runtime, dims
grants, and yellows warnings — the session segment gets its own accent). The
existing truncation cascade (drop hint, then grants, then name) extends
naturally — the session segment sits with the run-id on the right and is among
the last things dropped, since it is the point of this feature.

**Subtlety — the count is display-only.** Rendering a live "N attached" count
means *something* must track how many agents are currently live in the run.
That tracking is **purely for display** (footer + `moat list`) and explicitly
does **not** drive teardown — the lifecycle stays "the original agent owns the
container" (Decision 2). We deliberately do not turn this into reference-counted
teardown.

Mechanism (implementation detail, to settle in the plan): a lightweight
attached-agent registry, e.g. one entry per live agent under the run directory
(`<run-dir>/agents/`) written on join and removed on exit, with pid-based
liveness so a crashed join can be pruned rather than inflating the count.
A daemon-tracked counter is the alternative; if used, the daemon API change must
be additive-only to preserve the backwards-compatibility contract. For a display
count, occasional staleness is tolerable.

## Architecture

### New command: `cmd/moat/cli/join_cmd.go`

`moat join <run> <agent> [agent flags…]`:

1. Resolve the run (reuse `resolveRunArgSingle`, same as `exec`). Require
   `StateRunning`.
2. Look up the agent provider by name (`"claude"` → claude provider) from the
   provider registry (the same registry that backs `moat claude`).
3. **Verify the run has that provider configured** (see Scope). If not, error.
4. Ask the provider for the in-container command line for a *join* — the same
   `["claude", "--dangerously-skip-permissions", …]` construction the create
   path produces, honoring the shared agent flags (`--continue`, `--resume`,
   `--prompt`, …). No host staging dir, no mounts — config already lives in the
   container.
5. Allocate a per-join index, open its console stream, and run the agent via an
   **interactive exec** (below), teeing console to that stream.

`join` never enters `manager.Create`.

### New runtime capability: interactive (PTY-backed) exec

The principal new piece of plumbing. Today:

- `Runtime.Exec(ctx, id, cmd, stdin []byte, stdout, stderr io.Writer)` is
  **non-interactive** — fixed stdin buffer, no PTY. Fine for a headless
  (`--prompt`) join.
- TTY support (`AttachOptions{TTY, InitialWidth/Height}`, `ResizeTTY`) exists
  only for the container's **main** process via `StartAttached`, not for exec.

claude is a TUI, so an interactive join needs a `docker exec -it` equivalent:
raw-mode stdin streaming + a PTY + resize. Add an interactive exec variant to
the `Runtime` interface, implemented for both Docker and Apple backends, e.g.:

```go
// ExecInteractive runs a command inside a running container with a PTY,
// streaming the caller's stdin/stdout and honoring terminal resizes.
ExecInteractive(ctx context.Context, id string, cmd []string, opts ExecOptions) error
```

where `ExecOptions` carries the attached streams, `TTY bool`, and initial
width/height (parallel to `AttachOptions`). The existing `Exec` is reused
unchanged for headless joins.

### Manager method

A `manager.Join` (or `manager.ExecInteractive`) method that:

- validates run state and provider configuration,
- resolves the runtime via the existing `runtimeForRun`,
- opens the per-join console writer,
- registers a live attached-agent entry for the run (display-only count, see
  Decision 5) and removes it when the join exits,
- delegates to `Runtime.ExecInteractive` (interactive) or `Runtime.Exec`
  (headless),
- records the join to the audit store (the network/audit interleaving is already
  automatic via the shared token).

### What is explicitly *not* touched

- No `Create` path changes.
- No image build, mounts, or proxy registration for a join.
- No daemon API changes (the shared proxy token already covers the joined
  agent). This preserves the daemon backwards-compatibility contract.

## Error handling

| Situation                                  | Behavior                                                              |
| ------------------------------------------ | -------------------------------------------------------------------- |
| Run not found                              | Reuse `exec`'s resolution error.                                     |
| Run not `StateRunning`                     | Clear error: run is `<state>`; can only join a running run.          |
| Unknown agent name                         | Clear error listing known agents.                                    |
| Agent not configured in the run (v1 scope) | Clear error pointing at the deferred cross-provider path.            |
| Interactive join without a TTY (piped)     | Fall back to headless `Exec`, or require `--prompt`; error if neither.|

## Testing

- **Unit:** `join` command arg parsing (run + agent + passthrough flags);
  provider lookup and "provider not configured in run" rejection; in-container
  command construction matches the create path for the same flags.
- **Interactive exec (runtime):** Docker and Apple `ExecInteractive` —
  PTY allocation, resize propagation, exit-code surfacing via `ExecError`.
  Mirror existing `Exec` tests.
- **Observability:** a join writes its own console stream (split), while
  network/audit events land in the run's single `network.jsonl` / audit chain
  (interleaved). Assert no child-run registry entry is created.
- **Status footer / count:** the attached-agent registry increments on join and
  decrements on exit; the primary footer and `moat list` reflect the live count;
  a crashed join (dead pid) is pruned rather than inflating the count; the count
  does not affect container teardown.
- **E2E (`-tags=e2e`):** start `moat claude`, `moat join <run> claude --prompt`,
  assert the second agent runs in the same container (shared workspace, no new
  container created, no new proxy registration) and the run still tears down
  when the original agent exits.

## Future extensions (out of scope for v1)

- **Multi-agent provisioning at create time.** The clean path to cross-provider
  join is *not* hot-patching a live container — it is letting a run be created
  with multiple agents configured up front (e.g. `deps: [claude, codex]` or an
  agent list in `moat.yaml`). Both providers' configs are materialized and both
  grant sets are added to the run's proxy context at create time, so
  `moat join <run> codex` "just works." This is the preferred direction over
  runtime config/grant injection.
- **Container-side worktree for joins (`moat join … --wt`).** By default a join
  shares the primary agent's working directory, so two agents can contend over
  one git working tree. Today's `--wt` solves this contention *host-side* — it
  creates an isolated git worktree (new branch + directory) on the host and
  mounts it as the workspace before the container starts. A join can't add a host
  mount to a running container, so the analogue is a **container-side** worktree:
  on join, run `git worktree add` *inside* the container to give the joined agent
  its own branch and working directory within the same repo, isolated from the
  primary's tree. Cleanup prunes the in-container worktree when the join exits
  (and via `moat clean`). Mirrors `--wt`'s isolation without a new mount.
- **Persistent / session container.** A container whose lifecycle is independent
  of any single agent (explicit teardown, or reference-counted "last one out").
  Would make the original agent no longer special.
- **Per-join durable console addressing in the UI** (`moat logs <run> --agent N`,
  nested `moat list` display) if the per-join index proves worth surfacing more
  richly.
```
