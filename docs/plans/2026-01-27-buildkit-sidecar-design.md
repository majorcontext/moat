# BuildKit Sidecar Design

**Date:** 2026-01-27
**Status:** Approved

## Overview

Add automatic BuildKit sidecar support to `docker:dind` mode to significantly improve build performance while maintaining full Docker daemon access for container lifecycle operations.

## Problem

The current `docker:dind` implementation starts a full Docker daemon inside the container, but builds are unreasonably slow due to the legacy builder. BuildKit provides:
- Layer caching
- `RUN --mount=type=cache` support
- Registry and local cache export
- Faster multi-stage builds

Users need both fast builds (BuildKit) and full Docker daemon access (`docker ps`, `docker run`, etc.).

## Solution

When users specify `docker:dind`, automatically deploy a BuildKit sidecar container connected via a shared Docker network. The main container gets both BuildKit for builds and a full Docker daemon for runtime operations.

## Architecture

### Docker Modes (Unchanged)

Two Docker access modes remain:
1. `docker:host` - Mounts host Docker socket (fast, full host access)
2. `docker:dind` - Privileged container with dockerd + buildkit sidecar (isolated, optimized)

### Container Topology

```
docker:dind run layout:
┌─────────────────────────────────────┐
│  Docker Network: moat-<run-id>      │
│                                      │
│  ┌──────────────────────────────┐  │
│  │ Buildkit Sidecar             │  │
│  │ moby/buildkit:latest         │  │
│  │ Hostname: buildkit           │  │
│  │ Port: 1234                   │  │
│  └──────────────────────────────┘  │
│           ↑                          │
│           │ tcp://buildkit:1234     │
│           │                          │
│  ┌──────────────────────────────┐  │
│  │ Main Container (privileged)  │  │
│  │ - dockerd (local daemon)     │  │
│  │ - BUILDKIT_HOST env var      │  │
│  │ - docker build → buildkit    │  │
│  │ - docker ps → local dockerd  │  │
│  └──────────────────────────────┘  │
└─────────────────────────────────────┘
```

### Container Lifecycle

1. **Setup:**
   - Create Docker network: `moat-<run-id>`
   - Start buildkit sidecar: `moby/buildkit:latest --addr tcp://0.0.0.0:1234`
   - Start main container (privileged) on same network

2. **Environment Variables:**
   - `MOAT_DOCKER_DIND=1` - Triggers dockerd startup in moat-init.sh
   - `BUILDKIT_HOST=tcp://buildkit:1234` - Routes builds to sidecar
   - `DOCKER_HOST=unix:///var/run/docker.sock` - Local dockerd for runtime ops

3. **Teardown:**
   - Stop main container
   - Stop buildkit sidecar
   - Remove network

### Benefits

- `docker build` automatically uses fast BuildKit with caching
- Full Docker daemon available for `docker ps`, `docker run`, lifecycle management
- BuildKit cache mounts (`RUN --mount=type=cache`) work efficiently
- No user configuration required - automatic with `docker:dind`
- Clean isolation boundary - no host Docker socket access

## Implementation Components

### 1. Container Runtime (internal/container/)

Add methods to Docker runtime interface:
- `StartSidecar(ctx, config) (containerID, error)` - Start buildkit sidecar
- `StopSidecar(ctx, containerID) error` - Stop and remove sidecar
- `CreateNetwork(ctx, name) (networkID, error)` - Create Docker network
- `RemoveNetwork(ctx, networkID) error` - Remove Docker network

Sidecar container configuration:
- **Image:** `moby/buildkit:latest`
- **Command:** `["--addr", "tcp://0.0.0.0:1234"]`
- **Network:** Attached to `moat-<run-id>` network
- **Hostname:** `buildkit`
- **Name:** `moat-buildkit-<run-id>`

### 2. Run Manager (internal/run/manager.go)

Modify `Create()`:
- Detect `docker:dind` mode via `ResolveDockerDependency()`
- When dind detected:
  1. Create Docker network
  2. Start buildkit sidecar
  3. Wait for sidecar health (10 second timeout)
  4. Add `BUILDKIT_HOST=tcp://buildkit:1234` to main container env
  5. Keep existing `MOAT_DOCKER_DIND=1` behavior
  6. Start main container on network

Modify `Stop()`:
- Stop buildkit sidecar if present
- Remove Docker network (best-effort)

### 3. Storage/State (internal/storage/)

Add fields to run metadata for cleanup:
```go
type RunMetadata struct {
    // ... existing fields ...
    BuildkitContainerID string `json:"buildkit_container_id,omitempty"`
    NetworkID           string `json:"network_id,omitempty"`
}
```

Persist for crash recovery and cleanup on restart.

### 4. Audit Logging (internal/audit/)

Add audit events:
- `docker_network_created` - Network creation for buildkit
- `buildkit_sidecar_started` - Sidecar container started
- `buildkit_sidecar_stopped` - Sidecar cleanup

Include in container audit data when buildkit active.

### 5. Dependency Parsing (No Changes)

`docker:dind` already parsed correctly in:
- `internal/deps/parser.go`
- `internal/deps/types.go`

No changes required.

## Error Handling

### Network Creation Failure
- Abort run creation with clear error
- Message: "Failed to create Docker network for buildkit sidecar: \<reason\>"
- No partial state created

### BuildKit Sidecar Startup Failure
- Clean up network and abort
- Wait up to 10 seconds for sidecar health
- Message: "BuildKit sidecar failed to start: \<reason\>"
- Check Docker Hub access if image pull fails

### Image Pull Failure
- Don't fail silently - buildkit is essential for performance
- Message: "Failed to pull moby/buildkit:latest. Ensure Docker can access Docker Hub."

### Cleanup and Resource Leaks
- `Stop()` cleans up sidecar even if main container fails
- Network removal is best-effort (may fail if containers attached)
- On restart, detect orphaned resources by `moat-buildkit-<run-id>` prefix
- `moat clean` removes orphaned buildkit sidecars and networks

### Port Conflicts
- BuildKit port 1234 is inside container network, not host-exposed
- Network isolation prevents conflicts
- Multiple moat runs with buildkit can run simultaneously

## Compatibility

### Backward Compatibility
- Existing `docker:dind` runs without buildkit continue working
- Detection: if `BuildkitContainerID` is empty in run state, legacy dind-only
- No migration needed - new behavior applies to new runs only

### Runtime Support
- **Docker runtime:** Full support (dind + buildkit sidecar)
- **Apple containers:** Not supported (no privileged mode, no sidecars)
- Error message unchanged for Apple containers with `docker:dind`

## Testing Strategy

### Unit Tests
- `internal/container/docker_test.go` - Network and sidecar methods
- `internal/run/manager_test.go` - Buildkit integration in Create/Stop
- Verify env vars: `BUILDKIT_HOST` set correctly
- Verify state: sidecar ID and network ID persisted

### E2E Tests
- `internal/e2e/docker_test.go` - Full dind+buildkit lifecycle
- Verify builds use buildkit (check for BuildKit output)
- Verify docker runtime ops work (`docker ps`, `docker run`)
- Verify cache mounts: `RUN --mount=type=cache`
- Test cleanup: sidecar and network removed on stop

### Manual Testing
- Performance comparison: dind-only vs dind+buildkit build times
- Multi-run isolation: multiple dind runs with buildkit simultaneously
- Error scenarios: image pull failure, network conflicts

## Open Questions

None - design approved.

## References

- BuildKit documentation: https://github.com/moby/buildkit
- Docker networking: https://docs.docker.com/network/
- Moat docker:dind implementation: `internal/run/docker.go`
