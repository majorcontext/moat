# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

AgentOps runs AI agents in isolated Docker containers with credential injection and full observability. Key features:

- **Isolated Execution** - Each agent runs in its own Docker container with workspace mounting
- **Credential Injection** - Transparent auth header injection via TLS-intercepting proxy (agent never sees raw tokens)
- **Smart Image Selection** - Automatically selects container images based on `agent.yaml` runtime config
- **Full Observability** - Captures logs, network requests, and traces for every run
- **Declarative Config** - Configure agents via `agent.yaml` manifests

## Architecture

```
cmd/agent/           CLI entry point (Cobra commands)
internal/
  config/            agent.yaml parsing, mount string parsing
  credential/        Secure credential storage, GitHub OAuth device flow
  docker/            Docker client wrapper for container lifecycle
  image/             Runtime-based image selection (node/python/go → base image)
  log/               Structured logging (slog wrapper)
  proxy/             TLS-intercepting proxy for credential injection
  run/               Run lifecycle management (create/start/stop/destroy)
  storage/           Per-run storage for logs, traces, network requests
```

### Key Flows

**Credential Injection:** `agent grant github` → OAuth device flow → token stored encrypted → `agent run --grant github` → proxy started → container traffic routed through proxy → Authorization headers injected for matching hosts

**Image Selection:** `agent.yaml` runtime field → `image.Resolve()` → node:X / python:X / golang:X / ubuntu:22.04

**Observability:** Container stdout → `storage.LogWriter` → `~/.agentops/runs/<id>/logs.jsonl`; Proxy requests → `storage.NetworkRequest` → `network.jsonl`

## Development Commands

```bash
# Build
go build ./...

# Run tests
go test ./...

# Run a single test
go test -run TestName ./path/to/package

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

- Use generic placeholder names in examples (e.g., `my-agent`, `my-project`) rather than product-specific names that imply dependencies
- Examples should tell a story: explain the problem being solved, show what happens at each step, and include sample output
- When showing CLI commands, include the expected output so users know what to expect
- **Documentation must match actual behavior.** When writing or updating docs, verify claims against the code. Check output formats, confirm flows work as described, and test sample commands. Inaccurate docs erode trust.

## Git Commits

- Use [Conventional Commits](https://www.conventionalcommits.org/) format: `type(scope): description`
  - Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `build`, `ci`, `perf`
  - Scope is optional but encouraged (e.g., `feat(api): add user endpoint`)
- Do not include `Co-Authored-By` lines for Claude in commit messages
