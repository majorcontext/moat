# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Moat runs AI agents in isolated containers with credential injection and full observability. Key features:

- **Isolated Execution** - Each moat runs in its own container (Docker or Apple containers) with workspace mounting
- **Credential Injection** - Transparent auth header injection via TLS-intercepting proxy (agent never sees raw tokens)
- **Smart Image Selection** - Automatically selects container images based on `moat.yaml` runtime config
- **Full Observability** - Captures logs, network requests, and traces for every run
- **Declarative Config** - Configure agents via `moat.yaml` manifests
- **Multi-Runtime Support** - Automatically uses Apple containers (macOS 26+) or Docker

## Architecture

```
cmd/moat/           CLI entry point (Cobra commands)
internal/
  audit/             Tamper-proof audit logging with cryptographic verification
  claude/            Claude Code settings and Dockerfile generation
  cli/               Shared CLI helpers (environment parsing, mount helpers)
  codex/             Codex CLI settings and Dockerfile generation
  config/            moat.yaml parsing, mount string parsing
  container/         Container runtime abstraction (Docker and Apple containers)
  credential/        Secure credential storage (GitHub, Anthropic, AWS)
  daemon/            Proxy daemon lifecycle, Unix socket API, and run registration
  image/             Runtime-based image selection (node/python/go → base image)
  log/               Structured logging (slog wrapper)
  provider/          Provider registry and interfaces (CredentialProvider, AgentProvider)
  providers/         Provider implementations:
    aws/               AWS IAM role assumption and credential endpoint
    claude/            Claude Code CLI, grants, and config generation
    codex/             Codex CLI, grants, and config generation
    github/            GitHub token management and refresh
  run/               Run lifecycle management (create/start/stop/destroy)
  storage/           Per-run storage for logs, traces, network requests
  ui/                TTY-aware colored output and formatting helpers
```

### Key Flows

**Credential Injection:** `moat grant github` → token from gh CLI, env var, or PAT prompt → token stored encrypted → `moat run --grant github` → run registered with proxy daemon → container traffic routed through proxy → proxy resolves run by auth token → Authorization headers injected for matching hosts

**Image Selection:** `moat.yaml` `dependencies` field → `image.Resolve()` → node:X-slim / python:X-slim / golang:X / debian:bookworm-slim

**Observability:** Container stdout → `storage.LogWriter` → `~/.moat/runs/<id>/logs.jsonl`; Proxy requests → `storage.NetworkRequest` → `network.jsonl`

**Container Runtime Selection:** `container.NewRuntime()` auto-detects: Apple containers on macOS 26+ with Apple Silicon, otherwise Docker

**Audit Logging:** Console/network/credential events → `audit.Store.Append()` → hash-chained entries in SQLite → `moat audit <run-id>` displays chain with verification; `--export` creates portable proof bundle with attestations

**MCP Integration:** `moat.yaml` defines remote MCP servers → `.claude.json` generated with relay URLs → Claude Code connects to proxy relay → proxy injects credentials → request forwarded to real MCP server with SSE streaming support

### Proxy Daemon

The credential-injecting proxy runs as a shared daemon process that outlives the CLI. A single daemon serves all active runs.

- **Lifecycle:** Started automatically by `moat run` or manually via `moat proxy start`. Auto-shuts down after 5 minutes idle (no active runs).
- **Management API:** Unix socket at `~/.moat/proxy/daemon.sock`. The CLI registers/unregisters runs via this socket.
- **Per-run credential scoping:** Each run gets a cryptographic auth token (32 bytes from `crypto/rand`). The proxy looks up run-specific credentials, headers, network policy, and MCP config by token. Both Docker and Apple containers use token-based proxy auth (`HTTP_PROXY=http://moat:token@host:port`).
- **Responsibilities:** Credential injection, token refresh, MCP relay, hostname routing, and network request logging.
- **Lock file:** `~/.moat/proxy/daemon.lock` records PID, ports, and build commit.
- **Backwards compatibility:** The daemon API (`internal/daemon/api.go`) **must remain backwards-compatible across binary versions**. The daemon process outlives the CLI that spawned it, so older daemons serve newer CLIs and vice versa. Rules: additive-only fields, no removed/renamed fields, new endpoints must handle 404 gracefully. See the package doc in `api.go`.

See `github.com/majorcontext/gatekeeper/proxy` and `internal/daemon/` for implementation.

### MCP (Model Context Protocol) Support

Moat supports two types of MCP servers:

1. **Remote HTTP MCP servers** (top-level `mcp:` in moat.yaml) - External MCP servers accessed via HTTPS with credential injection through a proxy relay pattern
2. **Local process MCP servers** (under `claude.mcp:` or `codex.mcp:`) - MCP servers running as child processes inside the container

**Remote MCP Architecture:**

The proxy relay pattern works around Claude Code's HTTP client not respecting `HTTP_PROXY`:
- Container's `.claude.json` points to proxy relay endpoints: `http://proxy:port/mcp/{name}`
- Proxy relay intercepts requests, injects real credentials from grant store
- Forwards to actual MCP server with SSE streaming support
- Circular proxy prevented via `NO_PROXY` env var and `http.Transport{Proxy: nil}`

**Key Implementation Files:**
- `github.com/majorcontext/gatekeeper/proxy` - Relay handler and credential injection
- `internal/providers/claude/config.go` - `.claude.json` generation
- `internal/run/manager.go` - MCP setup during container creation
- `internal/config/config.go` - `MCPServerConfig` (remote) and `MCPServerSpec` (local) types

## Development Commands

```bash
# Build
go build ./...

# Run unit tests (includes race detector)
make test-unit

# Run a single test
make test-unit ARGS='-run TestName'

# Run E2E tests (requires container runtime)
go test -tags=e2e -v ./internal/e2e/

# Run tests with coverage (includes race detector)
make coverage

# Lint (if golangci-lint is installed)
golangci-lint run
```

## Code Style

- Follow standard Go conventions and `go fmt` formatting
- Use `go vet` to catch common issues
- **After completing a batch of changes, always run `make lint` and fix any issues before committing.** This catches formatting, vet, and lint errors early. If `golangci-lint` is not installed, fall back to `go vet ./...`.

## Codebase Invariants

Rules PR review has caught more than once, ordered by how often and how severely they bite. Check the relevant ones when touching that area:

1. **Test the companion case — the most common review miss.** A test that asserts one direction or one property almost always needs its mirror: all-missing → also assert all-present; "explicit value wins" → also assert the catalog fallback still populates; the happy path → also cover the refresh/error path and symbolic inputs (`stable`/`latest`). One-sided tests pass while the regression they exist to catch ships anyway. (#357, #376, #383, #389)
2. **Detector ↔ validator parity.** `run.DetectMissingGrants` (CLI pre-flight) must classify exactly what `validateGrants`/`validateMCPGrants` (the `Create` gate) reject — same empty-string handling, same error buckets. When you edit one, edit the other, and update the drift-guard test (`TestDetectMissingGrantsMatchesValidators`) to assert **both** directions. (#389)
3. **Catalog lookups filter by auth type.** `oauth.LookupServerURL` returns `""` for non-OAuth catalog entries (`!e.OAuth`) so `moat grant oauth <api-key-server>` fails clearly instead of attempting OAuth discovery and failing confusingly. Preserve this property when adding catalog entries or lookup helpers. (#383, #386)
4. **Match documented contracts exactly.** Env/flag checks use the documented value (`MOAT_NO_PROMPT == "1"`, not `!= ""`, which silently accepts `0`/`false`). Don't collapse distinct error causes (not-found vs permission-denied vs decrypt-failed) into one catch-all bucket — each implies different user guidance. (#389)

## Logging vs User-Visible Output

Two separate systems — don't mix them up:

- **`internal/log`** — Structured debug/diagnostic logging (`log.Debug`, `log.Info`, `log.Warn`, `log.Error`). Writes to `~/.moat/debug/` as JSON. Only appears on stderr with `--verbose`. Use for internal state, timing, request details — anything useful for debugging but not for the user.
- **`internal/ui`** — User-facing messages (`ui.Warn`, `ui.Error`, `ui.Info`). Always prints to stderr. Colored prefixes when stderr is a TTY. Use for warnings, errors, and status the user needs to see.

For command output (tables, status, results), write directly to stdout with `fmt`. Use `ui.Bold`, `ui.Green`, `ui.OKTag()` etc. for styling — they return plain strings when stdout isn't a TTY or `NO_COLOR` is set.

Don't use `ui` style functions inside `tabwriter` — ANSI codes break column alignment.

## Error Messages

- Good error messages are documentation - when config is missing or something fails, tell users exactly what to set and how
- Users shouldn't have to search docs to understand what went wrong
- Include actionable steps: what env var to set, what command to run, where to find more info

## Documentation

See [docs/STYLE-GUIDE.md](docs/STYLE-GUIDE.md) for tone, voice, and formatting guidelines. Key principles:

- **Be objective** — State facts, avoid marketing language
- **Be factual** — Make specific, verifiable claims
- **Be practical** — Show working examples first, explain after
- **Documentation must match actual behavior.** When writing or updating docs, verify claims against the code. Check output formats, confirm flows work as described, and test sample commands. Inaccurate docs erode trust.

### Documentation URL structure

The documentation site is published at `majorcontext.com/moat`. Files in `docs/content` map to URLs with folder and file number prefixes removed:

- `docs/content/concepts/01-sandboxing.md` → `majorcontext.com/moat/concepts/sandboxing`
- `docs/content/guides/04-ssh.md` → `majorcontext.com/moat/guides/ssh`
- `docs/content/reference/02-moat-yaml.md` → `majorcontext.com/moat/reference/moat-yaml`

When referencing documentation in error messages or code, use these URLs.

### Keeping docs up to date

When you add or change functionality, update the relevant documentation:

- **CLI commands/flags** — Update `docs/content/reference/01-cli.md`
- **moat.yaml fields** — Update `docs/content/reference/02-moat-yaml.md`
- **New features** — Add or update the relevant guide in `docs/content/guides/`
- **Architectural changes** — Update concept pages in `docs/content/concepts/`
- **Examples** — Keep `examples/` directories current with working code

Documentation is part of the feature. A feature without docs is incomplete.

### Changelog

`CHANGELOG.md` tracks every released version, including patch releases. When adding an entry:

- **Added/Changed/Fixed/Security/Breaking** — use these section headings per [Keep a Changelog](https://keepachangelog.com)
- **Breaking changes** go under a dedicated `### Breaking` heading with migration steps (what command to run, what to rename)
- **Security fixes** go under `### Security` with impact description and whether user action is required
- **Fix entries** follow the pattern: "Fix X — previously, Y happened when Z" so users can tell if they were affected
- **Bold the feature name** for major additions; leave minor entries plain
- **Link every entry** to its PR: `([#NNN](https://github.com/majorcontext/moat/pull/NNN))`
- **Each release** gets a 1–2 sentence summary paragraph under the version heading
- Follow `docs/STYLE-GUIDE.md` — no marketing language, no passive voice, no filler

## Git Commits

- Use [Conventional Commits](https://www.conventionalcommits.org/) format: `type(scope): description`
  - Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `build`, `ci`, `perf`
  - Scope is optional but encouraged (e.g., `feat(api): add user endpoint`)
- Do not include `Co-Authored-By` lines for Claude in commit messages

## Design Specs & Plans

- Store all design specs and implementation plans in `docs/plans/`
- Naming convention: `YYYY-MM-DD-<topic>-design.md` for specs, `YYYY-MM-DD-<topic>-plan.md` for plans
- Do not create `docs/superpowers/` or other directories for specs — `docs/plans/` is the single location

## Before You Push

The `claude-review` bot reviews every push — but it's the same model reviewing the same diff, so catching issues locally first saves a round-trip. Before pushing a branch:

- **Self-review the diff** with `/code-review` (or the compound-engineering review agents). This catches the recurring classes the bot flags: edge cases, missing test coverage, error-classification mistakes.
- **Re-read open PR review threads** before each new push. "Previous review flagged this — still not fixed" has recurred; address prior feedback before adding more.
- **Check the high-frequency traps** from "Codebase Invariants" above — especially companion-case test coverage — plus empty/malformed inputs (empty strings, trailing-separator names like `mcp:`).
- **Fill the CHANGELOG PR link** — replace the `#NNN` placeholder with the real PR number. CI now fails on an unfilled placeholder.

## Creating Pull Requests

- Use `gh pr create` with default flags only (no `--base`, `--head`, etc.)
- If `gh pr create` fails, report the error to the operator immediately
- Do not attempt to work around failures by adding flags or changing configuration
- Let the operator fix any repository or remote configuration issues
