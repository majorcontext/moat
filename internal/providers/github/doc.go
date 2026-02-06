// Package github implements the GitHub credential provider.
//
// The GitHub provider acquires and manages GitHub tokens for container runs.
// Tokens can be obtained from:
//   - Environment variables (GITHUB_TOKEN, GH_TOKEN)
//   - The gh CLI (gh auth token)
//   - Interactive PAT prompt
//
// The provider configures the proxy to inject Bearer tokens for api.github.com
// and github.com. Containers receive a format-valid placeholder token that
// passes gh CLI local validation, while the real token is injected at the
// network layer by the proxy.
//
// Token refresh is supported for CLI and environment sources (30 minute interval).
// PATs entered interactively are static and cannot be refreshed.
package github
