# Claude Plugin Management Design

**Date:** 2026-01-19
**Status:** Draft

## Overview

Agents running Claude Code need plugins and MCP servers, but the current approach has friction:
- Plugin cache at `~/.claude/plugins/cache/` breaks when mounting `~/.claude`
- Private marketplaces need Git credentials that shouldn't enter sandboxes
- Plugin versions resolved at runtime are non-deterministic

Moat manages plugin fetching and caching on the host, mounts a read-only cache into containers, and generates Claude-native configuration pointing to that cache.

## Goals

- Support Claude's native `.claude/settings.json` for project plugins
- Support `agent.yaml` for run-specific overrides
- Fetch private marketplaces using Moat's credential broker (no raw credentials in containers)
- Cache plugins on host for reuse across runs
- Make plugin resolution deterministic via lockfile

## Background: Claude's Plugin Architecture

Claude Code has two extension mechanisms:

| Type | Description | Config Location |
|------|-------------|-----------------|
| **Plugins** | Bundles of commands, agents, skills, hooks, MCP servers, LSP servers | `settings.json` `enabledPlugins` |
| **MCP Servers** | Standalone Model Context Protocol servers | `.mcp.json` or `~/.claude.json` |

**Native configuration precedence:**
1. Managed settings (`/etc/claude-code/managed-settings.json`)
2. Local project (`.claude/settings.local.json`)
3. Shared project (`.claude/settings.json`)
4. User (`~/.claude/settings.json`)

**Plugin cache location:** `~/.claude/plugins/cache/{plugin-name}/{version}/`

## Configuration

### Configuration Layering

Moat merges plugin configuration from three sources:

```
1. agent.yaml claude.*              (highest - run overrides)
2. .claude/settings.json            (project defaults)
3. ~/.moat/claude/settings.json     (user defaults for moat runs)
```

| Source | Format | Purpose |
|--------|--------|---------|
| `agent.yaml` | Moat schema | Run-specific overrides, MCP with grants |
| `.claude/settings.json` | Native Claude | Project defaults, version controlled |
| `~/.moat/claude/settings.json` | Native Claude | Personal defaults for Moat runs |

### agent.yaml Schema

```yaml
claude:
  # Existing: sync session logs to host
  sync_logs: true | false

  # Plugin enable/disable overrides
  plugins:
    plugin-name@marketplace: true | false

  # Additional marketplaces for this run
  marketplaces:
    name:
      source: github | git | directory
      repo: owner/repo          # github
      url: git@host:path.git    # git
      path: /local/path         # directory
      ref: branch | tag         # optional

  # MCP server configuration
  mcp:
    server-name:
      command: string
      args: [string]
      env:
        VAR: value
        VAR2: "${secrets.NAME}"
      grant: github | anthropic
      cwd: /path
```

### .claude/settings.json (Native Claude Format)

```json
{
  "enabledPlugins": {
    "typescript-lsp@claude-plugins-official": true,
    "deployment-tools@acme-marketplace": true
  },
  "extraKnownMarketplaces": {
    "acme-marketplace": {
      "source": {
        "source": "git",
        "url": "git@github.com:acme-corp/claude-plugins.git"
      }
    }
  }
}
```

### Merge Rules

- `enabledPlugins`: Union all sources; later overrides earlier for same plugin
- `extraKnownMarketplaces`: Union all sources; later overrides earlier for same name
- `mcp` (agent.yaml only): Moat-specific, generates `.mcp.json`

**Example merge:**

```
# ~/.moat/claude/settings.json
enabledPlugins: { "personal-tool@my-marketplace": true }

# .claude/settings.json
enabledPlugins: { "team-tool@acme": true, "debug-tool@acme": true }

# agent.yaml
claude:
  plugins:
    debug-tool@acme: false

# Result:
enabledPlugins: {
  "personal-tool@my-marketplace": true,
  "team-tool@acme": true,
  "debug-tool@acme": false
}
```

## Architecture

### Plugin Cache

```
~/.moat/
├── claude/
│   ├── settings.json              # User defaults for moat runs
│   └── plugins/
│       ├── marketplaces/          # Cloned marketplace repos
│       │   ├── claude-plugins-official/
│       │   ├── acme-marketplace/
│       │   └── my-marketplace/
│       ├── cache/                 # Installed plugin files
│       │   ├── typescript-lsp/
│       │   │   └── 1.2.0/
│       │   └── deployment-tools/
│       │       └── 2.0.1/
│       └── plugins.lock           # Version lockfile
```

**Host operations:**
1. Clone/update marketplace repos using credential broker
2. Install plugins from marketplaces to cache
3. Track versions in lockfile

**Container mounts:**
```
~/.moat/claude/plugins/cache/ → /moat/claude-plugins/ (read-only)
```

### Credential Flow

```
┌─────────────────────────────────────────────────────────────┐
│                         HOST                                 │
├─────────────────────────────────────────────────────────────┤
│  1. Read .claude/settings.json                              │
│     └─ Found: acme-marketplace → git@github.com:acme/...   │
│                                                             │
│  2. Resolve credentials via moat grant                      │
│     └─ SSH key or token for github.com                     │
│                                                             │
│  3. Clone marketplace                                       │
│     └─ git clone → ~/.moat/claude/plugins/marketplaces/    │
│                                                             │
│  4. Install plugins to cache                                │
│     └─ Copy plugin files → ~/.moat/claude/plugins/cache/   │
│                                                             │
│  5. Generate container settings.json                        │
│     └─ directory sources pointing to cache                 │
├─────────────────────────────────────────────────────────────┤
│                       CONTAINER                             │
├─────────────────────────────────────────────────────────────┤
│  /moat/claude-plugins/ (read-only mount)                   │
│  ~/.claude/settings.json (generated)                       │
│                                                             │
│  Claude Code reads settings.json                            │
│  └─ Finds plugins via directory sources                    │
│  └─ No git/network access needed for plugins               │
└─────────────────────────────────────────────────────────────┘
```

### Container Configuration Generation

At run creation, Moat generates:

**`$HOME/.claude/settings.json`** in container:

```json
{
  "enabledPlugins": {
    "typescript-lsp@claude-plugins-official": true,
    "deployment-tools@acme-marketplace": true
  },
  "extraKnownMarketplaces": {
    "claude-plugins-official": {
      "source": {
        "source": "directory",
        "path": "/moat/claude-plugins/marketplaces/claude-plugins-official"
      }
    },
    "acme-marketplace": {
      "source": {
        "source": "directory",
        "path": "/moat/claude-plugins/marketplaces/acme-marketplace"
      }
    }
  }
}
```

**`/workspace/.mcp.json`** (if MCP configured):

```json
{
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {
        "GITHUB_TOKEN": "moat-proxy-injected"
      }
    }
  }
}
```

### MCP Credential Injection

MCP servers often need API credentials:

```yaml
claude:
  mcp:
    github:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
      grant: github  # Moat proxy injection

    slack:
      command: npx
      args: ["-y", "@anthropic/mcp-server-slack"]
      env:
        SLACK_BOT_TOKEN: "${secrets.SLACK_TOKEN}"  # Moat secrets
```

- `grant`: Credential injected at network layer via proxy (like existing anthropic grant)
- `secrets`: Value resolved at run creation and written to `.mcp.json`

## Implementation

### Files to Modify

1. **`internal/config/config.go`** - Extend ClaudeConfig
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

2. **`internal/claude/` (new package)**
   - `config.go` - Merge settings from all sources
   - `plugins.go` - Plugin cache management
   - `marketplace.go` - Marketplace cloning/updating
   - `mcp.go` - MCP config generation
   - `lockfile.go` - Version tracking

3. **`internal/run/manager.go`** - Wire up plugin mounting and config generation
   - Mount plugin cache read-only
   - Generate `~/.claude/settings.json` in container
   - Generate `.mcp.json` if MCP servers configured

4. **`cmd/moat/cli/claude.go`** (new file) - CLI commands
   ```bash
   moat claude plugins list
   moat claude plugins update
   moat claude plugins verify
   moat claude marketplace add <source>
   moat claude marketplace list
   ```

### Version Lockfile

**`~/.moat/claude/plugins/plugins.lock`:**

```json
{
  "version": 1,
  "resolved": {
    "typescript-lsp@claude-plugins-official": {
      "version": "1.2.0",
      "integrity": "sha256-abc123...",
      "resolvedAt": "2025-01-15T10:30:00Z"
    },
    "deployment-tools@acme-marketplace": {
      "version": "2.0.1",
      "integrity": "sha256-def456...",
      "marketplaceCommit": "a1b2c3d4",
      "resolvedAt": "2025-01-15T10:30:00Z"
    }
  }
}
```

## Limitations

- **MCP runtime requirements**: MCP servers may need Node/Python. Users must include appropriate dependencies in `agent.yaml`.
- **Plugin auto-update**: Requires explicit `moat claude plugins update` for reproducibility.
- **Conflicts**: When project and user configs conflict, last-write-wins per precedence. Warning logged.

## Examples

### Project with team plugins

```json
// .claude/settings.json (version controlled)
{
  "enabledPlugins": {
    "typescript-lsp@claude-plugins-official": true,
    "deployment-tools@acme-marketplace": true
  },
  "extraKnownMarketplaces": {
    "acme-marketplace": {
      "source": {
        "source": "git",
        "url": "git@github.com:acme-corp/claude-plugins.git"
      }
    }
  }
}
```

```yaml
# agent.yaml
grants:
  - github
  - anthropic

claude:
  sync_logs: true
```

Result: Team plugins enabled, private marketplace fetched via github grant.

### Run with plugin override

```yaml
# agent.yaml
claude:
  plugins:
    deployment-tools@acme-marketplace: false  # Disable for this run
    experimental@dev-marketplace: true        # Enable experimental

  marketplaces:
    dev-marketplace:
      source: git
      url: git@github.com:me/experimental-plugins.git
      ref: develop
```

### Run with MCP servers

```yaml
# agent.yaml
grants:
  - github

claude:
  mcp:
    github:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
      grant: github

    filesystem:
      command: npx
      args: ["-y", "@anthropic/mcp-server-filesystem", "/workspace"]
```
