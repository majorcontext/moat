# Apple Container Backend Design

## Goal

Add support for Apple's container tool as an alternative to Docker on macOS, with automatic detection and informative fallback.

## Background

Apple's [container](https://github.com/apple/container) tool runs Linux containers as lightweight VMs on Apple Silicon Macs. It uses OCI-compatible images and provides a CLI similar to Docker.

**Requirements:**
- macOS 26+ (Tahoe) for full networking support
- Apple Silicon (arm64)
- `container` CLI installed

## Design Decisions

1. **Prefer Apple's container with Docker fallback** - On macOS + Apple Silicon, try Apple's container first, fall back to Docker if unavailable
2. **Informative fallback** - Log messages explaining why fallback occurred (e.g., "Apple container requires macOS 26+, using Docker")
3. **Runtime abstraction** - Create interface to support multiple container backends

## Architecture

### Package Structure

```
internal/
  container/           # NEW: Runtime abstraction
    runtime.go         # Interface definition
    docker.go          # Docker implementation
    apple.go           # Apple container CLI wrapper
    detect.go          # Auto-detection logic
```

### Runtime Interface

```go
type Runtime interface {
    // Type returns the runtime type (Docker, Apple)
    Type() RuntimeType

    // Ping verifies the runtime is accessible
    Ping(ctx context.Context) error

    // Container lifecycle
    CreateContainer(ctx context.Context, cfg ContainerConfig) (string, error)
    StartContainer(ctx context.Context, id string) error
    StopContainer(ctx context.Context, id string) error
    WaitContainer(ctx context.Context, id string) (int64, error)
    RemoveContainer(ctx context.Context, id string) error
    ContainerLogs(ctx context.Context, id string) (io.ReadCloser, error)

    // GetHostAddress returns the address containers use to reach the host
    GetHostAddress() string

    // Close releases resources
    Close() error
}
```

### Auto-Detection Logic

```go
func NewRuntime() (Runtime, error) {
    if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
        if appleContainerAvailable() {
            rt, err := NewAppleRuntime()
            if err == nil {
                log.Info("using Apple container runtime")
                return rt, nil
            }
            log.Info("Apple container not available, falling back to Docker", "reason", err)
        }
    }

    rt, err := NewDockerRuntime()
    if err != nil {
        return nil, fmt.Errorf("no container runtime available: %w", err)
    }
    log.Info("using Docker runtime")
    return rt, nil
}
```

### Apple Container CLI Mapping

| Moat Operation | Apple Container CLI |
|-------------------|---------------------|
| CreateContainer | `container run --detach --name <id> [opts] <image> [cmd]` |
| StartContainer | (handled by run --detach) |
| StopContainer | `container stop <id>` |
| WaitContainer | `container wait <id>` |
| RemoveContainer | `container rm <id>` |
| ContainerLogs | `container logs --follow <id>` |
| EnsureImage | `container image pull <image>` |

### Networking

**Docker:**
- Linux: Host network mode, proxy at `127.0.0.1:<port>`
- macOS/Windows: Bridge mode, proxy at `host.docker.internal:<port>`

**Apple Container:**
- Each container gets its own IP (e.g., `192.168.64.3/24`)
- Gateway is host machine (e.g., `192.168.64.1`)
- Proxy reachable at `<gateway>:<port>`

## Implementation Steps

1. Create `internal/container/runtime.go` - Interface and types
2. Create `internal/container/docker.go` - Move Docker implementation
3. Create `internal/container/apple.go` - Apple CLI wrapper
4. Create `internal/container/detect.go` - Auto-detection
5. Update `internal/run/manager.go` - Use Runtime interface
6. Remove `internal/docker/client.go` - Superseded
7. Add tests for Apple container command generation

## Testing

- Unit tests for CLI command generation (no actual container needed)
- Integration tests on macOS with Apple container installed
- Verify Docker fallback when Apple container unavailable
- E2E tests should pass with both backends

## References

- [Apple Container GitHub](https://github.com/apple/container)
- [Apple Container How-To](https://github.com/apple/container/blob/main/docs/how-to.md)
- [WWDC25 - Meet Containerization](https://developer.apple.com/videos/play/wwdc2025/346/)
