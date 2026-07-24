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
			warnIfForcedDockerHostIsPodman(rt)
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

// warnIfForcedDockerHostIsPodman warns (without failing) when
// MOAT_RUNTIME=docker was explicitly requested but DOCKER_HOST points at a
// podman engine. Unlike the podman case, which fails hard, "docker" also names
// the client implementation actually in use — moat's Docker-API runtime, which
// works unmodified against podman's compat API — so a mismatch only warns.
// Best-effort: if IsPodmanEngine errors, nothing is emitted.
func warnIfForcedDockerHostIsPodman(rt Runtime) {
	if os.Getenv("DOCKER_HOST") == "" {
		return
	}
	dockerRT, ok := rt.(*DockerRuntime)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	isPodman, err := dockerRT.IsPodmanEngine(ctx)
	if err != nil || !isPodman {
		return
	}
	ui.Warn("MOAT_RUNTIME=docker was requested but DOCKER_HOST points at a podman engine; proceeding with the Docker runtime over that socket. Use --runtime podman to make this explicit.")
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
	dockerRT, err := newDefaultDockerRuntime(sandbox)
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

// podmanRootfulSocket is the well-known path to podman's rootful Docker-API
// socket on Linux. A package variable so tests can redirect it: unlike the
// other candidates it can't be neutralized via HOME/XDG_RUNTIME_DIR/TMPDIR,
// so a test host running rootful podman would otherwise dial the real socket.
var podmanRootfulSocket = "/run/podman/podman.sock"

// newDefaultDockerRuntime constructs a Docker runtime for the default endpoint.
// A package variable so tests can pin it to a scratch socket and force the
// "default socket unreachable" precondition the podman-fallback tests need,
// even on a host with a live dockerd. Mirrors the podmanRootfulSocket seam.
var newDefaultDockerRuntime = NewDockerRuntime

// xdgRuntimeDirFallback is the runtime-dir base for podman's rootless socket
// when XDG_RUNTIME_DIR is unset, as in sudo/cron/CI — podman still uses
// systemd's /run/user/<uid> regardless of whether the variable is exported.
// A package variable so tests can override the uid seam.
var xdgRuntimeDirFallback = func() string {
	return fmt.Sprintf("/run/user/%d", os.Getuid())
}

// podmanSocketCandidates returns paths to podman's Docker-API-compatible
// socket:
//
//   - macOS (podman machine): $TMPDIR/podman/<machine-name>-api.sock
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
			candidates = append(candidates, dockerSocketCandidate{m, "Podman machine"})
		}
		return candidates
	case "linux":
		var candidates []dockerSocketCandidate
		xdg := os.Getenv("XDG_RUNTIME_DIR")
		if xdg == "" {
			xdg = xdgRuntimeDirFallback()
		}
		if xdg != "" {
			candidates = append(candidates, dockerSocketCandidate{filepath.Join(xdg, "podman", "podman.sock"), "Podman (rootless)"})
		}
		candidates = append(candidates, dockerSocketCandidate{podmanRootfulSocket, "Podman (rootful)"})
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

		host := "unix://" + c.path
		log.Debug("trying alternative Docker socket", "path", c.path, "tool", c.name)

		// Set DOCKER_HOST so NewDockerRuntime picks up the socket, then
		// ping to verify it's reachable before committing to it.
		os.Setenv("DOCKER_HOST", host)

		rt, err := NewDockerRuntime(sandbox)
		if err != nil {
			os.Unsetenv("DOCKER_HOST")
			lastErr = fmt.Errorf("%s (%s): %w", c.path, c.name, err)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		pingErr := rt.Ping(ctx)
		if pingErr != nil {
			cancel()
			os.Unsetenv("DOCKER_HOST")
			lastErr = fmt.Errorf("%s (%s): ping failed: %w", c.path, c.name, pingErr)
			continue
		}

		if verify != nil {
			ok, verr := verify(rt, ctx)
			cancel()
			if verr != nil {
				os.Unsetenv("DOCKER_HOST")
				lastErr = fmt.Errorf("%s (%s): identifying engine: %w", c.path, c.name, verr)
				continue
			}
			if !ok {
				os.Unsetenv("DOCKER_HOST")
				log.Debug("candidate socket is not the expected engine, skipping", "path", c.path, "tool", c.name)
				continue
			}
		} else {
			cancel()
		}

		// Socket is reachable (and, if verify was given, confirmed to be the
		// expected engine) — DOCKER_HOST is already set.
		log.Debug("auto-detected Docker via "+c.name, "socket", c.path)
		return rt, nil
	}

	return nil, lastErr
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
