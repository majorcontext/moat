package moatinit

import (
	"errors"
	"fmt"
	"io"
)

// Context carries the frozen configuration, the Sys seam, the user command,
// and the stderr stream through every phase.
type Context struct {
	Sys    Sys
	Cfg    *Config
	Argv   []string // the user command (the entrypoint's "$@")
	Stderr io.Writer
}

// exitError aborts the entrypoint with a specific exit code. The user-facing
// message has already been written to Stderr by the phase (wording is
// contract and lives next to the phase logic), so the pipeline only carries
// the code.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("moat-init: exit %d", e.code) }

// Phase is one ordered step of the entrypoint. Phases return nil to
// continue, or an exitError to abort with that code. Any other error is a
// programming bug and is reported as a generic fatal.
type Phase struct {
	Name string
	Run  func(*Context) error
}

// phases returns the ordered phase list. The exact order is a
// correctness/security invariant (catalog X-ORDER-GLOBAL): /etc/hosts
// synthetic entries must precede anything that resolves moat-proxy/moat-host;
// agent staging and init files must precede the pre_run hook; the workspace
// volume populate must precede setup_workspace_mcp_json (so moat's .mcp.json
// wins over a user tree's copy) and the privilege drop (its chown needs
// root); the exec dispatch is last and replaces the process image.
//
// Phase bodies are filled in incrementally (plan §9 commits 2–6); until the
// exec dispatch phase lands, Run ends fail-closed rather than starting the
// user command without the privilege-drop contract.
func phases() []Phase {
	return []Phase{
		// Commit 2–6 land: extra-hosts, ssh-agent-bridge, claude-staging,
		// codex-staging, gemini-staging, copilot-staging, init-files,
		// clipboard, git-config, docker, named-volume-chown,
		// populate-workspace-volume, workspace-mcp-json, pre-run-hook,
		// exec-dispatch.
	}
}

// Run executes all phases in order. On success it never returns: the final
// phase replaces the process image via Sys.Exec. It returns the process exit
// code on failure.
//
// Fail-closed: reaching the end of the phase list means the exec dispatch
// did not run (it can only be skipped in an incomplete build), and starting
// the user command without the privilege-drop contract would silently run it
// as root — so refuse instead.
func Run(ctx *Context) int {
	for _, p := range phases() {
		if err := p.Run(ctx); err != nil {
			var exit exitError
			if errors.As(err, &exit) {
				return exit.code
			}
			fmt.Fprintf(ctx.Stderr, "moat-init: internal error in phase %s: %v\n", p.Name, err)
			return 1
		}
	}
	fmt.Fprintln(ctx.Stderr, "FATAL: moat-init reached the end of its phase list without exec'ing the command.")
	fmt.Fprintln(ctx.Stderr, "This build of moat-init is incomplete; use MOAT_INIT_IMPL=sh (the default) or rebuild moat.")
	return 1
}
