# Runtime Separation with Feature Managers Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Refactor Runtime interface to use Feature Manager pattern, eliminating type assertions and stub implementations.

**Architecture:** Split monolithic Runtime interface into minimal core (lifecycle operations) + optional feature managers (NetworkManager, SidecarManager) provided via composition. Docker provides managers, Apple returns nil.

**Tech Stack:** Go interfaces, Docker SDK, existing Runtime abstraction

---

## Task 1: Add Feature Manager Interfaces

**Files:**
- Modify: `internal/container/runtime.go`

**Context:** Define the new feature manager interfaces that will replace methods on Runtime. These interfaces will be returned by Runtime accessor methods.

**Step 1: Add NetworkManager interface**

Add after the Runtime interface definition (after line 122):

```go
// NetworkManager handles Docker network operations.
// Returned by Runtime.NetworkManager() - nil if not supported.
type NetworkManager interface {
	// CreateNetwork creates a network for inter-container communication.
	// Returns the network ID.
	CreateNetwork(ctx context.Context, name string) (string, error)

	// RemoveNetwork removes a network by ID.
	// Best-effort: does not fail if network doesn't exist.
	RemoveNetwork(ctx context.Context, networkID string) error
}
```

**Step 2: Add SidecarManager interface**

Add after NetworkManager:

```go
// SidecarManager handles sidecar container operations.
// Returned by Runtime.SidecarManager() - nil if not supported.
type SidecarManager interface {
	// StartSidecar starts a sidecar container (pull, create, start).
	// The container is attached to the specified network and assigned a hostname.
	// Returns the container ID.
	StartSidecar(ctx context.Context, cfg SidecarConfig) (string, error)

	// InspectContainer returns detailed container information.
	// Useful for checking sidecar state (running, health, etc).
	InspectContainer(ctx context.Context, containerID string) (ContainerInspectResponse, error)
}
```

**Step 3: Add ContainerInspectResponse types**

Add after SidecarManager:

```go
// ContainerInspectResponse holds detailed container state.
type ContainerInspectResponse struct {
	State *ContainerState
}

// ContainerState holds container execution state.
type ContainerState struct {
	Running bool
}
```

**Step 4: Add accessor methods to Runtime interface**

Add to the Runtime interface (before Close() method, around line 121):

```go
	// NetworkManager returns the network manager if supported, nil otherwise.
	// Docker provides this, Apple containers return nil.
	NetworkManager() NetworkManager

	// SidecarManager returns the sidecar manager if supported, nil otherwise.
	// Docker provides this, Apple containers return nil.
	SidecarManager() SidecarManager
```

**Step 5: Run tests to verify compilation**

Run: `go test ./internal/container/... -short`
Expected: FAIL - DockerRuntime and AppleRuntime don't implement new methods yet

**Step 6: Commit**

```bash
git add internal/container/runtime.go
git commit -m "feat(container): add NetworkManager and SidecarManager interfaces"
```

---

## Task 2: Implement Docker Feature Managers

**Files:**
- Modify: `internal/container/docker.go`

**Context:** Create feature manager implementations for Docker. These will hold the Docker client and implement the network/sidecar operations. The existing methods on DockerRuntime will remain temporarily during migration.

**Step 1: Add feature manager structs**

Add after the DockerRuntime struct definition (after line 34):

```go
// dockerNetworkManager implements NetworkManager for Docker.
type dockerNetworkManager struct {
	cli *client.Client
}

// dockerSidecarManager implements SidecarManager for Docker.
type dockerSidecarManager struct {
	cli *client.Client
}
```

**Step 2: Add manager fields to DockerRuntime**

Modify DockerRuntime struct (around line 32):

```go
type DockerRuntime struct {
	cli *client.Client

	// Feature managers
	networkMgr *dockerNetworkManager
	sidecarMgr *dockerSidecarManager
}
```

**Step 3: Initialize managers in NewDockerRuntime**

Modify NewDockerRuntime (around line 37-42):

```go
func NewDockerRuntime() (*DockerRuntime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	r := &DockerRuntime{
		cli:        cli,
		networkMgr: &dockerNetworkManager{cli: cli},
		sidecarMgr: &dockerSidecarManager{cli: cli},
	}
	return r, nil
}
```

**Step 4: Add accessor methods to DockerRuntime**

Add after NewDockerRuntime:

```go
// NetworkManager returns the Docker network manager.
func (r *DockerRuntime) NetworkManager() NetworkManager {
	return r.networkMgr
}

// SidecarManager returns the Docker sidecar manager.
func (r *DockerRuntime) SidecarManager() SidecarManager {
	return r.sidecarMgr
}
```

**Step 5: Move CreateNetwork to dockerNetworkManager**

Find the existing `CreateNetwork` method on DockerRuntime (around line 797-807).
Add new implementation on dockerNetworkManager after the accessor methods:

```go
// CreateNetwork creates a Docker network for inter-container communication.
func (m *dockerNetworkManager) CreateNetwork(ctx context.Context, name string) (string, error) {
	resp, err := m.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		return "", fmt.Errorf("creating network: %w", err)
	}
	return resp.ID, nil
}
```

**Step 6: Move RemoveNetwork to dockerNetworkManager**

Find the existing `RemoveNetwork` method on DockerRuntime (around line 809-826).
Add new implementation:

```go
// RemoveNetwork removes a Docker network by ID.
func (m *dockerNetworkManager) RemoveNetwork(ctx context.Context, networkID string) error {
	// Try to remove the network - ignore errors if it doesn't exist or has active endpoints
	err := m.cli.NetworkRemove(ctx, networkID)
	if err != nil {
		// Log but don't fail if network doesn't exist or removal fails
		// This is best-effort cleanup
		if !errdefs.IsNotFound(err) {
			log.Debug("failed to remove network", "network_id", networkID, "error", err)
		}
	}
	return nil
}
```

**Step 7: Move StartSidecar to dockerSidecarManager**

Find the existing `StartSidecar` method on DockerRuntime (around line 829-889).
Add new implementation:

```go
// StartSidecar starts a sidecar container (pull, create, start).
func (m *dockerSidecarManager) StartSidecar(ctx context.Context, cfg SidecarConfig) (string, error) {
	// Pull image if not present
	if err := ensureImage(ctx, m.cli, cfg.Image); err != nil {
		return "", fmt.Errorf("pulling sidecar image: %w", err)
	}

	// Prepare mounts
	mounts := make([]mount.Mount, 0, len(cfg.Mounts))
	for _, mt := range cfg.Mounts {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   mt.Source,
			Target:   mt.Target,
			ReadOnly: mt.ReadOnly,
		})
	}

	// Create container
	resp, err := m.cli.ContainerCreate(ctx,
		&container.Config{
			Image:    cfg.Image,
			Hostname: cfg.Hostname,
			Cmd:      cfg.Cmd,
		},
		&container.HostConfig{
			NetworkMode: container.NetworkMode(cfg.NetworkID),
			Privileged:  cfg.Privileged,
			Mounts:      mounts,
		},
		&network.NetworkingConfig{},
		nil,
		cfg.Name,
	)
	if err != nil {
		return "", fmt.Errorf("creating sidecar container: %w", err)
	}

	// Start container
	if err := m.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up on failure
		_ = m.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("starting sidecar container: %w", err)
	}

	return resp.ID, nil
}
```

**Step 8: Add InspectContainer to dockerSidecarManager**

Find the existing `InspectContainer` method on DockerRuntime (around line 891-898).
Add new implementation that returns our custom type:

```go
// InspectContainer returns container inspection data.
func (m *dockerSidecarManager) InspectContainer(ctx context.Context, containerID string) (ContainerInspectResponse, error) {
	inspect, err := m.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return ContainerInspectResponse{}, fmt.Errorf("inspecting container: %w", err)
	}

	var state *ContainerState
	if inspect.State != nil {
		state = &ContainerState{
			Running: inspect.State.Running,
		}
	}

	return ContainerInspectResponse{State: state}, nil
}
```

**Step 9: Run tests to verify Docker implementation**

Run: `go test ./internal/container/... -short -run TestDocker`
Expected: PASS - Docker tests should still work with both old methods and new managers

**Step 10: Commit**

```bash
git add internal/container/docker.go
git commit -m "feat(container): add Docker feature manager implementations"
```

---

## Task 3: Implement Apple Nil Managers

**Files:**
- Modify: `internal/container/apple.go`

**Context:** Apple runtime doesn't support networks or sidecars, so it returns nil for both managers. This is cleaner than stub implementations that return errors.

**Step 1: Add accessor methods to AppleRuntime**

Add after the NewAppleRuntime function (around line 60):

```go
// NetworkManager returns nil - Apple containers don't support networks.
func (r *AppleRuntime) NetworkManager() NetworkManager {
	return nil
}

// SidecarManager returns nil - Apple containers don't support sidecars.
func (r *AppleRuntime) SidecarManager() SidecarManager {
	return nil
}
```

**Step 2: Run tests to verify Apple implementation**

Run: `go test ./internal/container/... -short -run TestApple`
Expected: PASS - Apple tests should compile and pass

**Step 3: Commit**

```bash
git add internal/container/apple.go
git commit -m "feat(container): add Apple nil manager accessors"
```

---

## Task 4: Add Feature Manager Tests

**Files:**
- Create: `internal/container/feature_managers_test.go`

**Context:** Verify that Docker provides non-nil managers and Apple returns nil. This ensures the feature detection pattern works correctly.

**Step 1: Write test for Docker feature managers**

Create new file:

```go
package container

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerRuntimeFeatureManagers(t *testing.T) {
	rt, err := NewDockerRuntime()
	require.NoError(t, err)
	defer rt.Close()

	assert.NotNil(t, rt.NetworkManager(), "Docker should provide NetworkManager")
	assert.NotNil(t, rt.SidecarManager(), "Docker should provide SidecarManager")
}
```

**Step 2: Write test for Apple nil managers**

Add to the same file:

```go
func TestAppleRuntimeNoFeatureManagers(t *testing.T) {
	rt, err := NewAppleRuntime()
	if err != nil {
		t.Skip("Apple containers not available")
	}
	defer rt.Close()

	assert.Nil(t, rt.NetworkManager(), "Apple should not provide NetworkManager")
	assert.Nil(t, rt.SidecarManager(), "Apple should not provide SidecarManager")
}
```

**Step 3: Run tests**

Run: `go test ./internal/container/... -run TestFeatureManagers -v`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/container/feature_managers_test.go
git commit -m "test(container): add feature manager tests"
```

---

## Task 5: Migrate Manager - BuildKit Network Setup

**Files:**
- Modify: `internal/run/manager.go:1362-1374`

**Context:** Replace type assertion for CreateNetwork with feature manager pattern. This is the first migration to the new pattern in manager.go.

**Step 1: Update network creation code**

Find the BuildKit network setup (around line 1362-1374):

Replace:
```go
	// Create network and start BuildKit sidecar if enabled
	var networkID string
	if buildkitCfg.Enabled {
		log.Debug("creating network for buildkit sidecar", "network", buildkitCfg.NetworkName)
		netID, netErr := m.runtime.(*container.DockerRuntime).CreateNetwork(ctx, buildkitCfg.NetworkName) //nolint:errcheck
		if netErr != nil {
			cleanupProxy(proxyServer)
			cleanupSSH(sshServer)
			cleanupClaude(claudeGenerated)
			cleanupCodex(codexGenerated)
			return nil, fmt.Errorf("failed to create Docker network for buildkit sidecar: %w", netErr)
		}
		networkID = netID
```

With:
```go
	// Create network and start BuildKit sidecar if enabled
	var networkID string
	if buildkitCfg.Enabled {
		// Check for required feature managers
		netMgr := m.runtime.NetworkManager()
		if netMgr == nil {
			cleanupProxy(proxyServer)
			cleanupSSH(sshServer)
			cleanupClaude(claudeGenerated)
			cleanupCodex(codexGenerated)
			return nil, fmt.Errorf("BuildKit requires Docker runtime (networks not supported by %s)", m.runtime.Type())
		}

		log.Debug("creating network for buildkit sidecar", "network", buildkitCfg.NetworkName)
		netID, err := netMgr.CreateNetwork(ctx, buildkitCfg.NetworkName)
		if err != nil {
			cleanupProxy(proxyServer)
			cleanupSSH(sshServer)
			cleanupClaude(claudeGenerated)
			cleanupCodex(codexGenerated)
			return nil, fmt.Errorf("failed to create Docker network for buildkit sidecar: %w", err)
		}
		networkID = netID
```

**Step 2: Run tests**

Run: `go test ./internal/run/... -short -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/run/manager.go
git commit -m "refactor(run): use NetworkManager for BuildKit network creation"
```

---

## Task 6: Migrate Manager - BuildKit Sidecar Start

**Files:**
- Modify: `internal/run/manager.go:1404-1413`

**Context:** Replace type assertion for StartSidecar with feature manager pattern.

**Step 1: Store sidecar manager reference**

After the network manager check (after setting `networkID = netID`), add:

```go
		// Get sidecar manager
		sidecarMgr := m.runtime.SidecarManager()
		if sidecarMgr == nil {
			_ = netMgr.RemoveNetwork(ctx, networkID)
			cleanupProxy(proxyServer)
			cleanupSSH(sshServer)
			cleanupClaude(claudeGenerated)
			cleanupCodex(codexGenerated)
			return nil, fmt.Errorf("BuildKit requires Docker runtime (sidecars not supported by %s)", m.runtime.Type())
		}
```

**Step 2: Update StartSidecar call**

Find the StartSidecar call (around line 1404):

Replace:
```go
		buildkitContainerID, sidecarErr := m.runtime.(*container.DockerRuntime).StartSidecar(ctx, sidecarCfg) //nolint:errcheck
		if sidecarErr != nil {
			// Clean up network on failure
			_ = m.runtime.(*container.DockerRuntime).RemoveNetwork(ctx, networkID) //nolint:errcheck
```

With:
```go
		buildkitContainerID, err := sidecarMgr.StartSidecar(ctx, sidecarCfg)
		if err != nil {
			// Clean up network on failure
			_ = netMgr.RemoveNetwork(ctx, networkID)
```

**Step 3: Run tests**

Run: `go test ./internal/run/... -short -v`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/run/manager.go
git commit -m "refactor(run): use SidecarManager for BuildKit sidecar start"
```

---

## Task 7: Migrate Manager - BuildKit Ready Check

**Files:**
- Modify: `internal/run/manager.go:1418-1428`

**Context:** Replace InspectContainer type assertion with SidecarManager call.

**Step 1: Update InspectContainer call**

Find the BuildKit ready check loop (around line 1418-1424):

Replace:
```go
		for i := 0; i < 10; i++ {
			time.Sleep(1 * time.Second)
			inspect, inspectErr := m.runtime.(*container.DockerRuntime).InspectContainer(ctx, buildkitContainerID) //nolint:errcheck
			if inspectErr == nil && inspect.State != nil && inspect.State.Running {
				ready = true
				break
			}
		}
```

With:
```go
		for i := 0; i < 10; i++ {
			time.Sleep(1 * time.Second)
			inspect, err := sidecarMgr.InspectContainer(ctx, buildkitContainerID)
			if err == nil && inspect.State != nil && inspect.State.Running {
				ready = true
				break
			}
		}
```

**Step 2: Update cleanup on timeout**

Find the timeout cleanup (around line 1426-1428):

Replace:
```go
		if !ready {
			_ = m.runtime.StopContainer(ctx, buildkitContainerID)                  //nolint:errcheck
			_ = m.runtime.(*container.DockerRuntime).RemoveNetwork(ctx, networkID) //nolint:errcheck
```

With:
```go
		if !ready {
			_ = m.runtime.StopContainer(ctx, buildkitContainerID)
			_ = netMgr.RemoveNetwork(ctx, networkID)
```

**Step 3: Run tests**

Run: `go test ./internal/run/... -short -v`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/run/manager.go
git commit -m "refactor(run): use SidecarManager for BuildKit ready check"
```

---

## Task 8: Migrate Manager - Container Creation Cleanup

**Files:**
- Modify: `internal/run/manager.go:1468-1470`

**Context:** Replace RemoveNetwork type assertion in container creation failure cleanup.

**Step 1: Update cleanup code**

Find the container creation error handler (around line 1466-1471):

Replace:
```go
	if err != nil {
		// Clean up BuildKit resources on failure
		if buildkitCfg.Enabled && r.BuildkitContainerID != "" {
			_ = m.runtime.StopContainer(ctx, r.BuildkitContainerID)                  //nolint:errcheck
			_ = m.runtime.(*container.DockerRuntime).RemoveNetwork(ctx, r.NetworkID) //nolint:errcheck
		}
```

With:
```go
	if err != nil {
		// Clean up BuildKit resources on failure
		if buildkitCfg.Enabled && r.BuildkitContainerID != "" {
			_ = m.runtime.StopContainer(ctx, r.BuildkitContainerID)
			if netMgr := m.runtime.NetworkManager(); netMgr != nil {
				_ = netMgr.RemoveNetwork(ctx, r.NetworkID)
			}
		}
```

**Step 2: Run tests**

Run: `go test ./internal/run/... -short -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/run/manager.go
git commit -m "refactor(run): use NetworkManager in container creation cleanup"
```

---

## Task 9: Migrate Manager - Stop Method Cleanup

**Files:**
- Modify: `internal/run/manager.go:1909-1913`

**Context:** Replace type assertion in Stop method network cleanup.

**Step 1: Find Stop method network cleanup**

Find around line 1909-1913:

Replace:
```go
	if networkID != "" {
		log.Debug("removing docker network", "network_id", networkID)
		if dockerRuntime, ok := m.runtime.(*container.DockerRuntime); ok {
			if err := dockerRuntime.RemoveNetwork(ctx, networkID); err != nil {
```

With:
```go
	if networkID != "" {
		log.Debug("removing docker network", "network_id", networkID)
		if netMgr := m.runtime.NetworkManager(); netMgr != nil {
			if err := netMgr.RemoveNetwork(ctx, networkID); err != nil {
```

**Step 2: Run tests**

Run: `go test ./internal/run/... -short -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/run/manager.go
git commit -m "refactor(run): use NetworkManager in Stop method"
```

---

## Task 10: Migrate Manager - Destroy Method Cleanup

**Files:**
- Modify: `internal/run/manager.go:2196-2200`

**Context:** Replace type assertion in Destroy method network cleanup. This is the final migration point in manager.go.

**Step 1: Find Destroy method network cleanup**

Find around line 2196-2200:

Replace:
```go
	// Remove BuildKit network if present
	if r.NetworkID != "" {
		if dockerRuntime, ok := m.runtime.(*container.DockerRuntime); ok {
			if err := dockerRuntime.RemoveNetwork(ctx, r.NetworkID); err != nil {
```

With:
```go
	// Remove BuildKit network if present
	if r.NetworkID != "" {
		if netMgr := m.runtime.NetworkManager(); netMgr != nil {
			if err := netMgr.RemoveNetwork(ctx, r.NetworkID); err != nil {
```

**Step 2: Run all tests**

Run: `go test ./... -short -timeout=2m`
Expected: PASS - all tests should pass with manager.go fully migrated

**Step 3: Commit**

```bash
git add internal/run/manager.go
git commit -m "refactor(run): use NetworkManager in Destroy method"
```

---

## Task 11: Remove Old Methods from DockerRuntime

**Files:**
- Modify: `internal/container/docker.go`

**Context:** Now that manager.go is fully migrated, remove the old methods from DockerRuntime that are duplicates of the feature manager implementations.

**Step 1: Remove old CreateNetwork method**

Find and delete the CreateNetwork method on DockerRuntime (around line 797-807).
Keep only the implementation on dockerNetworkManager.

**Step 2: Remove old RemoveNetwork method**

Find and delete the RemoveNetwork method on DockerRuntime (around line 809-826).
Keep only the implementation on dockerNetworkManager.

**Step 3: Remove old StartSidecar method**

Find and delete the StartSidecar method on DockerRuntime (around line 829-889).
Keep only the implementation on dockerSidecarManager.

**Step 4: Remove old InspectContainer method**

Find and delete the InspectContainer method on DockerRuntime (around line 891-898).
Keep only the implementation on dockerSidecarManager.

**Step 5: Run tests**

Run: `go test ./... -short -timeout=2m`
Expected: PASS - all references now use feature managers

**Step 6: Commit**

```bash
git add internal/container/docker.go
git commit -m "refactor(container): remove old Docker methods now in feature managers"
```

---

## Task 12: Remove Old Methods from AppleRuntime

**Files:**
- Modify: `internal/container/apple.go`

**Context:** Remove stub implementations from Apple runtime that returned "unsupported" errors. They're replaced by nil manager accessors.

**Step 1: Remove CreateNetwork stub**

Find and delete the CreateNetwork stub method on AppleRuntime (around line 1076-1080).

**Step 2: Remove RemoveNetwork stub**

Find and delete the RemoveNetwork stub method on AppleRuntime (around line 1082-1086).

**Step 3: Remove StartSidecar stub**

Find and delete the StartSidecar stub method on AppleRuntime (around line 1088-1092).

**Step 4: Run tests**

Run: `go test ./... -short -timeout=2m`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/container/apple.go
git commit -m "refactor(container): remove Apple stub methods replaced by nil managers"
```

---

## Task 13: Remove Old Methods from Runtime Interface

**Files:**
- Modify: `internal/container/runtime.go`

**Context:** Final cleanup - remove the old method signatures from Runtime interface. Only NetworkManager() and SidecarManager() accessors remain.

**Step 1: Remove CreateNetwork from Runtime interface**

Find and delete from Runtime interface (around line 110-112):
```go
	// CreateNetwork creates a Docker network for inter-container communication.
	// Returns the network ID.
	CreateNetwork(ctx context.Context, name string) (string, error)
```

**Step 2: Remove RemoveNetwork from Runtime interface**

Find and delete from Runtime interface (around line 114-116):
```go
	// RemoveNetwork removes a Docker network by ID.
	// Best-effort: does not fail if network doesn't exist or has active endpoints.
	RemoveNetwork(ctx context.Context, networkID string) error
```

**Step 3: Remove StartSidecar from Runtime interface**

Find and delete from Runtime interface (around line 118-122):
```go
	// StartSidecar starts a sidecar container (pull, create, start).
	// The container is attached to the specified network and assigned a hostname.
	// Returns the container ID.
	StartSidecar(ctx context.Context, cfg SidecarConfig) (string, error)
```

**Step 4: Verify no InspectContainer in Runtime interface**

InspectContainer should not be in Runtime interface (it was Docker-specific).
If present, remove it.

**Step 5: Run all tests**

Run: `go test ./... -short -timeout=2m`
Expected: PASS - interface is clean, no compilation errors

**Step 6: Commit**

```bash
git add internal/container/runtime.go
git commit -m "refactor(container): remove old methods from Runtime interface"
```

---

## Task 14: Run E2E Tests

**Files:**
- None (testing only)

**Context:** Verify BuildKit integration still works end-to-end with the new feature manager architecture.

**Step 1: Run Docker E2E tests**

Run: `go test -tags=e2e ./internal/e2e/... -run Docker -v -timeout=5m`
Expected: PASS - BuildKit tests work with feature managers

**Step 2: Run all unit tests**

Run: `go test ./... -short -timeout=2m`
Expected: PASS

**Step 3: Build the binary**

Run: `go build ./cmd/moat`
Expected: SUCCESS - no compilation errors

**Step 4: Manual smoke test (optional)**

If you have a test workspace with docker:dind:
```bash
./moat run ./test-workspace
```
Expected: Container starts, BuildKit sidecar works

---

## Summary

This plan migrates the Runtime interface from a monolithic design with type assertions to a clean Feature Manager pattern.

**Benefits achieved:**
- No type assertions in manager.go
- No stub implementations in apple.go
- Clear feature boundaries via separate interfaces
- Type-safe feature detection via nil checks
- Easy to extend with new Docker-only features

**Files changed:**
- `internal/container/runtime.go` - Added feature manager interfaces, removed old methods
- `internal/container/docker.go` - Extracted feature managers, removed old methods
- `internal/container/apple.go` - Added nil accessors, removed stubs
- `internal/run/manager.go` - Replaced all type assertions with feature manager pattern
- `internal/container/feature_managers_test.go` - New tests for feature detection

**Testing strategy:**
- Unit tests verify Docker provides non-nil managers
- Unit tests verify Apple returns nil managers
- Manager tests verify graceful handling of missing features
- E2E tests verify BuildKit still works end-to-end
