package main

import (
	"os"

	"github.com/majorcontext/moat/cmd/moat/cli"
	"github.com/majorcontext/moat/internal/providers"
	claude "github.com/majorcontext/moat/internal/providers/claude"
	"github.com/majorcontext/moat/internal/quickstart"
)

func main() {
	// Register all credential and agent providers.
	providers.RegisterAll()

	// Wire quickstart prompt builder to break the import cycle
	// (providers/claude cannot import quickstart directly).
	claude.QuickstartPromptBuilder = quickstart.BuildPrompt

	// Register CLI commands for agent providers.
	// Must happen after providers.RegisterAll() to ensure all providers are available.
	cli.RegisterProviderCLI()

	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
