package container

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/network"
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
	defer rt.NetworkManager().RemoveNetwork(ctx, networkID)

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
	// This should NOT fail (best-effort cleanup)
	if err := rt.NetworkManager().RemoveNetwork(ctx, networkID); err != nil {
		t.Fatalf("RemoveNetwork should not fail for network with active endpoints: %v", err)
	}

	// Cleanup: stop and remove the container
	rt.StopContainer(ctx, containerID)
	rt.RemoveContainer(ctx, containerID)
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
		name               string
		buildkitHost       string
		expectBuildKitPath bool
	}{
		{
			name:               "uses buildkit when BUILDKIT_HOST is set",
			buildkitHost:       "tcp://192.0.2.1:1", // non-routable TEST-NET-1 address (RFC 5737)
			expectBuildKitPath: true,
		},
		{
			name:               "uses docker sdk when BUILDKIT_HOST is empty",
			buildkitHost:       "",
			expectBuildKitPath: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore BUILDKIT_HOST
			oldBuildKitHost := os.Getenv("BUILDKIT_HOST")
			defer func() {
				if oldBuildKitHost != "" {
					os.Setenv("BUILDKIT_HOST", oldBuildKitHost)
				} else {
					os.Unsetenv("BUILDKIT_HOST")
				}
			}()

			// Set BUILDKIT_HOST for this test
			if tt.buildkitHost != "" {
				os.Setenv("BUILDKIT_HOST", tt.buildkitHost)
			} else {
				os.Unsetenv("BUILDKIT_HOST")
			}

			// Create a minimal runtime
			rt := &DockerRuntime{}

			// Use a short deadline so BuildKit connection to non-routable address fails fast.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			dockerfile := "FROM alpine:latest\n"
			tag := "test:latest"
			opts := BuildOptions{}

			// Call BuildImage via BuildManager - it will fail (no actual client or buildkit),
			// but we can verify which path was taken by the error message.
			// Recover from any panic (Docker SDK may panic with nil client).
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						// If we get a panic, it's from Docker SDK path (nil pointer)
						err = fmt.Errorf("docker sdk panic: %v", r)
					}
				}()
				err = rt.BuildManager().BuildImage(ctx, dockerfile, tag, opts)
			}()

			if err == nil {
				t.Fatal("expected error, got nil")
			}

			// Determine which path was taken:
			// - Docker SDK path: nil *dockerBuildManager dereferences m.cli → panic
			//   (caught by recover as "docker sdk panic: ...")
			// - BuildKit path: returns a normal error (message varies by environment)
			errMsg := strings.ToLower(err.Error())
			isPanic := strings.Contains(errMsg, "docker sdk panic")
			if tt.expectBuildKitPath {
				if isPanic {
					t.Errorf("expected buildkit path, but got docker sdk panic: %v", err)
				}
			} else {
				if !isPanic {
					t.Errorf("expected docker sdk path (panic), got: %v", err)
				}
			}
		})
	}
}
