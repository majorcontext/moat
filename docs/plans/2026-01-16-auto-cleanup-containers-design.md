# Auto-Cleanup Containers Design

**Date:** 2026-01-16
**Status:** Approved

## Problem

Containers accumulate after runs complete, consuming significant disk space. Users must manually run `agent destroy <id>` to clean up. In practice, this is forgotten, leading to 70GB+ of orphaned containers.

## Solution

Automatically remove containers when runs complete. Provide a `--keep` flag for debugging scenarios where container inspection is needed.

## Design

### Default Behavior

Containers are removed immediately after a run completes (via `Wait()` or `Stop()`). Logs and network data are already captured to `~/.agentops/runs/<id>/`, so no observability is lost.

### Opt-Out for Debugging

```bash
agent run my-agent .           # Container auto-removed after exit
agent run my-agent . --keep    # Container preserved for inspection
```

When `--keep` is used, the container remains and can be cleaned up later with `agent destroy <id>`.

### Failure Handling

Cleanup failures are logged but don't fail the run:

```go
if rmErr := m.runtime.RemoveContainer(ctx, r.ContainerID); rmErr != nil {
    fmt.Fprintf(os.Stderr, "Removing container: %v\n", rmErr)
}
```

## Implementation

### Changes to `internal/run/types.go`

Add `KeepContainer` field to `Run` and `Options`:

```go
type Run struct {
    // ... existing fields ...
    KeepContainer bool
}

type Options struct {
    // ... existing fields ...
    KeepContainer bool
}
```

### Changes to `internal/run/manager.go`

Add cleanup in `Wait()` after the run completes:

```go
// After r.State = StateStopped
if !r.KeepContainer {
    if rmErr := m.runtime.RemoveContainer(context.Background(), r.ContainerID); rmErr != nil {
        fmt.Fprintf(os.Stderr, "Removing container: %v\n", rmErr)
    }
}
```

Add cleanup in `Stop()` after stopping the container:

```go
// After r.State = StateStopped
if !r.KeepContainer {
    if rmErr := m.runtime.RemoveContainer(ctx, r.ContainerID); rmErr != nil {
        fmt.Fprintf(os.Stderr, "Removing container: %v\n", rmErr)
    }
}
```

### Changes to `cmd/agent/cli/run.go`

Add `--keep` flag:

```go
runCmd.Flags().BoolVar(&keepFlag, "keep", false,
    "Keep container after run completes (for debugging)")
```

Pass to options:

```go
opts := run.Options{
    // ... existing ...
    KeepContainer: keepFlag,
}
```

## Exit Paths Covered

| Scenario | Handler | Cleanup |
|----------|---------|---------|
| Normal completion | `Wait()` | Yes |
| User interrupt (Ctrl+C) | `Stop()` | Yes |
| Context cancellation | `Stop()` via `Wait()` | Yes |
| Explicit destroy | `Destroy()` | Yes (existing) |
