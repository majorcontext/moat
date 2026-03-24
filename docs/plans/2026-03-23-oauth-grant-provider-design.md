# OAuth Grant Provider Design

## Summary

Add a generic OAuth credential provider to moat so that `moat grant oauth <name>`
acquires OAuth tokens via browser-based authorization code flow with PKCE, stores
them in the existing credential store, and refreshes them automatically during runs.

This replaces the MCP-specific OAuth approach from PR #188 with a generic OAuth
grant that works for MCP servers, REST APIs, and any other OAuth-protected service.

Closes #68.

## Motivation

Remote MCP servers (Notion, Linear, Atlassian) increasingly require OAuth for
authentication. The current `moat grant mcp` only supports static tokens pasted
interactively. PR #188 attempted to add OAuth directly into the MCP proxy layer,
but this coupled moat's proxy to OAuth token lifecycle (refresh, expiry, token
exchange) and only worked for MCP servers.

OAuth is a credential acquisition method, not an MCP concern. Modeling it as a
credential provider — like GitHub, SSH, or AWS — keeps the architecture clean and
makes OAuth tokens available to any grant consumer.

## Design

### Command: `moat grant oauth <name>`

```
moat grant oauth <name>
moat grant oauth <name> --url <mcp-server-url>
moat grant oauth <name> --auth-url <url> --token-url <url> --client-id <id> [--client-secret <secret>] [--scopes <scopes>]
```

The `<name>` identifies the OAuth provider (e.g., `notion`, `linear`, `custom-app`).
Credentials are stored under the grant name `oauth:<name>`.

**OAuth config resolution order:**

1. CLI flags (`--auth-url`, `--token-url`, `--client-id`, `--client-secret`, `--scopes`)
2. Custom provider file at `~/.moat/oauth/<name>.yaml`
3. MCP discovery — if the MCP server URL is known (from `--url` flag or from
   `mcp[name=<name>].url` in moat.yaml), attempt Protected Resource Metadata
   discovery → Auth Server Metadata → Dynamic Client Registration
4. Fail with a message explaining what's missing and how to provide it

If none of these resolve a complete config, the minimum required fields are
`auth_url` + `token_url` + `client_id`. When discovery provides these via DCR,
no manual config is needed.

### OAuth Flow

1. Resolve OAuth config from the sources above.
2. Generate PKCE code verifier (43-128 chars, RFC 7636) and code challenge (S256).
3. Start a local HTTP callback server on a random available port.
4. Build authorization URL with parameters: `response_type=code`, `client_id`,
   `redirect_uri` (localhost callback), `code_challenge`, `code_challenge_method=S256`,
   `state` (random CSRF token), `scope` (if configured).
   If discovery provided a `resource` identifier (RFC 8707), include it.
5. Open browser to authorization URL.
6. Wait for callback with authorization code (5 minute timeout).
7. Validate `state` parameter matches.
8. Exchange authorization code for tokens: POST to `token_url` with
   `grant_type=authorization_code`, `code`, `redirect_uri`, `code_verifier`,
   `client_id`. Include `client_secret` in request body if configured.
   Include `resource` parameter if available.
9. Parse token response: `access_token`, `refresh_token`, `expires_in`, `token_type`.
10. Store credential and print success.

**When `expires_in` is absent:** Set `ExpiresAt` to zero. `CanRefresh()` returns
false for zero `ExpiresAt` — treat as a non-expiring token. This avoids perpetual
refresh attempts.

### Credential Storage

Stored using the existing `provider.Credential` struct. No schema changes needed.
All fields including `client_secret` and `refresh_token` in `Metadata` are encrypted
at rest — the credential store serializes the entire struct and encrypts it with
AES-256-GCM before writing to disk.

```go
provider.Credential{
    Provider:  "oauth:notion",
    Token:     "<access_token>",
    ExpiresAt: time.Now().Add(expiresIn),
    CreatedAt: time.Now(),
    Metadata: map[string]string{
        "token_source":  "oauth",
        "refresh_token": "<refresh_token>",
        "token_url":     "https://api.notion.com/v1/oauth/token",
        "client_id":     "<client_id>",
        "resource":      "<resource_identifier>",  // RFC 8707, if discovered
    },
}
```

The `client_secret` is stored in metadata only if present (some providers use
public clients with PKCE only, no secret). The refresh flow reads it from metadata.

### OAuth Provider Implementation

New package: `internal/providers/oauth/`

Implements `provider.CredentialProvider` and `provider.RefreshableProvider`.

**Files:**

- `provider.go` — interface implementation, provider registration
- `grant.go` — browser OAuth flow (PKCE, callback server, token exchange)
- `refresh.go` — token refresh using stored refresh_token and token_url
- `discovery.go` — MCP OAuth discovery (RFC 9728, RFC 8414, RFC 7591)
- `config.go` — loads custom provider definitions from `~/.moat/oauth/*.yaml`

**Key design decisions:**

- **`ConfigureProxy` is a no-op.** The OAuth provider stores tokens but does not
  know which hosts they apply to. Host-to-credential mapping is configured in
  `moat.yaml` (under `mcp[].auth.grant` or future network auth config) and handled
  by the existing MCP credential injection code. This differs from GitHub where
  `ConfigureProxy` hardcodes `api.github.com` — OAuth grants are generic.

- **`RefreshInterval` returns 5 minutes.** The run manager's existing refresh loop
  calls `Refresh()` on this interval. The `Refresh()` implementation checks
  `ExpiresAt` and only actually refreshes when the token is within 10 minutes of
  expiry.

### Provider Registry Integration

The provider registry (`internal/provider/registry.go`) uses exact string matching.
OAuth grants use the format `oauth:<name>` (e.g., `oauth:notion`). The provider
registers as `"oauth"`.

**Grant name splitting.** The existing code in `internal/run/run.go` splits grant
names on `:` to find the provider name (`oauth:notion` → provider `oauth`). This
already works for SSH grants (`ssh:github.com` → provider `ssh`). The same split
logic applies to OAuth grants.

**Credential store key.** The full grant name `oauth:notion` is used as the
credential store key (the `Provider` field in the credential struct). This is the
same pattern as SSH: `ssh:github.com` is the store key, `ssh` is the provider
name. The code must pass the **full grant name** to `credStore.Get()` and
`credStore.Save()`, not the split prefix.

**Changes needed in existing code:**

- `internal/provider/registry.go` — add `GetByPrefix(name string)` method that
  tries exact match first, then splits on `:` and retries. Or: have call sites
  split before calling `Get()`. Review existing SSH grant handling to confirm
  which pattern is already in place.
- `internal/run/run.go` — `validateGrants` must use the full grant name for
  credential store lookups and the prefix for provider lookups. Verify this
  matches existing SSH behavior.
- `internal/daemon/refresh.go` — same split logic. After `Refresh()` returns,
  persist the updated credential to the store (see Token Refresh section).
- Error messages must include the full grant name: `Run: moat grant oauth notion`,
  not `Run: moat grant oauth`.

### Token Refresh

Refresh uses the existing `RefreshableProvider` mechanism.

```
Run starts
  -> manager loads oauth:notion credential from store
  -> manager registers credential with proxy (via MCP server config)
  -> manager starts refresh goroutine (existing code path)

Every 5 minutes (RefreshInterval):
  -> manager calls CanRefresh() -> true (token_source == "oauth" AND ExpiresAt != zero)
  -> Refresh() checks ExpiresAt:
     -> if token expires within 10 min: POST to token_url with refresh_token
     -> update credential with new access_token, refresh_token, ExpiresAt
     -> return updated credential
     -> caller persists to credential store (see below)
     -> caller updates proxy credential map
  -> if not near expiry: return current credential unchanged
```

**Credential persistence after refresh.** The existing daemon refresh loop
(`internal/daemon/refresh.go`) does not persist refreshed credentials to the store.
For GitHub tokens (which refresh from external sources like `gh auth token`), this
is acceptable. For OAuth, it's critical — the refresh token in memory may differ
from the one on disk, and a daemon restart would lose the current refresh token.

The daemon refresh loop must call `store.Save()` after a successful `Refresh()`.
This is a change to existing code that also benefits other refreshable providers.

**Refresh failure:** If the refresh token is revoked or the auth server is down,
`Refresh()` returns an error. The run manager logs the error. The proxy continues
injecting the (now expired) token — the MCP server will reject it with 401 and the
agent will see the error. The user must re-run `moat grant oauth <name>`.

**Daemon mode:** In daemon mode, credentials are pre-resolved in `RunContextData`.
Daemon-mode refresh for OAuth is the same unsolved problem as daemon-mode refresh
for GitHub tokens. This is out of scope for this design — tracked separately.

### moat.yaml Integration

No config schema changes. OAuth grants plug into existing `auth.grant`:

```yaml
grants:
  - github
  - oauth:notion

mcp:
  - name: notion
    url: https://mcp.notion.com/mcp
    auth:
      grant: oauth:notion
      header: Authorization
```

**Bearer prefix:** OAuth tokens require `Authorization: Bearer <token>` format.
The proxy identifies OAuth grants by the `oauth:` prefix in the grant name and
prepends `Bearer ` when injecting. Static MCP grants (`mcp-*`) continue to be
injected raw.

This requires a small change in `injectMCPCredentials` and `handleMCPRelay`:

```go
if strings.HasPrefix(grant, "oauth:") {
    credValue = "Bearer " + credValue
}
```

Note: this differs from the GitHub provider which handles Bearer prefix in
`ConfigureProxy`. Since OAuth's `ConfigureProxy` is a no-op (it doesn't know
which hosts to target), the Bearer prefix is applied at injection time. If a
future non-MCP use of OAuth grants is added (e.g., REST API network auth), the
Bearer prefix logic will need to move to a shared location.

**Grant validation:** `moat run` already validates that all referenced grants exist
in the credential store. An `oauth:notion` grant that hasn't been obtained fails with:

```
Error: grant "oauth:notion" not found

  Run: moat grant oauth notion
```

### Custom Provider Config

Users define OAuth providers in `~/.moat/oauth/<name>.yaml`:

```yaml
# ~/.moat/oauth/custom-app.yaml
auth_url: https://auth.example.com/authorize
token_url: https://auth.example.com/token
client_id: abc123
client_secret: shhh
scopes: read write
```

All fields except `client_secret` and `scopes` are required when discovery is not
available. This file never enters the container — it lives on the host in `~/.moat/`,
consistent with how all credential config is stored.

**Example: Linear MCP (no discovery support)**

Linear's MCP server at `mcp.linear.app` does not implement MCP OAuth discovery
(Protected Resource Metadata returns 404). Users must create an OAuth app at
`https://linear.app/settings/api/applications/new` and configure manually:

```yaml
# ~/.moat/oauth/linear.yaml
auth_url: https://linear.app/oauth/authorize
token_url: https://api.linear.app/oauth/token
client_id: <from Linear app settings>
client_secret: <from Linear app settings>
scopes: read,write,issues:create,comments:create
```

Note: Linear uses **comma-separated scopes**, not space-separated. The OAuth
provider must pass scopes through as-is without splitting or reformatting.

Then in `moat.yaml`:

```yaml
grants:
  - oauth:linear

mcp:
  - name: linear
    url: https://mcp.linear.app/mcp
    auth:
      grant: oauth:linear
      header: Authorization
```

Grant flow:

```
$ moat grant oauth linear
→ Opening browser for Linear authorization...
→ Token stored. Expires in 24h, refresh token saved.
```

Linear tokens expire in 24 hours with refresh tokens enabled by default (for apps
created after Oct 1, 2025). PKCE with S256 is supported, so `client_secret` is
optional on the token exchange when PKCE is used.

### MCP OAuth Discovery

The MCP specification defines a standard OAuth discovery flow using Protected
Resource Metadata (RFC 9728) and Authorization Server Metadata (RFC 8414). Testing
against real MCP servers shows this flow works well in practice.

#### Discovery Flow

When `moat grant oauth <name>` is given an MCP server URL (either from moat.yaml
or via `--url` flag), it can discover OAuth endpoints automatically:

1. **Get Protected Resource Metadata.** GET `{origin}/.well-known/oauth-protected-resource`.
   If the MCP endpoint has a path (e.g., `/mcp`), try the path-based variant first:
   `{origin}/.well-known/oauth-protected-resource/mcp`, then fall back to root.
   Returns JSON with `authorization_servers` array and `resource` identifier.

2. **Get Authorization Server Metadata.** GET `{auth_server}/.well-known/oauth-authorization-server`,
   fall back to `{auth_server}/.well-known/openid-configuration`.
   Returns JSON with `authorization_endpoint`, `token_endpoint`,
   `registration_endpoint`, `code_challenge_methods_supported`.

3. **Dynamic Client Registration.** POST to `registration_endpoint` with
   `client_name: "moat"`, `redirect_uris: ["http://127.0.0.1:{port}/callback"]`,
   `grant_types: ["authorization_code"]`,
   `response_types: ["code"]`,
   `token_endpoint_auth_method: "none"`.
   Returns a `client_id` that can be used immediately.

4. **Proceed with standard OAuth flow** using discovered endpoints and registered
   client_id.

#### Discovery Error Handling

Each discovery HTTP request has a 5-second timeout. Follow redirects up to 3 hops.

- **Non-200 response**: treat as "discovery not available" and fall through to next
  config source. Do not error — discovery is best-effort.
- **200 with invalid JSON or missing required fields**: log a warning with the URL
  and field name, fall through.
- **`code_challenge_methods_supported` missing S256**: warn and fall through.
  PKCE S256 is required by OAuth 2.1; servers that don't support it cannot be used
  with automated discovery.
- **DCR fails**: warn and fall through. The user can provide `--client-id` manually.

#### Real-World Server Support (tested 2026-03-23)

| Server | Protected Resource Metadata | Auth Server Metadata | DCR |
|--------|----------------------------|---------------------|-----|
| Notion (`mcp.notion.com`) | Yes | Yes (same host) | Yes |
| Context7 (`mcp.context7.com`) | Yes | Yes (at `context7.com`) | Yes |
| Linear (`mcp.linear.app`) | No (404) | N/A | N/A |

Both Notion and Context7 support the full discovery flow including Dynamic Client
Registration with `token_endpoint_auth_method: "none"` (public client, no secret
needed). This means `moat grant oauth` can work with zero pre-configuration for
these servers:

```
# Zero-config for servers with discovery
$ moat grant oauth notion --url https://mcp.notion.com/mcp
→ Discovered OAuth endpoints for mcp.notion.com
→ Registered client with authorization server
→ Opening browser for Notion authorization...
→ Token stored.
```

Linear does not implement discovery — users must provide config via
`~/.moat/oauth/linear.yaml` as shown in the Custom Provider Config section above.

#### DCR Client ID Caching

Dynamic Client Registration returns a new `client_id` each time. To avoid creating
duplicate registrations, cache the `client_id` per authorization server:

- Store in `~/.moat/oauth/<name>.yaml` after successful DCR (auto-created)
- Subsequent `moat grant oauth <name>` reuses the cached client_id
- If the cached client_id is rejected by the auth server (401/403 during
  authorization), re-register via DCR once. If the second attempt also fails,
  error with instructions. This avoids creating unbounded orphaned registrations.

#### Resource Parameter (RFC 8707)

The MCP spec requires the `resource` parameter in authorization and token requests.
Its value comes from the Protected Resource Metadata `resource` field. This is
stored in credential metadata for use during token refresh:

```go
Metadata: map[string]string{
    "token_source":  "oauth",
    "refresh_token": "<refresh_token>",
    "token_url":     "<discovered_token_url>",
    "client_id":     "<registered_client_id>",
    "resource":      "<resource_identifier>",  // for RFC 8707
},
```

### Proxy Changes

Minimal. Two changes to `internal/proxy/mcp.go`:

1. In `injectMCPCredentials`: when replacing a stub, check if the grant name starts
   with `oauth:` and prepend `Bearer ` to the credential value.

2. In `handleMCPRelay`: same Bearer prefix logic when injecting credentials.

No changes to proxy architecture, relay handling, SSE streaming, or daemon
communication.

### Future Work

**Non-MCP OAuth API auth.** The credential and refresh machinery is generic, but
the Bearer prefix injection currently lives in `internal/proxy/mcp.go`. When auth
support is added to network rules (e.g., `network.allow[].auth.grant: oauth:slack`),
the Bearer prefix logic should move to a shared injection layer so it applies to
all proxied requests, not just MCP relay requests.

**OIDC discovery.** Add `--oidc <issuer-url>` flag to `moat grant oauth`. This
skips MCP-specific discovery and goes directly to
`{issuer}/.well-known/openid-configuration` to fetch auth/token endpoints. The
metadata format is the same as RFC 8414 (OAuth Authorization Server Metadata is a
superset of OIDC discovery), so the same parsing code handles both. This would
support corporate identity providers (Okta, Auth0, Azure AD, Keycloak) without
manual endpoint configuration:

```
moat grant oauth corporate-app --oidc https://mycompany.okta.com
```

**Client Credentials grant.** Some APIs support machine-to-machine auth without
a browser (`grant_type=client_credentials`). Adding `--client-credentials` flag
to `moat grant oauth` would skip the browser flow and exchange client_id +
client_secret directly for a token. Useful for CI/headless environments.

### What This Does NOT Change

- **moat.yaml schema** — no new fields
- **Proxy architecture** — same relay pattern, same injection mechanism
- **Credential store schema** — `provider.Credential` already has all needed fields
- **Container config** — `.claude.json` generation unchanged
- **MCP relay** — same `/mcp/{name}` endpoint, same forwarding logic
- **Daemon API** — no new endpoints

### Testing Strategy

- **Unit tests for OAuth flow:** mock HTTP servers for auth and token endpoints,
  test PKCE generation, state validation, token exchange, error handling
- **Unit tests for refresh:** mock token endpoint, test refresh with valid/expired/
  revoked refresh tokens, test expiry buffer logic, test missing `expires_in`
- **Unit tests for config resolution:** CLI flags, custom YAML files, MCP
  discovery, missing config errors
- **Unit tests for discovery:** mock well-known endpoints, test path-based
  discovery fallback, test DCR, test error cases (invalid JSON, missing fields,
  timeouts, non-200 responses)
- **Unit tests for proxy Bearer prefix:** existing MCP injection tests extended
  with `oauth:*` grant names
- **Integration test:** end-to-end grant flow with a mock OAuth server (no real
  browser — test the callback server and token exchange directly)

### File Inventory

New files:
- `internal/providers/oauth/provider.go`
- `internal/providers/oauth/grant.go`
- `internal/providers/oauth/refresh.go`
- `internal/providers/oauth/discovery.go`
- `internal/providers/oauth/config.go`
- `internal/providers/oauth/provider_test.go`
- `internal/providers/oauth/grant_test.go`
- `internal/providers/oauth/refresh_test.go`
- `internal/providers/oauth/discovery_test.go`
- `cmd/moat/cli/grant_oauth.go`

Modified files:
- `internal/proxy/mcp.go` — Bearer prefix for `oauth:*` grants
- `internal/proxy/mcp_test.go` — tests for Bearer prefix
- `internal/providers/register.go` — import oauth provider
- `internal/daemon/refresh.go` — persist credentials after successful refresh
- `docs/content/guides/09-mcp.md` — document OAuth auth for MCP servers
- `docs/content/reference/01-cli.md` — document `moat grant oauth` command
- `docs/content/reference/02-moat-yaml.md` — document `oauth:*` grant format
