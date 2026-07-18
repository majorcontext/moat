// Command moat-init is the container entrypoint: the Go port of
// internal/deps/scripts/moat-init.sh (see internal/moatinit for the phases
// and docs/plans/2026-07-01-moat-init-go-rewrite-plan.md for the parity
// contract).
//
// It is cross-compiled static (CGO_ENABLED=0) for linux/amd64 and
// linux/arm64 by `go generate ./internal/initbin`, embedded into the moat
// host binary, and shipped into run images next to the shell script during
// the migration window (selected via the moat-init dispatcher).
package main

import (
	"fmt"
	"os"

	"github.com/majorcontext/moat/internal/moatinit"
)

func main() {
	sys := moatinit.NewSys()
	ctx := &moatinit.Context{
		Sys:    sys,
		Cfg:    moatinit.LoadConfig(sys),
		Argv:   os.Args[1:],
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	// --plan: print the ordered actions the entrypoint would take for this
	// environment, without performing any of them. A permanent, documented
	// debugging affordance — and the release pipeline's functional gate.
	if len(ctx.Argv) > 0 && ctx.Argv[0] == "--plan" {
		ctx.Argv = ctx.Argv[1:]
		if len(ctx.Argv) == 0 {
			ctx.Argv = []string{"<command>"}
		}
		for _, line := range moatinit.Plan(ctx) {
			fmt.Println(line)
		}
		return
	}

	os.Exit(moatinit.Run(ctx))
}
