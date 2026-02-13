// Package providers provides explicit registration of all credential and agent providers.
//
// Import this package to ensure all providers are registered with the registry.
// Each provider's init() function handles its own registration.
package providers

import (
	// Import all providers to trigger their init() registration.
	_ "github.com/majorcontext/moat/internal/providers/aws"    // registers AWS provider
	_ "github.com/majorcontext/moat/internal/providers/claude" // registers Claude/Anthropic provider
	_ "github.com/majorcontext/moat/internal/providers/codex"  // registers Codex/OpenAI provider
	_ "github.com/majorcontext/moat/internal/providers/gemini" // registers Gemini/Google provider
	_ "github.com/majorcontext/moat/internal/providers/github" // registers GitHub provider
	_ "github.com/majorcontext/moat/internal/providers/npm"    // registers npm provider

	"github.com/majorcontext/moat/internal/providers/configprovider"
)

// RegisterAll registers all credential providers.
// Go providers self-register via init() when this package is imported.
// Config-driven providers are loaded after, so Go providers take precedence.
func RegisterAll() {
	// Go providers already registered via init() on import.
	// Config-driven providers loaded after, so Go providers take precedence.
	configprovider.RegisterAll()
}
