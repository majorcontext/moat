// Package claude provides Claude Code plugin/marketplace management and session tracking.
//
// This package handles:
//   - Loading and merging Claude settings from multiple sources
//   - Generating Dockerfile snippets for plugin installation
//   - Session management for Claude Code runs
//
// Note: Credential provider setup and container configuration have moved to
// internal/providers/claude. This package now focuses on settings management,
// Dockerfile generation, and session tracking.
//
// # Settings Precedence
//
// Claude settings are loaded from multiple sources and merged with increasing precedence:
//
//  1. ~/.claude/plugins/known_marketplaces.json  Claude's registered marketplaces (lowest)
//  2. ~/.claude/settings.json                    Claude's native user settings
//  3. ~/.moat/claude/settings.json               User defaults for moat runs
//  4. .claude/settings.json                      Project settings (in workspace)
//  5. agent.yaml claude.*                        Run-specific overrides (highest)
//
// # Session Management
//
// Sessions are stored at ~/.moat/claude/sessions/<id>/metadata.json and track:
//   - Workspace path
//   - Associated run ID
//   - Grants used
//   - Session state (running, stopped, completed)
package claude
