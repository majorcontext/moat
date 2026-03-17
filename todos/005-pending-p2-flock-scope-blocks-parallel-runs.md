---
status: pending
priority: p2
issue_id: "005"
tags: [code-review, performance]
dependencies: []
---

# Exclusive flock Blocks All Parallel Runs

## Problem Statement

The flock in `provisionService` is held for the entire provision phase. If two `moat run` invocations both declare Ollama with models, the second blocks entirely until the first finishes pulling ALL its models — even if the models are different and wouldn't conflict.

`ollama pull` is idempotent and uses content-addressed blob storage internally. Two concurrent pulls of different models do not corrupt each other.

## Findings

- **Source**: Performance Oracle
- **Location**: `internal/run/services.go:250-261`
- **Impact at 10x concurrent runs**: Linear queue. 10 runs each pulling a 7B model could mean 1-2 hours wait for the last run.

## Proposed Solutions

### Option A: Remove flock, rely on Ollama's internal locking
- **Pros**: Simplest, Ollama handles concurrent blob access
- **Cons**: Risk if Ollama's internal locking has edge cases
- **Effort**: Small (delete ~10 lines)

### Option B: Per-model lock files
- Lock on `~/.moat/cache/ollama/.lock.<model-hash>` instead of a single lock
- **Pros**: Allows parallel pulls of different models
- **Cons**: More lock files to manage
- **Effort**: Small-Medium

## Recommended Action

Test whether concurrent `ollama pull` of different models is safe. If so, Option A. Otherwise, Option B.

## Acceptance Criteria

- [ ] Parallel `moat run` invocations with different Ollama models don't serialize unnecessarily
