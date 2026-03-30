# Gate Keeper: Credential-Injecting Proxy Extraction

**Date:** 2026-03-30
**Status:** Draft

## Overview

Extract moat's TLS-intercepting credential-injecting proxy into a reusable component called Gate Keeper. Gate Keeper runs as a standalone sidecar (ECS, Cloud Run, Modal) or is compiled into moat's CLI. It separates the use of credentials from the service that holds them — a common pattern in high-security applications (API Gateway + API Gate Keeper).

V1 lives in the moat repo as `internal/gatekeeper/` + `cmd/gatekeeper/`. Moat's CLI surface is unchanged.

## Motivation

The proxy + daemon system already does the hard work: TLS interception, per-host credential injection, network policy enforcement, token substitution, and request logging. But it's tightly coupled to moat's CLI lifecycle (daemon spawning, encrypted file store, interactive `moat grant` flow).

Separating Gate Keeper enables:

- **Sidecar deployment** — run alongside any service on ECS, Cloud Run, Kubernetes, or Modal
- **Stronger secret isolation** — secrets live only in Gate Keeper's memory, fetched directly from secret managers (never in env vars or on disk in production)
- **Reuse without moat** — teams that want credential injection without container sandboxing

## V1 Scope

### Included

- TLS-intercepting proxy with credential injection
- Token substitution (Telegram/OpenClaw use case)
- Keep policy enforcement (HTTP and LLM gateway scopes)
- Network policy rules (strict/permissive, per-host rules)
- Request logging (JSONL)
- Credential source interface with built-in adapters: `env`, `static`, `aws-secretsmanager`
- Standalone binary (`cmd/gatekeeper/`) with YAML config
- Dockerfile (distroless base)
- Dynamic registration API (Unix socket or TCP) — moat's daemon uses this path
- Unauthenticated single-tenant mode (sidecar) and token-authenticated multi-tenant mode (moat daemon)

### Deferred

- **MCP relay with end-user credentials (V1.5)** — the relay code exists but multi-user OAuth token management is complex. V1 design doesn't preclude adding `per-session` scoped credentials later.
- **AWS credential endpoint** — the `AWS_CONTAINER_CREDENTIALS_FULL_URI` pattern is moat-specific. Apps behind Gate Keeper typically get AWS credentials through their own IAM role.
- **Response transformers** — OAuth 403 workarounds and token scrubbing are Claude Code-specific. Gate Keeper supports a transformer hook interface so moat can register these, but doesn't ship built-in transformers.
- **Separate repository** — stays in moat repo for V1. Package boundaries are clean enough to lift out later.

## Package Structure

```
internal/gatekeeper/
  gatekeeper.go              # Core server: wires proxy + API + credential sources
  config.go                  # Gate Keeper YAML config parsing
  config_credential.go       # Credential source resolution

internal/gatekeeper/api/
  api.go                     # API types (RegisterRequest, RegisterResponse, etc.)
  server.go                  # HTTP API server
  client.go                  # HTTP client (Unix socket or TCP)
  registry.go                # Token -> RunContext mapping

internal/gatekeeper/credentialsource/
  source.go                  # CredentialSource interface
  env.go                     # Environment variable source
  static.go                  # Inline values (dev/test)
  awssecretsmanager.go       # AWS Secrets Manager direct SDK call

internal/provider/
  interfaces.go              # Split into composable interfaces (see below)
  registry.go                # Unified provider registry (unchanged)
  credential.go              # Credential type (unchanged)

internal/providers/{github,claude,aws,...}
  # Unchanged. Providers implement whichever interfaces they support.
  # Self-contained: no GitHub logic outside github/.

internal/proxy/
  # Stays in place. Gate Keeper imports it.
  # Moat-specific bits (AWS credential endpoint, OAuth response transformers)
  # stay behind hook interfaces.

internal/daemon/
  # Thin wrapper: moat-specific lifecycle on top of gatekeeper/api.
  # Daemon spawning, lock files, idle timer, liveness checking,
  # container health monitoring, persistence across restarts.

cmd/gatekeeper/
  main.go                    # Standalone binary
  Dockerfile                 # Distroless deployment image
```

### What stays in `internal/daemon/`

Moat-specific lifecycle — daemon spawning, lock files, idle timers, liveness checking, container health monitoring, persistence across daemon restarts. These are concerns of "Gate Keeper managed by moat CLI as a background process" and don't apply to standalone deployment.

### What moves to `internal/gatekeeper/api/`

API types (`RegisterRequest`, `RegisterResponse`, `CredentialSpec`, etc.), the HTTP server/client, and the token registry. These are Gate Keeper's core API surface, used by both moat and standalone mode.

## Interface Segregation

### Current state

There are two parallel registration systems:

1. **`internal/credential/`** — `ProviderSetup` interface with `RegisterProviderSetup`/`GetProviderSetup` registry. Defines the `ProxyConfigurer` interface.
2. **`internal/provider/`** — `CredentialProvider` interface with `provider.Register`/`provider.Get` registry. Also defines `AgentProvider`, `EndpointProvider`, `RefreshableProvider`, `InitFileProvider`, `DescribableProvider`, `RunStoppedHook`.

The `credential.ProxyConfigurer` interface (the actual injection surface) has these methods:

```go
// Current: internal/credential/provider.go
type ProxyConfigurer interface {
    SetCredential(host, value string)
    SetCredentialHeader(host, headerName, headerValue string)
    SetCredentialWithGrant(host, headerName, headerValue, grant string)
    AddExtraHeader(host, headerName, headerValue string)
    AddResponseTransformer(host string, transformer ResponseTransformer)
    RemoveRequestHeader(host, headerName string)
    SetTokenSubstitution(host, placeholder, realToken string)
}
```

The `provider.CredentialProvider` interface is:

```go
// Current: internal/provider/interfaces.go
type CredentialProvider interface {
    Name() string
    Grant(ctx context.Context) (*Credential, error)
    ConfigureProxy(p ProxyConfigurer, cred *Credential)
    ContainerEnv(cred *Credential) []string
    ContainerMounts(cred *Credential, containerHome string) ([]MountConfig, string, error)
    Cleanup(cleanupPath string)
    ImpliedDependencies() []string
}
```

### Proposed changes

**Step 1: Consolidate the dual registries.** The `credential.ProviderSetup` registry is a legacy layer. Gate Keeper should use the `provider` registry exclusively. During Phase 3, migrate any remaining `credential.ProviderSetup` callers to use `provider.Get()` with type assertions. The `credential.ProviderSetup` registry can be deprecated (callers migrated) but the types stay until all references are removed.

**Step 2: `ProxyConfigurer` stays in `internal/credential/`.** It's the injection surface — both `daemon.RunContext` and `proxy.Proxy` implement it. No rename, no method changes. Gate Keeper imports it as-is.

**Step 3: Split `CredentialProvider` into composable interfaces.** The monolithic interface splits, but provider implementations don't change — they already have all these methods as concrete struct methods.

```go
// internal/provider/interfaces.go

// ProxyProvider configures proxy credential injection for a given credential.
// The core interface Gate Keeper cares about.
type ProxyProvider interface {
    Name() string
    ConfigureProxy(p credential.ProxyConfigurer, cred *Credential)
}

// GrantProvider acquires credentials interactively.
// Moat CLI only — Gate Keeper never calls this.
type GrantProvider interface {
    Grant(ctx context.Context) (*Credential, error)
}

// ContainerProvider sets up the container environment for a credential.
// Moat only — Gate Keeper doesn't manage containers.
type ContainerProvider interface {
    ContainerEnv(cred *Credential) []string
    ContainerMounts(cred *Credential, containerHome string) ([]MountConfig, string, error)
    Cleanup(cleanupPath string)
}

// RefreshableProvider — unchanged from current definition.
type RefreshableProvider interface {
    CanRefresh(cred *Credential) bool
    RefreshInterval() time.Duration
    Refresh(ctx context.Context, p credential.ProxyConfigurer, cred *Credential) (*Credential, error)
}

// ImpliedDepsProvider declares dependencies between providers.
// Moat CLI only.
type ImpliedDepsProvider interface {
    ImpliedDependencies() []string
}

// These existing interfaces are unchanged and stay moat-only:
// - AgentProvider (embeds CredentialProvider — update to embed the composite)
// - EndpointProvider (embeds CredentialProvider — update to embed the composite)
// - InitFileProvider
// - DescribableProvider
// - RunStoppedHook
```

**Step 4: `CredentialProvider` becomes a composite** that preserves backwards compatibility. All existing code that expects `CredentialProvider` continues to work:

```go
type CredentialProvider interface {
    ProxyProvider
    GrantProvider
    ContainerProvider
    ImpliedDepsProvider
}
```

`AgentProvider` and `EndpointProvider` currently embed `CredentialProvider` — they continue to do so. No change needed at those embedding sites since the composite includes all the same methods.

### Registry changes

The `provider` registry continues to store `CredentialProvider` values and `provider.Get()` returns `CredentialProvider`. This preserves all existing call sites. Gate Keeper uses type assertions to narrow:

```go
p := provider.Get("github")

// Gate Keeper only needs ProxyProvider:
pp := p.(provider.ProxyProvider)  // always succeeds — CredentialProvider embeds ProxyProvider
pp.ConfigureProxy(runCtx, cred)

// Moat CLI uses the full interface as before:
cred, err := p.Grant(ctx)
```

For providers that only implement `ProxyProvider` (hypothetical future Gate Keeper-only providers), the registry would need a parallel `RegisterProxyProvider`/`GetProxyProvider` path. This is a future concern — all current providers implement the full `CredentialProvider` interface.

### Type divergence: MountConfig

`credential.ProviderSetup` references `container.MountConfig` while `provider.CredentialProvider` uses its own `provider.MountConfig`. Gate Keeper doesn't use either (no container management), so this divergence doesn't affect the extraction. Unification is a separate cleanup.

### Type alias: ProxyConfigurer

`internal/provider/interfaces.go` defines `type ProxyConfigurer = credential.ProxyConfigurer`. This alias stays. Gate Keeper code imports `credential.ProxyConfigurer` directly since it's the canonical definition. Moat code can use either import path.

### ToRunContext coupling

`RegisterRequest.ToRunContext()` in `internal/daemon/api.go` converts API types to `daemon.RunContext`. When API types move to `gatekeeper/api/`, this method stays in `internal/daemon/` as a free function or method on a daemon-local wrapper type. This avoids a dependency from `gatekeeper/api` back to `daemon`. The conversion is moat-specific (RunContext carries daemon lifecycle state that Gate Keeper's standalone mode doesn't need).

## Credential Sources

Gate Keeper's replacement for `moat grant`. Instead of interactive acquisition, Gate Keeper pulls credentials from external systems at startup and on refresh intervals.

```go
// internal/gatekeeper/credentialsource/source.go

// CredentialSource fetches a credential value from an external system.
type CredentialSource interface {
    Fetch(ctx context.Context) (string, error)
    Type() string
}

// RefreshableSource supports polling for rotated secrets.
type RefreshableSource interface {
    CredentialSource
    RefreshInterval() time.Duration
}
```

### V1 built-in sources

- **`env`** — reads from an environment variable. Covers Cloud Run, ECS task env, simple cases.
- **`static`** — inline value in config. Dev/test only, logged as warning at startup.
- **`aws-secretsmanager`** — direct AWS SDK call. Implements `RefreshableSource` for polling rotated secrets.

### How sources connect to providers

Gate Keeper config maps a credential source to a provider name. At startup:

1. Fetch the secret value from the source
2. Wrap it as `provider.Credential{Token: value}`
3. Look up the provider by name in the registry
4. Call `provider.ConfigureProxy(proxyConfigurer, cred)`

The **provider** handles "what to inject where" (GitHub knows `Authorization: Bearer` on `api.github.com`). The **source** handles "where to get the secret." No GitHub code in the credential source layer, no Secrets Manager code in the provider.

### Refresh precedence

If the source implements `RefreshableSource`, Gate Keeper polls it for new values. If the provider implements `RefreshableProvider`, the provider's `Refresh()` is called instead of the source's `Fetch()`. The distinction: source refresh re-fetches the raw secret (e.g., polls Secrets Manager for a rotated value), while provider refresh may have domain-specific logic (e.g., GitHub provider re-reads `gh auth token`).

When both exist, **source refresh feeds provider refresh**: Gate Keeper polls the source on its interval, and if the value changes, creates a new `provider.Credential` and calls `provider.ConfigureProxy()` again. The provider's `RefreshableProvider.Refresh()` is only used when there is no source (i.e., credentials were pushed via the dynamic registration API, which is moat's path).

## Configuration

### gatekeeper.yaml

```yaml
# Proxy listener
proxy:
  port: 8080
  host: 0.0.0.0

# TLS CA (generated if not provided)
tls:
  ca_cert: /etc/gatekeeper/ca.pem
  ca_key: /etc/gatekeeper/ca-key.pem

# Credentials — grant names match moat provider names
credentials:
  - grant: github
    source:
      type: env
      var: GITHUB_TOKEN

  - grant: anthropic
    source:
      type: aws-secretsmanager
      secret: prod/anthropic-api-key
      region: us-east-1

  - grant: telegram
    source:
      type: env
      var: TELEGRAM_BOT_TOKEN

# Network policy — same schema as moat.yaml network section
network:
  policy: strict
  allow:
    - api.github.com
    - api.anthropic.com
  rules:
    - host: api.github.com
      methods: [GET, POST]

# Keep policy — same as moat.yaml
policy:
  http: policy/http.yaml
  llm-gateway: policy/llm-gateway.yaml

# API server for dynamic registration
api:
  enabled: true
  socket: /var/run/gatekeeper.sock
  # OR
  listen: 127.0.0.1:9090

# Logging
log:
  level: info
  format: json
  output: stderr
  requests: /var/log/gatekeeper/requests.jsonl
```

### Alignment with moat.yaml

| Concern | moat.yaml | gatekeeper.yaml | Same schema? |
|---------|-----------|-----------------|--------------|
| Network policy | `network.policy` | `network.policy` | Yes |
| Network allow | `network.allow` | `network.allow` | Yes |
| Network rules | `network.rules` | `network.rules` | Yes |
| Keep policies | `network.policy_file` (single path) | `policy.*` (scope-to-path map) | Same YAML format per file; different top-level structure |
| Grants | `grants: [github]` | `credentials[].grant` | Names match |

Moat doesn't use `gatekeeper.yaml`. It pushes pre-resolved credentials through the `RegisterRequest` API, same as today.

## Standalone Binary & Deployment

### cmd/gatekeeper

Single-purpose binary. No Cobra, no subcommands. Config via `--config` flag or `GATEKEEPER_CONFIG` env var.

```go
func main() {
    // 1. Load gatekeeper.yaml
    // 2. Resolve credential sources, fetch initial values
    // 3. For each credential, look up provider, call ConfigureProxy()
    // 4. Start proxy server with Keep policy engines
    // 5. Start API server (if enabled)
    // 6. Start credential refresh loops
    // 7. Block on signal (SIGTERM/SIGINT), graceful shutdown
}
```

### Dockerfile

```dockerfile
FROM golang:1.23 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /gatekeeper ./cmd/gatekeeper

FROM gcr.io/distroless/static-debian12
COPY --from=builder /gatekeeper /gatekeeper
EXPOSE 8080
ENTRYPOINT ["/gatekeeper"]
```

Distroless base — no shell, minimal attack surface. Built with `CGO_ENABLED=0` — all Gate Keeper dependencies (proxy, Keep policy, TLS) must work as pure Go. The audit system's SQLite store is moat-specific and not compiled into Gate Keeper.

### Authentication modes

- **Token-authenticated (multi-tenant)** — proxy requires `Proxy-Authorization` with a registered token. Each token maps to its own credentials. This is moat's daemon model.
- **Unauthenticated (single-tenant)** — proxy accepts all connections, applies global credentials. For sidecar deployment where network namespace provides isolation (ECS task, Kubernetes pod).

### Health check

`GET /healthz` on the proxy port returns 200. Works with ECS health checks, Kubernetes liveness probes, etc.

### ECS sidecar example

```json
{
  "containerDefinitions": [
    {
      "name": "gatekeeper",
      "image": "gatekeeper:latest",
      "essential": true,
      "secrets": [
        {
          "name": "GITHUB_TOKEN",
          "valueFrom": "arn:aws:secretsmanager:us-east-1:123:secret:github-token"
        }
      ],
      "portMappings": [{"containerPort": 8080}]
    },
    {
      "name": "app",
      "image": "my-agent:latest",
      "environment": [
        {"name": "HTTP_PROXY", "value": "http://localhost:8080"},
        {"name": "HTTPS_PROXY", "value": "http://localhost:8080"}
      ],
      "dependsOn": [{"containerName": "gatekeeper", "condition": "START"}]
    }
  ]
}
```

## Migration Path

Four phases. Moat's test suite passes at every phase. TDD throughout.

### Phase 1: Extract API types and registry

Move serialization types from `internal/daemon/` to `internal/gatekeeper/api/`:
- `RegisterRequest`, `RegisterResponse`, `CredentialSpec`, `ExtraHeaderSpec`, etc.
- `Registry` (token to RunContext mapping)
- API server and client

`internal/daemon/` becomes a thin layer importing `gatekeeper/api` and adding moat-specific lifecycle (spawn, lock file, idle timer, liveness, persistence).

The daemon API wire format doesn't change — moving types to a new package does not affect JSON serialization. To maintain Go-level backwards compatibility, `internal/daemon/` re-exports the moved types as aliases (e.g., `type RegisterRequest = api.RegisterRequest`). Existing daemon callers continue to compile without changes. The aliases can be removed once all callers are migrated.

### Phase 2: Extract proxy into Gate Keeper's domain

`internal/proxy/` stays in place. Gate Keeper's core wiring in `internal/gatekeeper/gatekeeper.go`:

```go
type Server struct {
    proxy    *proxy.Proxy
    api      *api.Server
    registry *api.Registry
    sources  []credentialRefresher
}

func New(cfg *Config) (*Server, error) { ... }
func (s *Server) Start(ctx context.Context) error { ... }
func (s *Server) Stop(ctx context.Context) error { ... }
```

Moat's daemon creates a `gatekeeper.Server` internally.

### Phase 3: Split provider interfaces

Refactor `provider.CredentialProvider` into composable interfaces. Find-and-replace at call sites:
- Moat code calling `Grant()` asserts `GrantProvider`
- Code calling `ConfigureProxy()` asserts `ProxyProvider`
- Gate Keeper code only asserts `ProxyProvider` + optionally `RefreshableProvider`

Provider implementations unchanged.

### Phase 4: Add credential sources and standalone binary

Build `internal/gatekeeper/credentialsource/`, config parsing, `cmd/gatekeeper/`, Dockerfile. Purely additive — doesn't touch moat code paths.

## Testing Strategy

TDD at each phase. Moat's existing test suite must pass at every phase boundary.

### Phase 1 (API extraction)
- Existing daemon tests move with the code or are updated to import from `gatekeeper/api`
- Verify JSON serialization round-trips are identical (regression test: marshal with old types, unmarshal with new types)

### Phase 2 (Gate Keeper server)
- Unit tests for `gatekeeper.Server`: start/stop lifecycle, credential injection through proxy
- Integration test: start Gate Keeper with static config, make HTTPS request through proxy, verify credential header injected

### Phase 3 (Interface split)
- Compile-time verification: all providers still satisfy `CredentialProvider` composite
- Existing provider tests unchanged (they test concrete structs, not interfaces)

### Phase 4 (Credential sources and standalone binary)
- Unit tests for each `CredentialSource` implementation (`env`, `static`)
- `aws-secretsmanager` source tested with mock/fake AWS client (interface-based)
- Integration test for `cmd/gatekeeper`: load config, start proxy, inject credential, verify
- Dockerfile build tested in CI (`docker build` succeeds, binary starts and responds to `/healthz`)

## Future: MCP Relay with End-User Credentials (V1.5)

The MCP relay code exists in `internal/proxy/mcp.go` and works for service-level credentials. Multi-user OAuth (hosted agent platforms where each user has their own tokens) requires:

- Per-session scoped credentials in the config:
  ```yaml
  credentials:
    - grant: notion
      scope: per-session
      source:
        type: oauth
        provider: notion
        token_store: redis://...
  ```
- The `RegisterRequest` API already supports per-run credentials, so the dynamic path works
- `CredentialSource` could accept session context via a new interface method or context values

V1's design doesn't preclude this. The `CredentialSource` interface is intentionally minimal to allow extension.

## Future: Additional Credential Sources

The `CredentialSource` interface makes adding new sources straightforward:

- `gcp-secretmanager` — GCP Secret Manager
- `vault` — HashiCorp Vault
- `azure-keyvault` — Azure Key Vault
- `oauth` — OAuth token management with refresh (V1.5, ties into MCP relay)
