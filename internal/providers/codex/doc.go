// Package codex implements the Codex credential and agent provider.
//
// The Codex provider manages OpenAI credentials for running the Codex CLI in
// containers. It implements both CredentialProvider and AgentProvider interfaces.
//
// # Authentication
//
// Codex supports OpenAI API key authentication:
//
//   - API keys are validated against the /v1/models endpoint
//   - Keys must start with "sk-" prefix
//   - The real API key is never exposed to containers
//   - Proxy injection adds Authorization headers at network layer
//
// # Credential Provider
//
// The credential provider configures:
//   - Proxy headers for api.openai.com with Bearer token
//   - Container env with OPENAI_API_KEY placeholder (passes format validation)
//   - No container mounts needed (uses staging directory approach)
//
// # Agent Provider
//
// The agent provider handles:
//   - Container preparation with staging directory
//   - Session management for Codex runs
//   - CLI registration for `moat codex` commands
//
// # Placeholder Tokens
//
// The container receives placeholder values that pass format validation:
//
//	OPENAI_API_KEY=sk-moat-proxy-injected-placeholder-...
//
// This allows the Codex CLI to start without prompting for credentials,
// while the real API key is injected by the proxy at the network layer.
package codex
