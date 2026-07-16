package moatinit

import (
	"fmt"
	"time"
)

const (
	// sshSocketWaitIters mirrors SSH_SOCKET_WAIT_ITERS: iterations * 0.1s =
	// 2 second timeout for the socket to appear.
	sshSocketWaitIters = 20

	sshSocketDir  = "/run/moat/ssh"
	sshSocketPath = "/run/moat/ssh/agent.sock"
)

// sshAgentBridgePhase mirrors the SSH agent bridge block (SSH region): when
// MOAT_SSH_TCP_ADDR is set, bridge a Unix socket to the TCP-based SSH agent
// proxy on the host (needed on Docker-for-macOS, where Unix sockets can't
// cross bind mounts). socat stays the bridge — a targeted long-lived child
// that must outlive the entrypoint's exec — Go owns only the surrounding
// decisions: directory/mode/ownership, the wait loop, and the exact
// warnings.
//
// The entire region is non-fatal end-to-end (SSH-12): every failure either
// skips silently or warns, and the entrypoint proceeds to the user command.
func sshAgentBridgePhase(ctx *Context) error {
	cfg, sys := ctx.Cfg, ctx.Sys
	if cfg.SSHTCPAddr == "" {
		return nil // SSH-01: unset and empty are identical no-ops
	}

	// Create the socket directory — may need root for /run; best-effort
	// (SSH-02), and everything below is nested under its existence (SSH-03).
	_ = sys.MkdirAll(sshSocketDir, 0o755)
	if !isDir(sys, sshSocketDir) {
		return nil
	}
	// Permissions so moatuser (a different UID) can traverse; best-effort.
	_ = sys.Chmod(sshSocketDir, 0o755)
	// Chown whenever moatuser exists — note there is deliberately no root
	// check here (parity: as non-root the chown just fails silently).
	if u, ok := sys.LookupUser("moatuser"); ok {
		_ = sys.Chown(sshSocketDir, u.UID, u.GID)
	}

	// Start socat bridging a forking Unix listener (socket mode 0660 —
	// owner and group only) to the host TCP address.
	pid, err := sys.StartDetached(Cmd{
		Argv:   []string{"socat", "UNIX-LISTEN:" + sys.RealPath(sshSocketPath) + ",fork,mode=0660", "TCP:" + cfg.SSHTCPAddr},
		Stderr: ctx.Stderr,
	})
	if err != nil {
		// socat missing or unspawnable — the shell's `&` can't fail this
		// way, but its kill -0 check lands on the same warning.
		fmt.Fprintln(ctx.Stderr, "Warning: SSH agent bridge (socat) failed to start")
		return nil
	}

	// Wait for the socket to appear (SSH-07: break as soon as it exists).
	for i := 0; i < sshSocketWaitIters; i++ {
		if isSocket(sys, sshSocketPath) {
			break
		}
		sys.Sleep(100 * time.Millisecond)
	}

	switch {
	case !sys.ProcessAlive(pid):
		fmt.Fprintln(ctx.Stderr, "Warning: SSH agent bridge (socat) failed to start")
	case !isSocket(sys, sshSocketPath):
		fmt.Fprintln(ctx.Stderr, "Warning: SSH agent socket was not created after 2s")
	default:
		if u, ok := sys.LookupUser("moatuser"); ok {
			_ = sys.Chown(sshSocketPath, u.UID, u.GID) // best-effort
		}
	}
	return nil
}
