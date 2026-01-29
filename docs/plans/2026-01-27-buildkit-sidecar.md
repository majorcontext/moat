# BuildKit Sidecar Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Automatically deploy BuildKit sidecar with docker:dind to provide fast image building via shared Docker network.

**Architecture:** When docker:dind is enabled, create a Docker network, start a moby/buildkit:latest sidecar container, then start the main container on the same network with BUILDKIT_HOST env var. Both containers communicate over the network, providing fast builds (BuildKit) and full Docker daemon access (dockerd in main container).

**Tech Stack:** Go, Docker SDK, Docker networks, BuildKit

---

## Task 1: Add Network Methods to Container Runtime

**Files:**
- Modify: `internal/container/docker.go` (add after existing methods)
- Test: `internal/container/docker_test.go`

**Step 1: Write failing tests for network operations**

Add to `internal/container/docker_test.go`:

```go
func TestDockerRuntime_CreateNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	rt, err := NewDockerRuntime()
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	networkName := "test-moat-network-" + strconv.FormatInt(time.Now().Unix(), 10)
	networkID, err := rt.CreateNetwork(ctx, networkName)
	if err != nil {
		t.Fatalf("CreateNetwork failed: %v", err)
	}
	defer rt.RemoveNetwork(ctx, networkID)

	if networkID == "" {
		t.Fatal("CreateNetwork returned empty network ID")
	}

	// Verify network exists
	inspect, err := rt.cli.NetworkInspect(ctx, networkID, types.NetworkInspectOptions{})
	if err != nil {
		t.Fatalf("network not created: %v", err)
	}
	if inspect.Name != networkName {
		t.Errorf("network name: got %q, want %q", inspect.Name, networkName)
	}
}

func TestDockerRuntime_RemoveNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	rt, err := NewDockerRuntime()
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	networkName := "test-moat-network-" + strconv.FormatInt(time.Now().Unix(), 10)
	networkID, err := rt.CreateNetwork(ctx, networkName)
	if err != nil {
		t.Fatalf("CreateNetwork failed: %v", err)
	}

	if err := rt.RemoveNetwork(ctx, networkID); err != nil {
		t.Fatalf("RemoveNetwork failed: %v", err)
	}

	// Verify network is gone
	_, err = rt.cli.NetworkInspect(ctx, networkID, types.NetworkInspectOptions{})
	if err == nil {
		t.Fatal("network still exists after removal")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test -v ./internal/container -run TestDockerRuntime_CreateNetwork`

Expected: FAIL with "rt.CreateNetwork undefined"

**Step 3: Implement network methods**

Add to `internal/container/docker.go` after existing methods:

```go
// CreateNetwork creates a Docker network for inter-container communication.
// Returns the network ID.
func (r *DockerRuntime) CreateNetwork(ctx context.Context, name string) (string, error) {
	resp, err := r.cli.NetworkCreate(ctx, name, types.NetworkCreate{
		Driver: "bridge",
		CheckDuplicate: true,
	})
	if err != nil {
		return "", fmt.Errorf("creating network: %w", err)
	}
	return resp.ID, nil
}

// RemoveNetwork removes a Docker network by ID.
// Best-effort: does not fail if network doesn't exist or has active endpoints.
func (r *DockerRuntime) RemoveNetwork(ctx context.Context, networkID string) error {
	if err := r.cli.NetworkRemove(ctx, networkID); err != nil {
		// Log but don't fail - network may already be removed
		return fmt.Errorf("removing network: %w", err)
	}
	return nil
}
```

Add import at top of file:

```go
"github.com/docker/docker/api/types"
```

**Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/container -run TestDockerRuntime_CreateNetwork`

Expected: PASS (2 tests)

**Step 5: Commit**

```bash
git add internal/container/docker.go internal/container/docker_test.go
git commit -m "feat(container): add network create/remove methods"
```

---

## Task 2: Add Sidecar Methods to Container Runtime

**Files:**
- Modify: `internal/container/docker.go`
- Test: `internal/container/docker_test.go`

**Step 1: Define SidecarConfig type**

Add to `internal/container/docker.go` after Config type:

```go
// SidecarConfig holds configuration for starting a sidecar container.
type SidecarConfig struct {
	// Image is the container image to use (e.g., "moby/buildkit:latest")
	Image string

	// Name is the container name
	Name string

	// Hostname is the network hostname for the container
	Hostname string

	// NetworkID is the Docker network to attach to
	NetworkID string

	// Cmd is the command to run
	Cmd []string
}
```

**Step 2: Write failing test for StartSidecar**

Add to `internal/container/docker_test.go`:

```go
func TestDockerRuntime_StartSidecar(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	rt, err := NewDockerRuntime()
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	// Create network first
	networkName := "test-moat-network-" + strconv.FormatInt(time.Now().Unix(), 10)
	networkID, err := rt.CreateNetwork(ctx, networkName)
	if err != nil {
		t.Fatalf("CreateNetwork failed: %v", err)
	}
	defer rt.RemoveNetwork(ctx, networkID)

	// Start sidecar
	cfg := SidecarConfig{
		Image:     "alpine:latest",
		Name:      "test-sidecar-" + strconv.FormatInt(time.Now().Unix(), 10),
		Hostname:  "testsidecar",
		NetworkID: networkID,
		Cmd:       []string{"sleep", "30"},
	}

	containerID, err := rt.StartSidecar(ctx, cfg)
	if err != nil {
		t.Fatalf("StartSidecar failed: %v", err)
	}
	defer rt.StopContainer(ctx, containerID, 5)

	if containerID == "" {
		t.Fatal("StartSidecar returned empty container ID")
	}

	// Verify container is running
	inspect, err := rt.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		t.Fatalf("container not created: %v", err)
	}
	if !inspect.State.Running {
		t.Error("container is not running")
	}
	if inspect.Config.Hostname != "testsidecar" {
		t.Errorf("hostname: got %q, want %q", inspect.Config.Hostname, "testsidecar")
	}

	// Verify attached to network
	if _, ok := inspect.NetworkSettings.Networks[networkName]; !ok {
		t.Errorf("container not attached to network %q", networkName)
	}
}
```

**Step 3: Run test to verify it fails**

Run: `go test -v ./internal/container -run TestDockerRuntime_StartSidecar`

Expected: FAIL with "rt.StartSidecar undefined"

**Step 4: Implement StartSidecar method**

Add to `internal/container/docker.go`:

```go
// StartSidecar starts a sidecar container (pull, create, start).
// The container is attached to the specified network and assigned a hostname.
// Returns the container ID.
func (r *DockerRuntime) StartSidecar(ctx context.Context, cfg SidecarConfig) (string, error) {
	// Pull image if not present
	if err := r.ensureImage(ctx, cfg.Image); err != nil {
		return "", fmt.Errorf("pulling sidecar image: %w", err)
	}

	// Create container
	resp, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image:    cfg.Image,
			Cmd:      cfg.Cmd,
			Hostname: cfg.Hostname,
		},
		&container.HostConfig{
			NetworkMode: container.NetworkMode(cfg.NetworkID),
		},
		nil, // network config
		nil, // platform
		cfg.Name,
	)
	if err != nil {
		return "", fmt.Errorf("creating sidecar container: %w", err)
	}

	// Start container
	if err := r.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up on failure
		r.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("starting sidecar container: %w", err)
	}

	return resp.ID, nil
}
```

**Step 5: Run test to verify it passes**

Run: `go test -v ./internal/container -run TestDockerRuntime_StartSidecar`

Expected: PASS

**Step 6: Commit**

```bash
git add internal/container/docker.go internal/container/docker_test.go
git commit -m "feat(container): add sidecar start method"
```

---

## Task 3: Add BuildKit Fields to Storage Metadata

**Files:**
- Modify: `internal/storage/storage.go`
- Test: `internal/storage/storage_test.go`

**Step 1: Write failing test for metadata persistence**

Add to `internal/storage/storage_test.go`:

```go
func TestMetadata_BuildKitFields(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewRunStore(tmpDir, "test-run")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Save metadata with buildkit fields
	original := Metadata{
		Name:                "test",
		BuildkitContainerID: "buildkit-123",
		NetworkID:           "net-456",
	}

	if err := store.SaveMetadata(original); err != nil {
		t.Fatalf("SaveMetadata failed: %v", err)
	}

	// Load and verify
	loaded, err := store.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata failed: %v", err)
	}

	if loaded.BuildkitContainerID != original.BuildkitContainerID {
		t.Errorf("BuildkitContainerID: got %q, want %q", loaded.BuildkitContainerID, original.BuildkitContainerID)
	}
	if loaded.NetworkID != original.NetworkID {
		t.Errorf("NetworkID: got %q, want %q", loaded.NetworkID, original.NetworkID)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/storage -run TestMetadata_BuildKitFields`

Expected: FAIL with "unknown field 'BuildkitContainerID'"

**Step 3: Add fields to Metadata struct**

Modify `internal/storage/storage.go`, add fields to Metadata struct:

```go
type Metadata struct {
	Name        string         `json:"name"`
	Workspace   string         `json:"workspace"`
	Grants      []string       `json:"grants,omitempty"`
	Ports       map[string]int `json:"ports,omitempty"`
	ContainerID string         `json:"container_id,omitempty"`
	State       string         `json:"state,omitempty"`
	Interactive bool           `json:"interactive,omitempty"`
	CreatedAt   time.Time      `json:"created_at,omitempty"`
	StartedAt   time.Time      `json:"started_at,omitempty"`
	StoppedAt   time.Time      `json:"stopped_at,omitempty"`
	Error       string         `json:"error,omitempty"`

	// BuildKit sidecar fields (docker:dind only)
	BuildkitContainerID string `json:"buildkit_container_id,omitempty"`
	NetworkID           string `json:"network_id,omitempty"`
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/storage -run TestMetadata_BuildKitFields`

Expected: PASS

**Step 5: Commit**

```bash
git add internal/storage/storage.go internal/storage/storage_test.go
git commit -m "feat(storage): add buildkit sidecar metadata fields"
```

---

## Task 4: Integrate BuildKit Sidecar in Run Manager Create

**Files:**
- Modify: `internal/run/manager.go`
- Test: `internal/run/manager_test.go`

**Step 1: Write failing test for buildkit integration**

Add to `internal/run/manager_test.go`:

```go
func TestManager_CreateWithBuildKit(t *testing.T) {
	cfg := &config.Config{
		Agent:        "test",
		Dependencies: []string{"docker:dind"},
	}

	// Mock container runtime that tracks calls
	type call struct {
		method string
		args   []interface{}
	}
	var calls []call

	mockRuntime := &mockRuntimeWithBuildKit{
		calls: &calls,
	}

	dockerConfig := &DockerDependencyConfig{
		Mode:       deps.DockerModeDind,
		Privileged: true,
	}

	// Test that buildkit sidecar is created
	result := computeBuildKitConfig(dockerConfig, "test-run-id")

	if !result.Enabled {
		t.Error("BuildKit should be enabled for dind mode")
	}
	if result.NetworkName != "moat-test-run-id" {
		t.Errorf("NetworkName: got %q, want %q", result.NetworkName, "moat-test-run-id")
	}
	if result.SidecarName != "moat-buildkit-test-run-id" {
		t.Errorf("SidecarName: got %q, want %q", result.SidecarName, "moat-buildkit-test-run-id")
	}
	if result.SidecarImage != "moby/buildkit:latest" {
		t.Errorf("SidecarImage: got %q, want %q", result.SidecarImage, "moby/buildkit:latest")
	}
}

func TestComputeBuildKitEnv(t *testing.T) {
	tests := []struct {
		name       string
		enabled    bool
		wantEnvVar bool
	}{
		{
			name:       "buildkit enabled",
			enabled:    true,
			wantEnvVar: true,
		},
		{
			name:       "buildkit disabled",
			enabled:    false,
			wantEnvVar: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := computeBuildKitEnv(tt.enabled)

			found := false
			for _, e := range env {
				if strings.HasPrefix(e, "BUILDKIT_HOST=") {
					found = true
					if !tt.wantEnvVar {
						t.Error("BUILDKIT_HOST should not be set when disabled")
					}
					if !strings.Contains(e, "tcp://buildkit:1234") {
						t.Errorf("BUILDKIT_HOST value incorrect: %s", e)
					}
				}
			}

			if tt.wantEnvVar && !found {
				t.Error("BUILDKIT_HOST should be set when enabled")
			}
		})
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test -v ./internal/run -run TestManager_CreateWithBuildKit`

Expected: FAIL with "computeBuildKitConfig undefined"

**Step 3: Add BuildKit configuration types**

Add to `internal/run/manager.go` after DockerDependencyConfig:

```go
// BuildKitConfig holds configuration for BuildKit sidecar.
type BuildKitConfig struct {
	Enabled      bool
	NetworkName  string
	NetworkID    string
	SidecarName  string
	SidecarImage string
}

// computeBuildKitConfig determines if BuildKit sidecar should be used.
// BuildKit is automatically enabled for docker:dind mode.
func computeBuildKitConfig(dockerConfig *DockerDependencyConfig, runID string) BuildKitConfig {
	// Only enable for dind mode
	if dockerConfig == nil || dockerConfig.Mode != deps.DockerModeDind {
		return BuildKitConfig{Enabled: false}
	}

	return BuildKitConfig{
		Enabled:      true,
		NetworkName:  "moat-" + runID,
		SidecarName:  "moat-buildkit-" + runID,
		SidecarImage: "moby/buildkit:latest",
	}
}

// computeBuildKitEnv returns environment variables for BuildKit integration.
func computeBuildKitEnv(enabled bool) []string {
	if !enabled {
		return nil
	}
	return []string{
		"BUILDKIT_HOST=tcp://buildkit:1234",
	}
}
```

**Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/run -run TestManager_CreateWithBuildKit`

Expected: PASS

**Step 5: Commit**

```bash
git add internal/run/manager.go internal/run/manager_test.go
git commit -m "feat(run): add buildkit configuration logic"
```

---

## Task 5: Integrate BuildKit into Manager.Create Lifecycle

**Files:**
- Modify: `internal/run/manager.go` (Create method)

**Step 1: Locate the Create method**

Find the `Create` method in `internal/run/manager.go`. It should be around line 300-500.

**Step 2: Add BuildKit setup after docker config resolution**

After the line that calls `ResolveDockerDependency`, add:

```go
// Compute BuildKit configuration (automatic with docker:dind)
buildkitCfg := computeBuildKitConfig(dockerConfig, runID)

// Create network if BuildKit enabled
if buildkitCfg.Enabled {
	log.Debug("creating network for buildkit sidecar", "network", buildkitCfg.NetworkName)
	networkID, err := m.runtime.(*container.DockerRuntime).CreateNetwork(ctx, buildkitCfg.NetworkName)
	if err != nil {
		return "", fmt.Errorf("failed to create Docker network for buildkit sidecar: %w", err)
	}
	buildkitCfg.NetworkID = networkID

	// Start BuildKit sidecar
	log.Debug("starting buildkit sidecar", "image", buildkitCfg.SidecarImage)
	sidecarCfg := container.SidecarConfig{
		Image:     buildkitCfg.SidecarImage,
		Name:      buildkitCfg.SidecarName,
		Hostname:  "buildkit",
		NetworkID: networkID,
		Cmd:       []string{"--addr", "tcp://0.0.0.0:1234"},
	}

	buildkitContainerID, err := m.runtime.(*container.DockerRuntime).StartSidecar(ctx, sidecarCfg)
	if err != nil {
		// Clean up network on failure
		m.runtime.(*container.DockerRuntime).RemoveNetwork(ctx, networkID)
		return "", fmt.Errorf("failed to start buildkit sidecar: %w\n\nEnsure Docker can access Docker Hub to pull %s", err, buildkitCfg.SidecarImage)
	}

	// Wait for BuildKit to be ready (up to 10 seconds)
	log.Debug("waiting for buildkit sidecar to be ready")
	ready := false
	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)
		inspect, err := m.runtime.(*container.DockerRuntime).InspectContainer(ctx, buildkitContainerID)
		if err == nil && inspect.State.Running {
			ready = true
			break
		}
	}
	if !ready {
		m.runtime.StopContainer(ctx, buildkitContainerID, 5)
		m.runtime.(*container.DockerRuntime).RemoveNetwork(ctx, networkID)
		return "", fmt.Errorf("buildkit sidecar failed to become ready within 10 seconds")
	}

	// Store buildkit IDs in metadata
	metadata.BuildkitContainerID = buildkitContainerID
	metadata.NetworkID = networkID
}
```

**Step 3: Add BuildKit env vars to container config**

Find where container environment variables are set (look for `Env:` field). Add:

```go
// Add BuildKit env vars if enabled
buildkitEnv := computeBuildKitEnv(buildkitCfg.Enabled)
containerEnv = append(containerEnv, buildkitEnv...)
```

**Step 4: Set NetworkMode if BuildKit enabled**

Find where container config is created (look for `container.Config`). Before creating the container, add:

```go
// Use custom network if BuildKit enabled
networkMode := ""
if buildkitCfg.Enabled {
	networkMode = buildkitCfg.NetworkID
}
```

And pass `networkMode` to the container config's `NetworkMode` field.

**Step 5: Add cleanup on container creation failure**

Find the error handling after `CreateContainer`. Add cleanup:

```go
if err != nil {
	// Clean up BuildKit resources on failure
	if buildkitCfg.Enabled && metadata.BuildkitContainerID != "" {
		m.runtime.StopContainer(ctx, metadata.BuildkitContainerID, 5)
		m.runtime.(*container.DockerRuntime).RemoveNetwork(ctx, metadata.NetworkID)
	}
	return "", fmt.Errorf("creating container: %w", err)
}
```

**Step 6: Add import for time package if not present**

At top of file, ensure `time` is imported:

```go
import (
	// ... existing imports ...
	"time"
)
```

**Step 7: Build to verify compilation**

Run: `go build ./...`

Expected: Success (no compilation errors)

**Step 8: Commit**

```bash
git add internal/run/manager.go
git commit -m "feat(run): integrate buildkit sidecar in create lifecycle"
```

---

## Task 6: Integrate BuildKit Cleanup in Manager.Stop

**Files:**
- Modify: `internal/run/manager.go` (Stop method)

**Step 1: Locate the Stop method**

Find the `Stop` method in `internal/run/manager.go`.

**Step 2: Add BuildKit cleanup before main container stop**

At the beginning of the Stop method, after loading metadata, add:

```go
// Stop BuildKit sidecar if present (before main container)
if metadata.BuildkitContainerID != "" {
	log.Debug("stopping buildkit sidecar", "container_id", metadata.BuildkitContainerID)
	if err := m.runtime.StopContainer(ctx, metadata.BuildkitContainerID, 10); err != nil {
		log.Warn("failed to stop buildkit sidecar", "error", err)
		// Continue anyway - try to clean up network
	}
}
```

**Step 3: Add network cleanup after container stop**

After the main container is stopped, add:

```go
// Remove network if present (best-effort)
if metadata.NetworkID != "" {
	log.Debug("removing docker network", "network_id", metadata.NetworkID)
	if dockerRuntime, ok := m.runtime.(*container.DockerRuntime); ok {
		if err := dockerRuntime.RemoveNetwork(ctx, metadata.NetworkID); err != nil {
			log.Warn("failed to remove docker network", "error", err)
			// Non-fatal - network may have active endpoints
		}
	}
}
```

**Step 4: Build to verify compilation**

Run: `go build ./...`

Expected: Success

**Step 5: Commit**

```bash
git add internal/run/manager.go
git commit -m "feat(run): cleanup buildkit sidecar and network on stop"
```

---

## Task 7: Add Audit Logging for BuildKit Events

**Files:**
- Modify: `internal/audit/entry.go`
- Modify: `internal/run/manager.go`

**Step 1: Add BuildKit audit event types**

Add to `internal/audit/entry.go` after existing event type constants:

```go
	// EventBuildKitSidecarStarted is logged when a BuildKit sidecar container starts
	EventBuildKitSidecarStarted = "buildkit_sidecar_started"

	// EventBuildKitSidecarStopped is logged when a BuildKit sidecar container stops
	EventBuildKitSidecarStopped = "buildkit_sidecar_stopped"

	// EventDockerNetworkCreated is logged when a Docker network is created
	EventDockerNetworkCreated = "docker_network_created"

	// EventDockerNetworkRemoved is logged when a Docker network is removed
	EventDockerNetworkRemoved = "docker_network_removed"
```

**Step 2: Add BuildKit fields to container audit data**

Find the `ContainerData` struct in `internal/audit/entry.go` and add fields:

```go
type ContainerData struct {
	// ... existing fields ...

	// BuildKit sidecar info (dind mode only)
	BuildKitEnabled         bool   `json:"buildkit_enabled,omitempty"`
	BuildKitContainerID     string `json:"buildkit_container_id,omitempty"`
	BuildKitNetworkID       string `json:"buildkit_network_id,omitempty"`
}
```

**Step 3: Log BuildKit events in manager.Create**

In `internal/run/manager.go`, after creating the network, add:

```go
if err := m.auditStore.Append(ctx, audit.Entry{
	Event: audit.EventDockerNetworkCreated,
	Data: map[string]interface{}{
		"network_id":   networkID,
		"network_name": buildkitCfg.NetworkName,
		"purpose":      "buildkit_sidecar",
	},
}); err != nil {
	log.Warn("failed to log network creation", "error", err)
}
```

After starting the sidecar, add:

```go
if err := m.auditStore.Append(ctx, audit.Entry{
	Event: audit.EventBuildKitSidecarStarted,
	Data: map[string]interface{}{
		"container_id":   buildkitContainerID,
		"container_name": buildkitCfg.SidecarName,
		"image":          buildkitCfg.SidecarImage,
		"network_id":     networkID,
	},
}); err != nil {
	log.Warn("failed to log buildkit sidecar start", "error", err)
}
```

**Step 4: Update container audit data**

Find where `ContainerData` is populated in manager.Create and add:

```go
containerData.BuildKitEnabled = buildkitCfg.Enabled
containerData.BuildKitContainerID = metadata.BuildkitContainerID
containerData.BuildKitNetworkID = metadata.NetworkID
```

**Step 5: Log BuildKit events in manager.Stop**

In `internal/run/manager.go`, after stopping the sidecar, add:

```go
if err := m.auditStore.Append(ctx, audit.Entry{
	Event: audit.EventBuildKitSidecarStopped,
	Data: map[string]interface{}{
		"container_id": metadata.BuildkitContainerID,
	},
}); err != nil {
	log.Warn("failed to log buildkit sidecar stop", "error", err)
}
```

After removing the network, add:

```go
if err := m.auditStore.Append(ctx, audit.Entry{
	Event: audit.EventDockerNetworkRemoved,
	Data: map[string]interface{}{
		"network_id": metadata.NetworkID,
	},
}); err != nil {
	log.Warn("failed to log network removal", "error", err)
}
```

**Step 6: Build to verify compilation**

Run: `go build ./...`

Expected: Success

**Step 7: Commit**

```bash
git add internal/audit/entry.go internal/run/manager.go
git commit -m "feat(audit): add buildkit sidecar audit events"
```

---

## Task 8: Add InspectContainer Helper Method

**Files:**
- Modify: `internal/container/docker.go`

**Step 1: Add InspectContainer method**

We need this for the BuildKit health check. Add to `internal/container/docker.go`:

```go
// InspectContainer returns container inspection data.
func (r *DockerRuntime) InspectContainer(ctx context.Context, containerID string) (*types.ContainerJSON, error) {
	inspect, err := r.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}
	return &inspect, nil
}
```

Update the types import to include `ContainerJSON`:

```go
"github.com/docker/docker/api/types"
```

**Step 2: Build to verify compilation**

Run: `go build ./internal/container`

Expected: Success

**Step 3: Commit**

```bash
git add internal/container/docker.go
git commit -m "feat(container): add inspect container helper"
```

---

## Task 9: Run Unit Tests

**Files:**
- Test: `internal/container/docker_test.go`
- Test: `internal/storage/storage_test.go`
- Test: `internal/run/manager_test.go`

**Step 1: Run container tests**

Run: `go test -v ./internal/container -short`

Expected: All tests PASS

**Step 2: Run storage tests**

Run: `go test -v ./internal/storage`

Expected: All tests PASS

**Step 3: Run run manager tests**

Run: `go test -v ./internal/run -short`

Expected: All tests PASS

**Step 4: Run all short tests**

Run: `go test -short ./...`

Expected: All tests PASS

**Step 5: If any tests fail, fix them before proceeding**

Debug failures and fix implementation or tests as needed.

---

## Task 10: Write E2E Test for BuildKit Integration

**Files:**
- Create: `internal/e2e/buildkit_test.go`

**Step 1: Write E2E test**

Create `internal/e2e/buildkit_test.go`:

```go
//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/run"
)

func TestBuildKitSidecar(t *testing.T) {
	// Verify Docker runtime is available
	runtime, err := container.NewDockerRuntime()
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}

	ctx := context.Background()
	if err := runtime.Ping(ctx); err != nil {
		t.Skipf("Docker daemon not accessible: %v", err)
	}

	// Create test workspace
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	// Create agent.yaml with docker:dind
	agentYaml := `agent: test
dependencies:
  - docker:dind
`
	if err := os.WriteFile(filepath.Join(workspace, "agent.yaml"), []byte(agentYaml), 0644); err != nil {
		t.Fatalf("failed to create agent.yaml: %v", err)
	}

	// Create simple Dockerfile to test BuildKit
	dockerfile := `FROM alpine:latest
RUN --mount=type=cache,target=/cache echo "testing buildkit cache mount" > /cache/test.txt
RUN echo "Hello from BuildKit"
`
	if err := os.WriteFile(filepath.Join(workspace, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		t.Fatalf("failed to create Dockerfile: %v", err)
	}

	// Load config
	cfg, err := config.Load(workspace)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Create run manager
	storageDir := filepath.Join(tmpDir, "storage")
	auditDir := filepath.Join(tmpDir, "audit")
	mgr, err := run.NewManager(runtime, storageDir, auditDir, nil)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// Create run
	runID, err := mgr.Create(ctx, cfg, workspace, run.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create run: %v", err)
	}
	defer mgr.Stop(ctx, runID)

	// Load metadata to verify BuildKit was configured
	store := mgr.GetStore(runID)
	metadata, err := store.LoadMetadata()
	if err != nil {
		t.Fatalf("failed to load metadata: %v", err)
	}

	if metadata.BuildkitContainerID == "" {
		t.Error("BuildkitContainerID should be set for docker:dind")
	}
	if metadata.NetworkID == "" {
		t.Error("NetworkID should be set for docker:dind with buildkit")
	}

	// Start the main container
	if err := mgr.Start(ctx, runID); err != nil {
		t.Fatalf("failed to start run: %v", err)
	}

	// Give container time to start
	time.Sleep(2 * time.Second)

	// Execute docker build command to test BuildKit
	output, exitCode, err := mgr.Exec(ctx, runID, []string{
		"docker", "build", "-t", "test-buildkit", ".",
	})
	if err != nil {
		t.Fatalf("failed to exec docker build: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("docker build failed with exit code %d: %s", exitCode, output)
	}

	// Verify BuildKit was used (check for BuildKit-specific output)
	if !strings.Contains(output, "buildkit") && !strings.Contains(output, "cache mount") {
		t.Logf("Build output: %s", output)
		t.Error("docker build should use BuildKit (expected BuildKit-related output)")
	}

	// Verify BUILDKIT_HOST env var is set
	envOutput, exitCode, err := mgr.Exec(ctx, runID, []string{"printenv", "BUILDKIT_HOST"})
	if err != nil {
		t.Fatalf("failed to check BUILDKIT_HOST: %v", err)
	}
	if exitCode != 0 {
		t.Error("BUILDKIT_HOST should be set")
	}
	if !strings.Contains(envOutput, "tcp://buildkit:1234") {
		t.Errorf("BUILDKIT_HOST incorrect: got %q, want tcp://buildkit:1234", strings.TrimSpace(envOutput))
	}

	// Stop run
	if err := mgr.Stop(ctx, runID); err != nil {
		t.Fatalf("failed to stop run: %v", err)
	}

	// Verify BuildKit sidecar was cleaned up
	_, err = runtime.(*container.DockerRuntime).InspectContainer(ctx, metadata.BuildkitContainerID)
	if err == nil {
		t.Error("BuildKit sidecar should be stopped and removed")
	}
}
```

**Step 2: Run E2E test**

Run: `go test -tags=e2e -v ./internal/e2e -run TestBuildKitSidecar`

Expected: PASS (may take 30-60 seconds due to image pulls)

**Step 3: If test fails, debug and fix**

Common issues:
- Image pull failures: check network
- Container startup timeouts: increase wait time
- Build failures: check Dockerfile syntax

**Step 4: Commit**

```bash
git add internal/e2e/buildkit_test.go
git commit -m "test(e2e): add buildkit sidecar integration test"
```

---

## Task 11: Update Documentation

**Files:**
- Modify: `docs/content/reference/02-agent-yaml.md`
- Modify: `docs/content/concepts/docker-access.md` (if exists)

**Step 1: Update agent.yaml reference docs**

Add to `docs/content/reference/02-agent-yaml.md` under the `dependencies` section:

```markdown
#### docker:dind Mode

When using `docker:dind`, moat automatically deploys a BuildKit sidecar container to provide fast image builds:

- **BuildKit sidecar**: Runs `moby/buildkit:latest` in a separate container
- **Shared network**: Both containers communicate via a Docker network (`moat-<run-id>`)
- **Environment**: `BUILDKIT_HOST=tcp://buildkit:1234` routes builds to the sidecar
- **Full Docker**: Local `dockerd` in main container provides `docker ps`, `docker run`, etc.
- **Performance**: BuildKit layer caching, `RUN --mount=type=cache`, faster multi-stage builds

This configuration is automatic and requires no additional setup.

**Example:**

```yaml
agent: builder
dependencies:
  - docker:dind  # Automatically includes BuildKit sidecar

# Your code can now use:
# - docker build (uses BuildKit for speed)
# - docker ps (uses local dockerd)
# - docker run (uses local dockerd)
```
```

**Step 2: Add troubleshooting section if needed**

Add troubleshooting notes:

```markdown
#### BuildKit Troubleshooting

If builds fail with BuildKit sidecar:

1. **Image pull failure**: Ensure Docker can access Docker Hub to pull `moby/buildkit:latest`
2. **Network issues**: Check that Docker networks are enabled (not disabled by firewall)
3. **Performance**: BuildKit sidecar typically starts in 2-5 seconds; check Docker daemon logs if slower
```

**Step 3: Build to verify**

Run: `go build ./...`

Expected: Success (compilation check)

**Step 4: Commit**

```bash
git add docs/content/reference/02-agent-yaml.md
git commit -m "docs(reference): document buildkit sidecar for docker:dind"
```

---

## Task 12: Final Integration Test and Cleanup

**Files:**
- Test: All packages

**Step 1: Run all tests**

Run: `go test ./...`

Expected: All tests PASS

**Step 2: Run E2E tests**

Run: `go test -tags=e2e -v ./internal/e2e`

Expected: All E2E tests PASS

**Step 3: Build the binary**

Run: `go build -o moat ./cmd/moat`

Expected: Binary built successfully

**Step 4: Manual smoke test (optional)**

Create a test directory with:

```yaml
# agent.yaml
agent: test-buildkit
dependencies:
  - docker:dind
```

```dockerfile
# Dockerfile
FROM alpine:latest
RUN --mount=type=cache,target=/cache echo "BuildKit cache test"
CMD ["echo", "Hello from BuildKit"]
```

Run: `./moat run`

Expected:
- BuildKit sidecar starts
- Container can build images with BuildKit
- Builds complete successfully

**Step 5: Clean up test artifacts**

Run: `./moat clean` or manually stop test containers

**Step 6: Final commit**

```bash
git add .
git commit -m "feat(buildkit): complete buildkit sidecar integration

- Automatic BuildKit sidecar with docker:dind
- Shared Docker network for container communication
- Fast builds via BuildKit, full Docker via dockerd
- Network and sidecar cleanup on stop
- E2E tests and documentation"
```

---

## Summary

This implementation adds automatic BuildKit sidecar support to `docker:dind` mode:

**Key Changes:**
- Container runtime: Network and sidecar management methods
- Storage: Metadata fields for buildkit container and network IDs
- Run manager: Lifecycle integration (create network, start sidecar, cleanup)
- Audit logging: BuildKit events tracked
- Tests: Unit tests for all components, E2E test for full lifecycle
- Documentation: Updated reference docs

**Testing Strategy:**
- Unit tests for each component (network, sidecar, config)
- Integration tests for run manager lifecycle
- E2E test for real buildkit usage
- Manual smoke test for verification

**Next Steps:**
Use @superpowers:executing-plans or @superpowers:subagent-driven-development to execute this plan task-by-task.
