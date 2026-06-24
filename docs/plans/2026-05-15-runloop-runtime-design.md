# RunLoop as a Third Moat Runtime

**Date:** 2026-05-15
**Status:** Draft

## Summary

Add RunLoop as a third option for the `container.Runtime` abstraction, so
`moat claude --runtime=runloop` runs the agent in a RunLoop Devbox instead of a
local Docker or Apple container. Behaviour matches the local case as closely as
the platform allows: credential injection through Gatekeeper, network policy
enforcement, log capture, audit chain, interactive TTY.

Most of the design slots into existing abstractions. The single highest-risk
unknown is whether the HTTP clients Moat cares about (Claude Code, Anthropic
SDK, `gh`, `git`, `curl`, Node `undici`, Python `httpx`) accept an
`HTTPS_PROXY=https://...` upstream. A compatibility spike (Phase 2) gates the
rest of the work.

## Motivation

Today Moat runs locally — Docker or Apple containers — and the Gatekeeper
proxy daemon runs on the laptop alongside them. Useful, but it ties a long-
running agent to the operator's machine: closing the laptop ends the run,
beefy agents fight for local CPU/RAM, and there's no shared place for a team
to host running agents.

RunLoop (docs.runloop.ai) provides remote managed Linux sandboxes (Devboxes),
built from Dockerfile-based Blueprints, controllable via REST API / Python+TS
SDKs / `rli` CLI. It offers tunnels, account secrets, snapshots, and hostname-
level network policies. Treating it as a third runtime lets users hand off
long-running agents to managed infrastructure without a separate code path.

## Goals

- `moat run --runtime=runloop -- <cmd>` and `moat claude --runtime=runloop`
  behave like their local equivalents from the user's point of view.
- Credentials still live outside the agent's blast radius. Gatekeeper holds
  tokens; the agent only sees a per-run auth token.
- Audit chain, network log, and console capture all reach the laptop.
- Reuse the existing `container.Runtime` interface, Dockerfile generators,
  Gatekeeper, and grant providers. Avoid forking the run lifecycle.

## Non-goals

- Multi-tenant hosted Moat. The design assumes one operator's RunLoop account.
- Bind-mount-like live workspace editing. Initial sync is rsync at lifecycle
  boundaries (see Open Questions).
- Removing local runtimes. RunLoop is an additional option, not a replacement.
- Cross-region or HA Gatekeeper. One Gatekeeper Devbox per agent Devbox.

## Topology

Gatekeeper stays outside the agent container, but with `--runtime=runloop` it
runs in its own Devbox next to the agent's Devbox rather than on the laptop.

RunLoop has no private VPC between Devboxes. The `allow_devbox_to_devbox=true`
network policy flag routes inter-Devbox traffic through RunLoop's tunnel
infrastructure, not a private network. "Next to" therefore means: agent →
`tunnel.runloop.ai` → Gatekeeper, over public HTTPS, with bearer-token auth
on the tunnel.

```
[laptop: moat CLI]
   | creates both devboxes, pushes per-run creds, streams audit/logs
   v
+-- RunLoop account ------------------------------------+
| Gatekeeper devbox                                     |
|   - port 8080 exposed as authenticated tunnel         |
|   - holds tokens; emits audit + network.jsonl         |
|             ^   (HTTPS proxy w/ Bearer token)         |
|             |                                         |
| Agent devbox                                          |
|   HTTPS_PROXY=https://8080-{tk}.tunnel.runloop.ai     |
|   + Moat CA cert in trust store                       |
|   + per-run auth token                                |
|   network_policy: allow_devbox_to_devbox=true         |
+-------------------------------------------------------+
```

The laptop is in the loop only for control-plane operations (create, push
credentials, stream logs back). Tear down the laptop and the pair keeps
running; reconnect to attach, view logs, or stop.

## What slots in cleanly

### `container.Runtime` is the right seam

`internal/container/runtime.go` defines a 20-method interface that Docker and
Apple already satisfy (`internal/container/docker.go`, `apple.go`). The
multi-runtime work tracked in
`docs/plans/2026-04-11-multi-runtime-manager-plan.md` already moved the
`Manager` to a `RuntimePool` that resolves the correct runtime per run. Adding
`RuntimeRunLoop` as a third `RuntimeType` and a `runloop.Runtime` implementation
is structurally the same kind of addition Apple containers were.

Method-by-method mapping:

| `Runtime` method | RunLoop equivalent |
|---|---|
| `CreateContainer` | `POST /v1/devboxes` with a `blueprint_id` derived from the run's Dockerfile |
| `StartContainer` | No-op. Devboxes start on create. |
| `WaitContainer` | Poll `GET /v1/devboxes/{id}` until status terminal; map to exit code |
| `StopContainer` | `POST /v1/devboxes/{id}/shutdown` |
| `RemoveContainer` | Same shutdown; RunLoop has no separate remove |
| `Exec` | `POST /v1/devboxes/{id}/execute_sync` |
| `ContainerLogs` (follow) | `execute_async` with stream subscription |
| `ContainerLogsAll` | `execute_async` result body, after exit |
| `StartAttached` | `ssh -t` into the devbox via tunnel; forward `SIGWINCH` |
| `ResizeTTY` | SSH window-change message (sent by the same path) |
| `BuildManager` | Blueprint create/lookup, content-addressed (see below) |
| `NetworkManager` | Devbox-to-Devbox via `allow_devbox_to_devbox=true`; no user-defined networks |
| `ServiceManager` / `SidecarManager` | Initially: bake into blueprint. Future: separate service Devboxes. See Open Questions. |
| `GetHostAddress` | Returns the Gatekeeper tunnel hostname |
| `SupportsHostNetwork` | `false` |
| `SetupFirewall` | No-op. Replaced by RunLoop network policy at create time. Behavioural change, see Open Questions. |

### Existing Dockerfile generators are reusable

`internal/providers/claude/dockerfile.go` and the equivalent in
`internal/providers/codex/` already produce Dockerfiles for the agent
container. A RunLoop Blueprint is just a Dockerfile plus metadata. The new
`runloop.BuildManager` translates a generated Dockerfile to a Blueprint
create call, content-hashes the Dockerfile + context files, and caches the
resulting `blueprint_id` so identical inputs reuse a built Blueprint.

### `buildProxyEnv` is unchanged

`internal/run/manager.go:4206` (`buildProxyEnv`) builds the proxy env block
using a synthetic hostname (`moat-proxy`). The RunLoop adapter resolves
`moat-proxy` to the Gatekeeper tunnel URL via the `MOAT_EXTRA_HOSTS` /
`/etc/hosts` injection path that already exists for Apple containers
(`internal/run/manager.go` around line 1110, `synthHostStrategy`). The only
RunLoop-specific bit is the URL scheme — `https://` instead of `http://` —
and that lives in `runloop.Runtime`'s `GetHostAddress` plus a tweak to
`buildProxyEnv` to accept an upstream scheme. See the compatibility spike in
Open Questions.

### Snapshots map onto warm starts

RunLoop Devbox snapshots are a good fit for "I want the same agent
environment to come back up cheaply." Out of scope for the first slice, but
the design doesn't preclude it: a snapshot ID is just another field in the
run metadata, restored when the user runs the same `moat.yaml` again.

## What needs new code

### Package layout

```
internal/container/runloop/
  runtime.go          # Runtime interface implementation
  blueprint.go        # Dockerfile -> Blueprint translation + cache
  devbox.go           # Devbox lifecycle (create, poll, shutdown)
  tunnel.go           # Tunnel URL construction, bearer-token plumbing
  ssh.go              # StartAttached via ssh -t through the tunnel
  client.go           # Thin RunLoop REST/SDK wrapper
  network.go          # NetworkManager (no-op shape, policy applied at create)
  service.go          # ServiceManager (blueprint-bake; future: service devbox)
```

`internal/container/detect.go` and `internal/container/pool.go` learn about
`RuntimeRunLoop`. Selection: `--runtime=runloop` flag or `runtime: runloop`
in `moat.yaml`. Auto-detect does **not** pick RunLoop — it requires an
explicit account-level config (API key, default snapshot policy).

### Devbox pair lifecycle

A new `runloop.PairManager` lives alongside `runloop.Runtime`. On run create
it provisions:

1. The Gatekeeper Devbox, from a fixed Moat-published Blueprint that contains
   the Gatekeeper binary, the per-run CA material (uploaded via the admin
   endpoint after boot), and a `launch_commands` entry to start Gatekeeper
   listening on `:8080`.
2. A network policy on the Gatekeeper Devbox that allows the union of
   hostnames the active grants might proxy to (derived at create time from
   the registered providers — not hardcoded).
3. The agent Devbox, from the blueprint built from the agent Dockerfile.
4. A network policy on the agent Devbox with `allow_devbox_to_devbox=true`
   plus the minimal hostname set the agent itself needs outside the proxy
   path (typically empty — everything goes through Gatekeeper).
5. The authenticated tunnel on the Gatekeeper Devbox port 8080.
6. The agent Devbox's `HTTPS_PROXY` env, CA cert, and per-run auth token.

Teardown is the reverse: shut down agent Devbox, then Gatekeeper Devbox, then
revoke the tunnel.

### Credential push API on the Gatekeeper Devbox

Today the laptop talks to a local Gatekeeper daemon via a Unix socket
(`~/.moat/proxy/daemon.sock`, see `internal/daemon/api.go`). With Gatekeeper
in a Devbox, the same API needs to be reachable over the network. Approach:

- Add a small admin endpoint to the Gatekeeper image, listening on a separate
  port (e.g. `:8081`) inside the Devbox, exposed as its own tunnel.
- Wire it as a `daemon.Transport` implementation — the existing
  `internal/daemon/client.go` already abstracts over the wire, so the laptop
  side keeps using the same `daemon.Client` interface.
- Authenticate it with a one-shot bootstrap token generated at Devbox create
  time, replaced on first call with a long-lived per-run admin token.
- Strict additive-only API compatibility, same rule as the local daemon
  (see `internal/daemon/api.go` package doc).

This preserves the daemon's existing dependency-inversion design (see
`docs/plans/2026-04-03-proxy-dependency-inversion-design.md`) — only the
transport changes.

### PTY for `StartAttached`

Local runtimes attach to the container's TTY directly. For RunLoop the agent
Devbox is reachable via SSH (RunLoop exposes per-Devbox SSH). `StartAttached`
opens `ssh -t` into the Devbox, runs the agent command, forwards stdin/stdout
and `SIGWINCH` resize events. `ResizeTTY` becomes an SSH window-change.

Specifics that need care:

- Initial `InitialWidth`/`InitialHeight` are passed via `stty rows cols`
  inside the SSH session before the agent command runs, matching what the
  Apple `StartAttached` path does today via the runtime API.
- The TTY output also feeds the `logBuffer` in the run, exactly like Apple
  (`MEMORY.md` notes that Apple TTY output is captured via `logBuffer` in
  `StartAttached`, not the runtime log API).

### Workspace sync

First version: rsync at lifecycle boundaries. On run create the laptop
rsyncs `./` into `/workspace` on the agent Devbox. On stop (or on explicit
`moat pull`) it rsyncs back. This breaks the local UX of editing on the
laptop and seeing changes live in the container, so RunLoop runs that need
the workspace must opt in to a `remote_workspace: true` mode that disables
the bind-mount expectation and documents the rsync semantics.

Alternatives, deferred:

- **sshfs / FUSE** — preserves the live-edit feel, latency-sensitive.
- **No sync — clone from git inside the Devbox** — most honest for fully
  remote runs; doesn't help when the operator has uncommitted changes.

### Audit chain replication

`internal/audit` writes a hash-chained SQLite log per run, on the same
machine as the proxy. With Gatekeeper remote, the audit DB lives on the
Gatekeeper Devbox.

Options, written in order of preference:

1. **Stream entries to the laptop as they're written.** A small follower
   process on the laptop subscribes over the admin tunnel and appends to a
   local replica. Verification runs against the local copy. Authoritative
   chain lives on the Gatekeeper Devbox until shutdown.
2. **Pull on shutdown.** Simpler; loses entries if the Gatekeeper Devbox is
   destroyed before the laptop reconnects.

Recommend (1) but ship (2) first as a forcing function — most runs are short
enough that on-shutdown pull is adequate.

## Open questions

These need answers before, or during, implementation. Each is flagged
**spike** if a small experiment should answer it before committing to the
direction.

### 1. `HTTPS_PROXY=https://...` client compatibility (spike, blocking)

The single most likely thing to derail this design. `HTTPS_PROXY` pointing at
an `https://` URL is a valid HTTP-proxy spec (TLS to the proxy, then CONNECT),
but real-world client support is uneven. Concretely we need to validate:

- Claude Code's HTTP stack
- Anthropic SDK (Python and TypeScript)
- `gh` CLI
- `git` over HTTPS
- `curl`
- Node `undici` (the default for Node 18+ fetch)
- Python `httpx` and `requests`

If any of those don't handle `https://` upstream cleanly, the fallback is a
tiny in-Devbox shim listening on `http://127.0.0.1:N` that CONNECT-tunnels
each request out to the `https://` Gatekeeper tunnel. The shim holds no
credentials — Gatekeeper is still "outside the agent container" in the
trust sense — but it's an extra hop and another process to babysit.

**Action:** Phase 2 starts with this matrix. If `httpx`/`undici`/`gh` all
work, no shim. If any one fails, ship the shim from day one and treat it as
a permanent component.

### 2. Auth stacking on the tunnel

RunLoop tunnels can require a bearer token; Moat's proxy uses a per-run auth
token in `Proxy-Authorization`. Two options:

(a) **Stack them.** Outer `Authorization: Bearer {tunnel_token}` for the
tunnel; inner `Proxy-Authorization: Basic moat:{run_token}` for Gatekeeper.
Cleanest from a layering perspective, but several HTTP clients won't set both
headers cleanly on a proxied request — `Proxy-Authorization` is the one
clients are likely to omit when the proxy URL contains credentials.

(b) **Single Moat-signed token.** Run the RunLoop tunnel in open mode (no
bearer); Gatekeeper validates the request itself. Simpler for clients,
weaker outer auth — relies on TLS plus the obscure tunnel URL as a capability.

Recommend (b) for the first slice, with (a) as a hardening step once the
client-compat spike has shown which clients reliably send both.

### 3. Credential refresh ownership

Today the laptop refreshes tokens (gh, AWS STS, OAuth) and pushes the new
values to the local Gatekeeper. Two ways to handle this with a remote
Gatekeeper:

- **Laptop pushes refreshes over the admin tunnel.** Works only while the
  laptop is online. Fully laptop-independent long runs aren't possible.
- **Gatekeeper Devbox refreshes itself.** Needs the Gatekeeper image's
  network policy to allow OAuth/STS endpoints, plus the refresh credential
  material to live on the Devbox.

Decision: support both. Short interactive sessions use laptop push (no
extra surface area on Gatekeeper). For long unattended runs the user opts
into `runloop.refresh: remote`, and the relevant `provider.RefreshableProvider`
implementations (`internal/providers/github`, `internal/providers/aws`) run
on the Gatekeeper Devbox instead of the laptop. This must be made explicit
in docs — it changes the threat model.

### 4. Gatekeeper Devbox lifecycle: 1:1 or shared

- **1:1** — one Gatekeeper Devbox per agent Devbox. Strong isolation, simple
  lifecycle, expensive.
- **Shared** — one Gatekeeper serving many agent Devboxes, with the existing
  token-keyed multi-tenant proxy semantics. Cheaper, suspend/resume eligible,
  but reintroduces multi-tenant proxy isolation inside a sandbox.

Recommend 1:1 for the first release. Shared is a follow-up once the design
has run in the wild.

### 5. Network policy granularity

RunLoop network policies are hostname-only — no port or protocol filtering.
This is coarser than `internal/proxy`'s per-host/per-method/per-path rules,
but those still apply: RunLoop blocks at the network layer, Gatekeeper
blocks at the request layer.

Practical implication: the Gatekeeper Devbox's RunLoop policy is computed at
create time as the union of every hostname any active grant might proxy to.
This list comes from the registered providers, not a hardcoded constant.
Adding a grant after the fact requires either restarting the Gatekeeper
Devbox or accepting a wider initial policy. Recommend the former.

### 6. No equivalent for `SetupFirewall`

The local runtimes implement `SetupFirewall` (`runtime.go:111`) via
iptables/ip6tables inside the container, requiring `NET_ADMIN`. RunLoop
Devboxes don't grant that capability, so the in-container egress lockdown
isn't available.

Replacement: the RunLoop network policy on the agent Devbox restricts egress
to the Gatekeeper tunnel host (and any explicit `network.allow` entries).
This is functionally equivalent for the common case — egress is forced
through Gatekeeper — but it's a behavioural change worth documenting:
`network.policy: strict` plus a misconfigured grant fails differently
(blocked at the RunLoop edge, not the iptables rule).

### 7. Sidecars and service dependencies

Moat supports service sidecars (Postgres, Redis, etc.) via
`internal/container/docker_service.go` and the Apple equivalent. RunLoop has
no sidecar primitive — a Devbox is the unit.

Two options:

(a) **Bake into the blueprint.** Use Blueprint `launch_commands` to start
the service inside the agent Devbox. Works for single-instance dev services.

(b) **Service Devbox per service.** A separate Devbox per service tier,
joined to the agent via `allow_devbox_to_devbox=true`. Closer to today's
Docker behaviour but multiplies Devbox count.

Recommend (a) for the first slice. (b) is the long-term answer for services
that need their own lifecycle (e.g. a Postgres that should outlive the
agent).

### 8. Workspace sync UX

Already discussed under "What needs new code". The trade-off is fundamental:
RunLoop runs aren't on the same filesystem as the operator. Rsync at
boundaries with a `remote_workspace: true` opt-in is the honest minimum.
Anything live (sshfs, FUSE) is a future enhancement.

## Phasing

Suggested first-slice order. Each phase ends at a checkpoint where the
behaviour is demonstrable and the next phase has a clear go/no-go signal.

1. **Non-interactive end-to-end, no Gatekeeper.**
   `moat run --runtime=runloop -- echo hi`. Validates blueprint translation,
   exit codes, log streaming, run state machine. Direct internet from the
   Devbox; no credential injection.

2. **Gatekeeper Devbox pair + cert trust + single grant.**
   Add the Devbox pair lifecycle, the tunnel, and CA install. Wire one
   grant (Anthropic) through the existing provider path. **This is the
   phase that runs the `HTTPS_PROXY` compatibility matrix from Open
   Question 1.** If it fails, this phase also delivers the localhost shim.

3. **Per-run credential push API on Gatekeeper Devbox.**
   Move the admin endpoint into the Gatekeeper blueprint, route
   `daemon.Client` through the tunnel. Bring up multi-grant runs.

4. **Interactive `moat claude --runtime=runloop`.**
   PTY/SSH path, resize forwarding, log buffer capture matching the Apple
   container behaviour.

5. **Workspace sync.**
   Rsync at boundaries, `remote_workspace: true` opt-in, documented UX.

Snapshot support, refresh-on-Gatekeeper, and service Devboxes follow once
all five are in.

## Risks

- **Client compatibility with `HTTPS_PROXY=https://...`** — covered in
  Open Question 1. The localhost shim is the fallback and is small enough
  that the design survives even if every relevant client fails. The risk is
  schedule, not feasibility.
- **RunLoop API stability** — Moat would gain a dependency on a third-party
  managed service. The `runloop.Runtime` adapter contains all of that
  surface; if RunLoop changes shape, only that package moves.
- **Audit chain integrity across the network** — entries are written
  authoritatively on the Gatekeeper Devbox. If the Devbox is destroyed
  before replication completes, the local replica is incomplete. On-shutdown
  pull (the deferred fallback) is the worst case. Streaming replication
  closes most of the gap; the remainder is acceptable for a managed-runtime
  feature.
- **Cost surprise** — every run spins up two Devboxes. The CLI should warn
  on the first `--runtime=runloop` invocation and link to the per-Devbox
  cost docs. Shared Gatekeeper (Open Question 4) is the structural answer.
- **Documentation drift** — the threat model differs from local. CLAUDE.md,
  `docs/content/concepts/`, and the network-policy reference all need
  updates that explicitly mention RunLoop's coarser policy and the
  tunnel-based "inter-devbox" model.

## References

- `internal/container/runtime.go` — interface this runtime implements
- `internal/container/pool.go` — runtime selection and lazy init
- `internal/run/manager.go` (`buildProxyEnv` at line 4206; `proxyHost` /
  `synthHostStrategy` around line 1110) — proxy env wiring that the new
  runtime reuses unchanged
- `internal/daemon/api.go` — daemon API contract the credential push
  endpoint must remain compatible with
- `internal/providers/claude/dockerfile.go`,
  `internal/providers/codex/` — Dockerfile generators reused as Blueprint
  inputs
- `docs/plans/2026-04-11-multi-runtime-manager-plan.md` — prior work that
  made the `Manager` runtime-agnostic; this design depends on it
- `docs/plans/2026-03-30-gatekeeper-extraction-design.md` — Gatekeeper
  package boundary that lets it ship as a Blueprint
- `docs/plans/2026-04-03-host-traffic-blocking-design.md` — example of
  documenting a runtime-behaviour change in this style
