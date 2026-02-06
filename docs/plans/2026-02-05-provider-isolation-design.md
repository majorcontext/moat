# Provider Isolation Design

## Overview

Refactor provider-specific code into isolated, self-contained packages with clear interfaces. Eliminates hardcoded provider logic in the manager and CLI, enabling new providers to be added without modifying core code.

## Goals

- No provider-specific imports in `internal/run/manager.go`
- Providers implement well-documented interfaces
- Adding a new provider requires only creating a new package
- Clear separation: manager orchestrates, providers handle specifics

## Package Structure

```
internal/
  provider/              # Core interfaces and registry
    interfaces.go        # CredentialProvider, AgentProvider, EndpointProvider
    registry.go          # Explicit registration, lookup functions
    credential.go        # Credential type, storage interface
    errors.go            # Sentinel errors, GrantError type
    testing.go           # MockProvider for tests
    util/                # Shared utilities
      prompt.go          # PromptForToken, PromptForChoice
      env.go             # CheckEnvVars
      validate.go        # Token format validation

  providers/             # Provider implementations
    register.go          # RegisterAll() - explicit registration
    github/
      provider.go        # Implements CredentialProvider
      grant.go           # Grant logic (gh CLI, env, PAT)
      provider_test.go
    aws/
      provider.go        # Implements CredentialProvider + EndpointProvider
      grant.go           # Role ARN handling
      endpoint.go        # Credential endpoint handler
      provider_test.go
    claude/
      provider.go        # Implements CredentialProvider + AgentProvider
      grant.go           # OAuth + API key flows
      agent.go           # Container prep, config generation
      cli.go             # RegisterCLI for moat claude commands
      session.go         # Session management
      provider_test.go
    codex/
      provider.go        # Implements CredentialProvider + AgentProvider
      grant.go           # API key flow
      agent.go           # Container prep, config generation
      cli.go             # RegisterCLI for moat codex commands
      session.go         # Session management
      provider_test.go

  run/
    manager.go           # Orchestrator - imports only internal/provider
```

## Interfaces

### CredentialProvider

Implemented by all providers. Handles credential acquisition, proxy configuration, and container setup.

```go
type CredentialProvider interface {
    // Identity
    Name() string  // "github", "claude", "aws"

    // Credential acquisition (moat grant)
    Grant(ctx context.Context) (*Credential, error)

    // Proxy configuration
    ConfigureProxy(p ProxyConfigurer, cred *Credential)

    // Container setup
    ContainerEnv(cred *Credential) []string
    ContainerMounts(cred *Credential, containerHome string) ([]MountConfig, error)

    // Token refresh (return false/zero if not supported)
    CanRefresh(cred *Credential) bool
    RefreshInterval() time.Duration
    Refresh(ctx context.Context, p ProxyConfigurer, cred *Credential) (*Credential, error)

    // Cleanup
    Cleanup(cleanupPath string)

    // Dependencies (e.g., github implies gh, git)
    ImpliedDependencies() []string
}
```

### AgentProvider

Extends CredentialProvider for AI agent runtimes (Claude, Codex).

```go
type AgentProvider interface {
    CredentialProvider

    // Container preparation (staging dirs, config files)
    PrepareContainer(ctx context.Context, opts PrepareOpts) (*ContainerConfig, error)

    // Session management
    Sessions() ([]Session, error)
    ResumeSession(id string) error

    // CLI registration
    RegisterCLI(root *cobra.Command)
}
```

### EndpointProvider

For providers that expose HTTP endpoints to containers (AWS credential endpoint).

```go
type EndpointProvider interface {
    CredentialProvider

    // Register endpoints on the proxy
    RegisterEndpoints(mux *http.ServeMux, cred *Credential)
}
```

## Registry

Explicit registration, no `init()` magic.

```go
// internal/provider/registry.go

var (
    mu        sync.RWMutex
    providers = make(map[string]CredentialProvider)
)

func Register(p CredentialProvider)
func Get(name string) CredentialProvider
func GetAgent(name string) AgentProvider
func GetEndpoint(name string) EndpointProvider
func All() []CredentialProvider
func Agents() []AgentProvider
func Names() []string
```

```go
// internal/providers/register.go

func RegisterAll() {
    provider.Register(github.New())
    provider.Register(aws.New())
    provider.Register(claude.New())
    provider.Register(codex.New())
}
```

```go
// cmd/moat/main.go

func main() {
    providers.RegisterAll()
    // ... CLI setup
}
```

## Manager Integration

Manager becomes provider-agnostic:

```go
// internal/run/manager.go

import "github.com/majorcontext/moat/internal/provider"

func (m *Manager) Create(ctx context.Context, opts CreateOpts) (*Run, error) {
    for _, grantName := range opts.Grants {
        p := provider.Get(grantName)
        if p == nil {
            return nil, fmt.Errorf("unknown provider: %s", grantName)
        }

        cred, err := m.credStore.Get(grantName)
        if err != nil {
            return nil, err
        }

        // All providers: proxy + env + mounts
        p.ConfigureProxy(proxy, cred)
        env = append(env, p.ContainerEnv(cred)...)
        mounts, err := p.ContainerMounts(cred, containerHome)

        // Endpoint providers: register HTTP handlers
        if ep := provider.GetEndpoint(grantName); ep != nil {
            ep.RegisterEndpoints(proxy.Mux(), cred)
        }

        // Track refreshable credentials
        if p.CanRefresh(cred) {
            m.refreshTargets = append(m.refreshTargets, refreshTarget{
                provider: p,
                cred:     cred,
                interval: p.RefreshInterval(),
            })
        }
    }

    // Agent providers: prepare container
    if agent := provider.GetAgent(opts.Agent); agent != nil {
        containerCfg, err := agent.PrepareContainer(ctx, PrepareOpts{...})
        env = append(env, containerCfg.Env...)
        mounts = append(mounts, containerCfg.Mounts...)
    }
}
```

## CLI Integration

Generic commands use registry. Agent-specific commands registered by providers.

```go
// cmd/moat/cli/grant.go

func newGrantCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "grant <provider>",
        RunE: func(cmd *cobra.Command, args []string) error {
            p := provider.Get(args[0])
            if p == nil {
                return fmt.Errorf("unknown provider: %s\n\nAvailable: %s",
                    args[0], strings.Join(provider.Names(), ", "))
            }
            cred, err := p.Grant(cmd.Context())
            if err != nil {
                return err
            }
            return credStore.Save(args[0], cred)
        },
    }
}
```

```go
// cmd/moat/cli/root.go

func Execute() error {
    root := &cobra.Command{Use: "moat"}

    // Core commands
    root.AddCommand(newGrantCmd())
    root.AddCommand(newRunCmd())

    // Agent providers register their commands
    for _, agent := range provider.Agents() {
        agent.RegisterCLI(root)
    }

    return root.Execute()
}
```

## Error Handling

```go
// internal/provider/errors.go

var (
    ErrProviderNotFound    = errors.New("provider not found")
    ErrCredentialNotFound  = errors.New("credential not found")
    ErrCredentialExpired   = errors.New("credential expired")
    ErrRefreshNotSupported = errors.New("credential refresh not supported")
)

type GrantError struct {
    Provider string
    Cause    error
    Hint     string  // Actionable guidance
}
```

## Testing

- Each provider package has its own `*_test.go` files
- Manager tests use `MockProvider` - no real providers needed
- `internal/provider/testing.go` provides `MockProvider` with fluent builders

## Responsibilities

### Manager handles:
- Container lifecycle (create, start, stop, destroy)
- Proxy lifecycle (start, configure base settings)
- Grant orchestration (iterate grants, call provider methods)
- Credential refresh loop
- Run storage (logs, network traces, audit)
- MCP relay setup

### Providers handle:
- Credential acquisition (`moat grant` logic)
- Proxy configuration (headers, response transformers)
- Container environment (env vars, mounts)
- Config file generation (`.claude.json`, `auth.json`)
- Session management (for agent providers)
- CLI command registration (for agent providers)

## Migration Path

### Phase 1: Create interfaces (non-breaking)
- Add `internal/provider/` with interfaces, registry, utilities
- Existing code continues to work

### Phase 2: Migrate providers one at a time
- Start with GitHub (simplest, credential-only)
- Move to `internal/providers/github/`, implement new interfaces
- Update registration, verify tests pass
- Repeat for AWS, then Claude, then Codex

### Phase 3: Update manager
- Replace hardcoded provider imports with interface calls
- Remove special-case `if provider == "claude"` blocks
- Manager imports only `internal/provider`

### Phase 4: Update CLI
- Generic `grant` command uses registry
- Agent providers register their own commands
- Remove provider-specific CLI files (logic moves to providers)

### Phase 5: Cleanup
- Remove old provider locations
- Remove `internal/credential/` provider-specific files
- Update documentation

## Estimated Scope

- ~10-15 files to create (interfaces, new provider packages)
- ~20-25 files to modify (manager, CLI, tests)
- ~15-20 files to delete (old locations)
