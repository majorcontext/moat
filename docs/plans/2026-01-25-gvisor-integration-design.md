# gVisor Integration Design

**Date:** 2026-01-25
**Status:** Approved
**Goal:** Integrate gVisor as the default sandbox for Docker containers, providing stronger isolation via a userspace kernel.

---

## Overview

gVisor (runsc) intercepts syscalls and implements them in a userspace kernel (Sentry), providing defense-in-depth beyond standard Linux containers. This integration makes gVisor the **required** sandbox when using Docker runtime, with Apple containers continuing to use macOS virtualization.

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Runtime model | Docker `--runtime=runsc` | Least invasive; Docker natively supports OCI runtimes |
| Enablement | Required for Docker, opt-out with `--no-sandbox` | Secure by default |
| Network mode | netstack | Maximum isolation; all traffic goes through moat proxy anyway |
| Apple containers | Unchanged | Already isolated via macOS virtualization |
| Syscall tracing | Deferred | Focus on isolation first; design hooks for future observability |

## Behavior Matrix

| Platform | Default Runtime | Sandbox |
|----------|-----------------|---------|
| macOS (Apple Silicon) | Apple containers | macOS virtualization |
| macOS (Docker only) | Docker + runsc | gVisor |
| Linux | Docker + runsc | gVisor |
| Any + `--no-sandbox` | Docker + runc | None (warns) |

## Configuration

### agent.yaml

```yaml
# Optional - only needed to explicitly disable sandbox
sandbox: none
```

### CLI

```bash
moat run                   # Uses gVisor (Docker) or Apple containers (macOS)
moat run --no-sandbox      # Escape hatch: Docker + runc, warns about reduced isolation
moat run --runtime docker  # Force Docker runtime (requires gVisor)
```

## Implementation

### 1. Detection (`internal/container/detect.go`)

```go
// GVisorAvailable checks if runsc is configured as a Docker runtime.
func GVisorAvailable(ctx context.Context, cli *client.Client) bool {
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

### 2. Docker Runtime Changes (`internal/container/docker.go`)

```go
type DockerRuntime struct {
    client     *client.Client
    hostAddr   string
    ociRuntime string // "runsc" or "runc"

    // Future: syscall tracing config
    traceConfig *TraceConfig
}

func NewDockerRuntime(ctx context.Context, sandbox bool) (*DockerRuntime, error) {
    cli, err := client.NewClientWithOpts(client.FromEnv)
    if err != nil {
        return nil, err
    }

    ociRuntime := "runsc"
    if !sandbox {
        slog.Warn("running without gVisor sandbox - reduced isolation")
        ociRuntime = "runc"
    } else if !GVisorAvailable(ctx, cli) {
        return nil, fmt.Errorf("gVisor (runsc) required but not available\n\n%s", gvisorInstallInstructions)
    }

    return &DockerRuntime{
        client:     cli,
        ociRuntime: ociRuntime,
        // ...
    }, nil
}

func (d *DockerRuntime) CreateContainer(ctx context.Context, cfg Config) (string, error) {
    hostConfig := &container.HostConfig{
        Runtime: d.ociRuntime, // "runsc" or "runc"
        // ... existing config ...
    }
    // ...
}
```

### 3. Error Message

When gVisor is required but unavailable:

```
gVisor (runsc) is required but not installed.

To install on Linux:
  curl -fsSL https://gvisor.dev/archive.key | sudo gpg --dearmor -o /usr/share/keyrings/gvisor.gpg
  echo "deb [signed-by=/usr/share/keyrings/gvisor.gpg] https://storage.googleapis.com/gvisor/releases release main" | \
    sudo tee /etc/apt/sources.list.d/gvisor.list
  sudo apt update && sudo apt install runsc
  sudo runsc install

To install on Docker Desktop (macOS/Windows):
  See https://gvisor.dev/docs/user_guide/install/

To bypass (reduced isolation):
  moat run --no-sandbox
```

### 4. Future: Syscall Tracing Hooks

Design hooks for future syscall observability:

```go
type TraceConfig struct {
    Enabled    bool
    Categories []string // "file", "network", "process"
    Output     io.Writer
}
```

gVisor supports:
- `runsc trace` — attach to running container, stream syscall events
- `--strace` flag — log syscalls to stderr
- OCI hooks — inject tracing at container start

## File Changes

| File | Changes |
|------|---------|
| `internal/container/docker.go` | Add `ociRuntime` field, pass to `HostConfig.Runtime` |
| `internal/container/detect.go` | Add `GVisorAvailable()` function |
| `cmd/moat/cli/run.go` | Add `--no-sandbox` flag |
| `internal/config/agent.go` | Add `Sandbox` field to agent.yaml parsing |
| `docs/content/reference/01-cli.md` | Document `--no-sandbox` |
| `docs/content/concepts/sandboxing.md` | New page explaining gVisor vs Apple containers |

## Out of Scope

- Syscall tracing implementation (future work)
- Automatic runsc installation
- gVisor configuration tuning (memory limits, platform mode, etc.)
- Windows native support (relies on Docker Desktop's WSL2)

## Testing

1. **Unit tests**: Mock Docker client to verify runtime selection logic
2. **E2E tests**:
   - Verify container runs with `runsc` runtime
   - Verify `--no-sandbox` falls back to `runc`
   - Verify error when gVisor unavailable (without `--no-sandbox`)
3. **Manual testing**:
   - Docker Desktop on macOS with runsc installed
   - Linux with native runsc
   - Apple containers (unchanged behavior)

## References

- [gVisor Documentation](https://gvisor.dev/docs/)
- [gVisor Installation Guide](https://gvisor.dev/docs/user_guide/install/)
- [Using gVisor in Docker Desktop](https://dev.to/rimelek/using-gvisors-container-runtime-in-docker-desktop-374m)
