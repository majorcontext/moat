---
status: pending
priority: p1
issue_id: "002"
tags: [code-review, quality, agent-native]
dependencies: []
---

# Error Messages Reference Nonexistent `--service` Flag

## Problem Statement

Error messages in `internal/run/manager.go` tell users to run `moat logs <id> --service <name>`, but the `moat logs` command has no `--service` flag. Both the readiness failure and provisioning failure error paths produce this suggestion. An agent or human following the suggested command gets an "unknown flag" error — a dead end for debugging.

## Findings

- **Source**: Agent-Native Reviewer
- **Location**: `internal/run/manager.go:1960` (readiness failure) and `internal/run/manager.go:1976` (provisioning failure)
- **Verified**: `cmd/moat/cli/logs.go` has no `--service` flag

## Proposed Solutions

### Option A: Remove `--service` from error messages
- Change hints to `moat logs <id>` (which does exist and shows all logs)
- **Pros**: Immediate fix, no new feature needed
- **Cons**: Less specific — user sees all logs, not just service logs
- **Effort**: Small (2 lines)
- **Risk**: None

### Option B: Implement `--service` flag on `moat logs`
- Add filtering by service container name to the logs command
- **Pros**: Better UX, error messages become correct
- **Cons**: More work, separate feature
- **Effort**: Medium
- **Risk**: Low

## Recommended Action

Option A immediately (fix the error messages), Option B as a separate follow-up feature.

## Technical Details

- **Affected files**: `internal/run/manager.go`

## Acceptance Criteria

- [ ] Error messages reference only commands/flags that exist
- [ ] Running the suggested command actually works

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-17 | Created from code review | Agent-native reviewer caught this |
