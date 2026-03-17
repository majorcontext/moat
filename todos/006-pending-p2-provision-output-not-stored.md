---
status: pending
priority: p2
issue_id: "006"
tags: [code-review, agent-native]
dependencies: []
---

# Provision Output Not Captured to Run Store

## Problem Statement

Provision output (model pull progress) goes to `os.Stderr` but is not captured to the run's log store. In non-interactive mode (CI, automated callers), stderr may not be preserved. There is no way to retrieve provision output after the fact via `moat logs`.

## Findings

- **Source**: Agent-Native Reviewer
- **Location**: `internal/run/manager.go:1968` — `provisionService(ctx, svcMgr, info, svcConfigs[i], os.Stderr)`

## Proposed Solutions

Tee provision output to both `os.Stderr` and the run's `storage.LogWriter` using `io.MultiWriter`.

However, the `RunStore` is created after container creation (line 2148), which is after service provisioning. Capturing provision output to the store would require either:
- Moving store creation earlier in the `Create` flow (medium effort, risk of breaking other flows)
- Buffering provision output and flushing after store is available (adds complexity)

- **Effort**: Medium (due to store creation ordering)
- **Risk**: Low-Medium

**Deferred** — provision output streams to stderr during the run, which is sufficient for interactive use. Store capture is a follow-up.

## Acceptance Criteria

- [ ] Provision output is retrievable via `moat logs <run-id>` after the run
- [ ] Provision output still streams to stderr during the run
