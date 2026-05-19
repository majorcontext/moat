# Kiro CLI Support — Design

## Summary

Add first-class support for the [Kiro CLI](https://cli.kiro.dev) (`kiro-cli`) as
a MOAT agent provider, at full feature parity with the existing Codex and Claude
providers: credential grant, transparent proxy credential injection, container
config staging, local + remote MCP, runtime-context injection, and a `moat kiro`
command. Modeled on the kiro support in `/workspaces/agentbox` and the existing
`internal/providers/codex` implementation.

## Background

`kiro-cli` is AWS's agentic CLI. It authenticates to the Kiro/Q API at
`q.<region>.amazonaws.com` with a Bearer token and uses
`cognito-identity.<region>.amazonaws.com` for identity. agentbox installs it via
`curl -fsSL https://cli.kiro.dev/install | bash`, sets `KIRO_API_KEY=placeholder`,
assembles `~/.kiro/{agents,settings,steering}`, writes `settings/mcp.json`, and
relies on its proxy to inject the real Bearer token on `q.*.amazonaws.com` while
passing `cognito-identity.*.amazonaws.com` through unsigned.

MOAT's architecture: each agent is a package under `internal/providers/<name>/`
implementing `provider.CredentialProvider` + `provider.AgentProvider`, registered
in `internal/providers/register.go`, with a dependency entry in
`internal/deps/registry.yaml` + install case in `internal/deps/install.go`, a
config section in `internal/config/config.go`, a dispatch block in
`internal/run/manager.go`, and a CLI command. The Codex provider is the closest
analog and the template for this work.

## Decisions (settled during brainstorming)

1. **Credential acquisition:** `moat grant kiro` prompts for a Kiro token / API
   key (reads `KIRO_API_KEY` from env first, else prompts), stored encrypted like
   codex/gemini. **Static credential — no refresh.** Re-grant when it expires.
2. **Scope:** Full parity with codex/claude — local MCP servers, runtime-context
   file, and remote MCP relay are all in scope for v1.
3. **Implementation approach:** Dedicated `internal/providers/kiro` provider
   mirroring codex (rejected: config-driven `configprovider` preset — cannot
   implement `AgentProvider`; rejected: minimal-now/defer — fails parity goal).

## Architecture

### 1. CLI install — `internal/deps`

- `registry.yaml`: add
  ```yaml
  kiro-cli:
    description: Kiro CLI
    type: custom
    user-install: true
  ```
- `install.go`: add a `case "kiro-cli":` returning
  - command: `curl -fsSL https://cli.kiro.dev/install | bash -s -- --force`
  - env: `PATH` prepended with `/home/moatuser/.local/bin`
  (mirrors the `claude-code` native-installer case)

### 2. Credential provider — `internal/providers/kiro/`

Files mirror the codex package:

- **`provider.go`** — `Provider` implementing `CredentialProvider` +
  `AgentProvider`. `init()` calls `provider.Register(&Provider{})`.
  - `Name()` → `"kiro"`
  - `Grant(ctx)` → delegates to `NewGrant().Execute(ctx)`
  - `ConfigureProxy(p, cred)` → for each Kiro API host, call
    `p.SetCredentialWithGrant(host, "Authorization", "Bearer "+cred.Token, "kiro")`
  - `ContainerEnv(cred)` → `["KIRO_API_KEY=<placeholder>"]` so kiro-cli runs in
    API-key mode and emits the placeholder Bearer for the proxy to swap (exact
    codex `OPENAI_API_KEY` pattern)
  - `ContainerMounts` → `nil, "", nil` (staging-dir approach)
  - `Cleanup` → no-op
  - `ImpliedDependencies()` → `nil`
- **`constants.go`** — `KiroAPIKeyPlaceholder` (a syntactically plausible
  placeholder; real auth via proxy) and the Kiro API host list.
- **`grant.go`** — `Grant.Execute(ctx)`: read `KIRO_API_KEY` env, else prompt;
  return `*provider.Credential{Provider: "kiro", Token: ..., CreatedAt: now}`.
  `HasCredential()` helper. No network validation in v1 (no documented
  lightweight validation endpoint; revisit if one exists).
- **`agent.go`** — `PrepareContainer`: create temp staging dir, populate
  `~/.kiro` layout (see §3), build env (`ContainerEnv` + `MOAT_KIRO_INIT=`mount),
  return `ContainerConfig` with a read-only mount of the staging dir.
- **`cli.go`** — `RegisterCLI`, `NetworkHosts()`, `DefaultDependencies()`,
  `GetCredentialName()`, `runKiro` via `cli.RunProvider`.

**Hosts:**

| Host pattern | Treatment |
|---|---|
| `q.*.amazonaws.com`, `*.q.*.amazonaws.com` | Bearer token injected |
| `cognito-identity.*.amazonaws.com` | allowlisted, passthrough (no injection) |
| `cli.kiro.dev` | build-time installer only |

`NetworkHosts()` returns all of the above (so they're added to the run allow
list). `ConfigureProxy` only sets credentials on the `q.*` patterns.

### 3. Agent provider — container config staging

`PrepareContainer` writes a staging dir copied to `~/.kiro` by `moat-init.sh`
(via `MOAT_KIRO_INIT` env + read-only mount, exactly like
`MOAT_CODEX_INIT`/`CodexInitMountPath`). Staging contents:

- `settings/cli.json` — `{"chat.disableTrustAllConfirmation": true}` so
  `--trust-all-tools` works non-interactively (from agentbox).
- `settings/mcp.json` — `{"mcpServers": {...}}` built from:
  - **local** servers: `kiro.mcp` entries → `{command, args, env, cwd}`
  - **remote relay** servers: `opts.MCPServers[name]` (proxy relay URL) →
    native HTTP server entry. kiro-cli supports remote HTTP MCP servers
    natively — see https://kiro.dev/docs/cli/mcp/configuration/#remote-server
    for the exact JSON shape; confirm the precise key names against that doc
    during implementation.
- `agents/default.json` — a minimal default agent that includes steering
  resources (`file://~/.kiro/steering/**/*.md`) so the runtime-context file is
  loaded. Modeled on agentbox `agents/default.json`, trimmed (no subagents).
- `steering/moat-context.md` — `opts.RuntimeContext` (the rendered markdown),
  Kiro's equivalent of `AGENTS.md`/`CLAUDE.md`. Only written when non-empty.

### 4. Config — `internal/config/config.go`

```go
Kiro KiroConfig `yaml:"kiro,omitempty"`

type KiroConfig struct {
    SyncLogs *bool                     `yaml:"sync_logs,omitempty"`
    MCP      map[string]MCPServerSpec  `yaml:"mcp,omitempty"`
}
```
Add `ShouldSyncKiroLogs()` mirroring `ShouldSyncCodexLogs()` (enable when the
`kiro` grant is present, unless explicitly overridden).

### 5. Run wiring — `internal/run/manager.go`

- `needsKiroInit := slices.Contains(imgNeeds.initProviders, "kiro")`
- A kiro `PrepareContainer` dispatch block parallel to the codex block
  (~lines 2085–2160): gated on
  `needsKiroInit || hasKiroLocalMCP || ShouldSyncKiroLogs()`; fetch the `kiro`
  credential from the store; build local MCP config from `Config.Kiro.MCP`
  (with the same `grant`→placeholder-env handling codex does); build remote MCP
  relay map from `Config.MCP`; call `PrepareContainer`; append mounts/env;
  wire cleanup into the existing `cleanupAgentConfig` chain.

### 6. CLI command — `moat kiro`

`RegisterCLI` adds `moat kiro [workspace] [flags]` via `cli.RunProvider`
(mirrors `moat codex`):
- `BuildCommand`: base `kiro-cli chat --trust-all-tools --trust-tools=execute_bash`;
  with `-p <prompt>` append `--no-interactive <prompt>`; interactive otherwise
  with optional initial prompt.
- `Dependencies`: `["kiro-cli", "git"]` (no node runtime needed — native binary).
- `NetworkHosts`: from `kiro.NetworkHosts()`.
- `ConfigureAgent`: set `cfg.Kiro.SyncLogs`.
- `agent: kiro` recognized in moat.yaml; included in the default-agent
  resolution alongside claude/codex/gemini.

### 7. Registration & constants

- `internal/providers/register.go`: add
  `_ "github.com/majorcontext/moat/internal/providers/kiro"`
- `internal/credential`: add `ProviderKiro = Provider("kiro")` constant.

### 8. Documentation

- `docs/content/reference/01-cli.md` — `moat kiro` and `moat grant kiro`
- `docs/content/reference/02-moat-yaml.md` — `kiro:` config section
- `docs/content/guides/` — a Kiro guide (parallel to existing agent guides)
- `CHANGELOG.md` — `### Added` entry under the next unreleased version, linked
  to the PR

## Error handling

- Missing/expired credential: `moat kiro` dry-run note "No Kiro token
  configured. Run `moat grant kiro`." (parallels codex's `DryRunNote`).
- `kiro.mcp.<name>` referencing an undeclared grant: same explicit error codex
  produces (`grant %q not declared in top-level grants list`).
- Install failure (`cli.kiro.dev` unreachable at build): surfaced by the
  existing deps/build error path; no special handling.

## Testing

- `provider_test.go` — interface compliance asserts; `ConfigureProxy` sets the
  expected header on each Kiro host; `ContainerEnv` returns the placeholder.
- `grant_test.go` — `Execute` reads `KIRO_API_KEY` env; prompts when unset
  (table test with injected reader).
- `agent_test.go` — `PrepareContainer` writes `cli.json`, `mcp.json` (local +
  remote), `agents/default.json`, and `steering/moat-context.md` with expected
  contents; omits the steering file when `RuntimeContext` is empty.
- `config_test.go` — `KiroConfig` parses; `ShouldSyncKiroLogs` truth table.
- Reuse the existing manager/CLI test patterns for the dispatch + command.
- `make lint` / `go vet ./...` clean before commit.

## Verification points (resolve during implementation)

These cannot be confirmed here because `kiro-cli` is not runnable in this
environment. Each must be checked against a real `kiro-cli` (and the gatekeeper
proxy) before the corresponding code is finalized:

1. **`~/.kiro` config layout** — confirm exact paths/filenames kiro-cli reads
   for settings (`settings/cli.json`), MCP (`settings/mcp.json`), agents
   (`agents/default.json`), and steering. Adjust staging layout to match.
2. **Remote MCP JSON shape** — kiro-cli natively supports remote HTTP MCP
   servers (confirmed via
   https://kiro.dev/docs/cli/mcp/configuration/#remote-server). Confirm the
   precise key names for the HTTP server entry against that doc and emit them
   in `settings/mcp.json`.
3. **Wildcard credential injection** — confirm gatekeeper v0.2.0 matches
   wildcard host patterns (`q.*.amazonaws.com`) for credential injection.
   Evidence suggests yes (codex registers `*.openai.com`; `configprovider`
   accepts wildcard hosts). **If wildcards are not supported, scope to the
   single concrete host `q.us-east-1.amazonaws.com`** (per user decision) and
   document how to add other regions.

## Out of scope (v1)

- Token refresh / OAuth device-login flow (static credential only)
- Persistent Kiro sessions volume across runs
- Kiro skills layering (`~/.kiro/skills`)
- Kiro subagents in the default agent config
