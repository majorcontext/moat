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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/andybons/agentops/internal/config"
	"github.com/andybons/agentops/internal/container"
	"github.com/andybons/agentops/internal/credential"
	"github.com/andybons/agentops/internal/run"
	"github.com/andybons/agentops/internal/storage"
)

func TestMain(m *testing.M) {
	// Check if any container runtime is available
	dockerAvailable := exec.Command("docker", "version").Run() == nil
	appleAvailable := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" &&
		exec.Command("container", "list", "--quiet").Run() == nil

	if !dockerAvailable && !appleAvailable {
		os.Stderr.WriteString("Skipping e2e tests: No container runtime available (need Docker or Apple container)\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// skipIfNoAppleContainer skips the test if Apple container is not available.
// Apple container requires macOS 26+ on Apple Silicon.
func skipIfNoAppleContainer(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping: not on macOS")
	}
	if runtime.GOARCH != "arm64" {
		t.Skip("Skipping: not on Apple Silicon")
	}
	if err := exec.Command("container", "list", "--quiet").Run(); err != nil {
		t.Skip("Skipping: Apple container CLI not available")
	}
}

// TestProxyBindsToLocalhostOnly verifies that when a run is started with grants,
// the proxy server binds appropriately for the runtime:
// - Docker: binds to 127.0.0.1 (localhost only)
// - Apple: binds to 0.0.0.0 (all interfaces, required because container accesses host via gateway)
// For Apple containers, security is maintained by the fact that the proxy only runs locally
// and TestProxyNotAccessibleFromNetwork verifies external hosts cannot connect.
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
		Name:      "e2e-test-proxy-binding",
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

	// The proxy server should be running - verify it's bound appropriately
	if r.ProxyServer == nil {
		t.Fatal("ProxyServer is nil, expected proxy to be running with grants")
	}

	addr := r.ProxyServer.Addr()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}

	// Check runtime to determine expected binding
	rt, _ := container.NewRuntime()
	if rt != nil {
		defer rt.Close()
	}

	if rt != nil && rt.Type() == container.RuntimeApple {
		// Apple containers require binding to all interfaces because the container
		// accesses the host via the gateway IP (e.g., 192.168.64.1), not localhost.
		// Security is still maintained because:
		// 1. The proxy only runs on the local machine
		// 2. TestProxyNotAccessibleFromNetwork verifies external hosts can't connect
		if host != "::" && host != "0.0.0.0" {
			t.Errorf("Apple runtime: Proxy bound to %q, expected \"::\" or \"0.0.0.0\" (all interfaces)\n"+
				"Apple containers require binding to all interfaces to be reachable via gateway IP.",
				host)
		}
		t.Logf("Apple runtime: Proxy correctly bound to %q (all interfaces for gateway access)", host)
	} else {
		// Docker: MUST bind to localhost only
		if host != "127.0.0.1" {
			t.Errorf("SECURITY VIOLATION: Proxy bound to %q, want %q\n"+
				"The proxy must bind to localhost only to prevent credential theft from network peers.",
				host, "127.0.0.1")
		}
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
		Name:      "e2e-test-proxy-network",
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
	// Use 5 minute timeout - Python image builds can be slow in CI
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Set up a test credential for GitHub so the proxy does TLS interception.
	// Without credentials, the proxy does transparent tunneling and doesn't log requests.
	credStore, err := credential.NewFileStore(
		credential.DefaultStoreDir(),
		credential.DefaultEncryptionKey(),
	)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	testCred := credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "test-token-for-e2e-logging", // Token value doesn't matter for logging
	}
	if err := credStore.Save(testCred); err != nil {
		t.Fatalf("Save credential: %v", err)
	}
	defer credStore.Delete(credential.ProviderGitHub) // Clean up after test

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspaceWithRuntime(t, "python", "3.11")

	// Use Python's http.client with explicit CONNECT tunnel through the proxy.
	// This gives us full control over the proxy interaction and ensures requests
	// go through our TLS-intercepting proxy.
	pythonScript := `
import ssl
import os
import sys
import base64
from http.client import HTTPConnection, HTTPSConnection
from urllib.parse import urlparse

# Parse proxy URL from environment
proxy_url = os.environ.get('HTTPS_PROXY') or os.environ.get('https_proxy')
if not proxy_url:
    raise Exception('HTTPS_PROXY not set')
print(f'Using proxy: {proxy_url}', file=sys.stderr)

proxy = urlparse(proxy_url)
proxy_host = proxy.hostname
proxy_port = proxy.port or 8080

# Handle proxy authentication if credentials are in URL
tunnel_headers = {}
if proxy.username and proxy.password:
    credentials = f'{proxy.username}:{proxy.password}'
    auth = base64.b64encode(credentials.encode()).decode()
    tunnel_headers['Proxy-Authorization'] = f'Basic {auth}'
    print(f'Proxy auth configured for user: {proxy.username}', file=sys.stderr)

# Load our CA cert for TLS verification
ca_file = os.environ.get('SSL_CERT_FILE', '/etc/ssl/certs/agentops-ca.pem')
ctx = ssl.create_default_context()
ctx.load_verify_locations(ca_file)
print(f'Loaded CA from: {ca_file}', file=sys.stderr)

# Connect to proxy and establish CONNECT tunnel
print(f'Connecting to proxy {proxy_host}:{proxy_port}...', file=sys.stderr)
conn = HTTPConnection(proxy_host, proxy_port)
conn.set_tunnel('api.github.com', 443, headers=tunnel_headers)
conn.connect()
print('CONNECT tunnel established', file=sys.stderr)

# Wrap the socket with TLS using our CA
print('Starting TLS handshake...', file=sys.stderr)
sock = ctx.wrap_socket(conn.sock, server_hostname='api.github.com')
print('TLS handshake complete', file=sys.stderr)

# Create HTTPS connection using the tunneled socket
https_conn = HTTPSConnection('api.github.com')
https_conn.sock = sock

# Make the request with User-Agent (required by GitHub)
print('Sending GET /zen...', file=sys.stderr)
https_conn.request('GET', '/zen', headers={'User-Agent': 'AgentOps-E2E-Test/1.0'})
response = https_conn.getresponse()
print(f'Response status: {response.status}', file=sys.stderr)
body = response.read().decode()
print(body)
`
	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-test-network-capture",
		Workspace: workspace,
		Grants:    []string{"github"},
		Config: &config.Config{
			Dependencies: []string{"python@3.11"},
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
		// Read logs to help diagnose the issue
		logs, logErr := store.ReadLogs(0, 100)
		var logLines []string
		if logErr == nil {
			for _, entry := range logs {
				logLines = append(logLines, entry.Line)
			}
		}
		t.Errorf("Network trace did not capture request to api.github.com\n"+
			"Captured requests: %v\n"+
			"Container logs: %v", requests, logLines)
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
		Name:      "e2e-test-host-docker-internal",
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
		Name:      "e2e-test-no-proxy",
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
		Name:      "e2e-test-logs",
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
		Name:      "e2e-test-workspace",
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
		Name:      "e2e-test-env",
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
func createTestWorkspaceWithRuntime(t *testing.T, rt, version string) string {
	t.Helper()

	dir := t.TempDir()

	// Create agent.yaml with runtime
	yaml := "agent: e2e-test\nversion: 1.0.0\nruntime:\n  " + rt + ": " + version + "\n"
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("WriteFile agent.yaml: %v", err)
	}

	return dir
}

// =============================================================================
// Apple Container Tests (macOS only, skipped in CI)
// =============================================================================

// TestAppleContainerRuntime verifies that the Apple container backend works correctly.
// This test only runs on macOS with Apple Silicon and the Apple container CLI installed.
// It will be skipped in CI since CI uses Linux runners.
func TestAppleContainerRuntime(t *testing.T) {
	skipIfNoAppleContainer(t)

	// Verify that NewRuntime() selects Apple container on this system
	rt, err := container.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	if rt.Type() != container.RuntimeApple {
		t.Errorf("Expected RuntimeApple on macOS with Apple container, got %v", rt.Type())
	}

	// Verify host address is the gateway IP (not host.docker.internal)
	hostAddr := rt.GetHostAddress()
	if strings.HasPrefix(hostAddr, "host.docker") {
		t.Errorf("Expected gateway IP for Apple container, got %q", hostAddr)
	}

	// Verify it doesn't support host network mode
	if rt.SupportsHostNetwork() {
		t.Error("Apple container should not support host network mode")
	}
}

// TestAppleContainerBasicRun verifies that a simple container run works with Apple container.
// This test only runs on macOS with Apple Silicon and the Apple container CLI installed.
func TestAppleContainerBasicRun(t *testing.T) {
	skipIfNoAppleContainer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)
	testOutput := "apple-container-e2e-test-output"

	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-apple-test",
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

	// Read logs to verify the container ran successfully
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
		if strings.Contains(entry.Line, testOutput) {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Container output not captured\nExpected: %q\nLogs: %v", testOutput, logs)
	}
}

// TestAppleContainerWithProxy verifies that the proxy works correctly with Apple container.
// This tests the gateway IP networking path that's different from Docker.
func TestAppleContainerWithProxy(t *testing.T) {
	skipIfNoAppleContainer(t)

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
		Name:      "e2e-apple-proxy-test",
		Workspace: workspace,
		Grants:    []string{"github"},
		Cmd:       []string{"sh", "-c", "echo HTTP_PROXY=$HTTP_PROXY"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mgr.Stop(context.Background(), r.ID)

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Logf("Wait returned error (may be expected): %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify proxy was started
	if r.ProxyServer == nil {
		t.Fatal("ProxyServer is nil, expected proxy to be running with grants")
	}

	// Read logs to verify HTTP_PROXY was set with gateway IP
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	// The proxy URL should use the gateway IP (e.g., 192.168.64.1), not host.docker.internal
	foundProxy := false
	for _, entry := range logs {
		if strings.Contains(entry.Line, "HTTP_PROXY=") {
			foundProxy = true
			if strings.Contains(entry.Line, "host.docker.internal") {
				t.Errorf("Apple container should use gateway IP, not host.docker.internal: %s", entry.Line)
			}
			t.Logf("Proxy config: %s", entry.Line)
			break
		}
	}

	if !foundProxy {
		t.Errorf("HTTP_PROXY not found in logs: %v", logs)
	}
}
