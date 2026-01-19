# SSH Agent Proxy Design

**Date:** 2026-01-19
**Status:** Implemented

## Problem

Containers running `git clone git@github.com:...` or other SSH operations cannot authenticate because:

1. SSH private keys are not available inside the container
2. Direct SSH agent forwarding (`$SSH_AUTH_SOCK` mount) exposes all loaded keys for any host
3. There's no audit trail of SSH key usage

Moat's HTTP credential proxy solves this for HTTPS by injecting tokens without exposing them. SSH needs an equivalent mechanism.

## Solution

Implement a **scoped SSH agent proxy** that:

1. Runs on the host, connected to the user's real SSH agent
2. Filters key availability by granted hosts
3. Mounts a filtered socket into the container
4. Logs all SSH agent operations

This mirrors the HTTP credential proxy pattern: container connects to moat's filtered agent instead of the real agent.

### Alternatives Considered

| Approach | Why Not |
|----------|---------|
| Direct agent forwarding | Container can use any loaded key for any host |
| Ephemeral deploy keys | Requires API access to add/remove keys per repo |
| HTTPS URL rewriting | Doesn't work for non-git SSH use cases |
| SSH ProxyCommand | More invasive SSH config changes |

## Design

### Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Host System                              │
│  ┌──────────────┐    ┌────────────────────────────────────┐    │
│  │ User's SSH   │◄───┤ Moat SSH Agent Proxy               │    │
│  │ Agent        │    │ - Filters identities by grant      │    │
│  │              │    │ - Validates sign requests          │    │
│  └──────────────┘    │ - Logs to audit store              │    │
│                      └───────────────▲────────────────────┘    │
│                                      │ Unix socket             │
│  ┌───────────────────────────────────┼────────────────────┐    │
│  │ Container                         │                     │    │
│  │  SSH_AUTH_SOCK=/run/moat/ssh/agent.sock                 │    │
│  │  git clone git@github.com:org/repo.git                 │    │
│  └────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
```

### SSH Agent Protocol

The SSH agent protocol is simple request/response over Unix socket:

| Message | Direction | Purpose |
|---------|-----------|---------|
| `SSH_AGENTC_REQUEST_IDENTITIES` | Client→Agent | List public keys |
| `SSH_AGENT_IDENTITIES_ANSWER` | Agent→Client | Return public keys |
| `SSH_AGENTC_SIGN_REQUEST` | Client→Agent | Sign data with key |
| `SSH_AGENT_SIGN_RESPONSE` | Agent→Client | Return signature |

The proxy intercepts and filters these:

1. **`IDENTITIES_ANSWER`**: Only returns keys mapped to granted hosts
2. **`SIGN_REQUEST`**: Only forwards if key is allowed for current target host

### Host-to-Key Mapping

Users configure which keys can access which hosts:

```bash
# Grant default key for github.com
moat grant ssh --host github.com

# Grant specific key for gitlab.com
moat grant ssh --host gitlab.com --key ~/.ssh/work_key
```

Stored in `~/.moat/credentials/ssh.json`:

```json
{
  "mappings": [
    {
      "host": "github.com",
      "key_fingerprint": "SHA256:abc123...",
      "key_path": "~/.ssh/id_ed25519"
    }
  ]
}
```

### Host Detection Challenge

The SSH agent protocol doesn't include the target host in sign requests—only the key fingerprint and data to sign. To enforce host-based restrictions:

1. Set `GIT_SSH_COMMAND` to a wrapper that notifies the proxy of the target host
2. Track connection state in the proxy
3. Fallback: if key maps to exactly one granted host, allow signing

### Configuration

**agent.yaml:**

```yaml
agent: my-agent
grants:
  - github              # HTTP API access
  - ssh:github.com      # SSH access to github.com
  - ssh:gitlab.com      # SSH access to gitlab.com
```

**CLI:**

```bash
# Grant SSH access
moat grant ssh --host github.com
moat grant ssh --host gitlab.com --key ~/.ssh/work_key

# Run with SSH grants
moat run --grant ssh:github.com

# Run with all configured SSH hosts
moat run --grant ssh
```

### Security Model

**Docker:** Socket mounted at `/run/moat/ssh-agent.sock`, accessible only from container.

**Apple containers:** Per-run auth token (same pattern as HTTP proxy). Container environment includes authentication credentials in a wrapper script.

### Audit Logging

All SSH agent operations logged to audit store:

```json
{"type": "ssh", "data": {"action": "list"}}
{"type": "ssh", "data": {"action": "sign_allowed", "fingerprint": "SHA256:abc...", "host": "github.com"}}
{"type": "ssh", "data": {"action": "sign_denied", "fingerprint": "SHA256:def...", "host": "gitlab.com", "error": "not granted"}}
```

## Implementation

### Package Structure

```
internal/
  sshagent/
    protocol.go      # Identity types and fingerprint computation
    client.go        # SSH agent client wrapper
    proxy.go         # Filtering proxy logic
    server.go        # Unix socket and TCP server
  credential/
    ssh.go           # SSH grant storage
```

### Files to Modify

1. `internal/credential/types.go` - Add `SSHMapping` type
2. `internal/credential/store.go` - Add `GrantSSH`, `GetSSHMappings` methods
3. `internal/run/manager.go` - Start SSH proxy, mount socket, set env vars
4. `internal/audit/types.go` - Add SSH event types
5. `cmd/moat/cli/grant.go` - Add `grant ssh` subcommand

### Runtime Interface

No changes needed. Socket mounting uses existing `MountConfig`.

## Limitations

1. **Non-git SSH**: `GIT_SSH_COMMAND` wrapper only tracks git operations. Direct `ssh user@host` commands require manual host specification or fallback logic.

2. **Multiple keys per host**: Initial implementation supports one key per host. Multiple keys (personal + deploy) could be added later.

3. **SSH certificates**: Not supported initially. Keys only.

## Testing

**Unit tests:**
- Protocol parsing (identities answer, sign request)
- Key filtering logic
- Fingerprint computation

**Integration tests:**
- Git clone over SSH with granted host
- Git clone denied for non-granted host
- Audit log verification

**E2E tests:**
- Docker runtime SSH clone
- Apple container SSH clone
- Mixed HTTP + SSH grants
