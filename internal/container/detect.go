package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/docker/docker/client"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/ui"
)

// RuntimeOptions configures runtime creation.
type RuntimeOptions struct {
	// Sandbox enables gVisor sandboxing for Docker containers.
	// When true (default), requires gVisor and fails if unavailable.
	// When false, uses runc with reduced isolation.
	Sandbox bool
}

// DefaultRuntimeOptions returns the default runtime options.
// On Linux, defaults to sandbox=true (requires gVisor).
// On macOS and Windows, defaults to sandbox=false (gVisor unavailable in Docker Desktop).
func DefaultRuntimeOptions() RuntimeOptions {
	// gVisor is only available on Linux
	// Docker Desktop on macOS and Windows does not support gVisor
	sandbox := runtime.GOOS == "linux"
	if os.Getenv("MOAT_NO_SANDBOX") == "1" {
		sandbox = false
	}
	return RuntimeOptions{Sandbox: sandbox}
}

// NewRuntimeWithOptions creates a new container runtime with the given options.
func NewRuntimeWithOptions(opts RuntimeOptions) (Runtime, error) {
	// Check for explicit runtime override
	if override := os.Getenv("MOAT_RUNTIME"); override != "" {
		switch strings.ToLower(override) {
		case "docker":
			log.Debug("using Docker runtime (MOAT_RUNTIME=docker)")
			rt, err := newDockerRuntimeWithPingCandidates(opts.Sandbox, genuineDockerSockets())
			if err != nil {
				hint := "Set MOAT_RUNTIME=apple, use --runtime apple, or remove 'runtime: docker' from moat.yaml to use auto-detection."
				return nil, fmt.Errorf("Docker runtime requested (via MOAT_RUNTIME or moat.yaml) but not available: %w\n\n%s", err, hint)
			}
			// A mismatch here (DOCKER_HOST pointing at a podman engine while
			// "docker" was explicitly requested) is not probed for eagerly:
			// that would cost every startup with DOCKER_HOST set — including
			// the common remote-Docker and Rancher Desktop paths — a blocking
			// ServerVersion call purely on the chance of a warning most of
			// them would never see. Identity is instead reported lazily via
			// (*DockerRuntime).EngineName wherever it's first determined
			// (e.g. at run-creation time), reusing IsPodmanEngine's cache.
			return rt, nil
		case "apple":
			log.Debug("using Apple container runtime (MOAT_RUNTIME=apple)")
			rt, reason := tryAppleRuntime()
			if rt != nil {
				return rt, nil
			}
			return nil, fmt.Errorf("Apple container runtime not available: %s\n\nTo start the container system manually:\n  container system start", reason)
		case "podman":
			log.Debug("using Docker runtime over podman socket (MOAT_RUNTIME=podman)")
			rt, err := newPodmanRuntimeWithPing(opts.Sandbox)
			if err != nil {
				return nil, err
			}
			return rt, nil
		default:
			return nil, fmt.Errorf("unknown MOAT_RUNTIME value %q (use 'docker', 'apple', or 'podman')", override)
		}
	}

	// On macOS with Apple Silicon, prefer Apple's container tool
	var appleReason string
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		var rt Runtime
		rt, appleReason = tryAppleRuntime()
		if rt != nil {
			return rt, nil
		}
		if appleReason != "" {
			log.Debug(appleReason)
		}
	}

	// Fall back to Docker
	rt, err := newDockerRuntimeWithPing(opts.Sandbox)
	if err != nil {
		if appleReason != "" {
			return nil, fmt.Errorf("no container runtime available:\n  Apple containers: %s\n  Docker: %w\n\nTo start Apple containers manually:\n  container system start\n\nTo force a specific runtime:\n  moat run --runtime apple\n  moat run --runtime docker\n  moat run --runtime podman", appleReason, err)
		}
		return nil, fmt.Errorf("no container runtime available: %w", err)
	}
	return rt, nil
}

// NewRuntime creates a new container runtime, auto-detecting the best available option.
// On macOS with Apple Silicon, it prefers Apple's container tool if available,
// falling back to Docker otherwise. Docker containers use gVisor by default.
//
// The MOAT_RUNTIME environment variable can override auto-detection:
//   - MOAT_RUNTIME=docker: force Docker runtime
//   - MOAT_RUNTIME=apple: force Apple container runtime
//   - MOAT_RUNTIME=podman: force the Docker runtime against a podman socket
func NewRuntime() (Runtime, error) {
	return NewRuntimeWithOptions(DefaultRuntimeOptions())
}

// newDockerRuntimeWithPing creates a Docker runtime and verifies it's accessible.
// If the default Docker socket is unreachable and DOCKER_HOST is not set, it
// probes known alternative socket locations, including podman's (see
// alternativeDockerSockets). As a side effect, if an alternative socket is
// found, DOCKER_HOST is set permanently in the process environment to point
// to it.
//
// The probe includes podman candidates, so use it only where landing on a
// podman socket is acceptable. An explicit MOAT_RUNTIME=docker request must
// instead pass genuineDockerSockets to newDockerRuntimeWithPingCandidates.
func newDockerRuntimeWithPing(sandbox bool) (Runtime, error) {
	return newDockerRuntimeWithPingCandidates(sandbox, alternativeDockerSockets())
}

// newDockerRuntimeWithPingCandidates creates a Docker runtime and verifies
// it's accessible, falling back (when DOCKER_HOST is not explicitly set) to
// probing the given socket candidates.
func newDockerRuntimeWithPingCandidates(sandbox bool, fallbackCandidates []dockerSocketCandidate) (Runtime, error) {
	var rt Runtime
	dockerRT, err := detectEnv.newDockerRuntime(sandbox)
	if err != nil {
		return nil, fmt.Errorf("Docker runtime error: %w", err)
	}
	rt = dockerRT

	// Verify Docker is accessible
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rt.Ping(ctx); err != nil {
		// If DOCKER_HOST is not explicitly set, try known alternative socket
		// paths from tools like Rancher Desktop.
		if os.Getenv("DOCKER_HOST") != "" {
			return nil, err
		}
		altRT, _ := tryDockerSocketCandidates(fallbackCandidates, sandbox)
		if altRT == nil {
			return nil, err
		}
		rt = altRT
	}

	runtimeName := "Docker"
	if sandbox {
		runtimeName = "Docker+gVisor"
	} else if runtime.GOOS != "linux" {
		// On macOS/Windows, gVisor is unavailable in Docker Desktop
		log.Debug("using Docker runtime (gVisor unavailable on " + runtime.GOOS + ")")
		return rt, nil
	}
	log.Debug("using " + runtimeName + " runtime")
	return rt, nil
}

// newPodmanRuntimeWithPing creates a Docker runtime targeting a podman socket;
// podman's compat API works with it unmodified, so there is no separate podman
// Runtime implementation. A preset DOCKER_HOST is used as-is but verified to
// actually be podman, so MOAT_RUNTIME=podman can't silently succeed against a
// real Docker daemon. Otherwise podmanSocketCandidates are probed and
// DOCKER_HOST is set to the first that answers.
func newPodmanRuntimeWithPing(sandbox bool) (Runtime, error) {
	hint := "To start podman:\n  macOS:  podman machine start\n  Linux:  systemctl --user enable --now podman.socket"

	if os.Getenv("DOCKER_HOST") != "" {
		dockerRT, err := NewDockerRuntime(sandbox)
		if err != nil {
			return nil, fmt.Errorf("podman runtime error: %w", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if pingErr := dockerRT.Ping(ctx); pingErr != nil {
			return nil, fmt.Errorf("podman runtime requested (via MOAT_RUNTIME or moat.yaml) but DOCKER_HOST is unreachable: %w\n\n%s", pingErr, hint)
		}
		isPodman, err := dockerRT.IsPodmanEngine(ctx)
		if err != nil {
			return nil, fmt.Errorf("podman runtime requested (via MOAT_RUNTIME or moat.yaml) but could not identify the engine behind DOCKER_HOST=%s: %w", os.Getenv("DOCKER_HOST"), err)
		}
		if !isPodman {
			return nil, fmt.Errorf("podman runtime requested (via MOAT_RUNTIME or moat.yaml) but DOCKER_HOST=%s points at a non-podman engine", os.Getenv("DOCKER_HOST"))
		}
		return dockerRT, nil
	}

	// Verify each candidate is actually podman's compat API, not some other
	// engine answering on a podman-looking path.
	verifyPodman := func(dockerRT *DockerRuntime, ctx context.Context) (bool, error) {
		return dockerRT.IsPodmanEngine(ctx)
	}
	rt, probeErr := tryDockerSocketCandidatesVerified(podmanSocketCandidates(), sandbox, verifyPodman)
	if rt == nil {
		if probeErr != nil {
			return nil, fmt.Errorf("podman runtime requested (via MOAT_RUNTIME or moat.yaml): a podman socket was found but is unusable: %w\n\n%s", probeErr, hint)
		}
		return nil, fmt.Errorf("podman runtime requested (via MOAT_RUNTIME or moat.yaml) but no podman socket was found\n\n%s", hint)
	}
	return rt, nil
}

// dockerSocketCandidate represents a known Docker-compatible socket from a
// third-party container tool.
type dockerSocketCandidate struct {
	path string
	name string
}

// alternativeDockerSockets returns paths to Docker-compatible sockets from
// third-party tools. Checked when the default Docker socket is unreachable
// and DOCKER_HOST is not set. The first reachable socket wins.
//
// Entries are platform-specific: macOS-only paths are guarded by
// runtime.GOOS so they are not probed unnecessarily on Linux.
func alternativeDockerSockets() []dockerSocketCandidate {
	return append(genuineDockerSockets(), podmanSocketCandidates()...)
}

// genuineDockerSockets returns sockets backed by a real Docker engine, as
// opposed to podman's compat API. An explicit MOAT_RUNTIME=docker falls back
// only to these, so it can't silently land on a podman socket.
func genuineDockerSockets() []dockerSocketCandidate {
	var candidates []dockerSocketCandidate
	if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, dockerSocketCandidate{filepath.Join(home, ".rd", "docker.sock"), "Rancher Desktop"})
		}
	}
	return candidates
}

// detectEnviron holds the filesystem/constructor seams that tests need to
// redirect in order to exercise fallback and auto-detection paths
// hermetically (without touching the real HOME, XDG_RUNTIME_DIR, or an
// actual dockerd). Consolidated into a single struct — rather than one
// package variable per seam — so tests have exactly one thing to save and
// restore (see SwapDetectEnv in export_test.go) instead of several
// independent globals that could be left half-restored between tests. This
// also keeps the shipped binary from carrying more test-only mutable state
// than necessary: production code reads through detectEnv, but there's one
// declaration site for what the seams are, not five scattered across the
// file.
//
// Tests that call SwapDetectEnv must not use t.Parallel(): detectEnv is
// package-level mutable state, and a parallel test could observe another
// test's swapped values.
type detectEnviron struct {
	// rootfulSocket is the well-known path to podman's rootful Docker-API
	// socket on Linux. Redirected in tests: unlike the other candidates it
	// can't be neutralized via HOME/XDG_RUNTIME_DIR/TMPDIR, so a test host
	// running rootful podman would otherwise dial the real socket.
	rootfulSocket string

	// xdgRuntimeDir returns the runtime-dir base for podman's rootless socket
	// when XDG_RUNTIME_DIR is unset, as in sudo/cron/CI — podman still uses
	// systemd's /run/user/<uid> regardless of whether the variable is
	// exported. Redirected in tests to avoid depending on the invoking uid.
	xdgRuntimeDir func() string

	// connectionsPath locates podman's connections file
	// ($XDG_CONFIG_HOME/containers/podman-connections.json by default, see
	// podmanDefaultConnection in podman_machine.go). Redirected in tests so
	// they don't touch the caller's real HOME.
	connectionsPath func() string

	// newDockerRuntime constructs a Docker runtime for the default endpoint.
	// Redirected in tests to pin the "default socket unreachable"
	// precondition the podman-fallback tests need, even on a host with a
	// live dockerd.
	newDockerRuntime func(sandbox bool) (*DockerRuntime, error)
}

// defaultDetectEnviron returns detectEnviron's production values — the real
// filesystem paths and constructors, as opposed to whatever a test has
// substituted via SwapDetectEnv.
func defaultDetectEnviron() detectEnviron {
	return detectEnviron{
		rootfulSocket: "/run/podman/podman.sock",
		xdgRuntimeDir: func() string {
			return fmt.Sprintf("/run/user/%d", os.Getuid())
		},
		connectionsPath: func() string {
			base := os.Getenv("XDG_CONFIG_HOME")
			if base == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return ""
				}
				base = filepath.Join(home, ".config")
			}
			return filepath.Join(base, "containers", "podman-connections.json")
		},
		newDockerRuntime: NewDockerRuntime,
	}
}

// detectEnv is the package's single instance of the seams above. Production
// code reads fields off this variable rather than holding independent
// package-level vars; tests redirect it via SwapDetectEnv (export_test.go).
var detectEnv = defaultDetectEnviron()

// podmanSocketCandidates returns paths to podman's Docker-API-compatible
// socket:
//
//   - macOS (podman machine): $TMPDIR/podman/<machine-name>-api.sock, with
//     podman's own default connection probed first
//   - Linux rootless: $XDG_RUNTIME_DIR/podman/podman.sock, falling back to
//     /run/user/<uid>/podman/podman.sock when XDG_RUNTIME_DIR is unset
//   - Linux rootful: /run/podman/podman.sock
func podmanSocketCandidates() []dockerSocketCandidate {
	switch runtime.GOOS {
	case "darwin":
		matches, err := filepath.Glob(filepath.Join(os.TempDir(), "podman", "*-api.sock"))
		if err != nil {
			return nil
		}
		var candidates []dockerSocketCandidate
		for _, m := range matches {
			desc := "Podman machine"
			if name := podmanMachineName(m); name != "" {
				desc = "Podman machine " + name
			}
			candidates = append(candidates, dockerSocketCandidate{m, desc})
		}
		// Glob is sorted, so with several machines running the alphabetically
		// first would win regardless of which one podman itself targets.
		return preferDefaultPodmanMachine(candidates)
	case "linux":
		var candidates []dockerSocketCandidate
		xdg := os.Getenv("XDG_RUNTIME_DIR")
		if xdg == "" {
			xdg = detectEnv.xdgRuntimeDir()
		}
		if xdg != "" {
			candidates = append(candidates, dockerSocketCandidate{filepath.Join(xdg, "podman", "podman.sock"), "Podman (rootless)"})
		}
		candidates = append(candidates, dockerSocketCandidate{detectEnv.rootfulSocket, "Podman (rootful)"})
		return candidates
	default:
		return nil
	}
}

// tryAlternativeDockerSockets checks known Docker-compatible socket paths from
// third-party container tools when the default Docker socket is unreachable.
//
// If a working socket is found, DOCKER_HOST is set so all subsequent Docker
// client creation uses the discovered socket. Any candidate-probe error is
// discarded — callers that need it should use tryDockerSocketCandidates
// directly (see newPodmanRuntimeWithPing).
func tryAlternativeDockerSockets(sandbox bool) Runtime {
	rt, _ := tryDockerSocketCandidates(alternativeDockerSockets(), sandbox)
	return rt
}

// tryDockerSocketCandidates checks a list of known Docker-compatible socket
// paths, returning the Runtime for the first one that stats as a socket and
// answers a ping. If a working socket is found, DOCKER_HOST is set so all
// subsequent Docker client creation uses the discovered socket.
//
// If none succeeds, the returned error is the last failure among candidates
// that did stat as a socket, and nil if no candidate path existed at all — so
// callers can tell "nothing there" from "something there but broken".
func tryDockerSocketCandidates(candidates []dockerSocketCandidate, sandbox bool) (Runtime, error) {
	return tryDockerSocketCandidatesVerified(candidates, sandbox, nil)
}

// tryDockerSocketCandidatesVerified is tryDockerSocketCandidates with an
// optional identity check, called after a successful ping. A false result
// skips the candidate; an error is treated as a failed ping. The podman probe
// uses it because a matching socket path alone is no proof of engine identity.
func tryDockerSocketCandidatesVerified(candidates []dockerSocketCandidate, sandbox bool, verify func(*DockerRuntime, context.Context) (bool, error)) (Runtime, error) {
	var lastErr error
	for _, c := range candidates {
		// Use os.Stat (not Lstat) to follow symlinks — on macOS,
		// ~/.rd/docker.sock is a symlink to the actual socket.
		info, err := os.Stat(c.path)
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}

		rt, err := tryDockerSocketCandidate(c, sandbox, verify)
		if err != nil {
			lastErr = err
			continue
		}
		if rt == nil {
			// Candidate answered but failed the identity check; already
			// logged by tryDockerSocketCandidate.
			continue
		}

		// Socket is reachable (and, if verify was given, confirmed to be the
		// expected engine) — DOCKER_HOST is already set.
		log.Debug("auto-detected Docker via "+c.name, "socket", c.path)
		return rt, nil
	}

	return nil, lastErr
}

// tryDockerSocketCandidate dials a single socket candidate: sets DOCKER_HOST,
// constructs a client, pings, and (if verify is given) checks engine
// identity. Split out of tryDockerSocketCandidatesVerified's loop body so
// each candidate's ping context can be released via a single deferred
// cancel() instead of three separate manual cancel() calls scattered across
// the failure exits — one call away from a leak on the old shape.
//
// Returns (rt, nil) on success; (nil, nil) if the candidate answered the ping
// but verify rejected it (not an error worth reporting — already logged); or
// (nil, err) if stat/construction/ping/verify failed outright.
func tryDockerSocketCandidate(c dockerSocketCandidate, sandbox bool, verify func(*DockerRuntime, context.Context) (bool, error)) (*DockerRuntime, error) {
	host := "unix://" + c.path
	log.Debug("trying alternative Docker socket", "path", c.path, "tool", c.name)

	// Set DOCKER_HOST so NewDockerRuntime picks up the socket, then ping to
	// verify it's reachable before committing to it. Left set only if this
	// candidate is ultimately accepted.
	os.Setenv("DOCKER_HOST", host)
	accepted := false
	defer func() {
		if !accepted {
			os.Unsetenv("DOCKER_HOST")
		}
	}()

	rt, err := NewDockerRuntime(sandbox)
	if err != nil {
		return nil, fmt.Errorf("%s (%s): %w", c.path, c.name, err)
	}

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if pingErr := rt.Ping(pingCtx); pingErr != nil {
		rt.Close()
		return nil, fmt.Errorf("%s (%s): ping failed: %w", c.path, c.name, pingErr)
	}

	if verify != nil {
		// A fresh timeout rather than reusing pingCtx: pingCtx may already be
		// mostly spent by a slow-but-successful ping, and IsPodmanEngine (the
		// verify implementation in practice) layers its own 5s on top of
		// whatever it's handed — so a slow ping must not be allowed to starve
		// identification of a genuinely-alive engine.
		verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer verifyCancel()
		ok, verr := verify(rt, verifyCtx)
		if verr != nil {
			rt.Close()
			return nil, fmt.Errorf("%s (%s): identifying engine: %w", c.path, c.name, verr)
		}
		if !ok {
			log.Debug("candidate socket is not the expected engine, skipping", "path", c.path, "tool", c.name)
			rt.Close()
			return nil, nil
		}
	}

	accepted = true
	return rt, nil
}

// tryAppleRuntime attempts to create and verify an Apple runtime.
// Returns (runtime, "") on success, or (nil, reason) on failure.
func tryAppleRuntime() (Runtime, string) {
	if !appleContainerAvailable() {
		return nil, "'container' CLI not found in PATH (requires macOS 26+ with containerization framework)"
	}

	rt, err := NewAppleRuntime()
	if err != nil {
		return nil, fmt.Sprintf("failed to initialize: %v", err)
	}

	// Verify the system is running
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if pingErr := rt.Ping(ctx); pingErr != nil {
		// Try to auto-start the Apple container system.
		// startAppleContainerSystem polls for readiness internally, so if it
		// returns nil, the system is verified as up and we don't need to ping again.
		log.Debug("Apple container system not running, attempting to start...", "error", pingErr)
		if startErr := startAppleContainerSystem(); startErr != nil {
			return nil, fmt.Sprintf("system not running and failed to auto-start: %v", startErr)
		}
	}

	log.Debug("using Apple container runtime")
	return rt, ""
}

// startAppleContainerSystem starts the Apple container system using 'container system start'.
func startAppleContainerSystem() error {
	ui.Info("Starting Apple container system...")

	// Use a single timeout for the entire operation (start + readiness check)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "container", "system", "start")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("'container system start' failed: %w\n%s", err, msg)
		}
		return fmt.Errorf("'container system start' failed: %w", err)
	}

	// Wait for the system to be fully ready, respecting the parent context timeout
	ui.Info("Waiting for Apple container system to be ready...")
	const maxAttempts = 30
	for i := 0; i < maxAttempts; i++ {
		// Check if parent context is done
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for system to be ready: %w", ctx.Err())
		default:
		}

		checkCtx, checkCancel := context.WithTimeout(ctx, 2*time.Second)
		checkCmd := exec.CommandContext(checkCtx, "container", "list", "--quiet")
		var checkStderr strings.Builder
		checkCmd.Stderr = &checkStderr
		checkErr := checkCmd.Run()
		checkCancel()
		if checkErr == nil {
			return nil
		}
		log.Debug("readiness check failed", "attempt", i+1, "error", checkErr, "stderr", strings.TrimSpace(checkStderr.String()))

		// Log progress every 10 seconds
		if (i+1)%10 == 0 {
			ui.Infof("Still waiting for Apple container system... (%d/%d attempts)", i+1, maxAttempts)
		}

		// Sleep with context awareness
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for system to be ready: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}

	return fmt.Errorf("system started but did not become ready within the allotted timeout")
}

// NewRuntimeByType creates a runtime of the specified type.
// Returns an error if the runtime is not available on this system.
func NewRuntimeByType(rt RuntimeType, opts RuntimeOptions) (Runtime, error) {
	switch rt {
	case RuntimeDocker:
		return newDockerRuntimeWithPing(opts.Sandbox)
	case RuntimeApple:
		r, reason := tryAppleRuntime()
		if r != nil {
			return r, nil
		}
		return nil, fmt.Errorf("Apple container runtime not available: %s", reason)
	default:
		return nil, fmt.Errorf("unknown runtime type: %q", rt)
	}
}

// appleContainerAvailable checks if Apple's container CLI is installed.
// Requires macOS 26+ with the containerization framework.
func appleContainerAvailable() bool {
	_, err := exec.LookPath("container")
	return err == nil
}

// IsAppleSilicon returns true if running on Apple Silicon.
func IsAppleSilicon() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}

// GVisorAvailable checks if runsc is configured as a Docker runtime.
// Returns true if Docker reports "runsc" in its available runtimes.
//
// Deprecated: This function creates a new Docker client on each call, which is
// inefficient. Use DockerRuntime.gvisorAvailable() instead, which caches the
// result after the first check. This function is kept for backward compatibility
// with existing tests.
func GVisorAvailable(ctx context.Context) bool {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return false
	}
	defer cli.Close()

	info, err := cli.Info(ctx)
	if err != nil {
		return false
	}

	for name := range info.Runtimes {
		if name == "runsc" {
			return true
		}
	}
	return false
}
