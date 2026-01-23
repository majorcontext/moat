# Plan: Add OpenAI Codex CLI Runner

## Overview

Implement `moat codex` command to run OpenAI Codex CLI in isolated containers with credential injection, following the same patterns as `moat claude`.

## Key Differences from Claude

1. **Authentication**: OpenAI uses `OPENAI_API_KEY` or ChatGPT subscription login via device flow
2. **Auth Storage**: ChatGPT login stores tokens in `~/.codex/auth.json` (or OS keyring)
3. **Different Config Location**: Codex uses `~/.codex/` (TOML format) instead of `~/.claude/` (JSON)
4. **Network Requirements**: Must allow `api.openai.com`, `auth.openai.com`, `platform.openai.com`
5. **Full-auto Mode**: Codex has `--full-auto` flag similar to Claude's `--dangerously-skip-permissions`
6. **MCP Support**: Codex supports MCP servers via `~/.codex/config.toml`

## Network Note

The Moat firewall proxies HTTP/HTTPS and filters by hostname, not IP address. This means the Cloudflare CDN IP issue mentioned in [openai/codex#2454](https://github.com/openai/codex/issues/2454) does not apply - we just need to allow the `*.openai.com` hostnames.

## Implementation Plan

### Phase 1: OpenAI Credential Provider

**File: `internal/credential/types.go`**
- Add `ProviderOpenAI Provider = "openai"` constant

**File: `internal/credential/openai.go`** (new)
- `OpenAIAuth` struct for API key authentication
- `PromptForAPIKey()` - interactive API key entry
- `ValidateKey()` - test API call to verify key works
- `CreateCredential()` - create Credential from API key
- `CodexCredentials` struct for ChatGPT subscription tokens
- `GetCodexCredentials()` - retrieve from keyring or `~/.codex/auth.json`
- `CreateCredentialFromCodex()` - convert Codex token to Moat credential

**File: `internal/codex/provider.go`** (new)
- `OpenAISetup` implementing `credential.ProviderSetup`
- `ConfigureProxy()` - inject `Authorization: Bearer <token>` for `api.openai.com`
- `ContainerEnv()` - return `OPENAI_API_KEY=moat-proxy-injected` or `CODEX_API_KEY=moat-proxy-injected`
- `ContainerMounts()` - no special mounts needed
- `PopulateStagingDir()` - write auth.json placeholder for ChatGPT tokens
- Register provider in `init()`

### Phase 2: Codex Package Structure

**File: `internal/codex/doc.go`** (new)
```go
// Package codex provides OpenAI Codex CLI integration for Moat.
//
// This package handles:
//   - Loading and merging Codex settings from multiple sources
//   - Generating container configuration for Codex CLI integration
//   - Session management for Codex runs
```

**File: `internal/codex/session.go`** (new)
- Mirror `internal/claude/session.go` structure
- Session storage at `~/.moat/codex/sessions/<id>/metadata.json`
- `SessionManager` with Create/Get/List/Delete/Update operations

**File: `internal/codex/settings.go`** (new)
- Settings loading from `~/.codex/config.toml` (host)
- Handle TOML format (Codex uses TOML, not JSON)
- Support for profiles and model configuration

**File: `internal/codex/generate.go`** (new)
- `WriteCodexConfig()` - write minimal config to skip onboarding
- `PopulateStagingDir()` - copy auth.json placeholder if needed

### Phase 3: CLI Command

**File: `cmd/moat/cli/codex.go`** (new)
- `codexCmd` with subcommands (sessions, etc.)
- Plugin/marketplace commands are not needed (Codex doesn't have this concept)

**File: `cmd/moat/cli/codex_run.go`** (new)
```go
// Key flags:
// -p, --prompt STRING     Run with prompt (non-interactive)
// -g, --grant STRING[]    Additional grants
// -n, --name STRING       Session name
// -d, --detach            Run in background
// --noyolo                Disable --full-auto (require approvals)
// --allow-host STRING[]   Additional network hosts
// --rebuild               Force rebuild container
// --keep                  Keep container after run

func runCodex(cmd *cobra.Command, args []string) error {
    // 1. Resolve workspace
    // 2. Load agent.yaml if present
    // 3. Build grants (auto-add openai if credential exists)
    // 4. Ensure dependencies: node@20, git, codex-cli
    // 5. Build command: codex --full-auto (unless --noyolo)
    // 6. Allow network: api.openai.com, *.openai.com, auth.openai.com
    // 7. Create session record
    // 8. ExecuteRun()
}
```

**File: `cmd/moat/cli/codex_sessions.go`** (new)
- List/manage Codex sessions

### Phase 4: Grant Command Updates

**File: `cmd/moat/cli/grant.go`**
- Add case for `credential.ProviderOpenAI`
- `grantOpenAI()` function presenting menu:
  1. **ChatGPT subscription** (if Codex CLI installed) - uses `codex login` device flow
  2. **OpenAI API key** - from `OPENAI_API_KEY` env or interactive prompt
  3. **Import existing Codex credentials** (if `~/.codex/auth.json` exists)
- Validate key with test API call (optional, user choice)
- Save credential

### Phase 5: Config and MCP Support

**File: `internal/config/config.go`**
- Add `CodexConfig` struct mirroring `ClaudeConfig`:
```go
type CodexConfig struct {
    SyncLogs *bool                      `yaml:"sync_logs,omitempty"`
    MCP      map[string]MCPServerSpec   `yaml:"mcp,omitempty"`
}
```
- Add `Codex CodexConfig` field to main `Config` struct
- Add `ShouldSyncCodexLogs()` method

**File: `internal/codex/generate.go`**
- `GenerateMCPConfig()` - generate MCP server config for Codex
- Uses same `MCPServerSpec` format as Claude for consistency

### Phase 6: Run Manager Updates

**File: `internal/run/manager.go`**
- Add `needsCodexInit` check similar to `needsClaudeInit`
- Create Codex staging directory when OpenAI grant is present
- Mount at `/moat/codex-init`
- Set `MOAT_CODEX_INIT` env var for init script

**File: `internal/container/scripts/moat-init.sh`**
- Add Codex initialization alongside Claude:
```bash
if [ -n "$MOAT_CODEX_INIT" ] && [ -d "$MOAT_CODEX_INIT" ]; then
    mkdir -p "$HOME/.codex"
    cp -a "$MOAT_CODEX_INIT/." "$HOME/.codex/"
    chmod -R u+w "$HOME/.codex"
fi
```

### Phase 7: Code Consolidation

After implementing Codex, consolidate shared patterns:

**File: `internal/runner/runner.go`** (new, optional)
- Extract shared runner logic used by both Claude and Codex
- Common patterns: workspace resolution, grant handling, session management
- This is optional and can be deferred if the duplication is minimal

**File: `cmd/moat/cli/exec.go`**
- Already has `ExecFlags` and `ExecuteRun()` - reuse these

## File Summary

### New Files
- `internal/credential/openai.go` - OpenAI API key authentication
- `internal/codex/doc.go` - Package documentation
- `internal/codex/provider.go` - Credential provider setup
- `internal/codex/session.go` - Session management
- `internal/codex/settings.go` - Settings loading (TOML)
- `internal/codex/generate.go` - Config generation
- `cmd/moat/cli/codex.go` - CLI command structure
- `cmd/moat/cli/codex_run.go` - Main run logic
- `cmd/moat/cli/codex_sessions.go` - Session management CLI

### Modified Files
- `internal/credential/types.go` - Add ProviderOpenAI constant
- `cmd/moat/cli/grant.go` - Add OpenAI grant flow
- `internal/run/manager.go` - Add Codex initialization
- `internal/container/scripts/moat-init.sh` - Add Codex setup

## Dependencies to Install in Container

The Codex CLI is installed via npm:
```
npm install -g @openai/codex
```

This means the `codex-cli` dependency should resolve to the npm package.

**File: `internal/image/resolve.go`**
- Add `codex-cli` dependency that installs `@openai/codex` globally

## Testing Plan

1. Unit tests for credential handling
2. Unit tests for settings loading
3. E2E test: `moat codex` interactive session
4. E2E test: `moat codex -p "hello"` one-shot mode
5. E2E test: Session management (list, cleanup)

## Decisions Made

1. **ChatGPT Login Support**: YES - Full auth support including:
   - API key authentication
   - ChatGPT subscription via `codex login` device flow
   - Import existing credentials from `~/.codex/auth.json`

2. **MCP Servers**: YES - Use same `codex.mcp` config structure in agent.yaml for consistency with Claude

3. **Network Filtering**: Not an issue - Moat's firewall filters by hostname, not IP, so Cloudflare CDN IPs don't need special handling

4. **Config Profiles**: Deferred - Not in v1, can add later if needed
