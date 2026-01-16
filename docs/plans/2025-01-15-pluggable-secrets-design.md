# Pluggable Secrets Backend

## Overview

Add a `secrets:` block to `agent.yaml` that declares environment variables whose values are resolved from external secret managers at runtime. Secrets are resolved on the host before the container starts, and injected as plain environment variables - the agent never sees the secret references.

## Motivation

Setting up secrets for a new project is friction. Developers store API keys in 1Password (or similar), but getting them into a development environment requires manual copy-paste from vaults into `.env` files. This feature makes secret injection declarative and automatic.

## Example

```yaml
name: my-agent
agent: claude

env:
  NODE_ENV: production
  LOG_LEVEL: debug

secrets:
  OPENAI_API_KEY: op://Dev/OpenAI/api-key
  ANTHROPIC_API_KEY: op://Dev/Anthropic/api-key
```

## Behavior

1. Parse `secrets:` block from `agent.yaml`
2. For each entry, dispatch to appropriate resolver based on URI scheme
3. Resolve all secrets on the host (fail fast if any fail)
4. Merge resolved values into container environment alongside `env:`
5. Log secret resolution events to audit trail (names only, never values)

### Security Model

- Secrets resolved on host, before container starts
- Plain values injected as env vars
- Agent never sees `op://` references or auth mechanics
- Consistent with the `--grant` credential injection model
- `secrets:` values never logged; `env:` values safe to log

## Supported Schemes

### v1: 1Password

```
op://vault/item/field
```

Uses the `op` CLI. Inherits user's existing auth (biometric, session, service account).

### Fast-follow: AWS SSM

```
ssm:///path/to/parameter
```

Uses ambient AWS credentials (`~/.aws/credentials` or environment).

### Future

- `gsm://` - GCP Secret Manager
- `vault://` - HashiCorp Vault

## Architecture

### New Package: `internal/secrets/`

```
internal/secrets/
  resolver.go      # Interface + registry + Resolve() dispatcher
  onepassword.go   # 1Password implementation (v1)
  ssm.go           # AWS SSM implementation (stub for now)
  errors.go        # Typed errors with actionable messages
```

### Core Interface

```go
// Resolver resolves a secret reference to its plaintext value.
type Resolver interface {
    // Scheme returns the URI scheme this resolver handles (e.g., "op", "ssm").
    Scheme() string

    // Resolve fetches the secret value for the given reference.
    // The reference is the full URI (e.g., "op://Dev/OpenAI/api-key").
    Resolve(ctx context.Context, reference string) (string, error)
}
```

### Registry Pattern

```go
var resolvers = map[string]Resolver{}

func Register(r Resolver) {
    resolvers[r.Scheme()] = r
}

func Resolve(ctx context.Context, reference string) (string, error) {
    scheme := parseScheme(reference)  // "op://..." -> "op"
    r, ok := resolvers[scheme]
    if !ok {
        return "", &UnsupportedSchemeError{Scheme: scheme}
    }
    return r.Resolve(ctx, reference)
}
```

### 1Password Resolver

Shells out to `op read` rather than using the Go SDK:

- Inherits user's existing auth (biometric, session, service account)
- No SDK dependency to maintain
- Works with both personal and team accounts

```go
type OnePasswordResolver struct{}

func (r *OnePasswordResolver) Scheme() string {
    return "op"
}

func (r *OnePasswordResolver) Resolve(ctx context.Context, reference string) (string, error) {
    if _, err := exec.LookPath("op"); err != nil {
        return "", &BackendNotAvailableError{
            Backend: "1Password",
            Reason:  "op CLI not found in PATH",
            Fix:     "Install from https://1password.com/downloads/command-line/",
        }
    }

    cmd := exec.CommandContext(ctx, "op", "read", reference)
    out, err := cmd.Output()
    if err != nil {
        return "", r.parseError(err, reference)
    }

    return strings.TrimSpace(string(out)), nil
}
```

## Config Changes

Update `internal/config/config.go`:

```go
type Config struct {
    Name         string            `yaml:"name,omitempty"`
    Agent        string            `yaml:"agent"`
    Version      string            `yaml:"version,omitempty"`
    Dependencies []string          `yaml:"dependencies,omitempty"`
    Grants       []string          `yaml:"grants,omitempty"`
    Env          map[string]string `yaml:"env,omitempty"`
    Secrets      map[string]string `yaml:"secrets,omitempty"`  // NEW
    Mounts       []string          `yaml:"mounts,omitempty"`
    Ports        map[string]int    `yaml:"ports,omitempty"`
}
```

### Validation

At config load time:
- Secret names must not overlap with `env:` keys (fail with clear error)
- Warn on unrecognized URI schemes

## Run Integration

In `internal/run/` before container start:

```go
func resolveSecrets(ctx context.Context, cfg *config.Config) (map[string]string, error) {
    resolved := make(map[string]string, len(cfg.Secrets))

    for name, ref := range cfg.Secrets {
        val, err := secrets.Resolve(ctx, ref)
        if err != nil {
            return nil, fmt.Errorf("resolving secret %s: %w", name, err)
        }
        resolved[name] = val
    }

    return resolved, nil
}
```

Resolved secrets merge directly into `container.Config.Env`. The container runtime doesn't distinguish - it just sees env vars.

## Audit Logging

When secrets are resolved, log to audit trail:

```go
audit.Log(audit.SecretResolved{
    Name:    "OPENAI_API_KEY",
    Backend: "1password",
    // No value, ever
})
```

## Error Messages

Actionable errors with fix instructions:

### 1Password CLI not installed

```
Error: Cannot resolve secret OPENAI_API_KEY

  1Password CLI (op) not found in PATH.

  Install it from: https://1password.com/downloads/command-line/

  Then run: op signin
```

### Not authenticated

```
Error: Cannot resolve secret OPENAI_API_KEY

  1Password CLI is not signed in.

  Run: eval $(op signin)

  Or for CI/automation, set OP_SERVICE_ACCOUNT_TOKEN.
```

### Item not found

```
Error: Cannot resolve secret OPENAI_API_KEY

  Reference: op://Dev/OpenAI/api-key

  Item "OpenAI" not found in vault "Dev".

  Check the vault and item names in 1Password, or run:
    op item list --vault Dev
```

### Vault not accessible

```
Error: Cannot resolve secret OPENAI_API_KEY

  Reference: op://Dev/OpenAI/api-key

  Vault "Dev" not found or not accessible.

  List available vaults with:
    op vault list
```

## Implementation Plan

1. Add `Secrets` field to `config.Config`
2. Create `internal/secrets/` package with resolver interface
3. Implement 1Password resolver
4. Integrate into run lifecycle (resolve before container start)
5. Add audit logging for secret resolution
6. Add validation (no overlap between env and secrets keys)
7. Tests

## Future Considerations

- Caching resolved secrets for the duration of a run
- Secret rotation handling (re-resolve on long-running agents?)
- `agent secrets list` command to show what would be resolved (without values)
