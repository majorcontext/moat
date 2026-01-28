# gVisor Integration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make gVisor (runsc) the required sandbox for Docker containers, with explicit opt-out via `--no-sandbox`.

**Architecture:** Modify `DockerRuntime` to accept a sandbox boolean, detect gVisor availability via Docker API, and pass `Runtime: "runsc"` to container HostConfig. Add `--no-sandbox` flag to CLI.

**Tech Stack:** Go, Docker SDK (`github.com/docker/docker/client`), Cobra CLI

---

## Task 1: Add GVisorAvailable Detection Function

**Files:**
- Modify: `internal/container/detect.go`
- Create: `internal/container/detect_test.go`

**Step 1: Write the failing test**

Create `internal/container/detect_test.go`:

```go
package container

import (
	"context"
	"testing"
)

func TestGVisorAvailable(t *testing.T) {
	// This test verifies the function exists and returns a boolean.
	// Actual gVisor detection depends on Docker daemon configuration.
	ctx := context.Background()

	// Should not panic, should return bool
	_ = GVisorAvailable(ctx)
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/container/... -run TestGVisorAvailable`
Expected: FAIL with "undefined: GVisorAvailable"

**Step 3: Write minimal implementation**

Add to `internal/container/detect.go`:

```go
// GVisorAvailable checks if runsc is configured as a Docker runtime.
// Returns true if Docker reports "runsc" in its available runtimes.
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
```

Add import:
```go
import (
	// ... existing imports ...
	"github.com/docker/docker/client"
)
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/container/... -run TestGVisorAvailable`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/container/detect.go internal/container/detect_test.go
git commit -m "feat(container): add GVisorAvailable detection function"
```

---

## Task 2: Add ociRuntime Field to DockerRuntime

**Files:**
- Modify: `internal/container/docker.go`

**Step 1: Add field to struct**

Modify `DockerRuntime` struct in `internal/container/docker.go`:

```go
// DockerRuntime implements Runtime using Docker.
type DockerRuntime struct {
	cli        *client.Client
	ociRuntime string // "runsc" or "runc"
}
```

**Step 2: Update NewDockerRuntime to accept sandbox parameter**

Replace the `NewDockerRuntime` function:

```go
// NewDockerRuntime creates a new Docker runtime.
// If sandbox is true, requires gVisor (runsc) and fails if unavailable.
// If sandbox is false, uses standard runc runtime with a warning.
func NewDockerRuntime(sandbox bool) (*DockerRuntime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}

	ociRuntime := "runsc"
	if !sandbox {
		log.Warn("running without gVisor sandbox - reduced isolation")
		ociRuntime = "runc"
	} else {
		// Verify gVisor is available
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		info, err := cli.Info(ctx)
		if err != nil {
			cli.Close()
			return nil, fmt.Errorf("checking Docker info: %w", err)
		}

		found := false
		for name := range info.Runtimes {
			if name == "runsc" {
				found = true
				break
			}
		}
		if !found {
			cli.Close()
			return nil, fmt.Errorf("%w", ErrGVisorNotAvailable)
		}
	}

	return &DockerRuntime{cli: cli, ociRuntime: ociRuntime}, nil
}
```

**Step 3: Add error variable**

Add near the top of `internal/container/docker.go`:

```go
// ErrGVisorNotAvailable is returned when gVisor is required but not installed.
var ErrGVisorNotAvailable = errors.New(`gVisor (runsc) is required but not available

To install on Linux:
  curl -fsSL https://gvisor.dev/archive.key | sudo gpg --dearmor -o /usr/share/keyrings/gvisor.gpg
  echo "deb [signed-by=/usr/share/keyrings/gvisor.gpg] https://storage.googleapis.com/gvisor/releases release main" | \
    sudo tee /etc/apt/sources.list.d/gvisor.list
  sudo apt update && sudo apt install runsc
  sudo runsc install

To install on Docker Desktop (macOS/Windows):
  See https://gvisor.dev/docs/user_guide/install/

To bypass (reduced isolation):
  moat run --no-sandbox`)
```

Add import for `errors` if not present.

**Step 4: Verify it compiles**

Run: `go build ./internal/container/...`
Expected: Build succeeds

**Step 5: Commit**

```bash
git add internal/container/docker.go
git commit -m "feat(container): add sandbox parameter to NewDockerRuntime"
```

---

## Task 3: Pass OCI Runtime to Container Creation

**Files:**
- Modify: `internal/container/docker.go`

**Step 1: Update CreateContainer to use ociRuntime**

Find the `CreateContainer` method and add `Runtime` to the HostConfig. Locate this section:

```go
resp, err := r.cli.ContainerCreate(ctx,
	&container.Config{...},
	&container.HostConfig{
		Mounts:       mounts,
		NetworkMode:  networkMode,
		ExtraHosts:   cfg.ExtraHosts,
		PortBindings: portBindings,
		CapAdd:       cfg.CapAdd,
	},
```

Add the `Runtime` field:

```go
&container.HostConfig{
	Runtime:      r.ociRuntime, // "runsc" or "runc"
	Mounts:       mounts,
	NetworkMode:  networkMode,
	ExtraHosts:   cfg.ExtraHosts,
	PortBindings: portBindings,
	CapAdd:       cfg.CapAdd,
},
```

**Step 2: Verify it compiles**

Run: `go build ./internal/container/...`
Expected: Build succeeds

**Step 3: Commit**

```bash
git add internal/container/docker.go
git commit -m "feat(container): pass OCI runtime to container creation"
```

---

## Task 4: Update detect.go to Pass Sandbox Parameter

**Files:**
- Modify: `internal/container/detect.go`

**Step 1: Update NewRuntimeOptions struct**

Add a new options struct and update the detection logic. First, add the struct:

```go
// RuntimeOptions configures runtime creation.
type RuntimeOptions struct {
	// Sandbox enables gVisor sandboxing for Docker containers.
	// When true (default), requires gVisor and fails if unavailable.
	// When false, uses runc with reduced isolation.
	Sandbox bool
}

// DefaultRuntimeOptions returns the default runtime options.
func DefaultRuntimeOptions() RuntimeOptions {
	return RuntimeOptions{Sandbox: true}
}
```

**Step 2: Add NewRuntimeWithOptions function**

```go
// NewRuntimeWithOptions creates a new container runtime with the given options.
func NewRuntimeWithOptions(opts RuntimeOptions) (Runtime, error) {
	// Check for explicit runtime override
	if override := os.Getenv("MOAT_RUNTIME"); override != "" {
		switch strings.ToLower(override) {
		case "docker":
			log.Debug("using Docker runtime (MOAT_RUNTIME=docker)")
			return newDockerRuntimeWithPing(opts.Sandbox)
		case "apple":
			log.Debug("using Apple container runtime (MOAT_RUNTIME=apple)")
			rt, reason := tryAppleRuntime()
			if rt != nil {
				return rt, nil
			}
			return nil, fmt.Errorf("Apple container runtime not available: %s", reason)
		default:
			return nil, fmt.Errorf("unknown MOAT_RUNTIME value %q (use 'docker' or 'apple')", override)
		}
	}

	// On macOS with Apple Silicon, prefer Apple's container tool
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		if rt, reason := tryAppleRuntime(); rt != nil {
			return rt, nil
		} else if reason != "" {
			log.Debug(reason)
		}
	}

	// Fall back to Docker
	return newDockerRuntimeWithPing(opts.Sandbox)
}
```

**Step 3: Update newDockerRuntimeWithPing**

```go
// newDockerRuntimeWithPing creates a Docker runtime and verifies it's accessible.
func newDockerRuntimeWithPing(sandbox bool) (Runtime, error) {
	rt, err := NewDockerRuntime(sandbox)
	if err != nil {
		return nil, fmt.Errorf("no container runtime available: Docker error: %w", err)
	}

	// Verify Docker is accessible
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rt.Ping(ctx); err != nil {
		return nil, fmt.Errorf("no container runtime available: %w", err)
	}

	runtimeName := "Docker"
	if sandbox {
		runtimeName = "Docker+gVisor"
	}
	log.Debug("using " + runtimeName + " runtime")
	return rt, nil
}
```

**Step 4: Update NewRuntime to use defaults**

```go
// NewRuntime creates a new container runtime, auto-detecting the best available option.
// On macOS with Apple Silicon, it prefers Apple's container tool if available,
// falling back to Docker otherwise. Docker containers use gVisor by default.
//
// The MOAT_RUNTIME environment variable can override auto-detection:
//   - MOAT_RUNTIME=docker: force Docker runtime
//   - MOAT_RUNTIME=apple: force Apple container runtime
func NewRuntime() (Runtime, error) {
	return NewRuntimeWithOptions(DefaultRuntimeOptions())
}
```

**Step 5: Verify it compiles**

Run: `go build ./internal/container/...`
Expected: Build succeeds

**Step 6: Commit**

```bash
git add internal/container/detect.go
git commit -m "feat(container): add RuntimeOptions with sandbox support"
```

---

## Task 5: Add --no-sandbox Flag to CLI

**Files:**
- Modify: `cmd/moat/cli/exec.go`

**Step 1: Add NoSandbox field to ExecFlags**

Find `type ExecFlags struct` and add:

```go
type ExecFlags struct {
	Grants        []string
	Env           []string
	Name          string
	Rebuild       bool
	KeepContainer bool
	Detach        bool
	Interactive   bool
	NoSandbox     bool // New field
}
```

**Step 2: Add flag in AddExecFlags**

Find `func AddExecFlags` and add:

```go
func AddExecFlags(cmd *cobra.Command, flags *ExecFlags) {
	cmd.Flags().StringSliceVarP(&flags.Grants, "grant", "g", nil, "capabilities to grant (e.g., github, aws:s3.read)")
	cmd.Flags().StringArrayVarP(&flags.Env, "env", "e", nil, "environment variables (KEY=VALUE)")
	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "name for this run (default: from agent.yaml or random)")
	cmd.Flags().BoolVar(&flags.Rebuild, "rebuild", false, "force rebuild of container image")
	cmd.Flags().BoolVar(&flags.KeepContainer, "keep", false, "keep container after run completes (for debugging)")
	cmd.Flags().BoolVarP(&flags.Detach, "detach", "d", false, "run in background and return immediately")
	cmd.Flags().BoolVar(&flags.NoSandbox, "no-sandbox", false, "disable gVisor sandbox (reduced isolation, Docker only)")
}
```

**Step 3: Verify it compiles**

Run: `go build ./cmd/moat/...`
Expected: Build succeeds

**Step 4: Commit**

```bash
git add cmd/moat/cli/exec.go
git commit -m "feat(cli): add --no-sandbox flag"
```

---

## Task 6: Wire NoSandbox Through Run Manager

**Files:**
- Modify: `internal/run/manager.go`
- Modify: `cmd/moat/cli/exec.go`

**Step 1: Find run.NewManager and update**

First, read the manager to understand its structure:

Look for `NewManager` in `internal/run/manager.go` and add sandbox option. Add to `ManagerOptions` or `CreateOptions` struct (whichever exists for passing options to container creation).

If there's a `CreateOptions` struct, add:

```go
// NoSandbox disables gVisor sandbox for Docker containers.
NoSandbox bool
```

**Step 2: Update manager to pass sandbox to container runtime**

In the manager's runtime creation, use:

```go
rt, err := container.NewRuntimeWithOptions(container.RuntimeOptions{
	Sandbox: !opts.NoSandbox,
})
```

**Step 3: Update ExecuteRun in cmd/moat/cli/exec.go**

Pass the NoSandbox flag through to the manager. Find where `run.NewManager()` is called and update to pass options.

**Step 4: Verify it compiles**

Run: `go build ./...`
Expected: Build succeeds

**Step 5: Commit**

```bash
git add internal/run/manager.go cmd/moat/cli/exec.go
git commit -m "feat(run): wire NoSandbox through run manager"
```

---

## Task 7: Add sandbox Field to agent.yaml

**Files:**
- Modify: `internal/config/config.go`

**Step 1: Add Sandbox field to Config struct**

Find `type Config struct` and add:

```go
type Config struct {
	// ... existing fields ...

	// Sandbox configures container sandboxing.
	// "none" disables gVisor sandbox (Docker only).
	// Empty string or omitted uses default (gVisor enabled).
	Sandbox string `yaml:"sandbox,omitempty"`

	// ... rest of fields ...
}
```

**Step 2: Verify it compiles**

Run: `go build ./internal/config/...`
Expected: Build succeeds

**Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add sandbox field to agent.yaml"
```

---

## Task 8: Honor agent.yaml sandbox Setting in CLI

**Files:**
- Modify: `cmd/moat/cli/run.go`

**Step 1: Read sandbox from config**

In `runAgent` function, after loading config, check for sandbox setting:

```go
// Apply config defaults
if cfg != nil {
	// ... existing config handling ...

	// Check sandbox setting from config
	if cfg.Sandbox == "none" && !runFlags.NoSandbox {
		runFlags.NoSandbox = true
	}
}
```

**Step 2: Verify it compiles**

Run: `go build ./cmd/moat/...`
Expected: Build succeeds

**Step 3: Commit**

```bash
git add cmd/moat/cli/run.go
git commit -m "feat(cli): honor sandbox setting from agent.yaml"
```

---

## Task 9: Update Documentation

**Files:**
- Modify: `docs/content/reference/01-cli.md`
- Modify: `docs/content/reference/02-agent-yaml.md`

**Step 1: Document --no-sandbox flag in CLI reference**

Add to the `moat run` flags section:

```markdown
### --no-sandbox

Disables gVisor sandboxing for Docker containers. This reduces isolation but may be needed for compatibility with certain workloads that use unsupported syscalls.

**Warning:** Running without gVisor provides significantly less isolation. Use only when necessary.

```bash
moat run --no-sandbox
```
```

**Step 2: Document sandbox field in agent.yaml reference**

Add to the agent.yaml fields:

```markdown
### sandbox

Configures container sandboxing mode. Only affects Docker containers (Apple containers use macOS virtualization).

| Value | Description |
|-------|-------------|
| (empty/omitted) | Default: gVisor sandbox enabled |
| `none` | Disable gVisor sandbox (equivalent to `--no-sandbox`) |

```yaml
# Disable sandbox (not recommended)
sandbox: none
```

**Note:** Disabling the sandbox reduces isolation. Only use when the agent requires syscalls that gVisor doesn't support.
```

**Step 3: Commit**

```bash
git add docs/content/reference/01-cli.md docs/content/reference/02-agent-yaml.md
git commit -m "docs: document --no-sandbox flag and sandbox config"
```

---

## Task 10: Run Full Test Suite

**Step 1: Run all tests**

Run: `go test ./...`
Expected: All tests pass (except pre-existing keyring flake)

**Step 2: Build and verify CLI help**

Run: `go build -o moat ./cmd/moat && ./moat run --help`
Expected: Shows `--no-sandbox` flag in help output

**Step 3: Manual verification (if Docker+gVisor available)**

Run: `./moat run --no-sandbox -- echo "hello from runc"`
Expected: Runs successfully with warning about reduced isolation

Run: `./moat run -- echo "hello from gvisor"` (if runsc installed)
Expected: Runs with gVisor, or fails with install instructions if runsc not available

**Step 4: Commit any fixes**

If tests revealed issues, fix and commit.

---

## Summary

After completing all tasks:

1. **Detection**: `GVisorAvailable()` checks Docker for runsc runtime
2. **DockerRuntime**: Accepts `sandbox` bool, stores `ociRuntime` field
3. **Container creation**: Passes `Runtime: "runsc"` or `"runc"` to HostConfig
4. **CLI**: `--no-sandbox` flag available on all run commands
5. **Config**: `sandbox: none` in agent.yaml disables gVisor
6. **Docs**: CLI and agent.yaml reference updated

Files modified:
- `internal/container/detect.go` - RuntimeOptions, GVisorAvailable
- `internal/container/detect_test.go` - New test file
- `internal/container/docker.go` - ociRuntime field, sandbox parameter
- `internal/run/manager.go` - Pass sandbox to runtime creation
- `cmd/moat/cli/exec.go` - NoSandbox flag
- `cmd/moat/cli/run.go` - Honor config sandbox setting
- `internal/config/config.go` - Sandbox field
- `docs/content/reference/01-cli.md` - Document --no-sandbox
- `docs/content/reference/02-agent-yaml.md` - Document sandbox field
