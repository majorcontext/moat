# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Moat runs AI agents in isolated containers with credential injection and full observability. Key features:

- **Isolated Execution** - Each moat runs in its own container (Docker or Apple containers) with workspace mounting
- **Credential Injection** - Transparent auth header injection via TLS-intercepting proxy (agent never sees raw tokens)
- **Smart Image Selection** - Automatically selects container images based on `agent.yaml` runtime config
- **Full Observability** - Captures logs, network requests, and traces for every run
- **Declarative Config** - Configure agents via `agent.yaml` manifests
- **Multi-Runtime Support** - Automatically uses Apple containers (macOS 26+) or Docker

## Architecture

```
cmd/moat/           CLI entry point (Cobra commands)
internal/
  audit/             Tamper-proof audit logging with cryptographic verification
  claude/            Claude Code settings and Dockerfile generation
  cli/               Shared CLI helpers (environment parsing, mount helpers)
  codex/             Codex CLI settings and session tracking
  config/            agent.yaml parsing, mount string parsing
  container/         Container runtime abstraction (Docker and Apple containers)
  credential/        Secure credential storage (GitHub, Anthropic, AWS)
  image/             Runtime-based image selection (node/python/go → base image)
  log/               Structured logging (slog wrapper)
  provider/          Provider registry and interfaces (CredentialProvider, AgentProvider)
  providers/         Provider implementations:
    aws/               AWS IAM role assumption and credential endpoint
    claude/            Claude Code CLI, grants, and config generation
    codex/             Codex CLI, grants, and config generation
    github/            GitHub token management and refresh
  proxy/             TLS-intercepting proxy for credential injection and MCP relay
  run/               Run lifecycle management (create/start/stop/destroy)
  storage/           Per-run storage for logs, traces, network requests
  ui/                TTY-aware colored output and formatting helpers
```

### Key Flows

**Credential Injection:** `moat grant github` → token from gh CLI, env var, or PAT prompt → token stored encrypted → `moat run --grant github` → proxy started → container traffic routed through proxy → Authorization headers injected for matching hosts

**Image Selection:** `agent.yaml` `dependencies` field → `image.Resolve()` → node:X-slim / python:X-slim / golang:X / debian:bookworm-slim

**Observability:** Container stdout → `storage.LogWriter` → `~/.moat/runs/<id>/logs.jsonl`; Proxy requests → `storage.NetworkRequest` → `network.jsonl`

**Container Runtime Selection:** `container.NewRuntime()` auto-detects: Apple containers on macOS 26+ with Apple Silicon, otherwise Docker

**Audit Logging:** Console/network/credential events → `audit.Store.Append()` → hash-chained entries in SQLite → `moat audit <run-id>` displays chain with verification; `--export` creates portable proof bundle with attestations

**MCP Integration:** `agent.yaml` defines remote MCP servers → `.claude.json` generated with relay URLs → Claude Code connects to proxy relay → proxy injects credentials → request forwarded to real MCP server with SSE streaming support

### Proxy Security Model

The credential-injecting proxy has different security configurations per runtime:

- **Docker:** Proxy binds to `127.0.0.1` (localhost only). Containers reach it via `host.docker.internal` or host network mode.
- **Apple containers:** Proxy binds to `0.0.0.0` (all interfaces) because containers access host via gateway IP. Security is maintained via per-run cryptographic token authentication (32 bytes from `crypto/rand`). Token is passed to container in `HTTP_PROXY=http://moat:token@host:port` URL format.

See `internal/proxy/proxy.go` for the security model documentation.

### MCP (Model Context Protocol) Support

Moat supports two types of MCP servers:

1. **Remote HTTP MCP servers** (top-level `mcp:` in agent.yaml) - External MCP servers accessed via HTTPS with credential injection through a proxy relay pattern
2. **Local process MCP servers** (under `claude.mcp:` or `codex.mcp:`) - MCP servers running as child processes inside the container

**Remote MCP Architecture:**

The proxy relay pattern works around Claude Code's HTTP client not respecting `HTTP_PROXY`:
- Container's `.claude.json` points to proxy relay endpoints: `http://proxy:port/mcp/{name}`
- Proxy relay intercepts requests, injects real credentials from grant store
- Forwards to actual MCP server with SSE streaming support
- Circular proxy prevented via `NO_PROXY` env var and `http.Transport{Proxy: nil}`

**Key Implementation Files:**
- `internal/proxy/mcp.go` - Relay handler and credential injection
- `internal/providers/claude/config.go` - `.claude.json` generation
- `internal/run/manager.go` - MCP setup during container creation
- `internal/config/config.go` - `MCPServerConfig` (remote) and `MCPServerSpec` (local) types

## Development Commands

```bash
# Build
go build ./...

# Run unit tests
go test ./...

# Run a single test
go test -run TestName ./path/to/package

# Run E2E tests (requires container runtime)
go test -tags=e2e -v ./internal/e2e/

# Run tests with coverage
go test -coverprofile=coverage.out ./...

# Lint (if golangci-lint is installed)
golangci-lint run
```

## Code Style

- Follow standard Go conventions and `go fmt` formatting
- Use `go vet` to catch common issues
- **After completing a batch of changes, always run `make lint` and fix any issues before committing.** This catches formatting, vet, and lint errors early. If `golangci-lint` is not installed, fall back to `go vet ./...`.

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
- `docs/content/guides/03-ssh-access.md` → `majorcontext.com/moat/guides/ssh-access`
- `docs/content/reference/02-agent-yaml.md` → `majorcontext.com/moat/reference/agent-yaml`

When referencing documentation in error messages or code, use these URLs.

### Keeping docs up to date

When you add or change functionality, update the relevant documentation:

- **CLI commands/flags** — Update `docs/content/reference/01-cli.md`
- **agent.yaml fields** — Update `docs/content/reference/02-agent-yaml.md`
- **New features** — Add or update the relevant guide in `docs/content/guides/`
- **Architectural changes** — Update concept pages in `docs/content/concepts/`
- **Examples** — Keep `examples/` directories current with working code

Documentation is part of the feature. A feature without docs is incomplete.

## Git Commits

- Use [Conventional Commits](https://www.conventionalcommits.org/) format: `type(scope): description`
  - Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `build`, `ci`, `perf`
  - Scope is optional but encouraged (e.g., `feat(api): add user endpoint`)
- Do not include `Co-Authored-By` lines for Claude in commit messages

## Creating Pull Requests

- Use `gh pr create` with default flags only (no `--base`, `--head`, etc.)
- If `gh pr create` fails, report the error to the operator immediately
- Do not attempt to work around failures by adding flags or changing configuration
- Let the operator fix any repository or remote configuration issues
