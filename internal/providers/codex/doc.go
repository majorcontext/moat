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
//   - CLI registration for `moat codex` commands
//   - Doctor diagnostics for Codex configuration
//
// # Generated Configuration
//
// PrepareContainer writes a ~/.codex/config.toml (see NewConfig) that adapts
// Codex to running inside a moat container:
//
//   - Approvals and Codex's own sandbox are off. The container is already the
//     isolation boundary, and Codex's sandbox blocks the network the proxy
//     provides. `moat codex --noyolo` restores Codex's own defaults.
//   - shell_environment_policy.inherit = "all", so commands Codex runs keep the
//     proxy variables (HTTP_PROXY, SSL_CERT_FILE, ...). Codex's "core" default
//     strips them.
//   - /workspace is marked trusted, skipping the first-run trust prompt.
//   - Remote (proxy-relay) and local MCP servers both go in [mcp_servers].
//     Codex reads MCP servers from config.toml only; it ignores .mcp.json.
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
