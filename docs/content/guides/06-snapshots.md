---
title: "Workspace snapshots"
description: "Create point-in-time snapshots of your workspace for recovery."
keywords: ["moat", "snapshots", "backup", "restore", "recovery"]
---

# Workspace snapshots

This guide covers workspace snapshots—point-in-time captures of your workspace that let you recover from unwanted changes.

## What snapshots capture

A snapshot captures the contents of your mounted workspace directory at a specific moment. It includes:

- All files in the workspace
- File permissions
- Directory structure

Snapshots do not include:

- Container state (running processes, memory)
- Files outside the mounted workspace
- Network connections or credentials

## Enabling snapshots

Configure snapshots in `agent.yaml`:

```yaml
snapshots:
  triggers:
    disable_pre_run: false      # Snapshot before run starts
    disable_git_commits: false  # Snapshot on git commits
    disable_builds: false       # Snapshot on builds
    disable_idle: false         # Snapshot when idle
    idle_threshold_seconds: 30  # Seconds of inactivity before idle snapshot
```

All triggers are enabled by default. Set `disable_*: true` to disable specific triggers.

## Snapshot triggers

### Pre-run snapshot

Created when the run starts, before any code executes:

```yaml
snapshots:
  triggers:
    disable_pre_run: false
```

This provides a baseline to return to if the agent makes unwanted changes.

### Git commit snapshot

Created when a git commit is detected in the workspace:

```yaml
snapshots:
  triggers:
    disable_git_commits: false
```

Captures state at each commit point.

### Build snapshot

Created when build commands are detected (npm build, go build, etc.):

```yaml
snapshots:
  triggers:
    disable_builds: false
```

### Idle snapshot

Created when the agent has been idle for a configured duration:

```yaml
snapshots:
  triggers:
    disable_idle: false
    idle_threshold_seconds: 30
```

Captures periodic checkpoints during long-running sessions.

## Listing snapshots

List snapshots for a run:

```bash
$ moat snapshot list run_a1b2c3d4e5f6

SNAPSHOT ID      TIME                   TRIGGER    SIZE
snap_pre_xyz123  2025-01-21T10:00:00Z  pre_run    45 MB
snap_abc456      2025-01-21T10:05:23Z  git_commit 46 MB
snap_def789      2025-01-21T10:12:45Z  idle       48 MB
snap_ghi012      2025-01-21T10:15:30Z  git_commit 47 MB
```

## Creating manual snapshots

Create a snapshot at any time:

```bash
moat snapshot run_a1b2c3d4e5f6
```

Use this before risky operations or when you want to preserve current state.

## Restoring a snapshot

Restore workspace to a previous snapshot:

```bash
$ moat snapshot restore run_a1b2c3d4e5f6 snap_abc456

Restoring to snapshot snap_abc456 (2025-01-21T10:05:23Z)

Warning: This will replace the current workspace contents.
Files added since the snapshot will be deleted.
Files modified since the snapshot will be reverted.

Proceed? [y/N]: y

Restore complete.
```

After rollback:

- Files match the snapshot state exactly
- Files created after the snapshot are deleted
- Modified files are reverted to snapshot versions

### Restore with running agent

If the agent is still running, restore pauses execution:

```bash
$ moat snapshot restore run_a1b2c3d4e5f6 snap_abc456

Agent is running. Rollback will:
1. Pause the agent
2. Restore the workspace
3. Resume the agent

Proceed? [y/N]: y

Pausing agent...
Restoring workspace...
Resuming agent...

Restore complete.
```

## Retention policy

Configure how many snapshots to keep:

```yaml
snapshots:
  retention:
    max_count: 10           # Keep at most 10 snapshots
    delete_initial: false   # Never delete the pre-run snapshot
```

When `max_count` is exceeded, the oldest snapshots are deleted (except pre-run if `delete_initial: false`).

## Pruning snapshots

Manually prune old snapshots:

```bash
$ moat snapshot list prune run_a1b2c3d4e5f6 --keep 5

Pruning snapshots for run_a1b2c3d4e5f6
Keeping 5 most recent snapshots
Preserving pre-run snapshot

Will delete:
  snap_abc456 (2025-01-21T10:05:23Z)
  snap_def789 (2025-01-21T10:12:45Z)

Proceed? [y/N]: y

Deleted 2 snapshots
```

Preview what would be deleted:

```bash
moat snapshot list prune run_a1b2c3d4e5f6 --keep 5 --dry-run
```

## Excluding files

Exclude files from snapshots to reduce size:

```yaml
snapshots:
  exclude:
    ignore_gitignore: false  # Respect .gitignore
    additional:
      - node_modules/
      - .git/
      - "*.log"
      - build/
      - dist/
```

With `ignore_gitignore: false` (default), files in `.gitignore` are excluded from snapshots.

## Storage location

Snapshots are stored per-run:

```
~/.moat/runs/<run-id>/
  snapshots/
    snap_pre_xyz123/
      workspace/        # Full workspace copy
      metadata.json     # Trigger, timestamp
    snap_abc456/
      workspace/
      metadata.json
```

## Disk usage

Snapshots can consume significant disk space. Check usage:

```bash
$ moat status

Disk Usage:
  Runs: 156 MB
    run_a1b2c3d4e5f6: 89 MB (5 snapshots)
    run_d4e5f6a1b2c3: 67 MB (3 snapshots)
  Images: 1.2 GB
```

Reduce snapshot size by:

1. Excluding large directories (node_modules, build artifacts)
2. Reducing `max_count`
3. Increasing `idle_threshold_seconds`
4. Disabling triggers you don't need

## Disabling snapshots

Disable snapshots entirely:

```yaml
snapshots:
  disabled: true
```

Or disable via CLI:

```bash
moat run --no-snapshots ./my-project
```

## Example: Safe experimentation

1. Configure snapshots:
   ```yaml
   name: experiment

   grants:
     - anthropic
     - github

   snapshots:
     triggers:
       disable_pre_run: false
       disable_git_commits: false
     exclude:
       additional:
         - node_modules/
   ```

2. Start Claude Code:
   ```bash
   moat claude --name experiment ./my-project
   ```

3. Let Claude make changes. Snapshots are created automatically.

4. If something goes wrong, list snapshots:
   ```bash
   moat snapshot list run_a1b2c3d4e5f6
   ```

5. Restore to a good state:
   ```bash
   moat snapshot restore run_a1b2c3d4e5f6 snap_pre_xyz123
   ```

6. Continue working from the restored state.

## Troubleshooting

### "No snapshots found"

Snapshots may be disabled. Check `agent.yaml`:

```yaml
snapshots:
  disabled: false  # Should be false or omitted
```

### "Snapshot too large"

Add exclusions:

```yaml
snapshots:
  exclude:
    additional:
      - node_modules/
      - .git/
      - "*.log"
```

### "Disk full"

Prune old snapshots:

```bash
moat snapshot list prune run_a1b2c3d4e5f6 --keep 3
```

Or clean up old runs:

```bash
moat clean
```

### Restore failed

If restore fails partway through, the workspace may be in an inconsistent state. A safety snapshot is automatically created before in-place restores, so you can undo the restore:

```bash
# List snapshots to find the safety snapshot
moat snapshot list <run-id>

# Restore the safety snapshot to undo
moat snapshot restore <run-id> <safety-snapshot-id>
```

## Related guides

- [Running Claude Code](./01-running-claude-code.md) — Use snapshots with Claude Code
- [Exposing ports](./05-exposing-ports.md) — Independent snapshots per agent
