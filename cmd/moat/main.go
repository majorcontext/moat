package main

import (
	"os"

	"github.com/majorcontext/moat/cmd/moat/cli"
	"github.com/majorcontext/moat/internal/providers"
	claude "github.com/majorcontext/moat/internal/providers/claude"
	codex "github.com/majorcontext/moat/internal/providers/codex"
	gemini "github.com/majorcontext/moat/internal/providers/gemini"
	"github.com/majorcontext/moat/internal/quickstart"
)

func main() {
	// Register all credential and agent providers.
	providers.RegisterAll()

	// Wire quickstart prompt builders to break the import cycle
	// (provider packages cannot import quickstart directly).
	claude.QuickstartPromptBuilder = quickstart.BuildPrompt
	codex.QuickstartPromptBuilder = quickstart.BuildPrompt
	gemini.QuickstartPromptBuilder = quickstart.BuildPrompt

	// Register CLI commands for agent providers.
	// Must happen after providers.RegisterAll() to ensure all providers are available.
	cli.RegisterProviderCLI()

	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
