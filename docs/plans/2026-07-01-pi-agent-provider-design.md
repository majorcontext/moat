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

## De-risking spike (gates implementation)

The one unverified assumption: **does Pi's Node HTTP client honor `HTTP_PROXY` for its outbound LLM API calls?** Moat's transparent injection depends on it — the same constraint that forces the MCP relay for Claude Code, whose HTTP client ignores `HTTP_PROXY`.

Run this **before** writing provider code, using the sandbox's Docker-in-Docker (full egress is available; verify with `docker version`):

1. Install Pi in a container (`npm install -g --ignore-scripts @earendil-works/pi-coding-agent`).
2. Set `HTTP_PROXY`/`HTTPS_PROXY` to a capturing proxy and run `pi -p "hi" --provider anthropic` with a placeholder key.
3. Observe whether the request to `api.anthropic.com` traverses the proxy.

**Outcome A — honors `HTTP_PROXY`:** proceed with the codex-shaped design below unchanged.

**Outcome B — ignores `HTTP_PROXY`:** fall back to Pi's first-class `baseUrl` override — set the provider `baseUrl` (via `settings.json`/`models.json` or `ANTHROPIC_BASE_URL`) to the moat proxy relay, mirroring the MCP relay pattern. Same end state; the only delta is that `PrepareContainer` writes a `baseUrl` into staged config instead of relying on the transport-level proxy. The rest of the design (selection, failure paths, config, tests) is unaffected.

The spike also confirms the `--ignore-scripts` install works through the deps npm installer (see Open Items).

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

- **HTTP_PROXY transport** — the one real unknown; the spike resolves it (Outcome A vs B) before implementation.
- **`--ignore-scripts`** — Pi's documented install uses it. Confirm the deps npm installer can pass the flag, or that install succeeds without it. Verified during the spike.
- **auth.json vs env-var placeholder** — leaning `auth.json` (codex-consistent; can also carry the `baseUrl`/proxy value under Outcome B). Minor, decided during implementation.

## Appendix: why not `--grant claude`

The `claude` grant is a subscription **OAuth** token, injected as `Authorization: Bearer` + `anthropic-beta: oauth-…` with `x-api-key` stripped, and it depends on client-identity fields. Pi's default `--provider anthropic` mode emits an **API-key-shaped** request (`x-api-key`, no oauth-beta), so injecting an OAuth token onto it fails with the `x-organization-uuid header is required` class of error. Making `--grant claude` work would require staging a Pi Anthropic-OAuth `auth.json` entry so Pi emits OAuth-shaped requests, plus verifying token client-binding — deferred as future work. v1 uses the `anthropic` **API-key** grant, which matches Pi's request shape with zero fixups.
