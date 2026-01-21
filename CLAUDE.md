# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Moat runs AI agents in isolated containers with credential injection and full observability. Key features:

- **Isolated Execution** - Each moat runs in its own container (Docker or Apple containers) with workspace mounting
- **Credential Injection** - Transparent auth header injection via TLS-intercepting proxy (agent never sees raw tokens)
- **Smart Image Selection** - Automatically selects container images based on `agent.yaml` runtime config
- **Full Observability** - Captures logs, network requests, and traces for every run
- **Declarative Config** - Configure agents via `agent.yaml` manifests
- **Multi-Runtime Support** - Automatically uses Apple containers (macOS 15+) or Docker

## Architecture

```
cmd/moat/           CLI entry point (Cobra commands)
internal/
  audit/             Tamper-proof audit logging with cryptographic verification
  config/            agent.yaml parsing, mount string parsing
  container/         Container runtime abstraction (Docker and Apple containers)
  credential/        Secure credential storage (GitHub, Anthropic, AWS)
  image/             Runtime-based image selection (node/python/go → base image)
  log/               Structured logging (slog wrapper)
  proxy/             TLS-intercepting proxy for credential injection
  run/               Run lifecycle management (create/start/stop/destroy)
  storage/           Per-run storage for logs, traces, network requests
```

### Key Flows

**Credential Injection:** `moat grant github` → token from gh CLI, env var, or PAT prompt → token stored encrypted → `moat run --grant github` → proxy started → container traffic routed through proxy → Authorization headers injected for matching hosts

**Image Selection:** `agent.yaml` `dependencies` field → `image.Resolve()` → node:X / python:X / golang:X / ubuntu:22.04

**Observability:** Container stdout → `storage.LogWriter` → `~/.moat/runs/<id>/logs.jsonl`; Proxy requests → `storage.NetworkRequest` → `network.jsonl`

**Container Runtime Selection:** `container.NewRuntime()` auto-detects: Apple containers on macOS 15+ with Apple Silicon, otherwise Docker

**Audit Logging:** Console/network/credential events → `audit.Store.Append()` → hash-chained entries in SQLite → `moat audit <run-id>` displays chain with verification; `--export` creates portable proof bundle with attestations

### Proxy Security Model

The credential-injecting proxy has different security configurations per runtime:

- **Docker:** Proxy binds to `127.0.0.1` (localhost only). Containers reach it via `host.docker.internal` or host network mode.
- **Apple containers:** Proxy binds to `0.0.0.0` (all interfaces) because containers access host via gateway IP. Security is maintained via per-run cryptographic token authentication (32 bytes from `crypto/rand`). Token is passed to container in `HTTP_PROXY=http://moat:token@host:port` URL format.

See `internal/proxy/proxy.go` for the security model documentation.

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
