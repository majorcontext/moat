# Volume-Mode Workspaces — Design

**Date:** 2026-06-23
**Status:** Draft for review
**Scope:** Core feature only. The IDE-attach helper and shared/cache volumes are explicitly out of scope and become separate follow-on specs.

## Problem

Moat always bind-mounts the host working tree into the container at `/workspace`,
read-write. This has two costs:

1. **No host protection.** The agent can modify (or corrupt) host files directly,
   including the real `.git` directory — rewriting history, deleting refs, etc.
2. **macOS I/O is slow.** On Docker Desktop for macOS, bind mounts cross the
   osxfs/virtiofs boundary on every read and write. Large or I/O-heavy workloads
   pay a continuous penalty.

**Volume mode** addresses both: copy the host tree once into an isolated Docker
named volume, run the agent entirely against that volume, and protect the host.
On macOS the named volume lives inside the Docker Linux VM, so all agent I/O is
native-speed after a one-time copy-in.

## Goals

- Let a project (or a single run) opt into an isolated copy of the workspace in a
  named volume instead of a bind mount.
- Protect the host: nothing the agent does writes back to the host automatically.
- Recover the macOS I/O cost as a one-time startup copy rather than a per-operation tax.
- Provide an explicit, safe path to extract changes back to the host.

## Non-Goals (this spec)

- **IDE-attach helper** — a shortcut to open an IDE into the running container.
  Separate follow-on spec.
- **Shared / cache volumes** — read-mostly volumes (build caches) reused across
  sandboxes. Separate follow-on spec. Note: Moat's existing `volumes:` config
  (host-dir-backed named mounts) continues to work alongside volume mode and is
  unchanged here.
- **Apple containers** — volume mode is Docker-first (see Runtime Support).
- **Live or automatic sync** back to the host. Extraction is manual and explicit.
- **Persistent / reusable volumes** — volumes are ephemeral per run.

## Decisions (locked)

| Decision | Choice |
| --- | --- |
| Scope | Core volume-mode workspace only |
| Sync model | One-way copy-in; manual extraction (host fully protected) |
| Opt-in | `moat.yaml` `workspace.mode` default + CLI flag override |
| Copy-in source | Full working tree, honoring the existing `exclude:` list |
| Copy-in mechanism | Read-only staging bind + copy in `moat-init.sh` (as root) + chown |
| Extraction | Volume-aware snapshots (reuse `moat snapshot` / `restore --to`) |
| Lifecycle | Ephemeral per run; volume removed on run destroy |
| Runtime | Docker-first; Apple containers reject volume mode with a clear error |
| `.git` (standard repo) | Copied verbatim into the volume |
| `.git` (worktree/submodule) | Detected and rejected in v1 with a clear message (see Open Questions — worktree v1 scope is unresolved) |
| Snapshots & `.git` | Volume-mode snapshots **include** `.git` (lift the default exclusion) — see the secret-capture residual risk under Snapshots |
| In-place restore (volume mode) | Blocked; volume-mode `restore` requires `--to <dir>` to preserve the host-protection guarantee |

## User-Facing Behavior

### Opting in

`moat.yaml` gains a `workspace` block:

```yaml
workspace:
  mode: volume   # one of: bind (default) | volume
```

CLI override (per run), highest precedence:

```bash
moat run --workspace-mode volume   # force volume for this run
moat run --workspace-mode bind     # force bind, ignoring moat.yaml
```

Precedence: CLI flag > `moat.yaml` `workspace.mode` > default (`bind`).

A single `--workspace-mode <bind|volume>` flag is the whole surface; no
`--volume-workspace` convenience alias is added (two flags for one setting would
only add a conflict-detection path with no offsetting goal).

### What gets copied

The full workspace directory is copied into the volume, **except** paths listed
in the `exclude:` of the `/workspace` mount entry (the same field that drives
tmpfs overlays in bind mode). In volume mode, `exclude:` means "do not copy this
in" — e.g. `node_modules`, `dist`, build output. Excluded paths simply don't
exist in the volume; a `pre_run` hook can rebuild them (`npm ci`, etc.).

Excluding `.git` (or a subpath of it) is **disallowed** in volume mode and
produces a clear error — a partial `.git` is a broken repository. This is
enforced at config-validation time (in the `WorkspaceConfig`/mount validation
path, alongside the `mode` value check), so the error surfaces before any
container is created rather than failing silently inside `moat-init.sh`.

### Getting changes out

Extraction reuses the snapshot machinery, made volume-aware:

```bash
moat snapshot <run>                                  # capture /workspace from the volume
moat snapshot restore <run-id> [snapshot-id] --to ~/out   # extract to a side directory
```

(The `restore` signature is the existing one: `moat snapshot restore <run-id>
[snapshot-id]`, with `--to <dir>` selecting extract-to-directory over the default
in-place restore.)

Because volume-mode snapshots include `.git`, the extracted copy is a full repo
with any commits the agent made inside the volume. The blessed recovery flow
never writes the host directly:

```bash
moat snapshot restore <run-id> --to ~/agent-out
git -C ~/myrepo fetch ~/agent-out         # review / merge on your terms
```

**In-place restore is blocked in volume mode.** The default `restore` (no `--to`)
overwrites the *current* workspace in place — but in volume mode the host
directory was never the live tree, so writing the agent's changes straight into
the developer's source tree is exactly the host write-back the feature exists to
prevent. Volume-mode runs therefore require `--to <dir>`; a bare `restore`
errors with a message pointing at `--to`. (This also sidesteps the archive
backend's in-place `.git`-preserve behavior — see Snapshots.)

**Extraction is not automatic, and the volume is the only copy.** A run destroyed
before any extraction snapshot is taken loses all agent work, including in-volume
commits — a sharp reversal of bind mode, where the host tree persisted by
default. To prevent silent loss, `moat destroy` (and the end-of-run cleanup) on a
volume-mode run **warns and requires `--force`** when no post-pre-run snapshot
exists for that run. The volume guide leads with the
`snapshot → restore --to → fetch` flow so the safe path is the obvious one.

## Architecture

Volume mode threads through six units, each with a single clear responsibility.

### 1. Config — `internal/config`

- New `WorkspaceConfig` type with `Mode string` (`bind` | `volume`), default `bind`.
  Added as a top-level `Workspace WorkspaceConfig` field on `Config`.
- Validation rejects any value other than `bind`/`volume` with an actionable message.
- A helper resolves the effective mode given the CLI override (override > yaml > default).

### 2. CLI — `cmd/moat` run command

- Add `--workspace-mode <bind|volume>` and the `--volume-workspace` boolean alias.
- Wire the resolved mode into run options. The alias sets mode to `volume`;
  passing both the alias and an explicit `--workspace-mode bind` is a conflict error.

### 3. Run manager — `internal/run/manager.go`

This is where mode is realized. When the effective mode is `volume`:

- **Guard: runtime.** If the runtime is Apple containers, fail fast with a clear
  error ("volume mode requires the Docker runtime; set `workspace.mode: bind` or
  run with `--runtime docker`"). No silent fallback.
- **Guard: git worktree/submodule.** If the workspace's `.git` is a *file* (not a
  directory), reject volume mode with a message pointing to bind mode or the main
  checkout. (Submodules surface the same `.git`-file shape.) This replaces bind
  mode's worktree special-case, which does not apply here.
- **Mounts.** Instead of bind-mounting the host at `/workspace`:
  - Add a **read-only** bind of the host workspace at a staging path
    (`/mnt/host-workspace`).
  - Add a **named-volume** mount at `/workspace` (see runtime abstraction below),
    with a per-run volume name `moat-ws-<run-id>`.
- **Init env.** Pass to the container: `MOAT_WORKSPACE_VOLUME=1`,
  `MOAT_WORKSPACE_STAGING=/mnt/host-workspace`, and the exclude list
  (`MOAT_WORKSPACE_EXCLUDES`) so `moat-init.sh` can populate the volume.
  `MOAT_WORKSPACE_EXCLUDES` is **newline-delimited** (one `./`-prefixed pattern
  per line), so the init script can feed it to `tar --exclude-from` without shell
  word-splitting; see the injection note in §5. Patterns are `./`-prefixed to
  match the `./`-rooted tar member names (so nested patterns like `dist/sub`
  match, not just single-component names). NUL delimiting was avoided because GNU
  tar 1.34's `--null --exclude-from` only applies the first record.
- **Container user.** Volume mode must start the container as **root** so the
  populate + `chown` step can run, then drop to `moatuser`. This matters on
  native Linux Docker, where Moat otherwise starts the container as the workspace
  owner's UID (so `moat-init.sh` runs non-root from the start and a `chown` would
  fail `EPERM`, leaving the fresh root-owned volume mountpoint unwritable). When
  `MOAT_WORKSPACE_VOLUME=1`, force `User: root` (re-introducing the existing gosu
  privilege-drop path) regardless of platform; the macOS path already runs init
  as root.
- **Cleanup.** Remove the volume only at `moat destroy` (full teardown), NOT in
  the per-container-exit cleanup path. The volume is the sole copy of the agent's
  work and must survive run-stop so the user can `moat snapshot` (extract) it
  before tearing down. A partially-created run (volume created, container create
  fails) removes its own volume via a deferred cleanup in `Create`. The daemon's
  idle GC reclaims any leaked `moat-ws-*` volume whose run directory no longer
  exists (i.e. the run was destroyed but volume removal failed, or the run was
  SIGKILLed).

### 4. Container runtime — `internal/container`

- Extend `MountConfig` to express a named volume, e.g. a `Kind` field
  (`bind` | `volume`) plus `Name` for the volume case. Bind remains the default
  (zero-value `Kind` == `bind`) so existing call sites are unaffected — **but**
  `buildContainerMounts` currently hardcodes `mount.TypeBind` for every entry and
  must be updated to branch on `Kind` (emit `mount.TypeVolume` with `Source:
  Name` for volume entries). A test asserts a zero-value `Kind` still produces a
  bind mount, so the existing paths don't silently change.
- The runtime today has no volume lifecycle at all. Add `VolumeCreate`,
  `VolumeRemove`, and `VolumeList` to the `Runtime` interface (the last is needed
  for orphan GC — see Error Handling) and implement them on both runtimes. These
  are new interface methods, not just a Docker-internal detail.
- **Docker** (`docker.go`): map a `volume` mount to `mount.TypeVolume` with
  `Source: <name>`. Create the volume via `VolumeCreate` before container create;
  remove it via `VolumeRemove(name, force)` on cleanup.
- **Apple** (`apple.go`): named-volume mounts and the volume lifecycle methods are
  unsupported; the manager-level guard prevents reaching here, but they also
  return a clear error if called, for defense in depth.

### 5. Init script — `internal/deps/scripts/moat-init.sh`

Add `populate_workspace_volume()`, gated by `MOAT_WORKSPACE_VOLUME=1`, run **as
root** before the privilege drop and before the `pre_run` hook:

1. Copy `MOAT_WORKSPACE_STAGING` → `/workspace`, honoring `MOAT_WORKSPACE_EXCLUDES`.
   Use `tar` (universally available in the base images; `rsync` is an optional
   optimization when present), with two non-negotiable flags:
   - `--exclude-from <file>` fed from the **newline-delimited** `MOAT_WORKSPACE_EXCLUDES`
     (write the records to a temp file; never expand the variable inside an
     `sh -c` string or an unquoted `tar --exclude=$VAR`). The exclude patterns
     originate in user-controlled `moat.yaml` and this step runs as **root**, so
     an unsanitized expansion is a root-phase command-injection vector. As a
     second layer, `ValidateExcludes` rejects patterns containing shell
     metacharacters (restrict to a path-safe character class) before they ever
     reach the env.
   - `--no-dereference` so symlinks are copied as symlinks, not followed. Without
     it, a workspace symlink pointing outside the tree (e.g. `./creds ->
     ~/.ssh/id_rsa`) would copy the *target's* contents into the volume, giving
     the agent persistent access to host files outside the workspace.
2. `chown -R moatuser:moatuser /workspace` so the agent owns its tree (the fresh
   volume mountpoint is created as root). This is the permission fix the feature needs;
   it lives in init rather than relying on a user `pre_run` because it must run as root.

The existing `pre_run` hook still runs afterward as `moatuser` in `/workspace`
(now the volume) — unchanged contract.

The read-only staging bind (`/mnt/host-workspace`) remains mounted for the
container's lifetime (Docker cannot cleanly unmount mid-run). This is acceptable
for the host-protection goal — the bind is read-only, so the agent can *read* but
never *write* host files — but it is a deliberate, documented residual: the
original host tree is visible to the agent at the staging path throughout the run.

### 6. Snapshots — `internal/snapshot`

The snapshot subsystem today is **host-path-coupled** — `snapshot.NewEngine` takes
a host workspace directory, the `Backend.Create(workspacePath, …)` interface walks
that host path, and the run manager auto-fires a pre-run snapshot (and escape-key
manual snapshots) against it. None of that reaches a Docker volume. Making capture
"volume-aware" is therefore a real new unit, not a one-line change. This is the
load-bearing dependency of the whole extraction model, so it is specified
concretely here:

- **New volume capture path.** Add a volume-mode `Backend.Create` source: spin a
  short-lived helper container that mounts the volume **read-only**, `tar` its
  `/workspace` to the host, and hand the archive to the existing **archive
  backend** (tar.gz). Capturing from a sidecar (rather than `exec` into the live
  container) avoids racing the agent's writes and avoids a privileged exec while
  the agent is active. The APFS copy-on-write backend does not apply (the data
  isn't on host APFS), so volume-mode snapshots always use the archive backend.
  The run must record a **volume-mode flag in `storage.Metadata`** so post-run CLI
  commands (`moat snapshot`, `moat destroy`) know to use the volume path, not the
  stale host path.
- **Automatic pre-run / escape-key snapshots in volume mode.** Because the host
  staging path is *not* the live tree, an auto pre-run snapshot of it captures the
  pristine input, not agent work — misleading. In volume mode, **suppress the
  automatic pre-run snapshot** (the host copy is already on disk; the user's source
  tree is the baseline) and route escape-key/manual snapshots through the volume
  capture path above. Do not silently snapshot the host staging dir.
- **Lift the always-on `.git` exclusion** for volume-mode snapshots so commits made
  inside the volume survive extraction. All *other* exclusion rules are preserved
  (gitignore handling per `ignore_gitignore`, and `additional` patterns). Bind-mode
  snapshots are **unchanged** — they still exclude `.git`, because in bind mode the
  host already holds the real repo. The CLI docs must make this bind-vs-volume
  contrast explicit so users know when `.git` is captured.
- **Secret-capture residual risk (accepted).** Including `.git` means snapshot
  archives under `~/.moat/runs/<id>/snapshots/` now contain full git history and
  config — which routinely holds credentials (PATs in remote URLs in `.git/config`,
  `.git-credentials`, secrets removed from the tree but still in history). These
  archives persist under the retention policy and survive `moat destroy`.
  Mitigations: (1) write snapshot archives with `0600` perms (the archive backend
  currently uses `0644`); (2) emit a one-line warning at `moat snapshot` time in
  volume mode ("archive includes `.git` — may contain credentials in history");
  (3) call out the risk in the volume-mode guide. The decision to include `.git`
  stands; this risk is documented and accepted, not silently shipped.
- **`restore --to` works as-is** (it is a bare extract via `RestoreTo`). **In-place
  `restore` must be disabled for volume-mode archives**: the archive backend's
  in-place path backs up the destination's existing `.git`, wipes the tree,
  extracts, then renames the backup back over `.git` — which, now that `.git` is
  *inside* the archive, would clobber the agent's commits with the stale host
  `.git`. Volume-mode runs require `--to` (enforced at the CLI per "Getting changes
  out"), which avoids this path entirely.

## Data Flow

```
moat run (workspace.mode=volume)
  └─ manager: guards (Docker? non-worktree?) ─ fail fast if not
       ├─ create Docker volume  moat-ws-<run-id>
       ├─ mounts: host (ro) → /mnt/host-workspace ; volume → /workspace
       └─ env: MOAT_WORKSPACE_VOLUME, _STAGING, _EXCLUDES
  └─ container start → moat-init.sh (root)
       ├─ populate_workspace_volume(): tar copy staging → /workspace (excludes)
       ├─ chown -R moatuser /workspace
       ├─ pre_run hook (moatuser, /workspace)
       └─ drop privileges → exec agent
  └─ agent runs entirely against the fast volume; host untouched
       (auto pre-run snapshot suppressed in volume mode — host staging is not the live tree)
  └─ moat snapshot <run>  → sidecar container tars /workspace from volume (incl .git) → archive
  └─ moat snapshot restore <run-id> --to ~/out  → host-side copy for review/fetch (in-place blocked)
  └─ moat destroy <run>   → stop container → VolumeRemove(moat-ws-<run-id>)
                            (warns / needs --force if no extraction snapshot exists)
```

## Error Handling

- **Apple + volume mode:** reject at manager pre-flight with remediation (switch
  runtime or use bind mode).
- **Worktree/submodule + volume mode:** reject with remediation (main checkout or
  bind mode). Detection: `.git` is a file rather than a directory.
- **`.git` in `exclude:` under volume mode:** reject (partial repo is broken).
- **Volume create/populate failure:** abort the run with the underlying error;
  remove any partially-created volume so no orphan is left.
- **`moat destroy` while the container is still running:** Docker refuses
  `VolumeRemove` on an in-use volume, so cleanup must stop the container before
  removing the volume (the existing destroy path already stops the container;
  order the volume removal after that).
- **Orphaned volumes from crashed runs:** named `moat-ws-<run-id>`; removed on the
  normal destroy path. Note this is a **Docker named-volume** GC problem and is
  *separate* from the existing `moat volumes` command, which manages host-directory
  volumes under `~/.moat/volumes/` and has no Docker API access — do not extend
  that command. The correct mechanism is a Docker `VolumeList` filtered by the
  `moat-ws-` prefix, cross-referenced against live run IDs, removing the
  unreferenced ones. **v1 commitment:** (a) the volume-mode guide documents manual
  cleanup (`docker volume ls --filter name=moat-ws- ` then `docker volume rm …`),
  and (b) the daemon's existing idle-cleanup path runs the prefix-filtered
  `VolumeList` GC pass so orphans are reclaimed without user action. A dedicated
  `moat volumes prune --docker` command is deferred, but v1 does not silently leak.

## Edge Cases

- **Empty workspace:** copy produces an empty `/workspace`; valid.
- **Very large `.git`/tree:** one-time copy-in cost on macOS; documented. Users who
  want to skip large artifacts use `exclude:` (but not for `.git`).
- **Existing `volumes:` (cache mounts):** continue to mount alongside the workspace
  volume; names don't collide (`moat-ws-*` prefix vs. user volume names).
- **Interactive / TTY, network policy, credentials, MCP:** unaffected — only the
  `/workspace` mount changes.

## Testing

Following Codebase Invariant #1 (test the companion case), each assertion gets its mirror.

- **Copy-in honors excludes:** an excluded path is absent in the volume **and**
  (companion) non-excluded files are present with correct content.
- **Permissions:** files under `/workspace` are owned by `moatuser` after init
  **and** (companion) a root-created mountpoint is chowned, not left root-owned.
- **Snapshot includes `.git` in volume mode** **and** (companion) gitignore/
  `additional` exclusions are still applied (only the `.git` rule is lifted).
- **Worktree rejected** in volume mode **and** (companion) a normal repo in volume
  mode succeeds — detector/validator parity, both directions.
- **Apple runtime rejected** in volume mode **and** (companion) Apple + bind mode
  still works.
- **Lifecycle:** the volume exists during the run **and** (companion) is removed on
  destroy.
- **Config/flag precedence:** CLI flag overrides `moat.yaml` **and** (companion) the
  yaml default applies when no flag is passed; invalid `mode` values are rejected.
- **Linux container user:** in volume mode the container starts as root and
  `chown` succeeds (files end up `moatuser`-owned) **and** (companion) a bind-mode
  run on Linux still starts as the workspace UID (no behavior change off the volume path).
- **Exclude injection rejected:** an `exclude:` pattern with shell metacharacters
  is rejected by `ValidateExcludes` **and** (companion) ordinary patterns
  (`node_modules`, `dist`) still validate and are honored by the copy-in.
- **Symlink handling:** a workspace symlink pointing outside the tree is copied as
  a symlink (target contents not pulled into the volume) **and** (companion) an
  in-tree symlink is preserved.
- **In-place restore blocked in volume mode:** a bare `moat snapshot restore` on a
  volume-mode run errors and points at `--to` **and** (companion) `restore --to`
  extracts successfully, and a bind-mode in-place restore still works.
- **Destroy guard:** destroying a volume-mode run with no extraction snapshot
  warns / requires `--force` **and** (companion) destroy proceeds cleanly once a
  snapshot exists (and a bind-mode destroy is unaffected).
- **Auto pre-run snapshot suppressed in volume mode** **and** (companion) the
  pre-run snapshot still fires in bind mode.

## Documentation

- `docs/content/reference/02-moat-yaml.md` — `workspace.mode` field.
- `docs/content/reference/01-cli.md` — `--workspace-mode` flag; note volume-mode
  behavior of `moat snapshot` (captures from the volume, **includes `.git`**, and
  requires `--to` for restore), contrasted with bind-mode snapshots (exclude
  `.git`, in-place restore allowed).
- `docs/content/guides/` — a short "Volume-mode workspaces" guide (when to use,
  copy-in semantics, extraction flow, git caveats).
- `CHANGELOG.md` — `### Added` entry with PR link placeholder.

## Open Questions / Follow-ons

- **Worktree support** — reconstruct a standalone repo in the volume (clone-style):
  fast-follow after v1.
- **Apple named-volume support** — host-dir-backed equivalent; separate effort.
- **IDE-attach helper** and **shared cache volumes** — separate specs.

## Deferred / Open Questions

### From 2026-06-23 review

- **Is rejecting git worktrees acceptable for v1?** (P1, raised by adversarial
  review) — **Resolved 2026-06-23: ship v1 with the rejection guard.** Worktree
  users stay on bind mode; clone-style standalone-repo reconstruction is a separate
  later plan, not v1. The guard rejects any `.git`-as-file workspace (worktree,
  submodule, `--separate-git-dir`) with a clear message pointing to bind mode or the
  main checkout. Rationale: keeps v1 tractable and the riskiest code (reconstruction)
  out of the critical path; revisit prioritization based on real `moat wt` usage.
