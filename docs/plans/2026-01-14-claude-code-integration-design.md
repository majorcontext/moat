# Claude Code Integration Design

**Date:** 2026-01-14
**Goal:** Run Claude Code inside Moat to test isolation, credential injection, and observability

## Overview

This design adds Anthropic as a credential provider and creates a test setup to run Claude Code in an Moat container. Claude Code is an AI coding assistant that needs to communicate with `api.anthropic.com`. Running it inside Moat tests:

1. **Isolation** - Container sandbox with workspace mounting
2. **Credential injection** - Proxy intercepts API calls and injects the Anthropic key
3. **Observability** - Capture all console output and network requests

## Anthropic Credential Provider

### Authentication Methods

Two methods, matching flexibility of existing GitHub provider:

```bash
agent grant anthropic           # OAuth device flow (browser-based)
agent grant anthropic --api-key # Direct API key entry
```

### Implementation

**New files:**
- `internal/credential/anthropic.go` - OAuth device flow and API key validation

**Modified files:**
- `internal/credential/types.go` - Add `ProviderAnthropic`
- `cmd/agent/cli/grant.go` - Add `anthropic` subcommand

### Proxy Credential Injection

Anthropic uses `x-api-key` header (not `Authorization`). Update proxy to support custom headers:

```go
// New method signature
proxy.SetCredentialHeader(host, headerName, headerValue)

// Usage for Anthropic
proxy.SetCredentialHeader("api.anthropic.com", "x-api-key", token)
```

## Container Configuration

### Environment Variables

```bash
# Route traffic through Moat proxy
HTTPS_PROXY=http://moat:${TOKEN}@${HOST}:${PORT}

# Trust proxy CA for TLS interception
NODE_EXTRA_CA_CERTS=/etc/moat/ca.pem

# Dummy key so Claude Code doesn't error on startup
ANTHROPIC_API_KEY=moat-proxy
```

### Required Domains

Claude Code requires access to (from https://code.claude.com/docs/en/network-config):

| Domain | Purpose | Credential Injection |
|--------|---------|---------------------|
| `api.anthropic.com` | Claude API | Yes |
| `claude.ai` | WebFetch safeguards | No |
| `statsig.anthropic.com` | Telemetry | No |
| `sentry.io` | Error reporting | No |

### CA Certificate Mounting

The proxy's CA certificate must be mounted into the container and referenced via `NODE_EXTRA_CA_CERTS` so Node.js trusts the TLS-intercepting proxy.

## Test Workspace

```
examples/claude-code/
├── agent.yaml          # Configuration
├── main.py             # Buggy code for Claude to fix
└── README.md           # Task description
```

**agent.yaml:**
```yaml
agent: claude-code-test
runtime:
  node: "20"
grants:
  - anthropic
env:
  # Persist Claude Code sessions/auth/settings within the workspace
  CLAUDE_CONFIG_DIR: /workspace/.claude
```

This ensures Claude Code's session transcripts, OAuth tokens, and settings persist across runs in the `.claude/` directory within the workspace.

**main.py:**
```python
def fibonacci(n):
    if n <= 1:
        return n
    return fibonacci(n - 1) + fibonacci(n - 3)  # Bug: should be n-2

if __name__ == "__main__":
    print(fibonacci(10))
```

## Run Command

```bash
# Store Anthropic credentials
agent grant anthropic

# Run Claude Code interactively
agent run claude-code-test examples/claude-code -- claude
```

## Observability Output

After a run, inspect with:

```bash
agent logs <run-id>              # Console output
agent logs <run-id> --network    # API calls to api.anthropic.com
```

**Expected captures:**
- Claude Code startup and responses
- All tool invocations (file reads, edits, bash commands)
- API request/response timing and token usage

## Implementation Phases

### Phase 1: Anthropic Credential Provider
1. Add `ProviderAnthropic` to types
2. Create `anthropic.go` with OAuth + API key flows
3. Add `anthropic` grant subcommand
4. Update proxy for custom header support

### Phase 2: Container Setup
5. Mount CA cert into container
6. Set `NODE_EXTRA_CA_CERTS` env var
7. Handle Anthropic grant → set dummy key + configure proxy

### Phase 3: Test Workspace
8. Create `examples/claude-code/` directory with test files

### Phase 4: Validation
9. Run end-to-end test
10. Verify interactive mode, file editing, API call logging

## Open Questions

1. **Startup validation:** If Claude Code validates the API key on startup, the dummy key might fail. May need to investigate Claude Code's startup behavior.

2. **OAuth flow details:** Need to research Anthropic's OAuth device flow endpoints and response format.
