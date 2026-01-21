# Contributing to Moat

## Development Setup

```bash
git clone https://github.com/andybons/moat.git
cd moat
go build ./...
```

## Running Tests

```bash
# Unit tests
go test ./...

# Single test
go test -run TestName ./path/to/package

# E2E tests (requires container runtime)
go test -tags=e2e -v ./internal/e2e/

# With coverage
go test -coverprofile=coverage.out ./...
```

## Linting

```bash
golangci-lint run
```

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

**Credential Injection:**
1. `moat grant github` → token from gh CLI, env var, or PAT prompt → stored encrypted
2. `moat run --grant github` → proxy started → container traffic routed through proxy
3. Proxy intercepts HTTPS, injects `Authorization` headers for matching hosts

**Image Selection:**
- `agent.yaml` `dependencies` field → `image.Resolve()` → `node:X` / `python:X` / `golang:X` / `ubuntu:22.04`

**Observability:**
- Container stdout → `storage.LogWriter` → `~/.moat/runs/<id>/logs.jsonl`
- Proxy requests → `storage.NetworkRequest` → `network.jsonl`

**Container Runtime Selection:**
- `container.NewRuntime()` auto-detects: Apple containers on macOS 15+ with Apple Silicon, otherwise Docker

**Audit Logging:**
- Events → `audit.Store.Append()` → hash-chained entries in SQLite
- `moat audit <run-id>` displays chain with verification
- `--export` creates portable proof bundle

### Proxy Security Model

The credential-injecting proxy has different security configurations per runtime:

- **Docker:** Proxy binds to `127.0.0.1` (localhost only). Containers reach it via `host.docker.internal` or host network mode.
- **Apple containers:** Proxy binds to `0.0.0.0` (all interfaces) because containers access host via gateway IP. Security is maintained via per-run cryptographic token authentication (32 bytes from `crypto/rand`). Token is passed to container in `HTTP_PROXY=http://moat:token@host:port` URL format.

See [`internal/proxy/proxy.go`](internal/proxy/proxy.go) for the full security model documentation.

## Manual Testing

### Claude Code credential injection

Credential injection for Claude Code requires manual verification:

```bash
# 1. Grant Anthropic credentials
moat grant anthropic

# 2. Start Claude Code in a test directory
moat claude ./examples/claude-code

# 3. Verify Claude Code starts and can make API calls
#    - Claude should authenticate successfully
#    - Try a simple prompt to confirm API access works

# 4. Check that the token was injected (not in environment)
#    In another terminal while Claude is running:
moat run --grant anthropic -- env | grep -i anthropic
#    Should show ANTHROPIC_API_KEY=moat-proxy-injected (placeholder, not real token)

# 5. Verify network trace shows API calls
moat trace --network
#    Should show requests to api.anthropic.com with 200 status
```

### Codex credential injection

```bash
# 1. Grant OpenAI credentials
moat grant openai

# 2. Start Codex in a test directory
moat codex ./examples/agent-codex

# 3. Verify Codex starts and can make API calls
#    - Codex should authenticate successfully
#    - Try a simple prompt to confirm API access works

# 4. Check that the token was injected (not in environment)
moat run --grant openai -- env | grep -i openai
#    Should show OPENAI_API_KEY=moat-proxy-injected (placeholder, not real token)

# 5. Verify network trace shows API calls
moat trace --network
#    Should show requests to api.openai.com with 200 status
```

### GitHub credential injection

```bash
# 1. Grant GitHub credentials
moat grant github

# 2. Test credential injection
moat run --grant github -- curl -s https://api.github.com/user
#    Should return your GitHub user info

# 3. Verify token is not in environment
moat run --grant github -- env | grep -i token
#    Should show GH_TOKEN=moat-proxy-injected (placeholder, not real token)

# 4. Verify network trace
moat trace --network
#    Should show GET https://api.github.com/user with 200 status
```

## Code Style & Guidelines

For code style, error messages, documentation standards, and commit conventions, see [CLAUDE.md](CLAUDE.md).

Key points:
- Follow standard Go conventions and `go fmt`
- Use [Conventional Commits](https://www.conventionalcommits.org/) format: `type(scope): description`
- Error messages should be actionable—tell users exactly what to do
- Documentation must match actual behavior

## Data Directory Structure

```
~/.moat/
  config.yaml              # Global settings
  credentials/             # Encrypted tokens
  proxy/ca/                # Generated CA certificate
  runs/                    # Per-run data
    <run-id>/
      metadata.json        # Run configuration
      logs.jsonl           # Container output
      network.jsonl        # HTTP requests
      traces.jsonl         # OpenTelemetry spans
      secrets.jsonl        # Resolved secret names (not values)
      audit.db             # Tamper-proof audit database
```
