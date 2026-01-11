package main

import (
	"os"

	"github.com/andybons/agentops/cmd/agent/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
