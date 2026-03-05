//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/routing"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
)

// =============================================================================
// Endpoint Routing E2E Tests
//
// These tests verify that host-based routing works end-to-end:
// container starts services on exposed ports, the routing proxy registers
// routes, and external requests reach the correct container service.
//
// Reproduces: "unknown agent" error when connecting to endpoints
// =============================================================================

// TestEndpointRouteRegistration is a focused test that just checks whether
// routes get registered at all after Start. This is the minimal repro.
func TestEndpointRouteRegistration(t *testing.T) {
	env, cleanup := startEndpointRun(t, "e2e-route-reg",
		map[string]int{"web": 8000}, []string{"sleep", "30"}, nil)
	defer cleanup()

	r := env.runInfo
	t.Logf("Run created: ID=%s Name=%s Ports=%v HostPorts=%v", r.ID, r.Name, r.Ports, r.HostPorts)

	if len(r.Ports) == 0 {
		t.Fatal("Run has no ports — config.Ports not propagated to run")
	}

	// Check route table
	routeTable, err := routing.NewRouteTable(env.proxyDir)
	if err != nil {
		t.Fatalf("NewRouteTable: %v", err)
	}

	agents := routeTable.Agents()
	t.Logf("Registered agents: %v", agents)

	addr, ok := routeTable.Lookup("e2e-route-reg", "web")
	if !ok {
		routesPath := filepath.Join(env.proxyDir, "routes.json")
		data, _ := os.ReadFile(routesPath)
		t.Logf("routes.json content: %s", string(data))
		t.Fatalf("Route not found for agent 'e2e-route-reg' endpoint 'web'; registered agents: %v", agents)
	}
	t.Logf("Route registered: web -> %s", addr)

	// Now check if routing proxy is running
	lock, err := routing.LoadProxyLock(env.proxyDir)
	if err != nil {
		t.Fatalf("LoadProxyLock: %v", err)
	}
	if lock == nil {
		t.Fatal("Routing proxy lock file not found")
	}
	t.Logf("Routing proxy: PID=%d Port=%d Alive=%v", lock.PID, lock.Port, lock.IsAlive())
}

// TestEndpointRoutingBasic verifies that a container with exposed ports
// gets routes registered and the routing proxy can forward requests to it.
// This reproduces the "unknown agent" bug reported with examples/multi-endpoint.
func TestEndpointRoutingBasic(t *testing.T) {
	env, cleanup := startEndpointRun(t, "e2e-endpoint-basic",
		map[string]int{"web": 8000}, []string{"python3", "-m", "http.server", "8000"}, nil)
	defer cleanup()

	time.Sleep(2 * time.Second)

	routeTable, err := routing.NewRouteTable(env.proxyDir)
	if err != nil {
		t.Fatalf("NewRouteTable: %v", err)
	}

	addr, ok := routeTable.Lookup("e2e-endpoint-basic", "web")
	if !ok {
		t.Fatalf("Route not found for agent 'e2e-endpoint-basic' endpoint 'web'; registered agents: %v", routeTable.Agents())
	}
	t.Logf("Route registered: web -> %s", addr)

	proxyPort := getRoutingProxyPort(t)
	httpClient := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/", proxyPort)
	req, _ := http.NewRequest("GET", url, nil)
	req.Host = fmt.Sprintf("web.e2e-endpoint-basic.localhost:%d", proxyPort)

	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP request through routing proxy failed: %v", err)
	}
	defer resp.Body.Close()

	assertNotUnknownAgent(t, resp)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	t.Log("Endpoint routing works: request reached container service through routing proxy")
}

// TestEndpointRoutingMultiEndpoint verifies that multiple endpoints on the
// same agent are all accessible through the routing proxy.
func TestEndpointRoutingMultiEndpoint(t *testing.T) {
	env, cleanup := startEndpointRun(t, "e2e-multi-endpoint",
		map[string]int{"web": 3000, "api": 8001},
		[]string{"sh", "-c",
			"python3 -m http.server 3000 --directory /tmp &" +
				" python3 -m http.server 8001 --directory /workspace &" +
				" wait",
		}, nil)
	defer cleanup()

	time.Sleep(2 * time.Second)

	proxyPort := getRoutingProxyPort(t)
	routeTable, err := routing.NewRouteTable(env.proxyDir)
	if err != nil {
		t.Fatalf("NewRouteTable: %v", err)
	}

	for _, endpoint := range []string{"web", "api"} {
		addr, ok := routeTable.Lookup("e2e-multi-endpoint", endpoint)
		if !ok {
			t.Errorf("Route not found for endpoint %q", endpoint)
		} else {
			t.Logf("Route: %s -> %s", endpoint, addr)
		}
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	for _, endpoint := range []string{"web", "api"} {
		t.Run(endpoint, func(t *testing.T) {
			url := fmt.Sprintf("http://127.0.0.1:%d/", proxyPort)
			req, _ := http.NewRequest("GET", url, nil)
			req.Host = fmt.Sprintf("%s.e2e-multi-endpoint.localhost:%d", endpoint, proxyPort)

			resp, err := httpClient.Do(req)
			if err != nil {
				t.Fatalf("HTTP request to %s failed: %v", endpoint, err)
			}
			defer resp.Body.Close()

			assertNotUnknownAgent(t, resp)

			if resp.StatusCode == http.StatusBadGateway {
				t.Logf("Warning: %s returned 502 (backend may not be ready yet)", endpoint)
			}
			t.Logf("Endpoint %s: status %d (routing works)", endpoint, resp.StatusCode)
		})
	}
}

// TestEndpointRoutingWithTLS verifies that HTTPS connections through
// the routing proxy work correctly.
func TestEndpointRoutingWithTLS(t *testing.T) {
	_, cleanup := startEndpointRun(t, "e2e-endpoint-tls",
		map[string]int{"web": 8000}, []string{"python3", "-m", "http.server", "8000"}, nil)
	defer cleanup()

	time.Sleep(2 * time.Second)

	proxyPort := getRoutingProxyPort(t)

	// Load the CA cert to trust the routing proxy's TLS
	caDir := filepath.Join(config.GlobalConfigDir(), "proxy", "ca")
	caCertPath := filepath.Join(caDir, "ca.crt")
	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		t.Fatalf("Reading CA cert: %v", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		t.Fatal("Failed to parse CA cert")
	}

	tlsClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    certPool,
				ServerName: "web.e2e-endpoint-tls.localhost",
				MinVersion: tls.VersionTLS12,
			},
		},
	}

	url := fmt.Sprintf("https://127.0.0.1:%d/", proxyPort)
	req, _ := http.NewRequest("GET", url, nil)
	req.Host = "web.e2e-endpoint-tls.localhost"

	resp, err := tlsClient.Do(req)
	if err != nil {
		t.Fatalf("HTTPS request through routing proxy failed: %v", err)
	}
	defer resp.Body.Close()

	assertNotUnknownAgent(t, resp)

	t.Logf("HTTPS endpoint routing works: status %d", resp.StatusCode)
}

// TestEndpointRoutingWithGrants verifies that endpoint routing works
// when the run also has grants (daemon is involved).
// This tests the interaction between routing proxy and credential proxy.
func TestEndpointRoutingWithGrants(t *testing.T) {
	requireDocker(t)

	_, credCleanup := setupTestCredential(t)
	defer credCleanup()

	_, cleanup := startEndpointRun(t, "e2e-endpoint-grants",
		map[string]int{"web": 8000}, []string{"python3", "-m", "http.server", "8000"}, []string{"github"})
	defer cleanup()

	time.Sleep(2 * time.Second)

	proxyPort := getRoutingProxyPort(t)
	httpClient := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/", proxyPort)
	req, _ := http.NewRequest("GET", url, nil)
	req.Host = fmt.Sprintf("web.e2e-endpoint-grants.localhost:%d", proxyPort)

	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP request through routing proxy failed: %v", err)
	}
	defer resp.Body.Close()

	assertNotUnknownAgent(t, resp)

	t.Logf("Endpoint routing with grants works: status %d", resp.StatusCode)
}

// TestProxyTokenValidInContainer verifies that a container with grants
// can make outbound HTTP requests without getting "407 Invalid proxy token".
// This reproduces the second reported bug.
func TestProxyTokenValidInContainer(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		_, cleanup := setupTestCredential(t)
		defer cleanup()

		workspace := createTestWorkspace(t)

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: boolPtr(true)})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		// The container should be able to make an HTTP request through the proxy
		// without getting 407. We use curl to test and capture the output.
		r, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-proxy-token",
			Workspace: workspace,
			Grants:    []string{"github"},
			Cmd: []string{"sh", "-c",
				// Try to curl an HTTPS endpoint through the proxy.
				// We use --write-out to capture the HTTP status code.
				// If we get 407, the proxy token is invalid.
				"STATUS=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 10 https://api.github.com/zen 2>&1 || true) && " +
					"echo PROXY_STATUS=$STATUS",
			},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		defer mgr.Destroy(context.Background(), r.ID)

		if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
			t.Fatalf("Start: %v", err)
		}

		if err := mgr.Wait(ctx, r.ID); err != nil {
			t.Logf("Wait: %v", err)
		}

		// Give logs time to flush
		time.Sleep(500 * time.Millisecond)

		// Read logs to check for 407 status
		logs := readRunLogs(t, r.ID)
		t.Logf("Container output:\n%s", logs)

		if strings.Contains(logs, "PROXY_STATUS=407") {
			t.Fatal("BUG REPRODUCED: container got 407 Invalid proxy token when making HTTPS request through proxy")
		}

		if strings.Contains(logs, "PROXY_STATUS=000") {
			t.Log("Warning: curl returned 000 (connection failed) — proxy may not be reachable")
		}

		if strings.Contains(logs, "PROXY_STATUS=200") || strings.Contains(logs, "PROXY_STATUS=401") {
			// 200 = success, 401 = test token rejected by GitHub but proxy worked
			t.Log("Proxy token is valid — container can reach external services through proxy")
		}
	})
}

// =============================================================================
// Helpers
// =============================================================================

// boolPtr returns a pointer to a bool value.
func boolPtr(v bool) *bool { return &v }

// assertNotUnknownAgent checks an HTTP response for the "unknown agent"
// routing error and fails the test immediately if found.
func assertNotUnknownAgent(t *testing.T, resp *http.Response) {
	t.Helper()
	if resp.StatusCode != http.StatusNotFound {
		return
	}
	body, _ := io.ReadAll(resp.Body)
	var errResp map[string]string
	json.Unmarshal(body, &errResp)
	if errResp["error"] == "unknown agent" {
		t.Fatalf("BUG: got 'unknown agent' error — routing proxy cannot find registered routes (detail: %s)", errResp["detail"])
	}
}

type endpointTestEnv struct {
	mgr      *run.Manager
	runInfo  *run.Run
	proxyDir string
}

// startEndpointRun creates and starts a container with ports for endpoint testing.
// Returns the test environment and a cleanup function.
func startEndpointRun(t *testing.T, name string, ports map[string]int, cmd []string, grants []string) (*endpointTestEnv, func()) {
	t.Helper()
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)

	workspace := createTestWorkspaceWithPorts(t, ports)

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: boolPtr(true)})
	if err != nil {
		cancel()
		t.Fatalf("NewManager: %v", err)
	}

	cfg, err := config.Load(workspace)
	if err != nil {
		mgr.Close()
		cancel()
		t.Fatalf("config.Load: %v", err)
	}

	r, err := mgr.Create(ctx, run.Options{
		Name:      name,
		Workspace: workspace,
		Grants:    grants,
		Cmd:       cmd,
		Config:    cfg,
	})
	if err != nil {
		mgr.Close()
		cancel()
		t.Fatalf("Create: %v", err)
	}

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		mgr.Destroy(context.Background(), r.ID)
		mgr.Close()
		cancel()
		t.Fatalf("Start: %v", err)
	}

	env := &endpointTestEnv{
		mgr:      mgr,
		runInfo:  r,
		proxyDir: filepath.Join(config.GlobalConfigDir(), "proxy"),
	}

	cleanup := func() {
		mgr.Stop(context.Background(), r.ID)
		mgr.Destroy(context.Background(), r.ID)
		mgr.Close()
		cancel()
	}
	return env, cleanup
}

// getRoutingProxyPort reads the routing proxy lock file to find the port.
func getRoutingProxyPort(t *testing.T) int {
	t.Helper()
	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")
	lock, err := routing.LoadProxyLock(proxyDir)
	if err != nil {
		t.Fatalf("LoadProxyLock: %v", err)
	}
	if lock == nil {
		t.Fatal("Routing proxy lock file not found — proxy not running")
	}
	if !lock.IsAlive() {
		t.Fatal("Routing proxy process is not alive")
	}
	if lock.Port == 0 {
		t.Fatal("Routing proxy port is 0")
	}
	return lock.Port
}

// createTestWorkspaceWithPorts creates a temp dir with moat.yaml that has ports.
func createTestWorkspaceWithPorts(t *testing.T, ports map[string]int) string {
	t.Helper()

	dir := t.TempDir()

	// Sort port names for deterministic YAML output.
	portNames := make([]string, 0, len(ports))
	for name := range ports {
		portNames = append(portNames, name)
	}
	sort.Strings(portNames)

	portsYAML := ""
	for _, name := range portNames {
		portsYAML += fmt.Sprintf("  %s: %d\n", name, ports[name])
	}

	yaml := fmt.Sprintf(`name: e2e-test
dependencies:
  - python@3.11
ports:
%s`, portsYAML)

	if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("WriteFile moat.yaml: %v", err)
	}

	return dir
}

// readRunLogs reads all log lines for a run using storage package.
func readRunLogs(t *testing.T, runID string) string {
	t.Helper()

	store, err := storage.NewRunStore(storage.DefaultBaseDir(), runID)
	if err != nil {
		t.Logf("Warning: could not open run store: %v", err)
		return ""
	}

	entries, err := store.ReadLogs(0, 10000)
	if err != nil {
		t.Logf("Warning: could not read logs: %v", err)
		return ""
	}

	var lines []string
	for _, entry := range entries {
		lines = append(lines, entry.Line)
	}
	return strings.Join(lines, "\n")
}
