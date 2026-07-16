package moatinit

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
)

// execDispatchPhase is the terminal phase: hand the process over to the
// user's command (EXEC-11..13, X-PRIVILEGE-DROP).
//
// Go computes the branch; gosu performs the actual identity transition:
//
//   - already non-root (e.g. docker run --user): exec "$@" directly
//   - root with moatuser: exec gosu moatuser "$@" — gosu resolves the full
//     supplementary group set fresh from /etc/group at exec time, so groups
//     added earlier in this same run (dind/host-socket usermod) are picked
//     up; there is deliberately NO native setgroups/setgid/setuid here
//   - root without moatuser: fatal — running as root defeats the container
//     security model
//
// The handoff is a true exec (image replacement, PID preserved): the
// detached children (socat/Xvfb/dockerd) reparent exactly as under the
// shell's `exec`, and the exit code of the user command is the container's.
// Fork+wait would change parenting and signal behavior — prohibited.
//
// Both paths build the exec environment by explicitly removing
// MOAT_INIT_FILES from the inherited environment (INIT-10 defense in depth
// — the init-files phase already unset it, but the secret payload must be
// unable to reach the child even if that phase is ever reordered).
func execDispatchPhase(ctx *Context) error {
	sys := ctx.Sys
	env := scrubExecEnv(sys.Environ())

	switch {
	case sys.Geteuid() != 0:
		// Already non-root (e.g. --user was passed to docker run).
		return execFailure(ctx, ctx.Argv, sys.Exec(ctx.Argv, env))
	case moatuserExists(sys):
		// Running as root, moatuser exists - drop privileges.
		argv := append([]string{"gosu", "moatuser"}, ctx.Argv...)
		return execFailure(ctx, argv, sys.Exec(argv, env))
	default:
		// Running as root, no moatuser - fail with clear error.
		fmt.Fprintln(ctx.Stderr, "Error: Container started as root but moatuser does not exist.")
		fmt.Fprintln(ctx.Stderr, "This is a security issue - running as root defeats container isolation.")
		fmt.Fprintln(ctx.Stderr, "")
		fmt.Fprintln(ctx.Stderr, "If you're using a custom image, ensure it creates a 'moatuser' account:")
		fmt.Fprintln(ctx.Stderr, "  RUN useradd -m -u 5000 -s /bin/bash moatuser")
		fmt.Fprintln(ctx.Stderr, "")
		fmt.Fprintln(ctx.Stderr, "Or run the container with a non-root user:")
		fmt.Fprintln(ctx.Stderr, "  docker run --user 1000:1000 ...")
		return exitError{code: 1}
	}
}

// scrubExecEnv removes MOAT_INIT_FILES from an environment snapshot. The
// exec'd command must see every other variable the shell would have passed
// (parity: only MOAT_INIT_FILES is scrubbed — a broader MOAT_* scrub is a
// separable hardening follow-up, deliberately not part of the parity port).
func scrubExecEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if strings.HasPrefix(kv, "MOAT_INIT_FILES=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// errHandoffComplete marks a successful process handoff. Production
// syscall.Exec never returns on success, so this only occurs when a test
// fake records the exec and returns nil; Run maps it to exit code 0 instead
// of falling through to the fail-closed FATAL.
var errHandoffComplete = errors.New("moat-init: handoff complete")

// execFailure reports a failed exec. Success never returns, so reaching
// this with err != nil means the command could not be started at all; the
// codes mirror the shell's: 127 for not-found, 126 otherwise.
func execFailure(ctx *Context, argv []string, err error) error {
	if err == nil {
		return errHandoffComplete
	}
	fmt.Fprintf(ctx.Stderr, "moat-init: exec %s: %v\n", argv[0], err)
	code := 126
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		code = 127
	}
	return exitError{code: code}
}
