---
status: pending
priority: p3
issue_id: "009"
tags: [code-review, quality]
dependencies: []
---

# Ollama Registry Entry Missing `versions` Field

## Problem Statement

All existing service entries (postgres, mysql, redis) declare explicit `versions` lists. Ollama omits this. Additionally, the `image` value is unquoted (`ollama/ollama`) while others use quoted strings.

## Findings

- **Source**: Pattern Recognition Specialist
- **Location**: `internal/deps/registry.yaml:492-506`

## Acceptance Criteria

- [ ] Add `versions` list to ollama registry entry
- [ ] Quote the `image` value for consistency
