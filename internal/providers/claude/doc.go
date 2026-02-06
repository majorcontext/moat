// Package claude implements the Claude Code credential and agent provider.
//
// The Claude provider acquires and manages Anthropic credentials for container runs.
// Credentials can be obtained from:
//   - OAuth tokens via `claude setup-token` (recommended for Pro/Max subscribers)
//   - Anthropic API keys from console.anthropic.com
//   - Importing existing Claude Code credentials from keychain or file
//
// The provider configures the proxy to inject Bearer tokens (OAuth) or x-api-key
// headers (API keys) for api.anthropic.com. Containers receive placeholder tokens
// that pass local validation, while the real credentials are injected at the
// network layer by the proxy.
//
// For OAuth tokens, the provider registers a response transformer to handle
// 403 errors on OAuth endpoints that require scopes not available in long-lived
// tokens. This allows Claude Code to degrade gracefully.
//
// As an AgentProvider, this package also handles:
//   - Container preparation (staging directories, config files)
//   - Session management for Claude Code runs
//   - CLI commands (moat claude, moat claude sessions)
//   - Loading and merging Claude settings from multiple sources
//   - Generating Dockerfile snippets for plugin installation
package claude
