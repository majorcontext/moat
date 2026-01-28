# Runtime Separation Design

**Goal:** Improve separation between Docker and Apple container runtimes to prevent Docker-specific features from breaking Apple runtime compatibility.

**Problem:** The current `Runtime` interface mixes universal operations (start/stop containers) with Docker-only features (networks, sidecars). This forces Apple runtime to implement stub methods and requires manager.go to use type assertions like `m.runtime.(*container.DockerRuntime).CreateNetwork()`. Adding Docker-specific features (like BuildKit sidecar) pollutes the shared interface and risks breaking Apple compatibility.

**Solution:** Use the Feature Manager pattern - split the monolithic Runtime interface into a minimal core and separate optional feature managers.

---

## Architecture

### Core Principle

**Interface Segregation:** Separate Docker-specific features from the core Runtime interface using composition, not inheritance.

```
Runtime (minimal interface)
    ├─ NetworkManager (optional, Docker only)
    ├─ SidecarManager (optional, Docker only)
    └─ Future features (optional, runtime-specific)
```

### Pattern: Feature Managers via Composition

Runtimes provide feature managers on-demand:
- `runtime.NetworkManager()` returns `NetworkManager` interface for Docker, `nil` for Apple
- Manager code explicitly requests feature managers, handles nil gracefully
- No type assertions, no stub implementations, clear feature boundaries

---

## Design

### 1. Core Runtime Interface

The Runtime interface is reduced to universal operations all runtimes must support:

```go
// Runtime provides core container lifecycle operations.
// All runtimes (Docker, Apple) must implement this.
type Runtime interface {
    Type() RuntimeType
    Ping(ctx context.Context) error

    // Lifecycle
    CreateContainer(ctx context.Context, cfg Config) (string, error)
    StartContainer(ctx context.Context, id string) error
    StopContainer(ctx context.Context, id string) error
    WaitContainer(ctx context.Context, id string) (int64, error)
    RemoveContainer(ctx context.Context, id string) error

    // Logs & State
    ContainerLogs(ctx context.Context, id string) (io.ReadCloser, error)
    ContainerLogsAll(ctx context.Context, id string) ([]byte, error)
    ContainerState(ctx context.Context, id string) (string, error)

    // Images
    BuildImage(ctx context.Context, dockerfile string, tag string, opts BuildOptions) error
    ImageExists(ctx context.Context, tag string) (bool, error)
    GetImageHomeDir(ctx context.Context, imageName string) string
    ListImages(ctx context.Context) ([]ImageInfo, error)
    RemoveImage(ctx context.Context, id string) error

    // Interactive
    Attach(ctx context.Context, id string, opts AttachOptions) error
    StartAttached(ctx context.Context, id string, opts AttachOptions) error
    ResizeTTY(ctx context.Context, id string, height, width uint) error

    // Runtime properties
    GetHostAddress() string
    SupportsHostNetwork() bool
    GetPortBindings(ctx context.Context, id string) (map[int]int, error)
    SetupFirewall(ctx context.Context, id string, proxyHost string, proxyPort int) error
    ListContainers(ctx context.Context) ([]Info, error)

    Close() error

    // Feature manager accessors
    NetworkManager() NetworkManager
    SidecarManager() SidecarManager
}
```

**Removed from Runtime:**
- `CreateNetwork()` → moved to NetworkManager
- `RemoveNetwork()` → moved to NetworkManager
- `StartSidecar()` → moved to SidecarManager
- `InspectContainer()` → moved to SidecarManager (Docker-specific implementation)

### 2. Feature Manager Interfaces

Docker-specific features are isolated in separate interfaces:

```go
// NetworkManager handles Docker network operations.
// Returned by Runtime.NetworkManager() - nil if not supported.
type NetworkManager interface {
    // CreateNetwork creates a network for inter-container communication.
    CreateNetwork(ctx context.Context, name string) (string, error)

    // RemoveNetwork removes a network by ID.
    // Best-effort: does not fail if network doesn't exist.
    RemoveNetwork(ctx context.Context, networkID string) error
}

// SidecarManager handles sidecar container operations.
// Returned by Runtime.SidecarManager() - nil if not supported.
type SidecarManager interface {
    // StartSidecar starts a sidecar container (pull, create, start).
    StartSidecar(ctx context.Context, cfg SidecarConfig) (string, error)

    // InspectContainer returns detailed container information.
    InspectContainer(ctx context.Context, containerID string) (ContainerInspectResponse, error)
}

// ContainerInspectResponse holds detailed container state.
type ContainerInspectResponse struct {
    State *ContainerState
}

type ContainerState struct {
    Running bool
}
```

### 3. Docker Implementation

Docker provides feature managers as separate structs:

```go
type DockerRuntime struct {
    cli *client.Client

    networkMgr *dockerNetworkManager
    sidecarMgr *dockerSidecarManager
}

func NewDockerRuntime() (*DockerRuntime, error) {
    cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
    if err != nil {
        return nil, fmt.Errorf("creating docker client: %w", err)
    }

    r := &DockerRuntime{cli: cli}
    r.networkMgr = &dockerNetworkManager{cli: cli}
    r.sidecarMgr = &dockerSidecarManager{cli: cli}
    return r, nil
}

func (r *DockerRuntime) NetworkManager() NetworkManager {
    return r.networkMgr
}

func (r *DockerRuntime) SidecarManager() SidecarManager {
    return r.sidecarMgr
}

// --- Feature Manager Implementations ---

type dockerNetworkManager struct {
    cli *client.Client
}

func (m *dockerNetworkManager) CreateNetwork(ctx context.Context, name string) (string, error) {
    // Implementation moved here from DockerRuntime.CreateNetwork
}

func (m *dockerNetworkManager) RemoveNetwork(ctx context.Context, networkID string) error {
    // Implementation moved here from DockerRuntime.RemoveNetwork
}

type dockerSidecarManager struct {
    cli *client.Client
}

func (m *dockerSidecarManager) StartSidecar(ctx context.Context, cfg SidecarConfig) (string, error) {
    // Implementation moved here from DockerRuntime.StartSidecar
}

func (m *dockerSidecarManager) InspectContainer(ctx context.Context, containerID string) (ContainerInspectResponse, error) {
    // Implementation moved here
}
```

### 4. Apple Implementation

Apple returns nil for unsupported features:

```go
type AppleRuntime struct {
    containerBin string
    hostAddress  string
}

func (r *AppleRuntime) NetworkManager() NetworkManager {
    return nil
}

func (r *AppleRuntime) SidecarManager() SidecarManager {
    return nil
}
```

**No more stub implementations returning "unsupported" errors.**

### 5. Usage Pattern in Manager

Manager code becomes type-safe and self-documenting:

**Before (type assertions):**
```go
networkID, err := m.runtime.(*container.DockerRuntime).CreateNetwork(ctx, name)
```

**After (feature managers):**
```go
netMgr := m.runtime.NetworkManager()
if netMgr == nil {
    return nil, fmt.Errorf("BuildKit requires Docker runtime")
}
networkID, err := netMgr.CreateNetwork(ctx, name)
```

**Full BuildKit setup example:**
```go
if buildkitCfg.Enabled {
    // Check for required features
    netMgr := m.runtime.NetworkManager()
    sidecarMgr := m.runtime.SidecarManager()

    if netMgr == nil || sidecarMgr == nil {
        return nil, fmt.Errorf("BuildKit requires Docker runtime (networks and sidecars not supported by %s)", m.runtime.Type())
    }

    // Create network
    netID, err := netMgr.CreateNetwork(ctx, buildkitCfg.NetworkName)
    if err != nil {
        return nil, fmt.Errorf("failed to create Docker network: %w", err)
    }
    networkID = netID

    // Start sidecar
    buildkitID, err := sidecarMgr.StartSidecar(ctx, sidecarCfg)
    if err != nil {
        _ = netMgr.RemoveNetwork(ctx, networkID)
        return nil, fmt.Errorf("failed to start buildkit sidecar: %w", err)
    }
    buildkitContainerID = buildkitID

    // Wait for ready
    for i := 0; i < 10; i++ {
        time.Sleep(1 * time.Second)
        inspect, err := sidecarMgr.InspectContainer(ctx, buildkitContainerID)
        if err == nil && inspect.State != nil && inspect.State.Running {
            ready = true
            break
        }
    }
}
```

---

## Benefits

1. **No type assertions** - Eliminates `m.runtime.(*container.DockerRuntime)` casts
2. **No stub implementations** - Apple returns nil instead of "unsupported" errors
3. **Clear feature boundaries** - Docker-specific features isolated in feature managers
4. **Type safety** - Cannot accidentally call network methods on Apple runtime
5. **Self-documenting** - Code explicitly shows feature dependencies
6. **Easy to extend** - New Docker features add new managers without touching Runtime interface
7. **Fail fast** - Check for nil managers before attempting operations
8. **Better error messages** - "BuildKit requires Docker runtime" vs runtime panics

---

## Migration Strategy

### Phase 1: Add feature managers alongside existing methods
- Add `NetworkManager()` and `SidecarManager()` to Runtime interface
- Implement feature manager structs in docker.go
- Keep existing `CreateNetwork()`, `StartSidecar()` methods temporarily
- Apple returns nil for feature managers, keeps stub implementations

### Phase 2: Migrate manager.go to use feature managers
- Update all type assertion calls to use feature managers
- Replace `m.runtime.(*container.DockerRuntime).CreateNetwork()` with manager pattern
- Add nil checks for graceful failure
- Test thoroughly

### Phase 3: Remove old methods from Runtime interface
- Delete `CreateNetwork()`, `RemoveNetwork()`, `StartSidecar()` from Runtime interface
- Delete stub implementations from apple.go
- Delete old implementations from docker.go (now in feature managers)
- Verify no callers remain

---

## Testing Strategy

### Unit Tests

```go
func TestDockerRuntimeFeatureManagers(t *testing.T) {
    rt, err := NewDockerRuntime()
    require.NoError(t, err)

    assert.NotNil(t, rt.NetworkManager())
    assert.NotNil(t, rt.SidecarManager())
}

func TestAppleRuntimeNoFeatureManagers(t *testing.T) {
    rt, err := NewAppleRuntime()
    require.NoError(t, err)

    assert.Nil(t, rt.NetworkManager())
    assert.Nil(t, rt.SidecarManager())
}
```

### Integration Tests

- BuildKit setup fails gracefully on Apple runtime with clear error message
- Docker BuildKit integration works with feature managers
- Network cleanup works via NetworkManager
- No panics from nil manager access

---

## Files Changed

- `internal/container/runtime.go` - Update Runtime interface, add feature manager interfaces
- `internal/container/docker.go` - Extract feature managers, implement accessors
- `internal/container/apple.go` - Add nil-returning accessors, remove stubs in Phase 3
- `internal/run/manager.go` - Replace type assertions with feature manager calls

---

## Future Extensions

This pattern makes it easy to add new Docker-specific features without polluting Runtime:

- `VolumeManager` - Docker volume operations
- `ComposeManager` - Docker Compose integration
- `SwarmManager` - Docker Swarm orchestration
- `BuildKitManager` - Advanced BuildKit features beyond basic sidecar

Each new feature is a separate optional interface, discovered at runtime via nil checks.
