//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/netrules"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
)

// =============================================================================
// Host Traffic Blocking E2E Tests
//
// These tests verify that host-gateway traffic (container → host services) is
// blocked by default and can be selectively allowed via network.host in
// moat.yaml. The feature was added in #303.
//
// All tests use a real HTTP server on the host and verify reachability from
// inside a container. They require grants so the proxy daemon is active.
//
// NOTE: These tests are skipped on CI (GitHub Actions) because they cause the
// test process to enter kernel D-state (uninterruptible sleep), making it
// unkillable and freezing the runner. The root cause appears to be related to
// Docker bridge networking + host-gateway + proxy interactions on the CI host
// kernel. Tracked in https://github.com/majorcontext/moat/issues/315.
// =============================================================================

// startHostHTTPServer starts an HTTP server on a random port on 0.0.0.0.
// Returns the listener and the port. The server responds "host-ok" to any request.
func startHostHTTPServer(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("Failed to start host HTTP server: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "host-ok")
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() {
		srv.Close()
		ln.Close()
	})
	return ln, port
}

// hostTrafficCmd builds a shell command that:
// 1. Dumps the network environment (MOAT_HOST_GATEWAY, NO_PROXY, HTTP_PROXY) for diagnosis
// 2. Curls the given host:port and reports the HTTP status and response body
//
// The body is captured so proxy error messages (e.g. the upstream dial error that
// accompanies a 502) are visible in test logs instead of being silently dropped.
func hostTrafficCmd(host string, port int, label string) string {
	return fmt.Sprintf(
		`echo "DIAG_GATEWAY=$MOAT_HOST_GATEWAY" && `+
			`echo "DIAG_NO_PROXY=$NO_PROXY" && `+
			`echo "DIAG_HTTP_PROXY=$HTTP_PROXY" && `+
			`BODY=$(curl -s --connect-timeout 5 -w $'\n%%{http_code}' http://%s:%d/ 2>&1 || true) && `+
			`STATUS=$(printf '%%s' "$BODY" | tail -n 1) && `+
			`RESP=$(printf '%%s' "$BODY" | sed '$d') && `+
			`echo %s=$STATUS && `+
			`echo %s_BODY=$RESP`,
		host, port, label, label,
	)
}

// TestHostTrafficBlockedByDefault verifies that a container cannot reach a
// host service when network.host does not include the port.
// The container uses $MOAT_HOST_GATEWAY to address the host.
func TestHostTrafficBlockedByDefault(t *testing.T) {
	skipIfCI(t, "host traffic tests freeze CI runner — see #315")
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		_, cleanup := setupTestCredential(t)
		defer cleanup()

		_, port := startHostHTTPServer(t)

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: boolPtr(true)})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		workspace := createTestWorkspace(t)

		// No network.host — host traffic should be blocked.
		r, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-host-blocked",
			Workspace: workspace,
			Grants:    []string{"github"},
			Config: &config.Config{
				Network: config.NetworkConfig{
					Policy: "permissive",
					// No Host ports — default blocks everything
				},
			},
			Cmd: []string{
				"sh", "-c",
				hostTrafficCmd("$MOAT_HOST_GATEWAY", port, "HOST_STATUS"),
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

		time.Sleep(200 * time.Millisecond)

		logs := readRunLogs(t, r.ID)
		t.Logf("Container output:\n%s", logs)

		// The request should be blocked (407) — not reach the host server (200).
		if strings.Contains(logs, "HOST_STATUS=200") {
			t.Error("Host traffic was NOT blocked — container reached host service without network.host allowlist.\n" +
				"On Linux host-mode, MOAT_HOST_GATEWAY may be in NO_PROXY, causing traffic to bypass the proxy entirely.")
		}

		// Verify the proxy logged a blocked request with host-service marker.
		store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
		if err != nil {
			t.Fatalf("NewRunStore: %v", err)
		}
		requests, err := store.ReadNetworkRequests()
		if err != nil {
			t.Fatalf("ReadNetworkRequests: %v", err)
		}

		var foundBlocked bool
		for _, req := range requests {
			if req.StatusCode == 407 {
				foundBlocked = true
				t.Logf("Blocked request: %s %s → %d", req.Method, req.URL, req.StatusCode)
			}
		}
		if !foundBlocked && !strings.Contains(logs, "HOST_STATUS=200") {
			// Traffic didn't reach host and wasn't logged as blocked — might have
			// been blocked by firewall or connection refused. That's still OK as
			// long as the host wasn't reached.
			t.Logf("No blocked request in network log and no 200 — traffic may have been blocked at network layer (acceptable)")
		}
	})
}

// TestHostTrafficAllowedWithNetworkHost verifies that a container CAN reach a
// host service when the port is in network.host.
func TestHostTrafficAllowedWithNetworkHost(t *testing.T) {
	skipIfCI(t, "host traffic tests freeze CI runner — see #315")
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		_, cleanup := setupTestCredential(t)
		defer cleanup()

		_, port := startHostHTTPServer(t)

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: boolPtr(true)})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		workspace := createTestWorkspace(t)

		// Allow the specific port via network.host.
		r, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-host-allowed",
			Workspace: workspace,
			Grants:    []string{"github"},
			Config: &config.Config{
				Network: config.NetworkConfig{
					Policy: "permissive",
					Host:   []int{port},
				},
			},
			Cmd: []string{
				"sh", "-c",
				hostTrafficCmd("$MOAT_HOST_GATEWAY", port, "HOST_STATUS"),
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

		time.Sleep(200 * time.Millisecond)

		logs := readRunLogs(t, r.ID)
		t.Logf("Container output:\n%s", logs)

		if !strings.Contains(logs, "HOST_STATUS=200") {
			t.Errorf("Host traffic was blocked — expected 200 with port %d in network.host.\nLogs: %s", port, logs)
		}
	})
}

// TestHostTrafficWrongPortBlocked verifies that allowing one port does not
// open access to a different port on the host.
func TestHostTrafficWrongPortBlocked(t *testing.T) {
	skipIfCI(t, "host traffic tests freeze CI runner — see #315")
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		_, cleanup := setupTestCredential(t)
		defer cleanup()

		_, port := startHostHTTPServer(t)

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: boolPtr(true)})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		workspace := createTestWorkspace(t)

		// Allow a DIFFERENT port — the test server's port should still be blocked.
		wrongPort := port + 1

		r, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-host-wrong-port",
			Workspace: workspace,
			Grants:    []string{"github"},
			Config: &config.Config{
				Network: config.NetworkConfig{
					Policy: "permissive",
					Host:   []int{wrongPort},
				},
			},
			Cmd: []string{
				"sh", "-c",
				hostTrafficCmd("$MOAT_HOST_GATEWAY", port, "HOST_STATUS"),
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

		time.Sleep(200 * time.Millisecond)

		logs := readRunLogs(t, r.ID)
		t.Logf("Container output:\n%s", logs)

		if strings.Contains(logs, "HOST_STATUS=200") {
			t.Errorf("Host traffic was NOT blocked — container reached host service on port %d with only port %d allowed", port, wrongPort)
		}
	})
}

// TestHostTrafficStrictPolicyWithRules verifies that host-gateway blocking
// takes precedence over network.rules even in strict mode. Even if the host
// gateway address is whitelisted in network.rules, traffic to it should be
// blocked unless the port is in network.host.
func TestHostTrafficStrictPolicyWithRules(t *testing.T) {
	skipIfCI(t, "host traffic tests freeze CI runner — see #315")
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		_, cleanup := setupTestCredential(t)
		defer cleanup()

		_, port := startHostHTTPServer(t)

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: boolPtr(true)})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		workspace := createTestWorkspace(t)

		// Strict policy with the host gateway address in rules but NOT in
		// network.host. The gateway check should block before rules are evaluated.
		r, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-host-strict-rules",
			Workspace: workspace,
			Grants:    []string{"github"},
			Config: &config.Config{
				Network: config.NetworkConfig{
					Policy: "strict",
					Rules: []netrules.NetworkRuleEntry{
						{HostRules: netrules.HostRules{Host: "127.0.0.1"}},
						{HostRules: netrules.HostRules{Host: "localhost"}},
						{HostRules: netrules.HostRules{Host: "host.docker.internal"}},
						// Allow github so the grant resolves.
						{HostRules: netrules.HostRules{Host: "*.github.com"}},
						{HostRules: netrules.HostRules{Host: "github.com"}},
					},
					// No Host ports — host gateway blocked.
				},
			},
			// The leading sleep keeps the container alive long enough for the
			// post-start firewall setup (exec into running container for iptables)
			// to attach before the main process exits. Without it, a fast
			// echo/curl pipeline can finish in the window between StartContainer
			// returning and the firewall exec attaching, producing a spurious
			// "write init-p: broken pipe" failure. The firewall race itself is
			// orthogonal to what this test asserts and is tracked separately.
			Cmd: []string{
				"sh", "-c",
				"sleep 1 && " + hostTrafficCmd("$MOAT_HOST_GATEWAY", port, "HOST_STATUS"),
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

		time.Sleep(200 * time.Millisecond)

		logs := readRunLogs(t, r.ID)
		t.Logf("Container output:\n%s", logs)

		if strings.Contains(logs, "HOST_STATUS=200") {
			t.Error("Host traffic was NOT blocked — network.rules should not bypass host-gateway blocking")
		}
	})
}

// TestHostTrafficMultiplePorts verifies that multiple ports can be allowed
// simultaneously via network.host.
func TestHostTrafficMultiplePorts(t *testing.T) {
	skipIfCI(t, "host traffic tests freeze CI runner — see #315")
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		_, cleanup := setupTestCredential(t)
		defer cleanup()

		_, port1 := startHostHTTPServer(t)
		_, port2 := startHostHTTPServer(t)

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: boolPtr(true)})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		workspace := createTestWorkspace(t)

		r, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-host-multi-port",
			Workspace: workspace,
			Grants:    []string{"github"},
			Config: &config.Config{
				Network: config.NetworkConfig{
					Policy: "permissive",
					Host:   []int{port1, port2},
				},
			},
			Cmd: []string{
				"sh", "-c",
				fmt.Sprintf(
					`echo "DIAG_GATEWAY=$MOAT_HOST_GATEWAY" && `+
						`echo "DIAG_NO_PROXY=$NO_PROXY" && `+
						`S1=$(curl -s -o /dev/null -w '%%{http_code}' --connect-timeout 5 --retry 3 --retry-delay 1 --retry-all-errors http://$MOAT_HOST_GATEWAY:%d/ 2>&1 || true) && `+
						`S2=$(curl -s -o /dev/null -w '%%{http_code}' --connect-timeout 5 --retry 3 --retry-delay 1 --retry-all-errors http://$MOAT_HOST_GATEWAY:%d/ 2>&1 || true) && `+
						`echo HOST_PORT1=$S1 && echo HOST_PORT2=$S2`,
					port1, port2,
				),
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

		time.Sleep(200 * time.Millisecond)

		logs := readRunLogs(t, r.ID)
		t.Logf("Container output:\n%s", logs)

		if !strings.Contains(logs, "HOST_PORT1=200") {
			t.Errorf("Port %d not reachable despite being in network.host", port1)
		}
		if !strings.Contains(logs, "HOST_PORT2=200") {
			t.Errorf("Port %d not reachable despite being in network.host", port2)
		}
	})
}

// TestHostTrafficProxyBypass verifies that MOAT_HOST_GATEWAY is NOT in NO_PROXY,
// ensuring host traffic goes through the proxy where it can be blocked.
// This is a regression test — if MOAT_HOST_GATEWAY is in NO_PROXY, the
// proxy's host-gateway blocking is completely bypassed.
func TestHostTrafficProxyBypass(t *testing.T) {
	skipIfCI(t, "host traffic tests freeze CI runner — see #315")
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		_, cleanup := setupTestCredential(t)
		defer cleanup()

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: boolPtr(true)})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		workspace := createTestWorkspace(t)

		// Run a container that checks whether MOAT_HOST_GATEWAY is in NO_PROXY.
		r, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-host-noproxy-check",
			Workspace: workspace,
			Grants:    []string{"github"},
			Config: &config.Config{
				Network: config.NetworkConfig{
					Policy: "permissive",
				},
			},
			Cmd: []string{
				"sh", "-c",
				// Check if MOAT_HOST_GATEWAY value appears in NO_PROXY.
				// Use a simple grep — if it matches, the proxy is bypassed.
				`echo "DIAG_GATEWAY=$MOAT_HOST_GATEWAY" && ` +
					`echo "DIAG_NO_PROXY=$NO_PROXY" && ` +
					`if echo ",$NO_PROXY," | grep -qF ",$MOAT_HOST_GATEWAY,"; then ` +
					`echo "BYPASS=true"; else echo "BYPASS=false"; fi`,
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

		time.Sleep(200 * time.Millisecond)

		logs := readRunLogs(t, r.ID)
		t.Logf("Container output:\n%s", logs)

		if strings.Contains(logs, "BYPASS=true") {
			t.Error("MOAT_HOST_GATEWAY is in NO_PROXY — host traffic bypasses the proxy entirely, " +
				"making network.host blocking ineffective. " +
				"MOAT_HOST_GATEWAY must not be in NO_PROXY for host-gateway blocking to work.")
		}
	})
}
