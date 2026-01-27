# Docker Dependency Design

**Date:** 2026-01-26
**Status:** Approved

## Overview

Enable processes inside moat containers to use Docker by adding a `docker` dependency that automatically handles socket mounting and permissions.

## Use Cases

- Running integration tests that use Docker (e.g., moat's own e2e tests)
- Running disposable services like Postgres via testcontainers
- Any workflow where agents need `docker run` or `docker build`

## User Interface

Single-line declaration in agent.yaml:

```yaml
dependencies:
  - docker
```

Moat handles:
1. Installing the docker CLI in the container
2. Mounting the host's Docker socket
3. Setting up group permissions so the container user can access the socket

## Approach: Socket Mounting

We chose socket mounting (`/var/run/docker.sock`) over Docker-in-Docker because:

- Simpler to implement and debug
- Better performance (no nested daemon)
- Matches industry practice (GitHub Actions, GitLab CI)
- Moat already runs with significant trust (credential injection, workspace access)

## Apple Containers: Not Supported

The `docker` dependency only works with Docker runtime. Apple containers cannot access the host Docker socket because:

- Unix sockets don't work across the hypervisor boundary on macOS
- Nested virtualization requires M3+ chips and is undocumented for this use case

When used with Apple containers, moat returns a clear error:

```
Error: 'docker' dependency requires Docker runtime.

Apple containers cannot access the host Docker socket.
Either:
  - Remove 'docker' from dependencies, or
  - Use Docker runtime: moat run --runtime docker
```

## Implementation Details

### Dependency Type

New constant in `internal/deps/types.go`:

```go
const TypeDocker = "docker"
```

### Dockerfile Generation

Add to generated Dockerfile (`internal/deps/dockerfile.go`):

```dockerfile
RUN apt-get update && apt-get install -y docker.io && rm -rf /var/lib/apt/lists/*
```

### Container Configuration

Extend `container.Config` (`internal/container/runtime.go`):

```go
type Config struct {
    // ... existing fields ...
    GroupAdd []string  // Supplementary group IDs
}
```

### Socket Mounting and GID Detection

In `internal/run/manager.go`, when docker dependency is present:

1. Mount the socket:
```go
mounts = append(mounts, container.MountConfig{
    Source:   "/var/run/docker.sock",
    Target:   "/var/run/docker.sock",
    ReadOnly: false,
})
```

2. Detect socket GID and add to container:
```go
info, _ := os.Stat("/var/run/docker.sock")
if stat, ok := info.Sys().(*syscall.Stat_t); ok {
    groupAdd = append(groupAdd, fmt.Sprintf("%d", stat.Gid))
}
```

### Docker Runtime Implementation

Pass GroupAdd to Docker API (`internal/container/docker.go`):

```go
HostConfig: &container.HostConfig{
    GroupAdd: cfg.GroupAdd,
    // ... existing fields ...
}
```

### Runtime Validation

Early validation in `internal/run/manager.go`:

```go
if hasDockerDependency(opts.Config.Dependencies) {
    if m.runtime.Type() == container.RuntimeApple {
        return nil, fmt.Errorf(
            "'docker' dependency requires Docker runtime\n\n" +
            "Apple containers cannot access the host Docker socket.\n" +
            "Either:\n" +
            "  - Remove 'docker' from dependencies, or\n" +
            "  - Use Docker runtime: moat run --runtime docker")
    }
}
```

## Code Organization

Keep docker-specific logic isolated. Consider a helper file or package that encapsulates:
- Socket path detection
- GID detection
- Mount configuration
- Validation logic

This keeps docker-in-docker concerns separate from the main run manager.

## Files to Modify

| File | Change |
|------|--------|
| `internal/deps/types.go` | Add `TypeDocker` constant |
| `internal/deps/parse.go` | Recognize `docker` as valid dependency |
| `internal/deps/dockerfile.go` | Add docker CLI installation |
| `internal/container/runtime.go` | Add `GroupAdd []string` to Config |
| `internal/container/docker.go` | Pass GroupAdd to Docker API |
| `internal/run/manager.go` | Validate runtime, mount socket, detect GID |
| `internal/deps/dockerfile_test.go` | Test docker CLI in Dockerfile output |
| `internal/run/manager_test.go` | Test Apple container validation error |
| `internal/e2e/docker_test.go` | New e2e test for docker dependency |
| `docs/content/reference/02-agent-yaml.md` | Document `docker` dependency |

## Testing

**Unit tests:**
- Dependency parsing recognizes `docker`
- Dockerfile generation includes docker.io installation
- Apple container + docker dependency returns error

**E2E test:**
- Create container with docker dependency
- Run `docker ps` to verify socket access works

**Manual verification:**
- Run moat's own e2e tests from inside a moat container
