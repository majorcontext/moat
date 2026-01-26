# MCP Support Design

**Date:** 2026-01-25
**Status:** Approved

## Principle

**Moat doesn't implement MCP. It places, connects, and constrains MCP servers.**

MCP servers are just tools with I/O and authority. Moat's job is to control where they run, what they can access, and how traffic flows.

## Architecture Overview

For V0, moat supports **remote MCP servers over HTTP/HTTPS**. These are third-party services (Context7, GitHub MCP, etc.) that agents connect to over the network.

### Key Components

1. **agent.yaml MCP declarations** - Projects declare which MCP servers they want to use, with auth requirements
2. **Global MCP grants** - Users store MCP server credentials once via `moat grant mcp <name>`
3. **moat-init setup** - Injects MCP configuration into agent tooling (Claude) using stub credentials
4. **Proxy credential injection** - Replaces stub credentials with real values at network layer, based on URL + header matching
5. **Observability** - Logs MCP connections like regular HTTP traffic

### V0 Scope

**Included:**
- Remote MCP servers over HTTPS
- Custom header-based authentication (e.g., `CONTEXT7_API_KEY`)
- Credential injection through existing proxy infrastructure
- HTTP-level observability (connection, status, timing)

**Excluded (Future Versions):**
- OAuth-based MCP servers (V2 - requires browser flows)
- Sandbox-local MCP servers (Future - requires container orchestration)
- Host-side MCP servers (Future - requires host process management)
- MCP message-level parsing (V0 treats as opaque SSE streams)
- Grant delegation (Future - MCP servers exercising agent capabilities)

## Configuration Format

### agent.yaml MCP Section

```yaml
mcp:
  - name: context7          # Friendly name for logging/audit trails
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7   # References global grant
      header: CONTEXT7_API_KEY  # HTTP header name for auth

  - name: github
    url: https://github-mcp.example.com
    auth:
      grant: mcp-github
      header: Authorization   # Could use standard headers too
```

### Fields

- `name` (required): Identifier for the MCP server, used in logs and `claude mcp add` command
- `url` (required): HTTPS endpoint for the MCP server
- `auth.grant` (optional): Name of global grant to use for authentication
- `auth.header` (optional): Header name where credential should be injected

### Validation Rules

- If `auth` is specified, both `grant` and `header` must be present
- `url` must be HTTPS (no plain HTTP for security)
- Grant must exist when run starts (fail fast with clear error)
- No duplicate `name` values

### Unauthenticated MCP Servers

For public MCP servers that don't require authentication, omit the `auth` block:

```yaml
mcp:
  - name: public-mcp
    url: https://public.example.com/mcp
    # No auth block = no credential injection
```

## Credential Storage

### Grant Command

```bash
moat grant mcp <name>
```

This creates a grant with the naming convention `mcp-<name>`.

### Example Flows

```bash
# Context7
$ moat grant mcp context7
Enter CONTEXT7_API_KEY: ***************
Validating credential...
MCP credential 'mcp-context7' saved to ~/.moat/credentials/mcp-context7.enc

# GitHub MCP
$ moat grant mcp github
Enter GitHub MCP token: ***************
Validating credential...
MCP credential 'mcp-github' saved to ~/.moat/credentials/mcp-github.enc
```

### Storage Details

- Stored encrypted in `~/.moat/credentials/mcp-<name>.enc` (same as existing grants)
- Uses same encryption/decryption mechanism as existing credential store
- Revoke with `moat revoke mcp-<name>`

### Validation

For V0, validation is minimal - just check the credential is non-empty. We can't validate against the MCP server without knowing each server's auth endpoint. Future versions could add optional validation hooks.

### Listing Grants

```bash
$ moat grants list
github
anthropic
mcp-context7
mcp-github
```

## moat-init Integration

### Container Startup Process

When container starts, moat-init:

1. Checks if `claude` binary exists in PATH
2. If yes, reads MCP servers from agent.yaml
3. For each MCP server, calls `claude mcp add` with stub credential

### Example moat-init Logic

```bash
# Check if claude is available
if ! command -v claude &> /dev/null; then
  exit 0  # Silent skip if Claude not present
fi

# For each MCP server in agent.yaml:
# mcp-context7 -> generates stub "moat-stub-mcp-context7"
claude mcp add \
  --header "CONTEXT7_API_KEY: moat-stub-mcp-context7" \
  --transport http \
  context7 \
  https://mcp.context7.com/mcp
```

### Stub Credential Format

- Pattern: `moat-stub-{grant-name}`
- Example: `moat-stub-mcp-context7`
- Used as placeholder, never the real credential
- Proxy validates it matches this pattern before replacement

### Edge Cases

- If `claude mcp add` fails (wrong URL, Claude version incompatible), log warning but continue
- If MCP server already configured (from previous run), `claude mcp add` overwrites it
- moat-init runs on every container start, so config stays in sync with agent.yaml

### Configuration Lifecycle

- MCP config lives only in the container
- Recreated fresh on each `moat run` from agent.yaml
- No persistent state to manage

## Proxy Credential Injection

### Proxy Responsibilities

The proxy:
1. Loads MCP server configuration from agent.yaml at startup
2. Intercepts outbound requests
3. Matches request (host + header name) against MCP server config
4. Validates header value is a stub
5. Replaces stub with real credential from grant store

### Matching Logic

```
Request to: https://mcp.context7.com/mcp
Header: CONTEXT7_API_KEY: moat-stub-mcp-context7

Proxy checks agent.yaml:
- Does any MCP server have url matching host "mcp.context7.com"? → Yes, context7
- Does that server's auth.header == "CONTEXT7_API_KEY"? → Yes
- Extract grant name from config: "mcp-context7"

Validate:
- Is header value "moat-stub-mcp-context7"? → Yes (matches pattern moat-stub-{grant})

Inject:
- Load credential from ~/.moat/credentials/mcp-context7.enc
- Replace header value with real credential
- Forward request
```

### Security Properties

- Grant selection is based on **configuration** (agent.yaml URL + header), not request content
- Stub validation prevents accidental forwarding of real credentials
- Host matching prevents credential leakage to wrong servers
- Header name matching ensures credential goes in correct header

### Error Handling

- If grant missing: fail request, log error
- If header value doesn't match stub pattern: log warning, forward as-is (might be legitimate non-stub value)
- If URL doesn't match any MCP server: treat as regular HTTP (no injection)

## Observability and Audit Logging

### V0 Approach

For V0, MCP traffic is logged like regular HTTP.

### network.jsonl Entries

```json
{
  "timestamp": "2026-01-25T10:23:44.512Z",
  "method": "GET",
  "url": "https://mcp.context7.com/mcp",
  "status": 200,
  "duration_ms": 89,
  "headers": {
    "CONTEXT7_API_KEY": "[REDACTED]"
  }
}
```

### Audit Log Entries

```json
{
  "type": "credential.injected",
  "timestamp": "2026-01-25T10:23:44.500Z",
  "grant": "mcp-context7",
  "host": "mcp.context7.com",
  "header": "CONTEXT7_API_KEY"
}
```

### What's Captured

- MCP connection established (initial HTTP request)
- Connection duration and status
- Which grant was used for injection
- Host and header name (for audit trail)

### What's NOT Captured in V0

- Individual MCP tool calls (SSE message parsing)
- Tool names or parameters
- MCP protocol-level errors

### Documented Limitations

- V0 treats MCP as opaque SSE streams
- Audit shows "agent connected to context7" but not "agent called create_issue"
- For detailed tool-level observability, check agent logs

### Future Improvements (V2+)

- Parse SSE stream to extract MCP messages
- Log tool calls with parameters (redacting sensitive data)
- Aggregate metrics (tool call counts, latency per tool)

## Error Handling and User Experience

### Common Error Scenarios

**1. Missing grant when running:**

```bash
$ moat claude ./workspace

Error: MCP server 'context7' requires grant 'mcp-context7' but it's not configured

To fix:
  moat grant mcp context7

Then run again.
```

**2. Grant exists but wrong credential:**

```bash
$ moat trace --network

[10:23:44.512] GET https://mcp.context7.com/mcp 401 (89ms)

Warning: MCP authentication failed for 'context7'
This usually means the credential is invalid or expired.

To update:
  moat revoke mcp-context7
  moat grant mcp context7
```

**3. MCP server URL unreachable:**

```bash
$ moat trace --network

[10:23:44.512] GET https://mcp.context7.com/mcp ERR (timeout)

Error: Could not connect to MCP server 'context7'
Check that the URL in agent.yaml is correct:
  url: https://mcp.context7.com/mcp
```

**4. Claude not installed but MCP configured:**

```
(Silent - moat-init skips claude mcp add)
Container starts normally, but agent won't see MCP servers
```

### Validation at Startup

Checks performed at `moat run` startup:
- All MCP server grants exist (fail fast before container starts)
- URLs are HTTPS
- No duplicate MCP server names

### Fail Fast Principle

Better to error immediately with actionable message than start container and fail mysteriously during execution.

## End-to-End User Flow

### Complete Example: Adding Context7 MCP

```bash
# 1. User grants Context7 credential (global, one-time)
$ moat grant mcp context7
Enter CONTEXT7_API_KEY: ***************
Context7 MCP credential saved

# 2. User adds to agent.yaml (project-specific)
# agent.yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY

# 3. User runs Claude (project-specific)
$ moat claude ./workspace

# Behind the scenes:
# - Container starts
# - moat-init runs: claude mcp add --header "CONTEXT7_API_KEY: moat-stub-mcp-context7" ...
# - Proxy starts with MCP server config loaded
# - Claude Code runs, sees context7 MCP server available
# - Agent makes MCP request with stub
# - Proxy replaces stub with real credential
```

## Long-Term Goals

The long-term vision for MCP support in moat:

- **MCP servers authenticate as services** - Server proves its identity to moat
- **Agents grant capabilities** - Agent declares what permissions it gives to MCP servers
- **MCP servers may only exercise capabilities explicitly granted to the agent** - Delegation is explicit and auditable

V0 focuses on authentication TO MCP servers. Future versions will explore grant delegation and capability-based security.

## Implementation Notes

### Files to Modify

- `internal/config/config.go` - Add MCP server parsing to agent.yaml
- `internal/credential/store.go` - Add MCP grant type
- `cmd/moat/grant.go` - Add `moat grant mcp <name>` subcommand
- `internal/proxy/server.go` - Add MCP credential injection logic
- `internal/run/run.go` - Add MCP grant validation at startup
- moat-init scripts - Add `claude mcp add` integration

### Testing Strategy

- Unit tests for agent.yaml parsing
- Integration tests for credential injection
- E2E tests with mock MCP server
- Manual testing with real Context7 service

### Documentation Updates

- Add MCP guide: `docs/content/guides/XX-using-mcp-servers.md`
- Update CLI reference with `moat grant mcp`
- Update agent.yaml reference with MCP section
- Add limitations section for V0 observability
