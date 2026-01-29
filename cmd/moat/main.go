package main

import (
	"os"

	"github.com/majorcontext/moat/cmd/moat/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
