# TTY Trace Debugging Guide

## Overview

Moat includes a terminal I/O tracing system for debugging TUI (text user interface) rendering issues. This system captures all stdin/stdout/stderr, control sequences, terminal resizes, and timing information to help diagnose problems like:

- Partial screen repaints leaving old content
- Random overlays and garbled text
- Issues with spinners and loading animations
- Terminal resize handling problems

## Quick Start

### 1. Capture a Trace

Add `--tty-trace=filename.json` to any interactive moat command:

```bash
# Capture Claude Code session
moat claude --tty-trace=claude-session.json

# Capture interactive run
moat run -i --tty-trace=debug.json -- htop

# Capture attach session
moat attach -i <run-id> --tty-trace=attach.json
```

The trace will be saved when the session ends.

### 2. Analyze the Trace

```bash
# Show summary and find common issues
moat tty-trace analyze claude-session.json

# Decode all control sequences (verbose output)
moat tty-trace analyze claude-session.json --decode

# Find screen clear operations
moat tty-trace analyze claude-session.json --find-clears

# Find resize timing issues (clears within 100ms of resize)
moat tty-trace analyze claude-session.json --find-resize-issues

# Custom resize window (e.g., 50ms)
moat tty-trace analyze claude-session.json --find-resize-issues --resize-window=50
```

## Trace Format

Traces are stored as JSON with:

- **Metadata**: Run ID, command, environment variables (TERM, LANG, etc.), initial terminal size
- **Events**: Timestamped I/O events (stdout, stderr, stdin, resize, signal)

Example trace structure:

```json
{
  "metadata": {
    "timestamp": "2026-01-31T10:30:00Z",
    "run_id": "abc123",
    "command": ["claude"],
    "environment": {
      "TERM": "xterm-256color",
      "COLUMNS": "120",
      "LINES": "40"
    },
    "initial_size": {"width": 120, "height": 40}
  },
  "events": [
    {
      "ts": 0,
      "type": "stdout",
      "data": "ESC[2J..." (base64 in JSON)
    },
    {
      "ts": 50000000,
      "type": "resize",
      "size": {"width": 80, "height": 24}
    }
  ]
}
```

## Common TUI Issues and Diagnosis

### Issue: Partial screen repaints

**Symptom**: Old text remains on screen when TUI refreshes

**Diagnosis**:
```bash
moat tty-trace analyze trace.json --decode | grep -E "clear|ESC\[J|ESC\[K"
```

Look for missing screen clears or incorrect erase sequences.

### Issue: Random overlays

**Symptom**: Text appears on top of other text in wrong positions

**Diagnosis**:
```bash
moat tty-trace analyze trace.json --decode | grep -E "cursor position|ESC\[H|ESC\[.*H"
```

Check for cursor positioning sequences that might be missing or incorrect.

### Issue: Spinner/loading animation artifacts

**Symptom**: Loading spinner leaves characters behind

**Diagnosis**:
```bash
moat tty-trace analyze trace.json --decode | grep -A 2 -B 2 "spinner\|Loading\|..."
```

Look for the sequence of clears/cursor movements around spinner updates.

### Issue: Resize handling problems

**Symptom**: Screen is corrupted after terminal resize

**Diagnosis**:
```bash
moat tty-trace analyze trace.json --find-resize-issues
```

This finds cases where the app clears the screen too quickly after a resize event, before the app has processed the new size.

## Analysis Tips

### 1. Compare Working vs Broken Traces

Capture a trace of a working TUI app (like htop) and compare with the problematic app:

```bash
# Working example
moat run -i --tty-trace=good.json -- htop
# Problematic app
moat claude --tty-trace=bad.json

# Compare event counts
moat tty-trace analyze good.json | grep "Event breakdown"
moat tty-trace analyze bad.json | grep "Event breakdown"
```

### 2. Look for Timing Issues

Check if events happen in unexpected order:

```bash
moat tty-trace analyze trace.json --decode | head -100
```

Events are shown with timestamps in seconds (e.g., `[  0.123s]`).

### 3. Check Environment Variables

The trace captures TERM, COLUMNS, LINES, etc. Verify these match the host:

```bash
moat tty-trace analyze trace.json | grep -A 10 "Environment"
```

### 4. Find Problematic Sequences

Search for specific control sequences:

```bash
# Find all screen clears
moat tty-trace analyze trace.json --decode | grep "clear.*screen"

# Find cursor saves/restores
moat tty-trace analyze trace.json --decode | grep "save\|restore"

# Find scroll region changes
moat tty-trace analyze trace.json --decode | grep "scroll region"
```

## Sharing Traces

**Warning**: Traces contain all terminal I/O, including potentially sensitive data (API keys, passwords, etc.).

Before sharing a trace:

1. Review the output in a safe environment
2. Consider using `--decode` to see what's captured
3. Sanitize sensitive data manually if needed

**TODO**: Add `moat tty-trace sanitize` command to automatically redact sensitive data while preserving control sequences.

## Limitations

- Traces can be large for long sessions (every byte is recorded)
- No replay functionality yet (planned)
- Binary output (like images) is captured but may not display correctly in analysis

## Integration with Code

To add tracing to your own container runs:

```go
import "github.com/majorcontext/moat/internal/trace"

// Create recorder
recorder := trace.NewRecorder(runID, command, trace.GetTraceEnv(), trace.Size{Width: 80, Height: 24})

// Wrap I/O
stdout = trace.NewRecordingWriter(stdout, recorder, trace.EventStdout)
stdin = trace.NewRecordingReader(stdin, recorder, trace.EventStdin)

// Record resize
recorder.AddResize(newWidth, newHeight)

// Save when done
defer recorder.Save("/path/to/trace.json")
```

## Future Enhancements

- **Replay**: `moat tty-trace replay trace.json` to replay the session
- **Diff**: Compare two traces side-by-side
- **Sanitizer**: Automatic redaction of sensitive data
- **Real-time capture**: Stream events to file during session
- **Filter**: Extract specific time ranges or event types
