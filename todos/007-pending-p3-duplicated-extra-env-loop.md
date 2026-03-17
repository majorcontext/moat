---
status: pending
priority: p3
issue_id: "007"
tags: [code-review, quality]
dependencies: []
---

# Duplicated extra_env Loop in buildServiceConfig

## Problem Statement

The `for k, v := range spec.Service.ExtraEnv` loop is duplicated between the `needsPassword` and `!needsPassword` branches. The only difference is whether `{password}` substitution happens. Can be consolidated into a single loop since `strings.ReplaceAll` with empty password is a no-op.

## Findings

- **Source**: Code Simplicity Reviewer
- **Location**: `internal/run/services.go:150-162`
- **Impact**: ~5 LOC reduction

## Acceptance Criteria

- [ ] Single `extra_env` loop after password generation
- [ ] Existing tests still pass
