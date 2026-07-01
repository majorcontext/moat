# Pi Coding Agent Provider — Design

**Date:** 2026-07-01
**Status:** Draft for review
**Scope:** v1 = Pi as a Moat agent runtime backed by the existing `anthropic` and `openai` credential grants only. Every other Pi backend (Gemini, Bedrock, DeepSeek, xAI, Groq, OpenRouter, …) is explicit future work and fails hard.

## Problem

Moat supports the Claude Code, Codex, and Gemini coding agents. [Pi](https://github.com/earendil-works/pi) is an open-source, BYOK terminal coding agent in the same category: an npm-installed CLI (`@earendil-works/pi-coding-agent`, binary `pi`) with a minimal Read/Write/Edit/Bash core, model-agnostic across 20+ providers through its `pi-ai` layer. Users have asked to run Pi inside Moat sandboxes with the same transparent credential injection the other agents get.

The distinguishing trait is that **Pi has no credential of its own** — it runs whichever model backend the user points it at. That breaks the one-agent-one-credential assumption the existing providers rely on, so the design centers on how Pi selects a backend and reuses Moat's existing grants.

## Goals

- Run Pi inside a Moat container with transparent credential injection, reusing the **existing** `anthropic` and `openai` grants (no new credential type).
- Let the backend be **inferred from the grant** in the common single-grant case, with `moat.yaml` `pi.provider`/`pi.model` as an explicit override.
- **Fail hard, early, with actionable messages** on every known bad configuration (missing grant, ambiguous grants, unsupported backend, provider/grant mismatch) — rather than silently falling through to interactive login the way today's agents do.
- Match the existing codex/gemini provider shape so the code is unsurprising.

## Non-Goals (this spec)

- **Pi backends other than Anthropic and OpenAI.** Selecting any other provider is a hard error directing the user to file/await future work.
- **Pi OAuth subscription login** (`/login` with Claude Pro/Max, ChatGPT Plus/Pro, Copilot). v1 is API-key grants only. The `claude` OAuth grant is intentionally **not** wired to Pi (request-shape mismatch — see Appendix).
- **New credential injection logic.** Injection is entirely delegated to the existing `anthropic` (x-api-key) and `openai` (Bearer) credential providers.
- **Pi extensions/skills/subagents packaging.** Out of scope for v1.

## Decisions (locked)

| Decision | Choice |
| --- | --- |
| Backends in v1 | `anthropic` and `openai` only; all others fail hard as future work |
| Provider selection | Infer from the single present grant; `pi.provider`/`pi.model` override/disambiguate |
| Both grants present, no `pi.provider` | **Hard error** requiring `pi.provider` (no default preference order) |
| Config surface | Full `pi:` block: `provider`, `model`, `syncLogs`, `mcp` (mirrors codex/gemini) |
| Credential model | Reuse existing `anthropic`/`openai` grants; Pi injects nothing itself |
| Instruction file | `AGENTS.md` (Pi reads `AGENTS.md`/`CLAUDE.md`) |
| Auth staging | Format-valid **placeholder** key in `~/.pi/agent/auth.json`; proxy overwrites the header on the wire |
| Launch | Always explicit: `pi --provider <resolved> [--model <model>]` (Pi's default provider is `google`); set `PI_OFFLINE=1` to suppress startup network ops |
| Transport | Confirmed: Pi honors `HTTP_PROXY` (spike, Outcome A) — no `baseUrl` relay needed |

## De-risking spike — DONE (Outcome A)

The gating question was: **does Pi's Node HTTP client honor `HTTP_PROXY` for its outbound LLM API calls?** Moat's transparent injection depends on it — the same constraint that forces the MCP relay for Claude Code, whose HTTP client ignores `HTTP_PROXY`.

**Run 2026-07-01 in the sandbox (Docker 29.6.1, DIND):** installed `@earendil-works/pi-coding-agent@0.80.3` with `--ignore-scripts`, set `HTTP_PROXY`/`HTTPS_PROXY` to a logging proxy, ran `pi -p "…" --provider anthropic` with a placeholder key. **The proxy captured `CONNECT api.anthropic.com:443` — Pi routes its LLM calls through `HTTP_PROXY`.**

**→ Outcome A: transparent injection works.** Proceed with the codex-shaped design; the `baseUrl` fallback (Outcome B) is **not** needed. (Kept for the record: Outcome B would have set the provider `baseUrl` via `settings.json`/`models.json`/`ANTHROPIC_BASE_URL` to the proxy relay.)

Additional findings folded into the design below:

- **Pi's default provider is `google`** (`--provider … (default: google)`). Since v1 supports only anthropic/openai, Pi must **always** be launched with an explicit `--provider` (materialized from the resolved backend). The resolver is therefore load-bearing, not a convenience.
- **Pi reads `AGENTS.md` and `CLAUDE.md`** (per the `--no-context-files` flag) — confirms the `AGENTS.md` instruction-file choice.
- **`--ignore-scripts` global install works** through npm cleanly (no post-install build step needed). Resolves the Open Item.
- **Pi does not locally reject a plausible placeholder key** before sending — confirms the placeholder-then-proxy-overwrite flow.
- **`PI_OFFLINE=1` / `--offline`** disables startup network operations (the model catalog is bundled, "updated every release"); set it in the container to avoid stray egress and keep strict-policy runs clean. The inference call is not a startup op, so it still flows through the proxy.

## Architecture

### Credential flow (why this is small)

`anthropic` and `openai` already exist as credential providers and already inject correctly:

- `anthropic` → `SetCredentialWithGrant("api.anthropic.com", "x-api-key", <key>, "anthropic")` — matches Pi's default `--provider anthropic` request shape exactly.
- `openai` (alias → codex) → `SetCredentialWithGrant("api.openai.com", "Authorization", "Bearer "+<key>, "codex")` — matches Pi's OpenAI mode.

Both `SetCredentialWithGrant` calls **overwrite** the header, so Pi only needs a syntactically valid placeholder present to make it emit a request; the proxy replaces the value on the wire. Pi's own `ConfigureProxy`/`ContainerMounts` are therefore no-ops — Pi is purely the runtime.

### New package `internal/providers/pi/` (mirrors codex)

| File | Purpose |
| --- | --- |
| `provider.go` | `Provider` struct; `init()` → `provider.Register(&Provider{})`. Implements the `AgentProvider` surface. `Grant()` returns a **directing error** (`pi has no credential of its own; run 'moat grant anthropic' or 'moat grant openai'`). `ConfigureProxy`/`ContainerMounts` are no-ops. `ImpliedDependencies()` empty. |
| `resolve.go` | `resolvePiProvider(cfg *config.Config, store *credential.FileStore) (string, error)` — single source of truth for backend selection and all fail-hard paths. Pure and unit-testable. |
| `agent.go` | `PrepareContainer` — stage `/moat/pi-init`: `~/.pi/agent/auth.json` (placeholder key for the resolved backend), `~/.pi/agent/settings.json` (materialized `provider`+`model` so launch needs no arg plumbing; `baseUrl` too under Outcome B), `AGENTS.md` (runtime context), MCP config. Mount + `MOAT_PI_INIT` env. |
| `cli.go` | `RegisterCLI` → `moat pi` via `cli.RunProvider`. `GetCredentialGrant` closure returns `anthropic`/`openai`/`""` (modeled on claude's `resolveClaudeCredential`, delegating to `resolvePiProvider`). `DefaultDependencies()` = `node@22`, `git`, `pi-cli`. `NetworkHosts()` = `api.anthropic.com`, `api.openai.com`. `GetCredentialName()`. |
| `constants.go` | Placeholder keys (format-valid dummy `sk-ant-…` / `sk-…`). |
| `doctor.go` | `moat doctor` section. |
| `doc.go`, `*_test.go` | Package doc, tests. |

### Provider selection — `resolvePiProvider` precedence

1. `pi.provider` set → must be `anthropic` or `openai`, **and** its grant must be configured. Otherwise a hard error (unsupported, or mismatch).
2. Else exactly one of `{anthropic, openai}` granted → use it.
3. Else hard error (none, or both-without-override).

### Failure paths (fail hard, `validateGrants` message style)

Indented bullets, `Run: moat grant X`, matching `internal/run/run.go` wording.

| Case | Error (shape) |
| --- | --- |
| Neither granted, no `pi.provider` | `pi requires one of these grants: anthropic, openai` + `Run: moat grant anthropic` / `moat grant openai` |
| Both granted, no `pi.provider` | `pi: both anthropic and openai are granted — set pi.provider to choose one` |
| `pi.provider: anthropic` (or `openai`) but that grant absent | `pi.provider is "anthropic" but that grant isn't configured` + `Run: moat grant anthropic` |
| `pi.provider:` any other value | `pi provider "<x>" is not supported yet (supported: anthropic, openai)` |

This resolution runs in Pi's command path **before** `Manager.Create` allocates resources.

### Edits to existing files

- `internal/providers/register.go` — blank-import `pi` (required for `init()` to fire).
- `internal/deps/registry.yaml` — add `pi-cli` (npm `@earendil-works/pi-coding-agent`, `requires: [node]`; `--ignore-scripts` per Open Items).
- `internal/config/config.go` — `Pi PiConfig` field + `PiConfig{ Provider, Model string; SyncLogs *bool; MCP map[string]MCPServerSpec }` + `ShouldSyncPiLogs()` + MCP validation loop (`validateMCPServerSpec` with a `pi` section).
- `internal/cli/provider.go` `buildGrants` — conflict-suppression so an explicit `--grant anthropic`/`--grant openai` isn't double-added (mirror the existing `claude`/`anthropic` rule).
- `internal/deps/scripts/moat-init.sh` — `MOAT_PI_INIT` copy block (staged config + `AGENTS.md` → `~/.pi/`), mirroring the `MOAT_CODEX_INIT` block.
- `cmd/moat/cli/init.go` `agentConfigs()` — pi entry for `moat init` scaffolding.
- Docs: `reference/01-cli.md` (`moat pi`), `reference/02-moat-yaml.md` (`pi:` block), a `guides/` page, an `examples/` dir, and `CHANGELOG.md`.

Untouched (generic): `cmd/moat/cli/root.go` `RegisterProviderCLI`, `internal/image/resolver.go`, `TestDetectMissingGrantsMatchesValidators`.

## Testing (invariant #1: companion cases)

- **`resolvePiProvider` table test** — all four failure paths **and** all success paths: single-anthropic, single-openai, both+`pi.provider` override, `pi.provider` override wins over inference. (Every failure asserted alongside its passing mirror.)
- **`GetCredentialGrant` one-of resolution** — modeled on the claude provider test.
- **Config parse** — `pi:` block round-trips; `ShouldSyncPiLogs` default (unset) **and** explicit true/false (companion cases).
- **Drift guard** — assert `TestDetectMissingGrantsMatchesValidators` stays green unchanged (Pi reuses `anthropic`/`openai`, already handled symmetrically by detector and validators). No edit needed; documented so a future reader knows why.

## Open items / risks

- **HTTP_PROXY transport** — RESOLVED by the spike (Outcome A: Pi honors it).
- **`--ignore-scripts`** — RESOLVED: global install succeeds through npm with `--ignore-scripts`, no post-install build.
- **auth.json vs env-var placeholder** — leaning `auth.json` (codex-consistent, `0600`). Minor, decided during implementation.
- **Launch flags** — Pi must be launched with explicit `--provider`; how `moat pi` passes the resolved provider/model into the container's launch command needs to match how codex/gemini pass their invocation (materialize into `settings.json` where possible so the launch command stays plain `pi`, with flags as the fallback). Nailed down during implementation against the codex launch path.

## Appendix: why not `--grant claude`

The `claude` grant is a subscription **OAuth** token, injected as `Authorization: Bearer` + `anthropic-beta: oauth-…` with `x-api-key` stripped, and it depends on client-identity fields. Pi's default `--provider anthropic` mode emits an **API-key-shaped** request (`x-api-key`, no oauth-beta), so injecting an OAuth token onto it fails with the `x-organization-uuid header is required` class of error. Making `--grant claude` work would require staging a Pi Anthropic-OAuth `auth.json` entry so Pi emits OAuth-shaped requests, plus verifying token client-binding — deferred as future work. v1 uses the `anthropic` **API-key** grant, which matches Pi's request shape with zero fixups.
