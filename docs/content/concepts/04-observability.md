---
title: "Observability"
description: "Logs, network traces, and execution spans captured for every run."
keywords: ["moat", "observability", "logs", "traces", "network", "debugging"]
---

# Observability

Moat captures three types of observability data for every run: container logs, network requests, and execution traces. This data helps you understand what an agent did, debug failures, and audit behavior.

## Container logs

Container stdout and stderr are captured with timestamps.

View logs for the most recent run:

```bash
$ moat logs

[2025-01-21T10:23:44.512Z] Starting server on port 3000
[2025-01-21T10:23:44.789Z] Connected to database
[2025-01-21T10:23:45.123Z] Ready to accept requests
```

View logs for a specific run:

```bash
moat logs run_a1b2c3d4e5f6
```

Show the last N lines:

```bash
moat logs -n 50
```

### Log format

Logs are stored in JSONL format at `~/.moat/runs/<run-id>/logs.jsonl`:

```json
{"timestamp":"2025-01-21T10:23:44.512Z","stream":"stdout","line":"Starting server on port 3000"}
{"timestamp":"2025-01-21T10:23:44.789Z","stream":"stdout","line":"Connected to database"}
{"timestamp":"2025-01-21T10:23:45.001Z","stream":"stderr","line":"Warning: deprecated API"}
```

Each entry includes:
- `timestamp` — ISO 8601 timestamp
- `stream` — `stdout` or `stderr`
- `line` — The log line content

### Live output

To see output as it happens, attach to a running container:

```bash
moat attach run_a1b2c3d4e5f6
```

Or start a run in attached mode (the default):

```bash
moat run ./my-project
```

> **Note:** The `--follow` flag for `moat logs` is not yet implemented. Use `moat attach` for live output.

## Network traces

All HTTP and HTTPS requests through the proxy are recorded.

View network requests for the most recent run:

```bash
$ moat trace --network

[10:23:44.512] GET https://api.github.com/user 200 (89ms)
[10:23:45.001] POST https://api.anthropic.com/v1/messages 200 (1.2s)
[10:23:46.234] GET https://api.github.com/repos/org/repo 200 (45ms)
```

Each line shows:
- Timestamp
- HTTP method
- Full URL
- Response status code
- Request duration

### Verbose mode

Show request and response details:

```bash
$ moat trace --network -v

[10:23:44.512] GET https://api.github.com/user 200 (89ms)
  Request Headers:
    User-Agent: python-requests/2.28.0
    Accept: application/json
    Authorization: Bearer [REDACTED]
  Response Headers:
    Content-Type: application/json; charset=utf-8
    X-RateLimit-Limit: 5000
    X-RateLimit-Remaining: 4999
  Response Body (truncated):
    {"login": "your-username", "id": 1234567, ...}
```

Injected credentials are redacted in traces. The actual token is replaced with `[REDACTED]`.

### Network trace format

Traces are stored in JSONL format at `~/.moat/runs/<run-id>/network.jsonl`:

```json
{
  "timestamp": "2025-01-21T10:23:44.512Z",
  "method": "GET",
  "url": "https://api.github.com/user",
  "status": 200,
  "duration_ms": 89,
  "request_headers": {"User-Agent": "python-requests/2.28.0", ...},
  "response_headers": {"Content-Type": "application/json", ...},
  "request_body": null,
  "response_body": "{\"login\": \"your-username\", ...}"
}
```

Request and response bodies are captured up to 8KB. Larger bodies are truncated.

## Execution traces

Moat can capture OpenTelemetry-compatible execution traces. Traces record spans representing operations within the agent.

View traces for the most recent run:

```bash
$ moat trace

TRACE 4a7b2c...
├─ run/start (0ms)
├─ proxy/start (12ms)
├─ container/create (234ms)
├─ container/start (45ms)
├─ container/wait (5.2s)
│  ├─ network/request GET api.github.com/user (89ms)
│  └─ network/request POST api.anthropic.com/v1/messages (1.2s)
└─ container/stop (123ms)
```

Traces show the hierarchy and timing of operations.

### Trace storage

Traces are stored in JSONL format at `~/.moat/runs/<run-id>/traces.jsonl`, following the OpenTelemetry JSON format.

### Disabling tracing

Disable execution tracing in `agent.yaml`:

```yaml
tracing:
  disable_exec: true
```

Network request logging is separate from execution tracing and is always enabled.

## Data locations

All observability data is stored per-run:

```
~/.moat/runs/<run-id>/
  metadata.json    # Run metadata (name, state, timestamps)
  logs.jsonl       # Container stdout/stderr
  network.jsonl    # HTTP requests through proxy
  traces.jsonl     # Execution spans
  logs.db          # Tamper-proof audit log
```

## Retention

When a container exits, it is automatically removed, but run artifacts (logs, traces, network requests, audit data) are retained in `~/.moat/runs/<run-id>/`.

Remove artifacts manually:

```bash
# Remove a specific run's artifacts
moat destroy run_a1b2c3d4e5f6

# Remove all stopped runs
moat clean
```

View disk usage:

```bash
$ moat status

Disk Usage:
  Runs: 156 MB
  Images: 1.2 GB
```

## Querying observability data

The JSONL files can be processed with standard tools:

```bash
# Count requests by status code
cat ~/.moat/runs/run_*/network.jsonl | jq -r '.status' | sort | uniq -c

# Find slow requests (>1s)
cat ~/.moat/runs/run_*/network.jsonl | jq 'select(.duration_ms > 1000)'

# Search logs for errors
grep -r "error" ~/.moat/runs/run_*/logs.jsonl
```

## Related concepts

- [Audit logs](./03-audit-logs.md) — Tamper-proof logging of observability events
- [Credential management](./02-credentials.md) — What triggers credential-related traces
