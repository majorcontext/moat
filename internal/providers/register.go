// Package providers registers all credential provider implementations.
// Import this package to ensure all providers are available via
// credential.GetProviderSetup and credential.ImpliedDependencies.
package providers

import (
	_ "github.com/majorcontext/moat/internal/claude" // registers Anthropic provider
	_ "github.com/majorcontext/moat/internal/codex"  // registers OpenAI provider
	_ "github.com/majorcontext/moat/internal/github" // registers GitHub provider
)
