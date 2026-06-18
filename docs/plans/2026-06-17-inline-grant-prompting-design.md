# Inline Grant Prompting for `moat run` and Family — Design

**Date:** 2026-06-17
**Status:** Approved (design)

## Overview

When a run is missing one or more grants, and the session is attached to an
interactive terminal, prompt the user to grant them inline instead of failing
and forcing a manual `moat grant ...` plus a re-run. Non-interactive contexts
(CI, piped, `--no-prompt`) keep today's fail-fast behavior, byte-for-byte.

This applies to the shared run entry point used by `moat run`, `moat claude`,
`moat codex`, and the rest of the agent family.

**Scope for v1:** generic grants (`github`, `oauth:*`, `anthropic`, `claude`,
`npm`, `aws`, …) and MCP grants (`mcp:*`). SSH is deferred to a phase 2.

## Motivation

Today the flow is **detect → fail → tell the user to run `moat grant X` → re-run**:

```
missing grants:
  - github: not configured
    Run: moat grant github
  - oauth:notion: not configured
    Run: moat grant oauth notion

Configure the grants above, then run again.
```

Every missing grant costs a full round-trip. The detection logic and the
"what command would fix this" mapping (`grantToCommand`) already exist and are
centralized, so the building blocks for collapsing the round-trip are in place.

## Interaction model

Selected approach (from brainstorming):

- **Run the grant flow inline** when a grant is missing and we are on a TTY —
  drive the *existing* per-provider `Grant` logic in place, then continue the
  run. No re-invocation.
- **Fail fast unchanged** when not interactive (no TTY, or `--no-prompt` /
  `MOAT_NO_PROMPT=1`). The aggregated error and non-zero exit are identical to
  today.
- **Summarize, then prompt per-grant in sequence.** Print the full list of
  needed grants up front, then walk them one at a time, each with its own
  `[Y/n]` and its own flow. The user can skip any; skipped/failed grants fall
  back to the printed fix command and the run does not start unless every
  required grant ends up satisfied.
- **Inline-prompt what we can; fall back for what we can't.** Grants that cannot
  be prompted cleanly (AWS — needs mandatory flags; unknown-provider typos) are
  not offered `[Y/n]`; they print their fix line and count as still-missing.

## Architecture

Detection stays in the library; prompting lives in the CLI. `internal/run`
never reads from stdin.

### a) Structured detection (`internal/run`)

`validateGrants` and `validateMCPGrants` today build formatted error strings.
Refactor the detection half to return structured data:

```go
type MissingGrant struct {
    Grant      string // e.g. "oauth:notion"
    Reason     MissingReason // NotConfigured | DecryptFailed | UnknownProvider
    FixCommand string // e.g. "moat grant oauth notion" (from grantToCommand)
    Promptable bool   // false for aws (needs flags) and unknown-provider typos
}
```

A new pure function `DetectMissingGrants(grants []string, store *credential.FileStore) []MissingGrant`
replaces the detection halves of both validators (generic + MCP), returning a
single merged list. The existing error-formatting becomes a thin helper over
`[]MissingGrant` so the non-interactive path produces a **byte-identical**
message (preserves current behavior and tests). The library never touches
stdin.

### b) Shared grant helper (`cmd/moat/cli`)

Extract the core of `runGrant` into `grantInline(ctx context.Context, grant string) error`:
map the grant string to a provider + resource, call `prov.Grant(ctx)`, save the
credential via `saveCredential`. Both the `moat grant` command and the new
prompt loop call it. No behavior change to `moat grant` itself.

### c) Prompt loop (`cmd/moat/cli`)

`promptForMissingGrants(ctx context.Context, missing []run.MissingGrant) error`
runs the summary + per-grant `[Y/n]` loop and calls `grantInline`. Lives in the
CLI layer, unit-testable with a faked TTY / scripted reader.

## Control flow

In `cmd/moat/cli/run.go`, before `manager.Create`:

1. `missing := run.DetectMissingGrants(grants, store)` (generic + MCP, merged).
2. If empty → proceed as today.
3. If non-empty:
   - **Not a TTY, or `--no-prompt` / `MOAT_NO_PROMPT=1`** → format the existing
     aggregated error, exit non-zero. Byte-identical to today.
   - **Interactive** → `promptForMissingGrants(...)`, then **re-detect**. If
     anything is still missing (user said `n`, or a flow failed/aborted) → fall
     back to the aggregated error for the remainder, exit non-zero. If all
     satisfied → proceed to `manager.Create` with the complete set.

Re-detecting after the loop (rather than trusting the loop's bookkeeping) keeps
the gate authoritative — the run only starts if the credentials genuinely exist
and decrypt. It is a single pass; there is no retry loop.

## Prompt UX

```
3 grants needed before this run can start:
  • github        (GitHub access)
  • oauth:notion  (Notion via OAuth)
  • mcp:render    (Render MCP token)

Grant github now? [Y/n] y
  → <existing github grant flow runs: gh CLI / PAT paste>
  ✓ github granted

Grant oauth:notion now? [Y/n] y
  → Open this URL to authorize: https://...
  ✓ oauth:notion granted

Grant mcp:render now? [Y/n] n
  Skipped. Run later with: moat grant mcp render
```

- `Promptable == false` grants are not offered `[Y/n]` — they print their fix
  line in the summary and count as still-missing. The AWS line reads e.g.
  `aws: requires flags — run: moat grant aws --role=...`.
- Default is **Y** (Enter accepts).
- `Ctrl-C` aborts the whole run cleanly.
- Each flow surfaces its own existing output (browser URL, token prompt). A
  failure in one flow is reported and leaves that grant missing, but does not
  abort the loop — remaining grants are still offered.

## Edge cases

- **`--dry-run`** — detect and *report* what would be prompted, but never prompt
  or grant.
- **Profiles (`--profile`)** — `grantInline` and detection both honor the active
  profile, using the same store as today.
- **Re-detect mismatch** (granted but still failing, e.g. an immediately-expired
  credential) — falls into the still-missing path with its real error. One pass
  only; no infinite loop.
- **MCP grant naming** — detection reports the canonical `mcp:<name>`;
  `grantInline` accepts it via the existing catalog mapping (`mcp-<name>` still
  accepted).

## Testing

- `internal/run`: table tests for `DetectMissingGrants` — each `Reason`,
  promptable vs not, generic + MCP merge — and a test asserting the formatter
  output is unchanged versus the current error strings.
- `cmd/moat/cli`: `promptForMissingGrants` with a scripted reader + fake TTY,
  covering `Y` / `n` / skip, default-Enter, non-promptable skip,
  flow-error-stays-missing, and the `--no-prompt` / non-TTY fall-through.
- No container runtime required for any of the above.

## Out of scope (v1)

- SSH inline prompting (phase 2 — requires moving SSH validation earlier in
  `Create`, which reorders the SSH agent setup).
- Auto-granting non-interactive-capable flows.
- Any change to `moat grant`'s own UX.

## Documentation impact

- `docs/content/reference/01-cli.md` — document `--no-prompt` and
  `MOAT_NO_PROMPT`.
- `docs/content/reference/04-grants.md` — note the inline-prompt behavior on
  interactive runs.
- `CHANGELOG.md` — `### Added` entry linking the PR.
