# Claude Plugin Management Implementation Plan

**Date:** 2026-01-19
**Spec:** [2026-01-19-claude-plugins-design.md](./2026-01-19-claude-plugins-design.md)
**Status:** Implemented (core features)

## Implementation Status

| Phase | Status | Notes |
|-------|--------|-------|
| Phase 1: Configuration Schema | Complete | `internal/config/config.go` |
| Phase 2: Settings Parser | Complete | `internal/claude/settings.go` |
| Phase 3: Marketplace Management | Complete | `internal/claude/marketplace.go` |
| Phase 4: Lockfile | Deferred | Not critical for MVP |
| Phase 5: Container Config Generation | Complete | `internal/claude/generate.go` |
| Phase 6: Run Manager Integration | Complete | `internal/run/manager.go` |
| Phase 7: CLI Commands | Complete | `cmd/moat/cli/claude.go` |

## Overview

This plan implements plugin and MCP server management for Claude Code running inside Moat containers. The feature enables private marketplace support, deterministic plugin versions via lockfile, and MCP server credential injection.

**Why host-side management?** Claude Code's plugin cache (`~/.claude/plugins/`) stores metadata in `installed_plugins.json` with absolute host paths (e.g., `/Users/alice/.claude/plugins/cache/plugin-name/1.0.0/`). These paths don't exist inside containers, breaking plugin resolution when `~/.claude` is mounted. Moat solves this by managing plugins on the host and generating container-specific configuration with correct paths.

## Prerequisites

- SSH branch (`ssh`) must be merged first - provides `grants: ssh:github.com` for private marketplace access
- SSH grants enable `git clone git@github.com:org/private-plugins.git` without exposing credentials

## Implementation Phases

### Phase 1: Configuration Schema

**Goal:** Extend `agent.yaml` to support plugin and MCP configuration.

**Files:**
- `internal/config/config.go` - Add `ClaudeConfig` fields
- `internal/config/config_test.go` - Test new config parsing

**Changes to `ClaudeConfig`:**

```go
type ClaudeConfig struct {
    SyncLogs     *bool                      `yaml:"sync_logs,omitempty"`
    Plugins      map[string]bool            `yaml:"plugins,omitempty"`
    Marketplaces map[string]MarketplaceSpec `yaml:"marketplaces,omitempty"`
    MCP          map[string]MCPServerSpec   `yaml:"mcp,omitempty"`
}

type MarketplaceSpec struct {
    Source string `yaml:"source"` // github, git, directory
    Repo   string `yaml:"repo,omitempty"`
    URL    string `yaml:"url,omitempty"`
    Path   string `yaml:"path,omitempty"`
    Ref    string `yaml:"ref,omitempty"`
}

type MCPServerSpec struct {
    Command string            `yaml:"command"`
    Args    []string          `yaml:"args,omitempty"`
    Env     map[string]string `yaml:"env,omitempty"`
    Grant   string            `yaml:"grant,omitempty"`
    Cwd     string            `yaml:"cwd,omitempty"`
}
```

**Validation rules:**
- `MarketplaceSpec.Source` must be one of: `github`, `git`, `directory`
- If `Source` is `github`, `Repo` is required (format: `owner/repo`)
- If `Source` is `git`, `URL` is required
- If `Source` is `directory`, `Path` is required
- `MCPServerSpec.Command` is required
- `MCPServerSpec.Grant` must reference a valid grant in the config

---

### Phase 2: Claude Settings Parser

**Goal:** Parse and merge Claude's native `settings.json` format.

**Files:**
- `internal/claude/settings.go` (new) - Parse `.claude/settings.json`
- `internal/claude/settings_test.go` (new)

**Types:**

```go
// Settings represents Claude's native settings.json format
type Settings struct {
    EnabledPlugins        map[string]bool            `json:"enabledPlugins,omitempty"`
    ExtraKnownMarketplaces map[string]MarketplaceEntry `json:"extraKnownMarketplaces,omitempty"`
}

type MarketplaceEntry struct {
    Source MarketplaceSource `json:"source"`
}

type MarketplaceSource struct {
    Source string `json:"source"` // git, github, directory
    URL    string `json:"url,omitempty"`
    Path   string `json:"path,omitempty"`
}
```

**Functions:**
- `LoadSettings(path string) (*Settings, error)` - Load a single settings file
- `MergeSettings(base, override *Settings) *Settings` - Merge with precedence
- `LoadAllSettings(workspacePath, homeDir string) (*Settings, error)` - Load and merge all sources

**Merge precedence (lowest to highest):**
1. `~/.moat/claude/settings.json` (user defaults for moat runs)
2. `<workspace>/.claude/settings.json` (project defaults)
3. `agent.yaml` `claude.*` fields (run overrides)

---

### Phase 3: Marketplace Management

**Goal:** Clone and update marketplace repositories on the host.

**Files:**
- `internal/claude/marketplace.go` (new)
- `internal/claude/marketplace_test.go` (new)

**Cache structure:**
```
~/.moat/claude/plugins/
├── marketplaces/           # Cloned marketplace repos
│   ├── claude-plugins-official/
│   └── acme-marketplace/
└── plugins.lock            # Version lockfile
```

**Functions:**
- `EnsureMarketplace(name string, spec MarketplaceSpec, sshHosts []string) error`
  - Clone or update marketplace repo
  - For `git` source with SSH URL, requires matching SSH grant
  - For `github` source, tries HTTPS first, falls back to SSH
- `UpdateMarketplaces(settings *Settings, grants []string) error`
  - Update all marketplaces referenced in settings
- `MarketplacePath(name string) string`
  - Return path to cloned marketplace

**SSH detection:**
- Parse URL to detect SSH vs HTTPS
- SSH URLs: `git@github.com:org/repo.git`, `ssh://git@github.com/org/repo.git`
- HTTPS URLs: `https://github.com/org/repo.git`

**Private repo access check (fast-fail):**
When a marketplace URL is SSH-based:
1. Check if corresponding `ssh:host` grant is configured
2. If not granted, fail immediately with actionable error:
   ```
   Error: marketplace "acme" requires SSH access to github.com

   Private marketplaces need SSH authentication. Grant access with:
     moat grant ssh --host github.com

   Then add to agent.yaml:
     grants:
       - ssh:github.com
   ```
3. If granted but key not in agent, fail with:
   ```
   Error: SSH key for github.com not available

   Your SSH agent must have the key loaded:
     ssh-add ~/.ssh/id_ed25519

   Check available keys with: ssh-add -l
   ```

---

### Phase 4: Plugin Cache

**Goal:** Track plugin versions and provide deterministic resolution.

**Files:**
- `internal/claude/lockfile.go` (new)
- `internal/claude/lockfile_test.go` (new)

**Lockfile format (`~/.moat/claude/plugins/plugins.lock`):**
```json
{
  "version": 1,
  "resolved": {
    "typescript-lsp@claude-plugins-official": {
      "version": "1.2.0",
      "integrity": "sha256-abc123...",
      "marketplaceCommit": "a1b2c3d4",
      "resolvedAt": "2025-01-15T10:30:00Z"
    }
  }
}
```

**Functions:**
- `LoadLockfile() (*Lockfile, error)`
- `SaveLockfile(lf *Lockfile) error`
- `ResolvePlugin(name, marketplace string) (*ResolvedPlugin, error)`
- `UpdatePlugin(name, marketplace string) (*ResolvedPlugin, error)`

**Version resolution:**
1. Check lockfile for existing resolution
2. If locked, use that version
3. If not locked, resolve from marketplace and update lockfile

---

### Phase 5: Container Configuration Generation

**Goal:** Generate Claude configuration files to mount into containers.

**Files:**
- `internal/claude/generate.go` (new)
- `internal/claude/generate_test.go` (new)

**Generated files:**

1. **`$HOME/.claude/settings.json`** in container:
   - Merged settings from all sources
   - Marketplaces converted to `directory` source pointing to cache mount

2. **`/workspace/.mcp.json`** (if MCP configured):
   - MCP server definitions
   - Credentials resolved from grants or secrets

**Functions:**
- `GenerateContainerSettings(merged *Settings, cacheMountPath string) ([]byte, error)`
- `GenerateMCPConfig(servers map[string]MCPServerSpec, grants map[string]string) ([]byte, error)`

**Marketplace source rewriting:**
```json
// Input (from .claude/settings.json)
"acme-marketplace": {
  "source": {"source": "git", "url": "git@github.com:acme/plugins.git"}
}

// Output (for container)
"acme-marketplace": {
  "source": {"source": "directory", "path": "/moat/claude-plugins/marketplaces/acme-marketplace"}
}
```

---

### Phase 6: Run Manager Integration

**Goal:** Wire up plugin management into the run lifecycle.

**Files:**
- `internal/run/manager.go` - Add plugin mounting and config generation

**Changes to `Manager.Create`:**

1. **Load and merge settings:**
   ```go
   // After loading agent.yaml config
   claudeSettings, err := claude.LoadAllSettings(opts.Workspace, homeDir)
   if err != nil {
       return nil, fmt.Errorf("loading claude settings: %w", err)
   }
   ```

2. **Early validation for private marketplaces:**
   ```go
   // Check SSH requirements before doing any work
   for name, spec := range claudeSettings.ExtraKnownMarketplaces {
       if isSSHURL(spec.Source.URL) {
           host := extractHost(spec.Source.URL)
           if !hasSSHGrant(opts.Grants, host) {
               return nil, fmt.Errorf("marketplace %q requires SSH access to %s\n\n"+
                   "Private marketplaces need SSH authentication. Grant access with:\n"+
                   "  moat grant ssh --host %s\n\n"+
                   "Then add to agent.yaml:\n"+
                   "  grants:\n"+
                   "    - ssh:%s", name, host, host, host)
           }
       }
   }
   ```

3. **Ensure marketplaces exist:**
   ```go
   if err := claude.UpdateMarketplaces(claudeSettings, opts.Grants); err != nil {
       return nil, fmt.Errorf("updating marketplaces: %w", err)
   }
   ```

4. **Generate container config:**
   ```go
   containerSettings, err := claude.GenerateContainerSettings(claudeSettings, "/moat/claude-plugins")
   if err != nil {
       return nil, fmt.Errorf("generating claude settings: %w", err)
   }
   ```

5. **Add mounts:**
   ```go
   // Mount plugin cache (read-only)
   mounts = append(mounts, container.MountConfig{
       Source:   pluginCachePath,
       Target:   "/moat/claude-plugins",
       ReadOnly: true,
   })
   ```

6. **Write generated config to container:**
   - Write `settings.json` to temporary location
   - Mount as `$HOME/.claude/settings.json`
   - If MCP configured, write `.mcp.json` to workspace

---

### Phase 7: CLI Commands

**Goal:** Provide CLI for plugin management.

**Files:**
- `cmd/moat/cli/claude.go` (new)

**Commands:**

```bash
# List enabled plugins and their sources
moat claude plugins list

# Update all plugins to latest versions
moat claude plugins update

# Verify plugin integrity against lockfile
moat claude plugins verify

# Add a marketplace
moat claude marketplace add git@github.com:acme/plugins.git --name acme

# List configured marketplaces
moat claude marketplace list
```

**Implementation notes:**
- `plugins list` reads merged settings and lockfile
- `plugins update` fetches latest from marketplaces, updates lockfile
- `marketplace add` updates `~/.moat/claude/settings.json`

---

## Error Handling Strategy

### Fast-fail for missing SSH access

When processing a private marketplace:

```go
func validateMarketplaceAccess(spec MarketplaceSpec, grants []string) error {
    if !isSSHURL(spec.URL) {
        return nil // HTTPS doesn't need SSH grant
    }

    host := extractHost(spec.URL)
    if !hasSSHGrant(grants, host) {
        return &MarketplaceAccessError{
            Marketplace: spec,
            Host:        host,
            Reason:      "no SSH grant",
        }
    }

    // Check SSH agent has the key
    if err := checkSSHAgentHasKey(host); err != nil {
        return &MarketplaceAccessError{
            Marketplace: spec,
            Host:        host,
            Reason:      err.Error(),
        }
    }

    return nil
}
```

### User-friendly error messages

All errors should include:
1. What failed
2. Why it failed
3. How to fix it

Example:
```
Error: Cannot clone marketplace "acme-internal"

The marketplace at git@github.com:acme/internal-plugins.git requires SSH access,
but no SSH grant is configured for github.com.

To fix this:

1. Grant SSH access to GitHub:
   moat grant ssh --host github.com

2. Add the grant to your agent.yaml:
   grants:
     - ssh:github.com

3. Ensure your SSH key is loaded:
   ssh-add -l   # Should show your key
```

---

## Testing Strategy

### Unit Tests

1. **Config parsing:** Valid/invalid `agent.yaml` with claude config
2. **Settings merging:** Precedence rules work correctly
3. **URL parsing:** SSH vs HTTPS detection
4. **Lockfile:** Read/write/update operations

### Integration Tests

1. **Marketplace cloning:** Clone public marketplace via HTTPS
2. **Private marketplace:** Clone with SSH grant (requires SSH setup in CI)
3. **Config generation:** Generated settings.json is valid JSON

### E2E Tests

1. **Full flow:** Run with plugins enabled, verify Claude sees them
2. **MCP servers:** Run with MCP config, verify server starts

---

## Implementation Order

1. **Phase 1: Configuration Schema** - Foundation for everything
2. **Phase 2: Claude Settings Parser** - Needed for marketplace discovery
3. **Phase 3: Marketplace Management** - Core functionality
4. **Phase 6: Run Manager Integration** (partial) - Get basic flow working
5. **Phase 4: Plugin Cache** - Add lockfile support
6. **Phase 5: Container Config Generation** - Complete the flow
7. **Phase 6: Run Manager Integration** (complete) - Wire everything together
8. **Phase 7: CLI Commands** - User-facing tools

---

## Dependencies

- SSH branch must be merged for private marketplace support
- No new external dependencies required
- Uses standard library `encoding/json` for settings files
- Uses `os/exec` for git operations (already used elsewhere)

---

## Open Questions

1. **Plugin installation:** Should plugins be copied to cache or left in marketplace?
   - Current design: Left in marketplace, mounted directly
   - Alternative: Copy to separate cache for better isolation

2. **Lockfile scope:** Global or per-project?
   - Current design: Global (`~/.moat/claude/plugins/plugins.lock`)
   - Alternative: Per-project (`.moat/plugins.lock`)

3. **MCP credential format:** How to handle `${secrets.NAME}` syntax?
   - Option A: Resolve at run creation time (current design)
   - Option B: Pass through to container and resolve there
