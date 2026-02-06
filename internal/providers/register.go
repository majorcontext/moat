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
	_ "github.com/majorcontext/moat/internal/providers/github" // registers GitHub provider
)

// RegisterAll is a no-op provided for explicit registration semantics.
// All providers self-register via init() when this package is imported.
// This function exists so callers can write providers.RegisterAll() to
// make the registration explicit rather than relying on blank import side effects.
func RegisterAll() {
	// Providers register themselves via init() on import.
	// This function exists for documentation and explicit call semantics.
}
