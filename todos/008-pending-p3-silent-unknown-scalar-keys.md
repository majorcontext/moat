---
status: pending
priority: p3
issue_id: "008"
tags: [code-review, quality]
dependencies: []
---

# Unknown Scalar Keys in ServiceSpec Silently Ignored

## Problem Statement

`ServiceSpec.UnmarshalYAML` only captures unknown keys that have sequence values into `Extra`. If a user writes `services.ollama.foo: "bar"` (a scalar), it is silently ignored — no warning, no error. The `buildServiceConfig` validation only catches keys in `Extra`.

## Findings

- **Source**: Agent-Native Reviewer
- **Location**: `internal/config/config.go:171-192`

## Proposed Solutions

Log a warning or return an error for unrecognized non-sequence keys in `UnmarshalYAML`.

## Acceptance Criteria

- [ ] Unknown scalar keys in service config produce a warning or error
