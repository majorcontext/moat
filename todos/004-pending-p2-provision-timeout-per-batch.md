---
status: pending
priority: p2
issue_id: "004"
tags: [code-review, performance]
dependencies: []
---

# Provision Timeout is Per-Batch, Not Per-Item

## Problem Statement

The 30-minute `provisionTimeout` covers ALL provision commands combined. For large models (70B at ~40GB), a single pull can exceed 30 minutes on slower connections. Multiple large models will certainly exceed it.

## Findings

- **Source**: Performance Oracle, Agent-Native Reviewer
- **Location**: `internal/run/services.go:222,245`

## Proposed Solutions

### Option A: Per-item timeout
Move `context.WithTimeout` inside `ProvisionService` loop so each command gets 30 minutes.
- **Effort**: Small (move timeout into loop)

### Option B: Configurable timeout
Add `provision_timeout` to service config. Default 30min per item.
- **Effort**: Medium

## Recommended Action

Option A immediately (simple, correct). Option B as future work.

## Acceptance Criteria

- [ ] Each provision command gets its own 30-minute timeout
- [ ] Total provision time is bounded only by number_of_items * 30min
