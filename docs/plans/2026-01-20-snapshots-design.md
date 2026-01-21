# Snapshot & Execution Tracing Design

**Date:** 2026-01-20
**Status:** Draft
**Author:** Design session with Claude

---

## Overview

Protect workspaces from accidental damage during agent runs by capturing filesystem state at key moments, enabling one-click recovery. Additionally, provide full visibility into every command executed inside containers.

### Goals

1. **Pre-run protection** - Automatic snapshot before each run, enabling rollback if the agent causes damage
2. **Incremental checkpoints** - Event-based snapshots during runs (git commits, build completions, idle periods)
3. **User-triggered snapshots** - Manual `moat snapshot` command for explicit control
4. **Command tracing** - Full visibility into every command executed in containers
5. **One-click rollback** - Simple recovery with automatic safety snapshots

### Non-Goals

- Real-time file sync or continuous backup
- Cross-run snapshot management
- Container state checkpointing (CRIU)

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        moat run                              │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐      │
│  │  Pre-run    │───▶│   Agent     │───▶│  Post-run   │      │
│  │  Snapshot   │    │  Execution  │    │  Cleanup    │      │
│  └─────────────┘    └──────┬──────┘    └─────────────┘      │
│                            │                                 │
│         ┌──────────────────┼──────────────────┐             │
│         ▼                  ▼                  ▼             │
│   ┌──────────┐      ┌──────────┐       ┌──────────┐        │
│   │Git Commit│      │Container │       │  User    │        │
│   │ Trigger  │      │  Idle    │       │ Trigger  │        │
│   └────┬─────┘      └────┬─────┘       └────┬─────┘        │
│        └─────────────────┼──────────────────┘              │
│                          ▼                                  │
│                  ┌───────────────┐                          │
│                  │Snapshot Engine│                          │
│                  │ (Native/Tar)  │                          │
│                  └───────┬───────┘                          │
│                          ▼                                  │
│                  ~/.moat/runs/<id>/snapshots/               │
└─────────────────────────────────────────────────────────────┘
```

### Execution Tracer

Captures every command executed in the container from the host side:

```
┌─────────────────────────────────────────────────────────┐
│                         Host                             │
│  ┌──────────────┐      ┌──────────────────────────────┐ │
│  │   Moat CLI   │─────▶│     Execution Tracer         │ │
│  └──────────────┘      │  (eBPF on Linux /            │ │
│         │              │   ES Framework on macOS)      │ │
│         ▼              └─────────────┬────────────────┘ │
│  ┌──────────────┐                    │                  │
│  │  Container   │◀───cgroup filter───┘                  │
│  │  (unprivileged)                                      │
│  └──────────────┘                                       │
└─────────────────────────────────────────────────────────┘
```

Container remains unprivileged; tracing happens at the host level.

---

## Snapshot Backends

### Native Filesystem Backends

| Filesystem | Platform | Detection | Snapshot Command | Restore Command |
|------------|----------|-----------|------------------|-----------------|
| APFS | macOS | `diskutil info` shows APFS | `tmutil localsnapshot <path>` | `tmutil restore` |
| ZFS | Linux | `zfs list` on workspace path | `zfs snapshot pool/dataset@<name>` | `zfs rollback` |
| Btrfs | Linux | `btrfs subvolume show` | `btrfs subvolume snapshot` | Delete + restore from snapshot |

Native snapshots use copy-on-write semantics - only deltas are stored, making them extremely space-efficient.

### Archive Fallback

When native snapshots aren't available (e.g., ext4 on Linux):

```bash
# Create snapshot
tar -czf snapshot.tar.gz \
  --exclude-from=.gitignore \
  --exclude-from=.moat-snapshot-exclude \
  -C /workspace .

# Restore snapshot
tar -xzf snapshot.tar.gz -C /workspace
```

### Backend Selection Logic

```go
func detectBackend(workspacePath string) Backend {
    if isAPFS(workspacePath) { return APFSBackend{} }
    if isZFS(workspacePath)  { return ZFSBackend{} }
    if isBtrfs(workspacePath){ return BtrfsBackend{} }
    return ArchiveBackend{}
}
```

### Platform Reality

| Platform | Likely Backend | Performance |
|----------|---------------|-------------|
| macOS | APFS (native) | Fast, space-efficient |
| Linux desktop | Archive (tar.gz) | Slower, full copies |
| Linux server/NAS | Possibly ZFS/Btrfs | Fast if configured |

---

## Event Triggers

### Pre-run Snapshot

- Created immediately after container starts, before agent begins execution
- Type: `pre-run`
- Special: Never deleted by retention policy

### Git Commit Trigger

Detected via execution tracer watching for `git commit` commands.

- Debounce: Wait 2 seconds after commit completes (handles rapid commits)
- Type: `git`

### Build Completion Trigger

Detected via execution tracer. Built-in patterns:

```go
var buildCommands = []string{
    "npm run build", "npm run compile",
    "yarn build",
    "go build", "go install",
    "make", "make all", "make build",
    "cargo build",
    "mvn package", "mvn compile",
    "gradle build",
}
```

Type: `build`

### Container Idle Trigger

```go
type IdleDetector struct {
    threshold     time.Duration  // default: 30s
    checkInterval time.Duration  // default: 5s
}

func (d *IdleDetector) IsIdle(containerID string) bool {
    stats := runtime.ContainerStats(containerID)
    return stats.CPUPercent < 5.0 && stats.IOWait < 1.0
}
```

- Trigger snapshot when idle for `threshold_seconds` (default 30)
- Cooldown: Don't snapshot again until activity resumes and settles
- Type: `idle`

### User-Triggered

- `moat snapshot <run-id>` creates immediate snapshot
- Works whether run is active or stopped (if workspace still exists)
- Type: `manual`

---

## Execution Tracing

### Implementation

**Linux:** eBPF tracing of `execve` syscalls, filtered by container cgroup.

**macOS:** Endpoint Security Framework for process monitoring.

### Captured Data

```go
type ExecEvent struct {
    Timestamp   time.Time
    PID         int
    PPID        int
    Command     string        // e.g., "git"
    Args        []string      // e.g., ["commit", "-m", "fix bug"]
    WorkingDir  string
    ExitCode    *int          // nil if still running
    Duration    *time.Duration
}
```

### Storage

Stored in `~/.moat/runs/<run-id>/exec.jsonl` (one JSON object per line).

### Snapshot Triggers from Exec Events

| Pattern | Snapshot Type |
|---------|---------------|
| `git commit` exits successfully | `git` |
| Build command completes | `build` |
| No new exec events for 30s | `idle` |

---

## Exclusions

### Logic

1. Parse `.gitignore` in workspace root (if `ignore_gitignore: false`)
2. Merge with `additional` patterns from config
3. For native backends: exclusions applied during restore
4. For archive backend: exclusions applied during creation

### Default Behavior

- Respects `.gitignore` by default
- Users can add additional patterns via config

---

## Retention Policy

### Backend-Aware Retention

| Backend | Default Retention | Rationale |
|---------|------------------|-----------|
| Native (APFS/ZFS/Btrfs) | Unlimited | Space-efficient (copy-on-write) |
| Archive (tar.gz) | Last 10 snapshots | Full copies, disk adds up |

### Special Rules

- Pre-run snapshot (`pre-run` type) is never auto-deleted unless `delete_initial: true`
- User can manually prune with `moat snapshots prune`

---

## Snapshot Naming

Stripe-style IDs: `snap_<random>`

Examples:
- `snap_a3f9x2`
- `snap_k8m2p1`

Optional labels for manual snapshots: `snap_b2m7t4` with label `before-refactor`

---

## CLI Commands

### List Snapshots

```bash
$ moat snapshots <run-id>

ID              TYPE        SIZE      CREATED
snap_a3f9x2     pre-run     -         2 hours ago
snap_k8m2p1     git         +12 KB    1 hour ago
snap_v7n4q9     build       +234 KB   45 min ago
snap_j2h6w3     idle        +8 KB     30 min ago
snap_p9t1y5     manual      +52 KB    10 min ago

5 snapshots (backend: apfs)
```

### Create Manual Snapshot

```bash
$ moat snapshot <run-id>
snap_w5k9n3

$ moat snapshot <run-id> --label "before-refactor"
snap_b2m7t4 (before-refactor)
```

### Rollback (In-Place)

```bash
$ moat rollback <run-id> <snap-id>

Creating safety snapshot of current state... done (snap_r4x8m2)
Restoring workspace to snap_k8m2p1... done

To undo: moat rollback <run-id> snap_r4x8m2
```

### Rollback (To Directory)

```bash
$ moat rollback <run-id> <snap-id> --to ./restored/

Extracting snapshot to ./restored/... done
```

### Prune Snapshots

```bash
$ moat snapshots prune <run-id> --keep 5

Keeping: snap_a3f9x2, snap_p9t1y5, snap_j2h6w3, snap_v7n4q9, snap_k8m2p1
Deleting: 3 snapshots (freed 45 KB)
```

---

## Configuration

All boolean config values default to `false`. Features enabled by default use `disable_*` naming.

### System Defaults

With no configuration:
- Snapshots: enabled
- All triggers: enabled (pre_run, git_commits, builds, idle @ 30s)
- Execution tracing: enabled
- Exclusions: respects .gitignore
- Retention: 10 for archive, unlimited for native, preserve initial

### Schema

```yaml
snapshots:
  disabled: false                    # set true to disable snapshots entirely

  triggers:
    disable_pre_run: false           # set true to skip pre-run snapshot
    disable_git_commits: false       # set true to skip git commit snapshots
    disable_builds: false            # set true to skip build completion snapshots
    disable_idle: false              # set true to skip idle snapshots
    idle_threshold_seconds: 30       # idle duration before triggering

  exclude:
    ignore_gitignore: false          # set true to NOT use .gitignore
    additional: []                   # extra patterns beyond .gitignore

  retention:
    max_count: 10                    # archive backend only
    delete_initial: false            # set true to allow deleting pre-run snapshot

tracing:
  disable_exec: false                # set true to disable command tracing
```

### Example Overrides

```yaml
# Disable idle snapshots only
snapshots:
  triggers:
    disable_idle: true
```

```yaml
# Disable all snapshots
snapshots:
  disabled: true
```

```yaml
# Add extra exclusions
snapshots:
  exclude:
    additional:
      - "secrets/"
      - ".env.local"
```

```yaml
# Disable execution tracing
tracing:
  disable_exec: true
```

---

## Implementation

### New Packages

```
internal/
  snapshot/
    engine.go        # Backend detection, snapshot/restore operations
    backends/
      apfs.go        # macOS APFS snapshots via tmutil
      zfs.go         # ZFS snapshot commands
      btrfs.go       # Btrfs subvolume snapshots
      archive.go     # tar.gz fallback
    retention.go     # Retention policy enforcement
  trace/
    tracer.go        # Interface for execution tracing
    ebpf_linux.go    # Linux eBPF implementation
    esf_darwin.go    # macOS Endpoint Security Framework
    events.go        # ExecEvent type, storage
```

### Run Lifecycle Integration

```go
func (m *Manager) Create(...) {
    // ... existing code ...

    // Initialize snapshot engine
    run.snapEngine = snapshot.NewEngine(workspacePath)

    // Initialize execution tracer (if not disabled)
    if !cfg.Tracing.DisableExec {
        run.tracer = trace.NewTracer(container.CgroupPath)
    }
}

func (m *Manager) Start(...) {
    // ... existing code ...

    // Pre-run snapshot
    if !cfg.Snapshots.Disabled && !cfg.Snapshots.Triggers.DisablePreRun {
        run.snapEngine.Create("pre-run")
    }

    // Start tracer, subscribe to events
    run.tracer.Start()
    run.tracer.OnExec(run.handleExecEvent)
}

func (r *Run) handleExecEvent(event ExecEvent) {
    // Log to exec.jsonl
    r.store.WriteExecEvent(event)

    // Check for snapshot triggers
    if isGitCommit(event) && !r.cfg.Snapshots.Triggers.DisableGitCommits {
        r.snapEngine.Create("git")
    }
    if isBuildComplete(event) && !r.cfg.Snapshots.Triggers.DisableBuilds {
        r.snapEngine.Create("build")
    }
}
```

### Storage Additions

```
~/.moat/runs/<run-id>/
  ├── exec.jsonl           # execution events
  ├── snapshots/           # archive snapshots (if archive backend)
  │   └── snap_a3f9x2.tar.gz
  └── snapshots.json       # snapshot metadata index
```

### Snapshot Metadata

```json
{
  "id": "snap_a3f9x2",
  "type": "pre-run",
  "label": null,
  "backend": "apfs",
  "created_at": "2026-01-20T10:30:00Z",
  "size_delta": null,
  "native_ref": "com.apple.TimeMachine.2026-01-20-103000.local"
}
```

---

## Security Considerations

1. **Container isolation preserved** - Tracing happens at host level; container remains unprivileged
2. **Snapshot permissions** - Snapshots inherit workspace permissions; no escalation
3. **Sensitive data** - Exclusions should include secrets; `.gitignore` often handles this
4. **Archive storage** - Archive snapshots stored in user's home directory with standard permissions

---

## Future Considerations

1. **Incremental archives** - Use `rsync` or similar for delta-based archive snapshots
2. **Remote snapshot storage** - Push snapshots to cloud storage for disaster recovery
3. **Snapshot diffing** - `moat snapshots diff <snap1> <snap2>` to see changes
4. **Automatic rollback on failure** - If agent exits with error, offer to rollback

---

## References

- [JTBD Analysis: AI Agent Sandboxing](../research/2026-01-13-jtbd-analysis-ai-agent-sandboxing.md)
- [Containerization & Sandboxing Research](../research/2026-01-13-containerization-sandboxing-research.md)
- HN Discussion: "if an rm -rf hits .git, you're nowhere"
