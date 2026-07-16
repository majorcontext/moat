package moatinit

import "fmt"

// preRunHookPhase mirrors run_pre_run_hook (EXEC-01..06): run the pre_run
// command as moatuser in /workspace before the main command, on every
// container start.
//
// Dispatch matches the exec branches exactly (EXEC-14): already non-root →
// run directly, with the working directory confined to the child (the
// script's subshell `( cd /workspace && sh -c ... )` — the entrypoint's own
// cwd never changes); root with moatuser → `gosu moatuser sh -c "cd
// /workspace && $MOAT_PRE_RUN"` (gosu is its own process, no subshell
// needed); root without moatuser → silently skipped (the final dispatch
// fails closed later anyway).
//
// A failing hook is reported with the framed diagnostic and the
// entrypoint exits with the hook's LITERAL exit code (issue #372) — without
// this, a hook failure looks like the container itself failed to start.
func preRunHookPhase(ctx *Context) error {
	cfg, sys := ctx.Cfg, ctx.Sys
	if cfg.PreRun == "" {
		return nil // EXEC-01: unset and empty are identical no-ops
	}

	var hookStatus int
	switch {
	case sys.Geteuid() != 0:
		rc, err := sys.Run(Cmd{
			Argv:   []string{"sh", "-c", cfg.PreRun},
			Dir:    sys.RealPath("/workspace"),
			Stdout: ctx.Stdout,
			Stderr: ctx.Stderr,
		})
		hookStatus = rc
		if err != nil {
			// The child could not start at all (e.g. /workspace missing —
			// the subshell's `cd` failure in the script): non-zero, framed.
			hookStatus = 1
		}
	case moatuserExists(sys):
		rc, err := sys.Run(Cmd{
			Argv:   []string{"gosu", "moatuser", "sh", "-c", "cd /workspace && " + cfg.PreRun},
			Stdout: ctx.Stdout,
			Stderr: ctx.Stderr,
		})
		hookStatus = rc
		if err != nil {
			hookStatus = 1
		}
	default:
		hookStatus = 0 // EXEC-04: root without moatuser — hook silently skipped
	}

	if hookStatus != 0 {
		fmt.Fprintln(ctx.Stderr, "")
		fmt.Fprintf(ctx.Stderr, "moat: pre_run hook failed (exit code %d)\n", hookStatus)
		fmt.Fprintf(ctx.Stderr, "moat:   command: %s\n", cfg.PreRun)
		fmt.Fprintln(ctx.Stderr, "moat:   the pre_run hook runs as moatuser in /workspace before your command.")
		fmt.Fprintln(ctx.Stderr, "moat:   fix the command above, or remove hooks.pre_run from moat.yaml.")
		return exitError{code: hookStatus}
	}
	return nil
}
