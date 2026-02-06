package main

import (
	"os"

	"github.com/majorcontext/moat/cmd/moat/cli"
	"github.com/majorcontext/moat/internal/providers"
)

func main() {
	// Register all credential and agent providers.
	providers.RegisterAll()

	// Register CLI commands for agent providers.
	// Must happen after providers.RegisterAll() to ensure all providers are available.
	cli.RegisterProviderCLI()

	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
