---
title: "Volume-mode workspaces"
navTitle: "Volume workspaces"
description: "Run agents against an isolated copy of your workspace in an ephemeral Docker volume."
keywords: ["moat", "volume", "workspace", "isolation", "host protection", "macos"]
---

# Volume-mode workspaces

By default, Moat bind-mounts your host directory at `/workspace` inside the container. In bind mode, the agent reads and writes your actual files — useful for most workflows, but it means the agent can overwrite or delete anything in the directory.

Volume mode copies the working tree into an ephemeral Docker named volume before the run starts. The container operates on the copy; your host directory is never written to during the run.

## When to use volume mode

Volume mode suits two scenarios:

- **Host protection** — you want to inspect or approve changes before they land on your host (e.g., exploratory refactors, auto-generated migrations).
- **macOS I/O performance** — Docker Desktop's VirtioFS bind-mount layer adds overhead on read-heavy workloads. A named volume uses Docker's native storage and avoids that overhead.

If neither applies, bind mode is the right default.

## Enabling volume mode

### In moat.yaml

```yaml
workspace:
  mode: volume
```

### Per-run with a flag

```bash
moat claude --workspace-mode volume
moat run --workspace-mode volume -- npm test
```

The flag overrides `moat.yaml`. Pass `--workspace-mode bind` to override a `volume` setting in `moat.yaml` for a single run.

## What gets copied in

The full working tree is copied into the volume, except paths listed in the `/workspace` mount's `exclude:` field:

```yaml
workspace:
  mode: volume

mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
      - .venv
      - dist
```

Paths in `exclude:` are not copied into the volume. Use this to skip large directories that don't need to be there (build artifacts, dependency caches).

Exclude patterns may contain letters, digits, `/`, `.`, `-`, `_`, and `*`. Other characters are rejected.

`.git` cannot be excluded. The copy-in always transfers the full `.git` directory because a partial `.git` is a broken repository.

## Constraints

- **Docker only.** Volume mode requires the Docker runtime. The Apple container runtime does not support it; runs fail with a clear error. Pass `--runtime docker` if you need to force Docker on macOS.
- **Git worktrees and submodules are rejected.** When `.git` is a file rather than a directory (the case in `git worktree` checkouts and submodules), volume mode fails. Run from the main checkout or use `workspace.mode: bind`.

If any of these apply, use `workspace.mode: bind` (the default) instead.

A `mounts:` entry targeting `/workspace` is still allowed in volume mode — it is consulted only for its `exclude:` list (the named volume always provides `/workspace`, so no second mount is created).

## Extracting changes

The Docker named volume is the only copy of the agent's work. To get changes back to your host, capture a snapshot and extract it before destroying the run.

### Step 1: capture a snapshot

```bash
moat snapshot <run>
```

For volume-mode runs, `moat snapshot` reads `/workspace` from the volume rather than the host directory. The snapshot includes `.git`, so commits the agent made inside the container are preserved.

> **Caution:** snapshot archives include `.git`, which may contain credentials in git history or config (e.g., tokens embedded in remote URLs). Snapshot archives are written with mode `0600` (owner-read only). Store and transmit them accordingly.

### Step 2: extract to a directory

```bash
moat snapshot restore <run> --to ~/output
```

In-place restore is blocked for volume-mode runs (it would write back to the host, which is the opposite of what volume mode provides). You must use `--to`.

### Step 3: bring commits into your repository

```bash
git -C ~/myrepo fetch ~/output
git -C ~/myrepo merge FETCH_HEAD
```

Or inspect the diff first:

```bash
git -C ~/myrepo diff HEAD ~/output
```

## Destroying a volume-mode run

`moat destroy` refuses to destroy a volume-mode run that has no extraction snapshot, because doing so would permanently delete the agent's work:

```
this volume-mode run has no extraction snapshot; destroying it deletes the workspace volume and loses all agent changes.
Capture your work first: `moat snapshot <run>` then `moat snapshot restore <run> --to <dir>`, or pass --force to destroy anyway
```

Capture a snapshot first, or pass `--force` to discard the volume:

```bash
moat destroy --force run_a1b2c3d4e5f6
```

`moat clean` applies the same guard: it skips un-extracted volume-mode runs (even with `-f`/`--force`, which only suppresses the confirmation prompt) and exits non-zero when it does, so scripts can detect leftover work. Pass `--force-volumes` to remove them too.

## Volume lifecycle

The volume is named `moat-ws-<run-id>` and is removed when the run is destroyed. Volumes left behind by crashed runs are reclaimed automatically when the proxy daemon's idle timer fires (after 5 minutes with no active runs).

## Example: exploratory refactor

The following `moat.yaml` runs Claude Code in volume mode, excluding `node_modules` from the copy-in:

```yaml
name: refactor

grants:
  - anthropic
  - github

workspace:
  mode: volume

mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
```

Start the session:

```bash
moat claude
```

When Claude finishes, capture the result:

```bash
moat snapshot <run>
moat snapshot restore <run> --to ~/refactor-output
git -C ~/myrepo fetch ~/refactor-output
git -C ~/myrepo log FETCH_HEAD --oneline
```

Review the commits and merge if satisfied.

## Related

- [moat.yaml reference: workspace](../reference/02-moat-yaml.md#workspace) — `workspace.mode` field
- [moat.yaml reference: mounts](../reference/02-moat-yaml.md#mounts) — `exclude:` on mounts
- [Workspace snapshots](./07-snapshots.md) — snapshot triggers, retention, and pruning
- [Git worktrees](./12-worktrees.md) — parallel branches in bind mode
