# Debug File Logging Design

## Problem

Moat's internal logs write to stderr, which breaks TUIs in interactive mode. Users need persistent debug logs for troubleshooting without disrupting the terminal experience.

## Solution

Always-on file logging to `~/.moat/debug/` with daily rotation, configurable retention, and clean stderr output.

## File Structure

**Directory:** `~/.moat/debug/`

**Naming:** `YYYY-MM-DD.jsonl` (one file per day)

**Symlink:** `~/.moat/debug/latest` points to current day's file

**Format:** JSONL (one JSON object per line)

```json
{"ts":"2026-02-06T10:30:45.123Z","level":"DEBUG","msg":"loading config","component":"run","run_id":"abc123"}
{"ts":"2026-02-06T10:30:45.456Z","level":"INFO","msg":"container started","component":"container","image":"node:22-slim"}
```

**Fields:**
- `ts` — RFC3339 timestamp with milliseconds
- `level` — DEBUG, INFO, WARN, ERROR
- `msg` — Log message
- Additional structured fields from log calls

## Configuration

Add to `~/.moat/config.yaml`:

```yaml
debug:
  retention_days: 14  # Default: 14
```

Only retention is configurable. File logging is always on, always captures all levels, and uses a fixed path.

## Stderr Behavior

| Mode | Default stderr | With `--verbose` |
|------|----------------|------------------|
| Non-interactive | Warn, Error | Debug, Info, Warn, Error |
| Interactive (`-i`) | Warn, Error | Warn, Error (flag ignored) |

File logging always captures all levels regardless of mode or flags.

For real-time debugging in interactive mode: `tail -f ~/.moat/debug/latest`

## Scope

This covers moat's internal logging only (`log.Debug`, `log.Info`, etc.).

- Container stdout/stderr passes through to terminal unchanged
- Run logs remain in `~/.moat/runs/<id>/logs.jsonl`, viewed via `moat logs`

## Implementation

### New file: `internal/log/file.go`

Isolated file logging logic:

- `FileWriter` — Manages daily file, rotation, symlink updates
- `Cleanup()` — Deletes files older than retention period
- Thread-safe with `sync.Mutex`

### Modified: `internal/log/log.go`

Update `Init()` to:

1. Accept interactive mode flag
2. Set up dual slog handlers (stderr + file)
3. Run cleanup on startup

### Modified: `cmd/moat/cli/root.go`

Pass interactive mode to `log.Init()`.

### Optional: `internal/config/config.go`

Add `Debug.RetentionDays` if global config struct exists.

## Error Handling

- File write failures: Log warning to stderr, continue without file logging
- Cleanup errors: Log warning, don't block startup
- Missing directory: Create it
- Malformed filenames during cleanup: Ignore (don't delete)

Graceful degradation: File logging is best-effort, never crashes moat.

## Permissions

- Directory: `0755`
- Log files: `0644`
- Symlink: Updated atomically (temp file + rename)

## Startup Sequence

1. Create `~/.moat/debug/` if missing
2. Run cleanup (delete files older than retention)
3. Open today's file
4. Update `latest` symlink
5. Initialize slog handlers
