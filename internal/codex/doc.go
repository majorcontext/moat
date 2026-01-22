// Package codex provides OpenAI Codex CLI integration for Moat.
//
// This package handles:
//   - Loading Codex settings from ~/.codex/config.toml
//   - Generating container configuration for Codex CLI integration
//   - Session management for Codex runs
//   - Credential injection through proxy configuration
//
// # Authentication
//
// Codex supports two authentication methods:
//
//  1. OpenAI API Key - Standard API access billed per token
//  2. ChatGPT Subscription - OAuth-based access via ChatGPT Pro/Teams account
//
// Credentials are handled via proxy injection, never exposed to containers:
//   - Container receives OPENAI_API_KEY=moat-proxy-injected (placeholder)
//   - The Moat proxy intercepts requests and adds real Authorization headers
//
// # Session Management
//
// Sessions are stored at ~/.moat/codex/sessions/<id>/metadata.json and track:
//   - Workspace path
//   - Associated run ID
//   - Grants used
//   - Session state (running, stopped, completed)
//
// # Container Integration
//
// When starting a container with Codex configured:
//   - Staging directory is mounted at /moat/codex-init (read-only)
//   - moat-init script copies files to ~/.codex at startup
//   - Network access is allowed to *.openai.com hosts
//
// # Network Requirements
//
// Codex requires access to:
//   - api.openai.com (API requests)
//   - auth.openai.com (authentication)
//   - platform.openai.com (account management)
//   - chatgpt.com (subscription token validation)
package codex
