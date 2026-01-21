// Package claude provides Claude Code plugin and marketplace management for Moat.
//
// This package handles:
//   - Loading and merging Claude settings from multiple sources
//   - Managing plugin marketplaces (cloning, updating, caching)
//   - Generating container configuration for Claude Code integration
//   - Credential injection through MCP server configuration
//
// # Settings Precedence
//
// Claude settings are loaded from multiple sources and merged with increasing precedence:
//
//  1. ~/.claude/settings.json         Claude's native user settings (lowest priority)
//  2. ~/.moat/claude/settings.json    User defaults for moat runs
//  3. .claude/settings.json           Project settings (in workspace)
//  4. agent.yaml claude.*             Run-specific overrides (highest priority)
//
// # Plugin Marketplaces
//
// Marketplaces are repositories containing Claude Code plugins. They support:
//   - Git repositories (HTTPS or SSH)
//   - Local directories
//
// Marketplaces are cached locally in ~/.moat/claude/plugins/marketplaces/ and
// mounted read-only into containers.
//
// # Security Model
//
// Credentials are handled via proxy injection, never exposed to containers:
//   - MCP server configs use placeholder values for tokens
//   - The Moat proxy intercepts requests and adds real Authorization headers
//   - SSH access to private marketplaces requires explicit grants
//
// Path traversal attacks are prevented through validation in MarketplacePath()
// which rejects names containing path separators or traversal patterns.
//
// # Container Integration
//
// When starting a container with Claude plugins configured:
//   - Marketplace cache is mounted read-only at /moat/claude-plugins
//   - Generated settings.json is mounted at ~/.claude/settings.json
//   - MCP config is written to /workspace/.mcp.json if configured
//   - Temporary config files are stored in Run.ClaudeConfigTempDir and cleaned
//     up when the run stops or is destroyed
package claude
