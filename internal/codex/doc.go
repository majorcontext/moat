// Package codex provides Codex CLI session management and doctor diagnostics.
//
// This package handles:
//   - Session management for Codex runs
//   - Doctor diagnostics for Codex configuration
//
// Note: Credential provider setup and container configuration have moved to
// internal/providers/codex. This package now focuses on session tracking
// and diagnostics.
//
// # Session Management
//
// Sessions are stored at ~/.moat/codex/sessions/<id>/metadata.json and track:
//   - Workspace path
//   - Associated run ID
//   - Grants used
//   - Session state (running, stopped, completed)
package codex
