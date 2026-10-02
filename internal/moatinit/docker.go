package moatinit

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// dindTimeoutSeconds mirrors DIND_TIMEOUT_SECONDS: the readiness budget for
// dockerd in dind mode.
const dindTimeoutSeconds = 30

const dockerSocketPath = "/var/run/docker.sock"

// dockerSetupPhase mirrors the Docker access setup region: the mutual
// exclusion guard first (DOCKER-14 — before either mode body), then at most
// one of the dind or host-socket blocks.
func dockerSetupPhase(ctx *Context) error {
	cfg, sys := ctx.Cfg, ctx.Sys
	if dockerMutexViolated(cfg.DockerDIND, cfg.DockerGID) {
		fmt.Fprintln(ctx.Stderr, "Error: MOAT_DOCKER_DIND and MOAT_DOCKER_GID are mutually exclusive")
		fmt.Fprintln(ctx.Stderr, "Use MOAT_DOCKER_GID when mounting host's docker socket")
		fmt.Fprintln(ctx.Stderr, "Use MOAT_DOCKER_DIND when running Docker-in-Docker")
		return exitError{code: 1}
	}
	if dindActive(cfg.DockerDIND, sys.Geteuid()) {
		return dindSetup(ctx)
	}
	if hostGIDActive(cfg.DockerGID, sys.Geteuid(), isSocket(sys, dockerSocketPath)) {
		hostSocketSetup(ctx)
	}
	return nil
}

// dindSetup starts dockerd inside the container and waits for readiness
// (DOCKER-03..08). dockerd is a targeted long-lived child; readiness
// requires BOTH the socket existing AND `docker info` succeeding (the
// socket must exist for non-root users). Failure to start or to become
// ready within the budget is fatal.
func dindSetup(ctx *Context) error {
	sys := ctx.Sys
	fmt.Fprintln(ctx.Stderr, "Starting Docker daemon (dind mode)...")

	// Unguarded in the script: a mkdir failure here is fatal under set -e.
	if err := sys.MkdirAll("/var/run", 0o755); err != nil {
		return fatalPhaseError(ctx, "creating /var/run", err)
	}

	pid, err := sys.StartDetached(Cmd{
		Argv:    []string{"dockerd", "--storage-driver=vfs", "--log-level=warn"},
		LogFile: "/var/log/dockerd.log",
	})
	if err != nil {
		fmt.Fprintln(ctx.Stderr, "Error: Docker daemon failed to start")
		fmt.Fprintln(ctx.Stderr, "Check /var/log/dockerd.log for details:")
		tailDockerdLog(ctx)
		return exitError{code: 1}
	}

	fmt.Fprintln(ctx.Stderr, "Waiting for Docker daemon to be ready...")
	waited := 0
	for waited < dindTimeoutSeconds {
		// docker info gets a per-attempt timeout so a hang cannot consume
		// the whole 30s budget in one probe (plan Appendix B).
		if isSocket(sys, dockerSocketPath) {
			if rc, _ := sys.Run(Cmd{Argv: []string{"docker", "info"}, Timeout: 2 * time.Second}); rc == 0 {
				fmt.Fprintf(ctx.Stderr, "Docker daemon is ready (took %ds)\n", waited)
				break
			}
		}
		if !sys.ProcessAlive(pid) {
			fmt.Fprintln(ctx.Stderr, "Error: Docker daemon failed to start")
			fmt.Fprintln(ctx.Stderr, "Check /var/log/dockerd.log for details:")
			tailDockerdLog(ctx)
			return exitError{code: 1}
		}
		sys.Sleep(time.Second)
		waited++
	}
	if waited >= dindTimeoutSeconds {
		fmt.Fprintf(ctx.Stderr, "Error: Docker daemon did not become ready within %d seconds\n", dindTimeoutSeconds)
		socketState := "no"
		if isSocket(sys, dockerSocketPath) {
			socketState = "yes"
		}
		fmt.Fprintf(ctx.Stderr, "Socket exists: %s\n", socketState)
		fmt.Fprintln(ctx.Stderr, "Check /var/log/dockerd.log for details:")
		tailDockerdLog(ctx)
		return exitError{code: 1}
	}

	// Give moatuser dockerd access (best-effort throughout — DOCKER-08).
	if moatuserExists(sys) {
		if _, ok := sys.LookupGroupByName("docker"); !ok {
			_, _ = sys.Run(Cmd{Argv: []string{"groupadd", "docker"}})
		}
		_, _ = sys.Run(Cmd{Argv: []string{"usermod", "-aG", "docker", "moatuser"}})
	}
	return nil
}

// tailDockerdLog prints the last 20 lines of the dockerd log (the in-process
// port of `tail -20 /var/log/dockerd.log 2>/dev/null || true`).
func tailDockerdLog(ctx *Context) {
	data, err := ctx.Sys.ReadFile("/var/log/dockerd.log")
	if err != nil || len(data) == 0 {
		return // tail of a missing/empty log prints nothing
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	for _, l := range lines {
		fmt.Fprintln(ctx.Stderr, l)
	}
}

// hostSocketSetup mirrors the host-socket group block (DOCKER-10..13): the
// socket's GID is detected INSIDE the container (Docker Desktop on macOS
// translates ownership), a group is created at that GID when none exists,
// and moatuser joins the owning group. Warning-level only — never fatal.
func hostSocketSetup(ctx *Context) {
	sys := ctx.Sys
	info, err := sys.Stat(dockerSocketPath)
	var socketGID string
	if err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			socketGID = strconv.FormatUint(uint64(st.Gid), 10)
		}
	}
	if socketGID == "" {
		fmt.Fprintln(ctx.Stderr, "Warning: Failed to detect docker socket GID, docker access may not work")
		return
	}
	if _, ok := sys.LookupGroupByGID(socketGID); !ok {
		_, _ = sys.Run(Cmd{Argv: []string{"groupadd", "-g", socketGID, "moat-docker"}}) // best-effort
	}
	// Re-resolve: the group may pre-exist under any name, or have just been
	// created by the groupadd above.
	dockerGroup, _ := sys.LookupGroupByGID(socketGID)
	if dockerGroup != "" && moatuserExists(sys) {
		_, _ = sys.Run(Cmd{Argv: []string{"usermod", "-aG", dockerGroup, "moatuser"}}) // best-effort
	}
}

// dockerMutexViolated mirrors DOCKER-01: MOAT_DOCKER_DIND and MOAT_DOCKER_GID
// are mutually exclusive whenever BOTH are non-empty (any values — the guard
// tests emptiness, not "1").
func dockerMutexViolated(dind, gid string) bool {
	return dind != "" && gid != ""
}

// dindActive mirrors DOCKER-02: dind mode activates only when
// MOAT_DOCKER_DIND is exactly "1" AND the process is root. A non-root
// process with DIND=1 silently skips dind setup (no dockerd, no error).
func dindActive(dind string, euid int) bool {
	return dind == "1" && euid == 0
}

// hostGIDActive mirrors DOCKER-09: host-socket mode activates only when
// MOAT_DOCKER_GID is non-empty (any value) AND the process is root AND
// /var/run/docker.sock is a socket. Any missing condition silently skips
// host-mode setup.
func hostGIDActive(gid string, euid int, socketPresent bool) bool {
	return gid != "" && euid == 0 && socketPresent
}
