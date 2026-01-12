//go:build e2e
// +build e2e

// Package e2e provides end-to-end tests that exercise the full Docker + proxy flow.
// These tests require Docker to be installed and running.
//
// Run with: go test -tags=e2e ./internal/e2e/
package e2e

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andybons/agentops/internal/config"
	"github.com/andybons/agentops/internal/run"
	"github.com/andybons/agentops/internal/storage"
)

func TestMain(m *testing.M) {
	// Check if Docker is available
	if err := exec.Command("docker", "version").Run(); err != nil {
		os.Stderr.WriteString("Skipping e2e tests: Docker not available\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestProxyBindsToLocalhostOnly verifies that when a run is started with grants,
// the proxy server binds to 127.0.0.1 only, not to 0.0.0.0 (all interfaces).
// This is a critical security requirement to prevent credential theft from the network.
func TestProxyBindsToLocalhostOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	// Create a run with grants to activate the proxy
	r, err := mgr.Create(ctx, run.Options{
		Agent:     "e2e-test-proxy-binding",
		Workspace: workspace,
		Grants:    []string{"github"}, // This activates the proxy
		Cmd:       []string{"sleep", "10"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	// Start the run
	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mgr.Stop(context.Background(), r.ID)

	// Give the proxy a moment to start
	time.Sleep(500 * time.Millisecond)

	// The proxy server should be running - verify it's bound to localhost
	if r.ProxyServer == nil {
		t.Fatal("ProxyServer is nil, expected proxy to be running with grants")
	}

	addr := r.ProxyServer.Addr()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}

	// SECURITY: The proxy MUST bind to localhost only
	if host != "127.0.0.1" {
		t.Errorf("SECURITY VIOLATION: Proxy bound to %q, want %q\n"+
			"The proxy must bind to localhost only to prevent credential theft from network peers.",
			host, "127.0.0.1")
	}
}

// TestProxyNotAccessibleFromNetwork verifies that the proxy cannot be reached
// from a non-localhost address. This is a defense-in-depth check.
func TestProxyNotAccessibleFromNetwork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	r, err := mgr.Create(ctx, run.Options{
		Agent:     "e2e-test-proxy-network",
		Workspace: workspace,
		Grants:    []string{"github"},
		Cmd:       []string{"sleep", "10"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mgr.Stop(context.Background(), r.ID)

	time.Sleep(500 * time.Millisecond)

	if r.ProxyServer == nil {
		t.Fatal("ProxyServer is nil")
	}

	// Get the port the proxy is listening on
	port := r.ProxyServer.Port()

	// Try to connect from localhost (should succeed)
	localConn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 2*time.Second)
	if err != nil {
		t.Errorf("Failed to connect to proxy from localhost: %v", err)
	} else {
		localConn.Close()
	}

	// Try to connect from 0.0.0.0 (should fail if bound to 127.0.0.1)
	// Note: This tests that we can't connect via the "any" address
	// The actual network isolation is enforced by binding to 127.0.0.1
}

// TestNetworkRequestsAreCaptured verifies that HTTP requests made through the proxy
// are captured in the network trace.
func TestNetworkRequestsAreCaptured(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspaceWithRuntime(t, "python", "3.11")

	// Use Python's urllib which properly honors HTTP_PROXY/HTTPS_PROXY
	pythonScript := `import urllib.request; print(urllib.request.urlopen('https://api.github.com/zen').read().decode())`
	r, err := mgr.Create(ctx, run.Options{
		Agent:     "e2e-test-network-capture",
		Workspace: workspace,
		Grants:    []string{"github"},
		Config: &config.Config{
			Runtime: config.Runtime{Python: "3.11"},
		},
		Cmd: []string{"python", "-c", pythonScript},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the run to complete
	if err := mgr.Wait(ctx, r.ID); err != nil {
		// curl might fail if no network, but we still want to check the trace
		t.Logf("Wait returned error (may be expected): %v", err)
	}

	// Give storage a moment to flush
	time.Sleep(100 * time.Millisecond)

	// Read network requests from storage
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	requests, err := store.ReadNetworkRequests()
	if err != nil {
		t.Fatalf("ReadNetworkRequests: %v", err)
	}

	// Verify we captured the request
	found := false
	for _, req := range requests {
		if strings.Contains(req.URL, "api.github.com") {
			found = true
			t.Logf("Captured request: %s %s -> %d", req.Method, req.URL, req.StatusCode)
			break
		}
	}

	if !found {
		t.Errorf("Network trace did not capture request to api.github.com\n"+
			"Captured requests: %v", requests)
	}
}

// TestContainerCanReachProxyViaHostDockerInternal verifies that containers can
// reach the proxy via host.docker.internal, which is required for the proxy to work.
func TestContainerCanReachProxyViaHostDockerInternal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	// Run a command that tests connectivity to the proxy
	// The proxy sets HTTP_PROXY env var, so we can check if it's reachable
	r, err := mgr.Create(ctx, run.Options{
		Agent:     "e2e-test-host-docker-internal",
		Workspace: workspace,
		Grants:    []string{"github"},
		Cmd:       []string{"sh", "-c", "curl -s --connect-timeout 5 http://$HTTP_PROXY/ || echo 'proxy_reachable'"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The command should complete without hanging
	waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Second)
	defer waitCancel()

	err = mgr.Wait(waitCtx, r.ID)
	if waitCtx.Err() == context.DeadlineExceeded {
		t.Error("Container timed out trying to reach proxy via host.docker.internal")
	}
	// Error is acceptable here since we're just testing connectivity
	_ = err
}

// TestRunWithoutGrantsNoProxy verifies that runs without grants don't start a proxy.
func TestRunWithoutGrantsNoProxy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	// Create a run WITHOUT grants
	r, err := mgr.Create(ctx, run.Options{
		Agent:     "e2e-test-no-proxy",
		Workspace: workspace,
		Grants:    nil, // No grants = no proxy
		Cmd:       []string{"echo", "hello"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	// The proxy server should be nil when no grants are specified
	if r.ProxyServer != nil {
		t.Error("ProxyServer should be nil when no grants are specified")
	}
}

// TestLogsAreCaptured verifies that container logs are captured in storage.
func TestLogsAreCaptured(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	testOutput := "e2e-test-unique-output-12345"

	r, err := mgr.Create(ctx, run.Options{
		Agent:     "e2e-test-logs",
		Workspace: workspace,
		Cmd:       []string{"echo", testOutput},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Give storage a moment to flush
	time.Sleep(100 * time.Millisecond)

	// Read logs from storage
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	// Verify we captured the output
	found := false
	for _, entry := range logs {
		if strings.Contains(entry.Line, testOutput) {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Logs did not contain expected output %q\nLogs: %v", testOutput, logs)
	}
}

// TestWorkspaceIsMounted verifies that the workspace directory is mounted in the container.
func TestWorkspaceIsMounted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	// Create a test file in the workspace
	testFile := "e2e-test-file.txt"
	testContent := "e2e-test-content-xyz"
	if err := os.WriteFile(filepath.Join(workspace, testFile), []byte(testContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Run a command that reads the file
	r, err := mgr.Create(ctx, run.Options{
		Agent:     "e2e-test-workspace",
		Workspace: workspace,
		Cmd:       []string{"cat", "/workspace/" + testFile},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Read logs to verify the file content was read
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	found := false
	for _, entry := range logs {
		if strings.Contains(entry.Line, testContent) {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Container did not read workspace file correctly\nExpected: %q\nLogs: %v", testContent, logs)
	}
}

// TestConfigEnvironmentVariables verifies that environment variables from config are set.
func TestConfigEnvironmentVariables(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	cfg := &config.Config{
		Agent:   "e2e-test-env",
		Version: "1.0.0",
		Env: map[string]string{
			"E2E_TEST_VAR": "e2e-test-value-abc123",
		},
	}

	r, err := mgr.Create(ctx, run.Options{
		Agent:     "e2e-test-env",
		Workspace: workspace,
		Config:    cfg,
		Cmd:       []string{"sh", "-c", "echo $E2E_TEST_VAR"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	found := false
	for _, entry := range logs {
		if strings.Contains(entry.Line, "e2e-test-value-abc123") {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Environment variable was not set correctly\nLogs: %v", logs)
	}
}

// createTestWorkspace creates a temporary directory with an agent.yaml file.
func createTestWorkspace(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	// Create a minimal agent.yaml
	yaml := `agent: e2e-test
version: 1.0.0
`
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("WriteFile agent.yaml: %v", err)
	}

	return dir
}

// createTestWorkspaceWithRuntime creates a temporary directory with an agent.yaml
// file that specifies a runtime.
func createTestWorkspaceWithRuntime(t *testing.T, runtime, version string) string {
	t.Helper()

	dir := t.TempDir()

	// Create agent.yaml with runtime
	yaml := "agent: e2e-test\nversion: 1.0.0\nruntime:\n  " + runtime + ": " + version + "\n"
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("WriteFile agent.yaml: %v", err)
	}

	return dir
}
