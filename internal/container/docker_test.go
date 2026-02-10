package container

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

func TestConfig_GroupAdd(t *testing.T) {
	// Verify that GroupAdd field can be set on Config struct
	// and is properly typed as []string
	cfg := Config{
		Name:     "test-container",
		Image:    "ubuntu:22.04",
		GroupAdd: []string{"999", "docker"},
	}

	// Verify the GroupAdd field is set correctly
	if len(cfg.GroupAdd) != 2 {
		t.Errorf("expected GroupAdd to have 2 elements, got %d", len(cfg.GroupAdd))
	}
	if cfg.GroupAdd[0] != "999" {
		t.Errorf("expected GroupAdd[0] to be '999', got %q", cfg.GroupAdd[0])
	}
	if cfg.GroupAdd[1] != "docker" {
		t.Errorf("expected GroupAdd[1] to be 'docker', got %q", cfg.GroupAdd[1])
	}
}

func TestConfig_GroupAddEmpty(t *testing.T) {
	// Verify that Config works correctly with empty GroupAdd
	cfg := Config{
		Name:  "test-container",
		Image: "ubuntu:22.04",
	}

	// GroupAdd should be nil by default
	if cfg.GroupAdd != nil {
		t.Errorf("expected GroupAdd to be nil by default, got %v", cfg.GroupAdd)
	}
}

func TestConfig_Privileged(t *testing.T) {
	// Verify that Privileged field can be set on Config struct
	cfg := Config{
		Name:       "test-container",
		Image:      "ubuntu:22.04",
		Privileged: true,
	}

	// Verify the Privileged field is set correctly
	if !cfg.Privileged {
		t.Errorf("expected Privileged to be true, got false")
	}
}

func TestConfig_PrivilegedDefault(t *testing.T) {
	// Verify that Config defaults to non-privileged mode
	cfg := Config{
		Name:  "test-container",
		Image: "ubuntu:22.04",
	}

	// Privileged should be false by default
	if cfg.Privileged {
		t.Errorf("expected Privileged to be false by default, got true")
	}
}

func TestDockerRuntime_Type(t *testing.T) {
	// Test that DockerRuntime returns correct type
	// Note: This doesn't require a Docker daemon
	r := &DockerRuntime{}
	if r.Type() != RuntimeDocker {
		t.Errorf("Type() = %v, want %v", r.Type(), RuntimeDocker)
	}
}

func TestDockerRuntime_GVisorCaching(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rt, err := NewDockerRuntime(false)
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}
	defer rt.Close()

	// First call - should check and cache
	result1 := rt.gvisorAvailable()

	// Second call - should use cached result
	result2 := rt.gvisorAvailable()

	// Results should be consistent
	if result1 != result2 {
		t.Errorf("gvisorAvailable returned inconsistent results: first=%v, second=%v", result1, result2)
	}

	// Verify the cached value matches the result
	if rt.gvisorAvail != result1 {
		t.Errorf("cached value doesn't match result: cached=%v, result=%v", rt.gvisorAvail, result1)
	}
}

func TestDockerRuntime_GVisorCaching_Concurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	rt, err := NewDockerRuntime(false)
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}
	defer rt.Close()

	// Run 10 concurrent calls to verify thread safety and consistent results
	const numGoroutines = 10
	results := make([]bool, numGoroutines)
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = rt.gvisorAvailable()
		}(i)
	}
	wg.Wait()

	// All results should be identical (validates sync.Once correctness)
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Errorf("concurrent call %d returned %v, expected %v", i, results[i], results[0])
		}
	}

	// Verify the cached value matches all results
	if rt.gvisorAvail != results[0] {
		t.Errorf("cached value %v doesn't match results %v", rt.gvisorAvail, results[0])
	}
}

func TestDockerRuntime_CreateNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	rt, err := NewDockerRuntime(false)
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	networkName := "test-moat-network-" + strconv.FormatInt(time.Now().Unix(), 10)
	networkID, err := rt.NetworkManager().CreateNetwork(ctx, networkName)
	if err != nil {
		t.Fatalf("CreateNetwork failed: %v", err)
	}
	defer rt.NetworkManager().RemoveNetwork(ctx, networkID)

	if networkID == "" {
		t.Fatal("CreateNetwork returned empty network ID")
	}

	// Verify network exists
	inspect, err := rt.cli.NetworkInspect(ctx, networkID, network.InspectOptions{})
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
	rt, err := NewDockerRuntime(false)
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	networkName := "test-moat-network-" + strconv.FormatInt(time.Now().Unix(), 10)
	networkID, err := rt.NetworkManager().CreateNetwork(ctx, networkName)
	if err != nil {
		t.Fatalf("CreateNetwork failed: %v", err)
	}

	if err := rt.NetworkManager().RemoveNetwork(ctx, networkID); err != nil {
		t.Fatalf("RemoveNetwork failed: %v", err)
	}

	// Verify network is gone
	_, err = rt.cli.NetworkInspect(ctx, networkID, network.InspectOptions{})
	if err == nil {
		t.Fatal("network still exists after removal")
	}
}

func TestDockerRuntime_RemoveNetwork_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	rt, err := NewDockerRuntime(false)
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	// Try to remove a network that doesn't exist
	if err := rt.NetworkManager().RemoveNetwork(ctx, "nonexistent-network-id"); err != nil {
		t.Fatalf("RemoveNetwork should not fail for non-existent network: %v", err)
	}
}

func TestDockerRuntime_RemoveNetwork_ActiveEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	rt, err := NewDockerRuntime(false)
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	// Create a network
	networkName := "test-moat-network-" + strconv.FormatInt(time.Now().Unix(), 10)
	networkID, err := rt.NetworkManager().CreateNetwork(ctx, networkName)
	if err != nil {
		t.Fatalf("CreateNetwork failed: %v", err)
	}
	defer rt.NetworkManager().ForceRemoveNetwork(ctx, networkID)

	// Create a container attached to the network
	containerName := "test-moat-container-" + strconv.FormatInt(time.Now().Unix(), 10)
	containerID, err := rt.CreateContainer(ctx, Config{
		Name:        containerName,
		Image:       "alpine:latest",
		Cmd:         []string{"sleep", "10"},
		NetworkMode: networkName, // Attach to the network
	})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	defer rt.RemoveContainer(ctx, containerID)

	// Start the container to create an active endpoint
	if err := rt.StartContainer(ctx, containerID); err != nil {
		t.Fatalf("StartContainer failed: %v", err)
	}

	// Try to remove the network while the container is running
	// This should now return an error (active endpoints are no longer silently swallowed)
	if err := rt.NetworkManager().RemoveNetwork(ctx, networkID); err == nil {
		t.Fatal("RemoveNetwork should fail for network with active endpoints")
	}

	// ForceRemoveNetwork should succeed even with active endpoints
	if err := rt.NetworkManager().ForceRemoveNetwork(ctx, networkID); err != nil {
		t.Fatalf("ForceRemoveNetwork failed: %v", err)
	}

	// Verify network is gone
	_, err = rt.cli.NetworkInspect(ctx, networkID, network.InspectOptions{})
	if err == nil {
		t.Fatal("network still exists after force removal")
	}

	// Cleanup: stop and remove the container
	rt.StopContainer(ctx, containerID)
	rt.RemoveContainer(ctx, containerID)
}

func TestDockerRuntime_ForceRemoveNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	rt, err := NewDockerRuntime(false)
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	// Create a network
	networkName := "test-moat-network-" + strconv.FormatInt(time.Now().Unix(), 10)
	networkID, err := rt.NetworkManager().CreateNetwork(ctx, networkName)
	if err != nil {
		t.Fatalf("CreateNetwork failed: %v", err)
	}

	// Create and start a container attached to the network
	containerName := "test-moat-container-" + strconv.FormatInt(time.Now().Unix(), 10)
	containerID, err := rt.CreateContainer(ctx, Config{
		Name:        containerName,
		Image:       "alpine:latest",
		Cmd:         []string{"sleep", "30"},
		NetworkMode: networkName,
	})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	defer rt.RemoveContainer(ctx, containerID)

	if err := rt.StartContainer(ctx, containerID); err != nil {
		t.Fatalf("StartContainer failed: %v", err)
	}

	// ForceRemoveNetwork should disconnect the container and remove the network
	if err := rt.NetworkManager().ForceRemoveNetwork(ctx, networkID); err != nil {
		t.Fatalf("ForceRemoveNetwork failed: %v", err)
	}

	// Verify network is gone
	_, err = rt.cli.NetworkInspect(ctx, networkID, network.InspectOptions{})
	if err == nil {
		t.Fatal("network still exists after force removal")
	}

	// Cleanup
	rt.StopContainer(ctx, containerID)
}

func TestDockerRuntime_ForceRemoveNetwork_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	rt, err := NewDockerRuntime(false)
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	// ForceRemoveNetwork should not fail for a nonexistent network
	if err := rt.NetworkManager().ForceRemoveNetwork(ctx, "nonexistent-network-id"); err != nil {
		t.Fatalf("ForceRemoveNetwork should not fail for non-existent network: %v", err)
	}
}

func TestDockerRuntime_ListNetworks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	rt, err := NewDockerRuntime(false)
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	// Create a moat-managed network (CreateNetwork adds moat.managed label)
	networkName := "test-moat-network-" + strconv.FormatInt(time.Now().Unix(), 10)
	networkID, err := rt.NetworkManager().CreateNetwork(ctx, networkName)
	if err != nil {
		t.Fatalf("CreateNetwork failed: %v", err)
	}
	defer rt.NetworkManager().RemoveNetwork(ctx, networkID)

	// ListNetworks should include the network we just created
	networks, err := rt.NetworkManager().ListNetworks(ctx)
	if err != nil {
		t.Fatalf("ListNetworks failed: %v", err)
	}

	found := false
	for _, n := range networks {
		if n.ID == networkID {
			found = true
			if n.Name != networkName {
				t.Errorf("network name: got %q, want %q", n.Name, networkName)
			}
			break
		}
	}
	if !found {
		t.Errorf("created network %s not found in ListNetworks results", networkName)
	}
}

func TestDockerRuntime_StartSidecar(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	rt, err := NewDockerRuntime(false)
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	// Create network first
	networkName := "test-moat-network-" + strconv.FormatInt(time.Now().Unix(), 10)
	networkID, err := rt.NetworkManager().CreateNetwork(ctx, networkName)
	if err != nil {
		t.Fatalf("CreateNetwork failed: %v", err)
	}
	defer rt.NetworkManager().RemoveNetwork(ctx, networkID)

	// Start sidecar
	cfg := SidecarConfig{
		Image:     "alpine:latest",
		Name:      "test-sidecar-" + strconv.FormatInt(time.Now().Unix(), 10),
		Hostname:  "testsidecar",
		NetworkID: networkID,
		Cmd:       []string{"sleep", "30"},
	}

	containerID, err := rt.SidecarManager().StartSidecar(ctx, cfg)
	if err != nil {
		t.Fatalf("StartSidecar failed: %v", err)
	}
	defer rt.StopContainer(ctx, containerID)

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

func TestDockerRuntime_StartSidecar_ValidationEmptyImage(t *testing.T) {
	ctx := context.Background()
	rt := &DockerRuntime{}
	rt.sidecarMgr = &dockerSidecarManager{}

	cfg := SidecarConfig{
		Image:     "", // Empty image
		Name:      "test-sidecar",
		Hostname:  "testsidecar",
		NetworkID: "network-123",
		Cmd:       []string{"sleep", "30"},
	}

	_, err := rt.SidecarManager().StartSidecar(ctx, cfg)
	if err == nil {
		t.Fatal("StartSidecar should fail with empty image")
	}
	if err.Error() != "sidecar image cannot be empty" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDockerRuntime_StartSidecar_ValidationEmptyNetworkID(t *testing.T) {
	ctx := context.Background()
	rt := &DockerRuntime{}
	rt.sidecarMgr = &dockerSidecarManager{}

	cfg := SidecarConfig{
		Image:     "alpine:latest",
		Name:      "test-sidecar",
		Hostname:  "testsidecar",
		NetworkID: "", // Empty network ID
		Cmd:       []string{"sleep", "30"},
	}

	_, err := rt.SidecarManager().StartSidecar(ctx, cfg)
	if err == nil {
		t.Fatal("StartSidecar should fail with empty network ID")
	}
	if err.Error() != "sidecar network ID cannot be empty" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDockerRuntime_StartSidecar_ValidationEmptyName(t *testing.T) {
	ctx := context.Background()
	rt := &DockerRuntime{}
	rt.sidecarMgr = &dockerSidecarManager{}

	cfg := SidecarConfig{
		Image:     "alpine:latest",
		Name:      "", // Empty name
		Hostname:  "testsidecar",
		NetworkID: "network-123",
		Cmd:       []string{"sleep", "30"},
	}

	_, err := rt.SidecarManager().StartSidecar(ctx, cfg)
	if err == nil {
		t.Fatal("StartSidecar should fail with empty name")
	}
	if err.Error() != "sidecar name cannot be empty" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDockerRuntime_BuildImage_PathSelection(t *testing.T) {
	tests := []struct {
		name            string
		buildkitHost    string
		disableBuildKit string
		wantLegacy      bool // true if we expect the legacy builder path
	}{
		{
			name:         "uses standalone buildkit when BUILDKIT_HOST is set",
			buildkitHost: "tcp://192.0.2.1:1", // non-routable TEST-NET-1 address (RFC 5737)
			wantLegacy:   false,
		},
		{
			name:            "uses legacy builder when MOAT_DISABLE_BUILDKIT is set",
			disableBuildKit: "1",
			wantLegacy:      true,
		},
		{
			name:       "tries embedded buildkit then falls back to legacy builder",
			wantLegacy: true, // embedded fails (non-existent socket), falls back
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore env vars
			oldBuildKitHost := os.Getenv("BUILDKIT_HOST")
			oldDisableBuildKit := os.Getenv("MOAT_DISABLE_BUILDKIT")
			defer func() {
				if oldBuildKitHost != "" {
					os.Setenv("BUILDKIT_HOST", oldBuildKitHost)
				} else {
					os.Unsetenv("BUILDKIT_HOST")
				}
				if oldDisableBuildKit != "" {
					os.Setenv("MOAT_DISABLE_BUILDKIT", oldDisableBuildKit)
				} else {
					os.Unsetenv("MOAT_DISABLE_BUILDKIT")
				}
			}()

			// Set env vars for this test
			if tt.buildkitHost != "" {
				os.Setenv("BUILDKIT_HOST", tt.buildkitHost)
			} else {
				os.Unsetenv("BUILDKIT_HOST")
			}
			if tt.disableBuildKit != "" {
				os.Setenv("MOAT_DISABLE_BUILDKIT", tt.disableBuildKit)
			} else {
				os.Unsetenv("MOAT_DISABLE_BUILDKIT")
			}

			// Create a Docker client pointing to a non-existent socket.
			// This avoids nil-pointer panics and produces proper errors from each path.
			cli, err := client.NewClientWithOpts(client.WithHost("unix:///nonexistent.sock"))
			if err != nil {
				t.Fatalf("failed to create docker client: %v", err)
			}
			defer cli.Close()

			mgr := &dockerBuildManager{cli: cli}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			err = mgr.BuildImage(ctx, "FROM alpine:latest\n", "test:latest", BuildOptions{})
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			// Verify routing by checking whether the error came from the legacy builder.
			// The legacy builder always wraps errors as "building image: ..." from
			// buildImageWithBuilder's call to m.cli.ImageBuild. The standalone BuildKit
			// path never reaches that code — its errors come from WaitForReady or Solve.
			errMsg := strings.ToLower(err.Error())
			gotLegacy := strings.Contains(errMsg, "building image")

			if tt.wantLegacy && !gotLegacy {
				t.Errorf("expected legacy builder path (error containing \"building image\"), got: %v", err)
			}
			if !tt.wantLegacy && gotLegacy {
				t.Errorf("expected standalone buildkit path, but got legacy builder error: %v", err)
			}
		})
	}
}
