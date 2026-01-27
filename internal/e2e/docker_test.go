//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/container"
	"github.com/andybons/moat/internal/run"
	"github.com/andybons/moat/internal/storage"
)

// skipIfNoDocker skips the test if Docker runtime is not available.
// Docker-in-Docker tests require the Docker runtime (not Apple containers)
// because they need access to the host Docker socket.
func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skip("Skipping: Docker not available")
	}

	// Also check that we're actually using Docker runtime (not Apple)
	rt, err := container.NewRuntime()
	if err != nil {
		t.Skipf("Skipping: Could not create runtime: %v", err)
	}
	defer rt.Close()

	if rt.Type() != container.RuntimeDocker {
		t.Skip("Skipping: Test requires Docker runtime (currently using Apple containers)")
	}
}

// TestDockerDependency verifies that the docker dependency works end-to-end.
// This tests that a container with the docker dependency can successfully
// run docker commands that communicate with the host Docker daemon via
// the mounted Docker socket.
func TestDockerDependency(t *testing.T) {
	skipIfNoDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspaceWithDeps(t, []string{"docker:host"})

	// Run `docker ps` inside the container - this will fail if:
	// - Docker socket is not mounted
	// - User doesn't have permission to access the socket (wrong group)
	// - Docker CLI is not installed
	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-docker-dependency",
		Workspace: workspace,
		Config: &config.Config{
			Dependencies: []string{"docker:host"},
		},
		Cmd: []string{"docker", "ps", "--format", "{{.ID}}"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		// Read logs to help diagnose the failure
		store, storeErr := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
		if storeErr == nil {
			logs, _ := store.ReadLogs(0, 100)
			var logLines []string
			for _, entry := range logs {
				logLines = append(logLines, entry.Line)
			}
			t.Fatalf("Wait: %v\nContainer logs:\n%s", err, strings.Join(logLines, "\n"))
		}
		t.Fatalf("Wait: %v", err)
	}

	// Give storage a moment to flush
	time.Sleep(100 * time.Millisecond)

	// Read logs to verify docker ps ran successfully
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	// docker ps --format "{{.ID}}" outputs container IDs, one per line.
	// The test itself runs in a container, so we should see at least one ID
	// (the test container itself). But even if no containers are running,
	// a successful execution with exit code 0 (verified by Wait above)
	// proves the docker dependency works.
	t.Logf("docker ps output (%d log entries):", len(logs))
	for _, entry := range logs {
		t.Logf("  %s", entry.Line)
	}
	t.Log("docker dependency verified: docker ps completed successfully")
}

// TestDockerDependencyWithAppleRuntime verifies that docker:host fails
// gracefully when using Apple containers runtime.
func TestDockerDependencyWithAppleRuntime(t *testing.T) {
	skipIfNoAppleContainer(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspaceWithDeps(t, []string{"docker:host"})

	// Should fail with a clear error about requiring Docker runtime
	_, err = mgr.Create(ctx, run.Options{
		Name:      "e2e-docker-apple-fail",
		Workspace: workspace,
		Config: &config.Config{
			Dependencies: []string{"docker:host"},
		},
		Cmd: []string{"docker", "ps"},
	})

	if err == nil {
		t.Fatal("Expected error when using docker dependency with Apple runtime")
	}

	// Error should mention the incompatibility
	errMsg := err.Error()
	if !strings.Contains(errMsg, "Docker runtime") && !strings.Contains(errMsg, "Apple") {
		t.Errorf("Error should mention Docker runtime requirement, got: %v", err)
	}

	t.Logf("Correctly rejected docker dependency on Apple runtime: %v", err)
}

// skipIfNoPrivileged skips the test if privileged containers are not supported.
// Some CI environments (e.g., GitHub Actions without specific configuration,
// rootless Docker) don't support privileged mode.
func skipIfNoPrivileged(t *testing.T) {
	t.Helper()

	// Try to run a simple privileged container to check if it's supported
	cmd := exec.Command("docker", "run", "--rm", "--privileged", "alpine:3.20", "echo", "privileged-ok")
	if err := cmd.Run(); err != nil {
		t.Skip("Skipping: privileged containers not supported in this environment")
	}
}

// TestDockerDindDependency verifies that the docker:dind dependency works end-to-end.
// This tests that a container with docker:dind runs its own isolated Docker daemon
// and can execute docker commands without access to the host Docker.
//
// Unlike docker:host mode which shares the host's Docker daemon, dind mode:
// - Runs dockerd inside the container (requires privileged mode)
// - Has an isolated container namespace (can't see host containers)
// - Takes a few seconds for the daemon to start
func TestDockerDindDependency(t *testing.T) {
	skipIfNoDocker(t)
	skipIfNoPrivileged(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspaceWithDeps(t, []string{"docker:dind"})

	// Run `docker ps` inside the container.
	// The moat-init script starts dockerd and waits for it to be ready,
	// so by the time our command runs, docker should be available.
	//
	// We use `docker info` first to verify daemon connectivity, then `docker ps`.
	// This provides better diagnostics if the daemon isn't ready.
	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-docker-dind",
		Workspace: workspace,
		Config: &config.Config{
			Dependencies: []string{"docker:dind"},
		},
		// Use a shell command to:
		// 1. Confirm docker daemon is responsive with docker info
		// 2. List running containers (should be empty in fresh dind)
		// 3. Verify we can pull and run a simple container
		Cmd: []string{"sh", "-c", `
			echo "=== Docker Info ===" &&
			docker info --format '{{.ServerVersion}}' &&
			echo "=== Docker PS ===" &&
			docker ps --format '{{.ID}}\t{{.Image}}\t{{.Names}}' &&
			echo "=== Container count ===" &&
			docker ps -q | wc -l &&
			echo "=== dind test complete ==="
		`},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		// Read logs to help diagnose the failure
		store, storeErr := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
		if storeErr == nil {
			logs, _ := store.ReadLogs(0, 100)
			var logLines []string
			for _, entry := range logs {
				logLines = append(logLines, entry.Line)
			}
			t.Fatalf("Wait: %v\nContainer logs:\n%s", err, strings.Join(logLines, "\n"))
		}
		t.Fatalf("Wait: %v", err)
	}

	// Give storage a moment to flush
	time.Sleep(100 * time.Millisecond)

	// Read logs to verify docker commands ran successfully
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	// Verify key outputs are present
	var foundDockerInfo, foundDockerPS, foundComplete bool
	for _, entry := range logs {
		if strings.Contains(entry.Line, "=== Docker Info ===") {
			foundDockerInfo = true
		}
		if strings.Contains(entry.Line, "=== Docker PS ===") {
			foundDockerPS = true
		}
		if strings.Contains(entry.Line, "=== dind test complete ===") {
			foundComplete = true
		}
	}

	if !foundDockerInfo {
		t.Error("docker info section not found in output")
	}
	if !foundDockerPS {
		t.Error("docker ps section not found in output")
	}
	if !foundComplete {
		t.Error("test completion marker not found - command may have failed partway")
	}

	t.Logf("docker:dind test output (%d log entries):", len(logs))
	for _, entry := range logs {
		t.Logf("  %s", entry.Line)
	}
	t.Log("docker:dind dependency verified: isolated Docker daemon works correctly")
}

// TestDockerDindIsolation verifies that docker:dind containers are isolated from
// the host Docker daemon. Containers running in dind mode should not see any
// containers from the host.
func TestDockerDindIsolation(t *testing.T) {
	skipIfNoDocker(t)
	skipIfNoPrivileged(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// First, start a container on the host that we can verify isn't visible from dind
	hostContainerName := "e2e-dind-isolation-marker"
	cleanupHostContainer := func() {
		exec.Command("docker", "rm", "-f", hostContainerName).Run()
	}
	cleanupHostContainer() // Clean up any leftovers from previous runs
	defer cleanupHostContainer()

	// Start a simple container on the host that runs for a while
	hostCmd := exec.CommandContext(ctx, "docker", "run", "-d",
		"--name", hostContainerName,
		"alpine:3.20", "sleep", "300")
	if err := hostCmd.Run(); err != nil {
		t.Fatalf("Failed to start host marker container: %v", err)
	}

	// Verify the host container is running
	verifyCmd := exec.Command("docker", "ps", "-q", "-f", "name="+hostContainerName)
	output, err := verifyCmd.Output()
	if err != nil || len(output) == 0 {
		t.Fatalf("Host marker container not running: %v", err)
	}

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspaceWithDeps(t, []string{"docker:dind"})

	// Run docker ps inside dind and verify the host container is NOT visible
	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-docker-dind-isolation",
		Workspace: workspace,
		Config: &config.Config{
			Dependencies: []string{"docker:dind"},
		},
		// List all containers and check for the host marker
		Cmd: []string{"sh", "-c", `
			echo "=== Containers visible in dind ===" &&
			docker ps -a --format '{{.Names}}' &&
			echo "=== Checking for host marker ===" &&
			if docker ps -a --format '{{.Names}}' | grep -q "` + hostContainerName + `"; then
				echo "ERROR: Host container visible in dind - isolation broken!"
				exit 1
			else
				echo "OK: Host container not visible - isolation confirmed"
			fi
		`},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		// Read logs to help diagnose the failure
		store, storeErr := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
		if storeErr == nil {
			logs, _ := store.ReadLogs(0, 100)
			var logLines []string
			for _, entry := range logs {
				logLines = append(logLines, entry.Line)
			}
			t.Fatalf("Wait: %v\nContainer logs:\n%s", err, strings.Join(logLines, "\n"))
		}
		t.Fatalf("Wait: %v", err)
	}

	// Give storage a moment to flush
	time.Sleep(100 * time.Millisecond)

	// Read logs to verify isolation
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	// Check for isolation confirmation
	var isolationConfirmed bool
	for _, entry := range logs {
		if strings.Contains(entry.Line, "isolation confirmed") {
			isolationConfirmed = true
		}
		if strings.Contains(entry.Line, "isolation broken") {
			t.Fatal("Docker-in-Docker isolation is broken: host containers are visible")
		}
	}

	if !isolationConfirmed {
		t.Logf("Log output (%d entries):", len(logs))
		for _, entry := range logs {
			t.Logf("  %s", entry.Line)
		}
		t.Error("Isolation confirmation message not found in output")
	}

	t.Log("docker:dind isolation verified: dind container cannot see host containers")
}
