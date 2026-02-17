---
title: "Observability tools"
navTitle: "Observability"
description: "View logs, network traces, execution spans, and audit data for any Moat run."
keywords: ["moat", "observability", "logs", "traces", "network", "audit", "debugging"]
---

# Observability tools

This guide walks through Moat's observability commands for inspecting what happened during a run. You will learn how to view container logs, network traces, execution spans, and audit data -- and how to query raw data files directly.

For architecture details on how this data is captured, see [Observability](../concepts/03-observability.md).

## Prerequisites

- A working Moat installation with Docker or Apple container runtime
- At least one run, active or completed (use `moat list` to check)

## Viewing logs

`moat logs` displays container stdout and stderr with timestamps. Logs are streamed to `logs.jsonl` in real time as the container produces output, so they are available while the run is still active.

View logs for the most recent run:

```bash
$ moat logs

[2025-01-21T10:23:44.512Z] Starting server on port 3000
[2025-01-21T10:23:44.789Z] Connected to database
[2025-01-21T10:23:45.123Z] Ready to accept requests
```

View logs for a specific run:

```bash
$ moat logs run_a1b2c3d4e5f6
```

Show the last N lines:

```bash
$ moat logs -n 50
```

### Following logs

Use `-f`/`--follow` to stream log output from a running container:

```bash
$ moat logs -f my-agent

[2025-01-21T10:23:44.512Z] Starting server on port 3000
[2025-01-21T10:23:44.789Z] Connected to database
... (new lines appear as the container writes them)
```

Press `Ctrl+C` to stop following. Combine with `-n` to show recent history before streaming:

```bash
$ moat logs -n 20 -f my-agent
```

See [CLI reference](../reference/01-cli.md) for the complete list of `moat logs` flags.

## Network traces

`moat trace --network` shows all HTTP and HTTPS requests that passed through the proxy during a run.

View network requests for the most recent run:

```bash
$ moat trace --network

[10:23:44.512] GET https://api.github.com/user 200 (89ms)
[10:23:45.001] POST https://api.anthropic.com/v1/messages 200 (1.2s)
[10:23:46.234] GET https://api.github.com/repos/org/repo 200 (45ms)
```

Each line shows the timestamp, HTTP method, URL, response status code, and request duration.

View network requests for a specific run:

```bash
$ moat trace --network run_a1b2c3d4e5f6
```

### Verbose mode

Add `-v` to include request headers, response headers, and response bodies:

```bash
$ moat trace --network -v

[10:23:44.512] GET https://api.github.com/user 200 (89ms)
  Request Headers:
    User-Agent: python-requests/2.28.0
    Authorization: Bearer [REDACTED]
  Response Headers:
    Content-Type: application/json; charset=utf-8
    X-RateLimit-Remaining: 4999
  Response Body (truncated):
    {"login": "your-username", "id": 1234567, ...}
```

Injected credentials are redacted -- the actual token is replaced with `[REDACTED]`.

## Execution spans

`moat trace` (without `--network`) displays execution spans showing the hierarchy and timing of operations within a run.

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

Use this to identify which phase of a run took the longest. For example, a slow `container/create` span indicates image build or pull time, while a slow `container/wait` span reflects the agent's own execution.

View spans for a specific run:

```bash
$ moat trace run_a1b2c3d4e5f6
```

## Audit verification

`moat audit` verifies the integrity of a run's tamper-proof audit log. The audit log records credential usage, network requests, console output, and other events in a cryptographically linked hash chain.

Verify a run's audit log:

```bash
$ moat audit run_a1b2c3d4e5f6

Auditing run: run_a1b2c3d4e5f6
===============================================================
Log Integrity
  [ok] Hash chain: 47 entries, no gaps, all hashes valid
Local Signatures
  [ok] 1 attestations, all signatures valid
External Attestations (Sigstore/Rekor)
  - No Rekor proofs found
===============================================================
VERDICT: [ok] INTACT - No tampering detected
```

The verification checks hash chain integrity (each entry links to the previous), sequence continuity (no gaps), and Ed25519 signature validity.

### Viewing audit entries

List individual entries in a run's audit log:

```bash
$ moat audit run_a1b2c3d4e5f6 --list

SEQ  TIME                  TYPE        SUMMARY
1    2025-01-21T10:23:44Z  Credential  github granted
2    2025-01-21T10:23:45Z  Network     GET api.github.com/user 200
3    2025-01-21T10:23:45Z  Console     {"login": "your-username"...
...
```

Filter entries by event type:

```bash
# Show only credential events
$ moat audit run_a1b2c3d4e5f6 --list --type=credential

# Show only network events
$ moat audit run_a1b2c3d4e5f6 --list --type=network
```

For details on the hash chain structure and trust model, see [Observability](../concepts/03-observability.md).

## Exporting proof bundles

Export a self-contained proof bundle for offline verification or sharing:

```bash
$ moat audit run_a1b2c3d4e5f6 --export proof.json
```

The bundle contains:

- All audit entries with their hashes
- Signatures and attestations
- Run metadata (run ID, timestamps)

Verify an exported bundle on any machine -- the original run data is not required:

```bash
$ moat audit verify proof.json

Verifying bundle: proof.json
===============================================================
VERDICT: [ok] INTACT - No tampering detected
```

This is useful for sharing audit evidence with reviewers or storing proof bundles in version control.

## Data locations and retention

All observability data is stored per-run under `~/.moat/runs/<run-id>/`:

| File | Contents |
|------|----------|
| `metadata.json` | Run metadata (name, state, timestamps) |
| `logs.jsonl` | Container stdout/stderr |
| `network.jsonl` | HTTP requests through proxy |
| `traces.jsonl` | Execution spans |
| `logs.db` | Tamper-proof audit log (SQLite) |

When a container exits, Moat removes the container but retains these artifacts.

### Removing run artifacts

```bash
# Remove a specific run's artifacts
$ moat destroy run_a1b2c3d4e5f6

# Remove all stopped runs
$ moat clean

# Preview what would be removed
$ moat clean --dry-run
```

Check disk usage with `moat status`:

```bash
$ moat status

Disk Usage:
  Runs: 156 MB
  Images: 1.2 GB
```

## Querying raw data

The JSONL files are line-delimited JSON, so standard tools like `jq` work directly.

### Log queries

Each log entry has three fields: `timestamp`, `stream` (`stdout` or `stderr`), and `line`.

```bash
# Filter stderr lines
$ jq 'select(.stream == "stderr")' ~/.moat/runs/run_a1b2c3d4e5f6/logs.jsonl
```

### Network queries

Each network entry has `timestamp`, `method`, `url`, `status`, `duration_ms`, and header/body fields.

```bash
# Count requests by status code
$ jq -r '.status' ~/.moat/runs/run_a1b2c3d4e5f6/network.jsonl | sort | uniq -c

# Find slow requests (over 1 second)
$ jq 'select(.duration_ms > 1000) | {method, url, duration_ms}' \
    ~/.moat/runs/run_a1b2c3d4e5f6/network.jsonl

# List all hosts contacted
$ jq -r '.url' ~/.moat/runs/run_a1b2c3d4e5f6/network.jsonl | \
    sed 's|https\?://||' | cut -d/ -f1 | sort -u

# Find all 401 responses across runs
$ jq 'select(.status == 401)' ~/.moat/runs/run_*/network.jsonl
```

> **Note:** Request and response bodies in `network.jsonl` are captured up to 8 KB. Larger bodies are truncated.

## Troubleshooting

### No output from `moat logs`

The run may not have produced any output yet. Verify the run exists with `moat list`. If the run is still active, use `moat logs -f` to wait for new output, or `moat attach` to interact with the container directly:

```bash
$ moat logs -f run_a1b2c3d4e5f6
$ moat attach run_a1b2c3d4e5f6
```

### No network traces

Network traces are only recorded for requests that pass through the proxy. If the agent makes requests that bypass the proxy (for example, to `localhost` inside the container), those requests are not captured.

Verify the run used grants (which activate the proxy):

```bash
$ moat audit run_a1b2c3d4e5f6 --list --type=credential
```

### Audit verification fails

If `moat audit` reports a broken hash chain, the audit log file may have been modified. The output indicates which sequence number has a mismatch. For stronger guarantees, export proof bundles immediately after runs and store them in append-only storage. See [Observability](../concepts/03-observability.md) for the trust model.

### Missing `traces.jsonl`

Execution tracing may be disabled in the run's `agent.yaml`:

```yaml
tracing:
  disable_exec: true
```

Remove or set this to `false` to re-enable execution tracing. Network request logging is separate and always enabled.

## Related pages

- [Observability](../concepts/03-observability.md) -- Architecture and data flow for logs, traces, and network captures
- [Observability](../concepts/03-observability.md) -- Hash chain structure, trust model, and attestation details
- [CLI reference](../reference/01-cli.md) -- Complete flag listings for `moat logs`, `moat trace`, and `moat audit`
