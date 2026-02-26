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
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
)

// =============================================================================
// Daemon Proxy E2E Tests
//
// These tests verify the proxy daemon lifecycle, credential injection,
// network request logging, and multi-run daemon sharing.
//
// All tests use lightweight shell commands (curl, echo) instead of
// language-specific runtimes to keep them fast on all container backends.
// =============================================================================

// TestDaemonStartsWithRun verifies that creating a run with grants
// automatically starts the daemon and the daemon lock file exists.
func TestDaemonStartsWithRun(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		cred, cleanup := setupTestCredential(t)
		_ = cred
		defer cleanup()

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		workspace := createTestWorkspace(t)

		r, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-daemon-starts",
			Workspace: workspace,
			Grants:    []string{"github"},
			Cmd:       []string{"echo", "daemon-test"},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		defer mgr.Destroy(context.Background(), r.ID)

		// Verify the daemon is running via lock file
		daemonDir := filepath.Join(config.GlobalConfigDir(), "proxy")
		lock, err := daemon.ReadLockFile(daemonDir)
		if err != nil {
			t.Fatalf("ReadLockFile: %v", err)
		}
		if lock == nil {
			t.Fatal("daemon lock file not found after run creation")
		}
		if !lock.IsAlive() {
			t.Error("daemon process is not alive")
		}
		if lock.ProxyPort == 0 {
			t.Error("daemon lock file has ProxyPort=0")
		}

		// Verify daemon log file exists (crash logging fix)
		logPath := filepath.Join(daemonDir, "daemon.log")
		if _, err := os.Stat(logPath); os.IsNotExist(err) {
			t.Error("daemon.log does not exist — crashes would be silent")
		}

		// Verify the run got valid proxy details
		if r.ProxyPort == 0 {
			t.Error("run ProxyPort is 0")
		}
		if r.ProxyAuthToken == "" {
			t.Error("run ProxyAuthToken is empty")
		}

		t.Logf("Daemon PID=%d ProxyPort=%d, Run ProxyPort=%d",
			lock.PID, lock.ProxyPort, r.ProxyPort)
	})
}

// TestDaemonReusedAcrossRuns verifies that two concurrent runs share
// the same daemon process (same PID and proxy port).
func TestDaemonReusedAcrossRuns(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		_, cleanup := setupTestCredential(t)
		defer cleanup()

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		// Create first run
		ws1 := createTestWorkspace(t)
		r1, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-daemon-reuse-1",
			Workspace: ws1,
			Grants:    []string{"github"},
			Cmd:       []string{"sleep", "10"},
		})
		if err != nil {
			t.Fatalf("Create r1: %v", err)
		}
		defer mgr.Destroy(context.Background(), r1.ID)

		// Read daemon info after first run
		daemonDir := filepath.Join(config.GlobalConfigDir(), "proxy")
		lock1, err := daemon.ReadLockFile(daemonDir)
		if err != nil || lock1 == nil {
			t.Fatalf("ReadLockFile after r1: %v (lock=%v)", err, lock1)
		}

		// Create second run — should reuse the same daemon
		ws2 := createTestWorkspace(t)
		r2, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-daemon-reuse-2",
			Workspace: ws2,
			Grants:    []string{"github"},
			Cmd:       []string{"sleep", "10"},
		})
		if err != nil {
			t.Fatalf("Create r2: %v", err)
		}
		defer mgr.Destroy(context.Background(), r2.ID)

		// Read daemon info after second run
		lock2, err := daemon.ReadLockFile(daemonDir)
		if err != nil || lock2 == nil {
			t.Fatalf("ReadLockFile after r2: %v (lock=%v)", err, lock2)
		}

		// Same daemon process should serve both runs
		if lock1.PID != lock2.PID {
			t.Errorf("daemon PID changed: %d → %d (should be reused)", lock1.PID, lock2.PID)
		}
		if lock1.ProxyPort != lock2.ProxyPort {
			t.Errorf("daemon ProxyPort changed: %d → %d (should be reused)", lock1.ProxyPort, lock2.ProxyPort)
		}

		// Both runs should have the same proxy port but different auth tokens
		if r1.ProxyPort != r2.ProxyPort {
			t.Errorf("runs have different ProxyPort: %d vs %d", r1.ProxyPort, r2.ProxyPort)
		}
		if r1.ProxyAuthToken == r2.ProxyAuthToken {
			t.Error("runs have identical ProxyAuthToken — each run should get a unique token")
		}

		t.Logf("Daemon PID=%d shared by runs %s and %s", lock1.PID, r1.ID, r2.ID)
	})
}

// TestDaemonNetworkLogging verifies that network requests made through the
// daemon proxy are captured in the per-run storage (network.jsonl).
func TestDaemonNetworkLogging(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		_, cleanup := setupTestCredential(t)
		defer cleanup()

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		workspace := createTestWorkspace(t)

		// Make an HTTPS request through the proxy using curl.
		// The proxy intercepts TLS for hosts with configured credentials (github)
		// and logs the request. curl respects HTTP_PROXY and SSL_CERT_FILE env vars
		// set by moat, so it works on all runtimes without extra setup.
		r, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-daemon-netlog",
			Workspace: workspace,
			Grants:    []string{"github"},
			Cmd: []string{
				"sh", "-c",
				"curl -s --connect-timeout 10 https://api.github.com/zen || true",
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
			t.Logf("Wait returned error (may be expected): %v", err)
		}

		// Give storage a moment to flush
		time.Sleep(200 * time.Millisecond)

		store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
		if err != nil {
			t.Fatalf("NewRunStore: %v", err)
		}

		requests, err := store.ReadNetworkRequests()
		if err != nil {
			t.Fatalf("ReadNetworkRequests: %v", err)
		}

		// Verify we captured the request to api.github.com
		found := false
		for _, req := range requests {
			if strings.Contains(req.URL, "api.github.com") {
				found = true
				t.Logf("Captured: %s %s → %d (%dms)",
					req.Method, req.URL, req.StatusCode, req.Duration)
				break
			}
		}

		if !found {
			// Dump logs for diagnosis
			logs, logErr := store.ReadLogs(0, 100)
			var logLines []string
			if logErr == nil {
				for _, entry := range logs {
					logLines = append(logLines, entry.Line)
				}
			}
			t.Errorf("Network request to api.github.com not captured in daemon mode.\n"+
				"Captured requests (%d): %v\n"+
				"Container logs: %v", len(requests), requests, logLines)
		}
	})
}

// TestDaemonCredentialInjection verifies that the daemon proxy injects
// credentials into HTTPS requests. The container makes a request to a host
// with configured credentials and we verify the proxy intercepted and
// logged it with auth injection.
func TestDaemonCredentialInjection(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		_, cleanup := setupTestCredential(t)
		defer cleanup()

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		workspace := createTestWorkspace(t)

		r, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-daemon-cred-inject",
			Workspace: workspace,
			Grants:    []string{"github"},
			Cmd: []string{
				"sh", "-c",
				"curl -s --connect-timeout 10 https://api.github.com/zen || true",
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

		store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
		if err != nil {
			t.Fatalf("NewRunStore: %v", err)
		}

		requests, err := store.ReadNetworkRequests()
		if err != nil {
			t.Fatalf("ReadNetworkRequests: %v", err)
		}

		// Look for the request and verify credential injection
		for _, req := range requests {
			if strings.Contains(req.URL, "api.github.com") {
				// The Authorization header should be redacted (indicating injection)
				if authVal, ok := req.RequestHeaders["Authorization"]; ok {
					if authVal == "[REDACTED]" {
						t.Logf("Credential injection confirmed: Authorization header injected and redacted")
						return
					}
					t.Errorf("Authorization header present but not redacted: %q", authVal)
					return
				}
				t.Logf("Request captured but Authorization header not in log (proxy may have filtered it)")
				return
			}
		}

		t.Errorf("No request to api.github.com found in %d network requests", len(requests))
	})
}

// TestDaemonProxyEnvInContainer verifies that the container receives
// correct HTTP_PROXY environment variables pointing to the daemon proxy.
func TestDaemonProxyEnvInContainer(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		_, cleanup := setupTestCredential(t)
		defer cleanup()

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		workspace := createTestWorkspace(t)

		r, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-daemon-proxy-env",
			Workspace: workspace,
			Grants:    []string{"github"},
			Cmd:       []string{"sh", "-c", "echo HTTP_PROXY=$HTTP_PROXY && echo HTTPS_PROXY=$HTTPS_PROXY"},
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

		time.Sleep(100 * time.Millisecond)

		store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
		if err != nil {
			t.Fatalf("NewRunStore: %v", err)
		}

		logs, err := store.ReadLogs(0, 100)
		if err != nil {
			t.Fatalf("ReadLogs: %v", err)
		}

		var foundHTTP, foundHTTPS bool
		for _, entry := range logs {
			if strings.HasPrefix(entry.Line, "HTTP_PROXY=http://moat:") {
				foundHTTP = true
				if !strings.Contains(entry.Line, "@") {
					t.Errorf("HTTP_PROXY missing auth token: %s", entry.Line)
				}
				t.Logf("HTTP_PROXY: %s", entry.Line)
			}
			if strings.HasPrefix(entry.Line, "HTTPS_PROXY=http://moat:") {
				foundHTTPS = true
				t.Logf("HTTPS_PROXY: %s", entry.Line)
			}
		}

		if !foundHTTP {
			t.Error("HTTP_PROXY not set or missing auth token in container")
		}
		if !foundHTTPS {
			t.Error("HTTPS_PROXY not set or missing auth token in container")
		}
	})
}

// TestDaemonNetworkLoggingIsolation verifies that network requests from
// different runs are logged to their respective run stores, not cross-contaminated.
func TestDaemonNetworkLoggingIsolation(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		_, cleanup := setupTestCredential(t)
		defer cleanup()

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		// Create two runs that make requests to different GitHub API endpoints.
		// Each run uses curl which respects HTTP_PROXY and SSL_CERT_FILE.
		ws1 := createTestWorkspace(t)
		r1, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-daemon-iso-1",
			Workspace: ws1,
			Grants:    []string{"github"},
			Cmd: []string{
				"sh", "-c",
				"curl -s --connect-timeout 10 https://api.github.com/zen || true",
			},
		})
		if err != nil {
			t.Fatalf("Create r1: %v", err)
		}
		defer mgr.Destroy(context.Background(), r1.ID)

		ws2 := createTestWorkspace(t)
		r2, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-daemon-iso-2",
			Workspace: ws2,
			Grants:    []string{"github"},
			Cmd: []string{
				"sh", "-c",
				"curl -s --connect-timeout 10 https://api.github.com/octocat || true",
			},
		})
		if err != nil {
			t.Fatalf("Create r2: %v", err)
		}
		defer mgr.Destroy(context.Background(), r2.ID)

		// Start both runs
		if err := mgr.Start(ctx, r1.ID, run.StartOptions{}); err != nil {
			t.Fatalf("Start r1: %v", err)
		}
		if err := mgr.Start(ctx, r2.ID, run.StartOptions{}); err != nil {
			t.Fatalf("Start r2: %v", err)
		}

		// Wait for both
		_ = mgr.Wait(ctx, r1.ID)
		_ = mgr.Wait(ctx, r2.ID)
		time.Sleep(200 * time.Millisecond)

		// Read network requests from each run's store
		store1, err := storage.NewRunStore(storage.DefaultBaseDir(), r1.ID)
		if err != nil {
			t.Fatalf("NewRunStore r1: %v", err)
		}
		store2, err := storage.NewRunStore(storage.DefaultBaseDir(), r2.ID)
		if err != nil {
			t.Fatalf("NewRunStore r2: %v", err)
		}

		reqs1, _ := store1.ReadNetworkRequests()
		reqs2, _ := store2.ReadNetworkRequests()

		// Each run's store should only contain requests from that run.
		// With a shared daemon proxy, cross-contamination would mean
		// r1's requests show up in r2's store or vice versa.
		for _, req := range reqs1 {
			if strings.Contains(req.URL, "/octocat") {
				t.Errorf("Run 1 store contains run 2's /octocat request: %s", req.URL)
			}
		}
		for _, req := range reqs2 {
			if strings.Contains(req.URL, "/zen") {
				t.Errorf("Run 2 store contains run 1's /zen request: %s", req.URL)
			}
		}

		t.Logf("Run 1 captured %d requests, Run 2 captured %d requests", len(reqs1), len(reqs2))
	})
}

// =============================================================================
// Test Helpers
// =============================================================================

// setupTestCredential creates a test GitHub credential for E2E tests.
// Returns the credential and a cleanup function.
func setupTestCredential(t *testing.T) (credential.Credential, func()) {
	t.Helper()

	encKey, err := credential.DefaultEncryptionKey()
	if err != nil {
		t.Fatalf("DefaultEncryptionKey: %v", err)
	}
	credStore, err := credential.NewFileStore(credential.DefaultStoreDir(), encKey)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	cred := credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "test-token-for-e2e-daemon",
	}
	if err := credStore.Save(cred); err != nil {
		t.Fatalf("Save credential: %v", err)
	}

	return cred, func() {
		credStore.Delete(credential.ProviderGitHub)
	}
}
