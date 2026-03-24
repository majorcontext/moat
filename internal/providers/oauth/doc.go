// Package oauth implements an OAuth 2.1 credential provider for Moat.
//
// It supports browser-based authorization code flow with PKCE, automatic
// token refresh via refresh_token grant, and MCP OAuth discovery (RFC 9728,
// RFC 8414, RFC 7591).
//
// Credentials are stored under "oauth:<name>" in the encrypted credential
// store, allowing multiple independent OAuth integrations.
//
// Configuration can come from CLI flags, a YAML file at
// ~/.moat/oauth/<name>.yaml, or automatic MCP server discovery.
package oauth
