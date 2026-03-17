---
status: pending
priority: p2
issue_id: "003"
tags: [code-review, security]
dependencies: []
---

# Cache Directory Permissions Too Permissive

## Problem Statement

Cache directory `~/.moat/cache/<service>/` is created with `os.MkdirAll(path, 0o755)` (world-readable). Lock file created with `0o644`. On shared systems, any local user can read cached model data.

## Findings

- **Source**: Security Sentinel
- **Location**: `internal/run/manager.go:1912` (MkdirAll), `internal/run/services.go:252` (lock file)

## Proposed Solutions

Change `0o755` to `0o700` and `0o644` to `0o600`. One-line fixes each.

## Acceptance Criteria

- [ ] Cache directory created with `0o700`
- [ ] Lock file created with `0o600`
