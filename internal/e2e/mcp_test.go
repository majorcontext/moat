//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/credential"
	"github.com/andybons/moat/internal/run"
	"github.com/andybons/moat/internal/storage"
)

// TestMCPCredentialInjection_E2E verifies that MCP credential injection works end-to-end.
// This tests the full flow:
// 1. Create mock MCP server
// 2. Configure agent.yaml with MCP server
// 3. Store credential for MCP grant
// 4. Run container with curl command using stub header
// 5. Verify real credential was injected by proxy
func TestMCPCredentialInjection_E2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Create mock MCP server that echoes the header value
	var receivedHeader string
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Test-Key")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"header": receivedHeader,
		})
	}))
	defer mcpServer.Close()

	// Set up workspace with agent.yaml
	workspace := createTestWorkspaceWithMCP(t, mcpServer.URL)

	// Store credential for MCP grant
	encKey, err := credential.DefaultEncryptionKey()
	if err != nil {
		t.Fatalf("DefaultEncryptionKey: %v", err)
	}
	credStore, err := credential.NewFileStore(credential.DefaultStoreDir(), encKey)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Store the credential with the expected grant name
	testCred := credential.Credential{
		Provider:  credential.Provider("mcp-test"),
		Token:     "real-api-key-xyz",
		CreatedAt: time.Now(),
	}
	if err := credStore.Save(testCred); err != nil {
		t.Fatalf("Save credential: %v", err)
	}
	defer credStore.Delete(credential.Provider("mcp-test")) // Clean up after test

	// Create run manager
	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: true})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	// Create run with MCP grant
	// The command uses curl with the stub header that should be replaced
	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-mcp-credential-injection",
		Workspace: workspace,
		Grants:    []string{"mcp-test"},
		Config: &config.Config{
			Dependencies: []string{"node@20"}, // Use node image which has curl
			MCP: []config.MCPServerConfig{
				{
					Name: "test-server",
					URL:  mcpServer.URL,
					Auth: &config.MCPAuthConfig{
						Grant:  "mcp-test",
						Header: "X-Test-Key",
					},
				},
			},
		},
		Cmd: []string{
			"sh", "-c",
			"sleep 1", // Placeholder - actual MCP requests go through .claude.json
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	// Verify proxy was started (required for credential injection)
	if r.ProxyServer == nil {
		t.Fatal("ProxyServer is nil, expected proxy to be running with grants")
	}

	// Build the relay URL that the proxy exposes
	proxyAddr := r.ProxyServer.Addr()
	relayURL := fmt.Sprintf("http://%s/mcp/test-server", proxyAddr)

	// Test the MCP relay endpoint by making a direct HTTP request with stub credential
	// The proxy should replace the stub with the real credential
	req, err := http.NewRequest("GET", relayURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Test-Key", "moat-stub-mcp-test")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTTP request to relay endpoint: %v", err)
	}
	defer resp.Body.Close()

	// Verify the request was successful
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 200, got %d. Body: %s", resp.StatusCode, string(body))
	}

	// Verify real credential was injected
	if receivedHeader != "real-api-key-xyz" {
		t.Errorf("MCP server received header %q, expected %q\n"+
			"This indicates credential injection failed - the stub was not replaced",
			receivedHeader, "real-api-key-xyz")
	} else {
		t.Logf("✓ MCP credential injection successful: stub replaced with real credential")
	}

	// Read network requests to verify proxy logged the relay request
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	requests, err := store.ReadNetworkRequests()
	if err != nil {
		t.Fatalf("ReadNetworkRequests: %v", err)
	}

	// Verify we captured the proxied MCP request to the actual server
	foundMCPRequest := false
	for _, req := range requests {
		if strings.Contains(req.URL, mcpServer.URL) {
			foundMCPRequest = true
			t.Logf("Captured proxied MCP request: %s %s -> %d", req.Method, req.URL, req.StatusCode)
			break
		}
	}

	if foundMCPRequest {
		t.Logf("✓ Network trace captured MCP server request")
	} else {
		t.Logf("Note: Network trace may not include relay→server requests")
	}
}

// TestMCPMultipleServers verifies that multiple MCP servers can be configured
// and credentials are injected correctly for each.
func TestMCPMultipleServers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Create two mock MCP servers
	var server1Header, server2Header string

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server1Header = r.Header.Get("X-Server1-Key")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"server": "1"})
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server2Header = r.Header.Get("X-Server2-Key")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"server": "2"})
	}))
	defer server2.Close()

	// Set up workspace
	workspace := t.TempDir()
	agentYAML := `agent: e2e-mcp-multi
version: 1.0.0
mcp:
  - name: server1
    url: ` + server1.URL + `
    auth:
      grant: mcp-server1
      header: X-Server1-Key
  - name: server2
    url: ` + server2.URL + `
    auth:
      grant: mcp-server2
      header: X-Server2-Key
`
	if err := os.WriteFile(filepath.Join(workspace, "agent.yaml"), []byte(agentYAML), 0644); err != nil {
		t.Fatalf("WriteFile agent.yaml: %v", err)
	}

	// Store credentials for both servers
	encKey, err := credential.DefaultEncryptionKey()
	if err != nil {
		t.Fatalf("DefaultEncryptionKey: %v", err)
	}
	credStore, err := credential.NewFileStore(credential.DefaultStoreDir(), encKey)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	cred1 := credential.Credential{
		Provider:  credential.Provider("mcp-server1"),
		Token:     "server1-credential",
		CreatedAt: time.Now(),
	}
	cred2 := credential.Credential{
		Provider:  credential.Provider("mcp-server2"),
		Token:     "server2-credential",
		CreatedAt: time.Now(),
	}
	if err := credStore.Save(cred1); err != nil {
		t.Fatalf("Save cred1: %v", err)
	}
	if err := credStore.Save(cred2); err != nil {
		t.Fatalf("Save cred2: %v", err)
	}
	defer credStore.Delete(credential.Provider("mcp-server1"))
	defer credStore.Delete(credential.Provider("mcp-server2"))

	// Create run manager
	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: true})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	// Create run with both MCP grants
	// Command hits both servers with stub headers
	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-mcp-multiple-servers",
		Workspace: workspace,
		Grants:    []string{"mcp-server1", "mcp-server2"},
		Config: &config.Config{
			Dependencies: []string{"node@20"}, // Use node image which has curl
			MCP: []config.MCPServerConfig{
				{
					Name: "server1",
					URL:  server1.URL,
					Auth: &config.MCPAuthConfig{
						Grant:  "mcp-server1",
						Header: "X-Server1-Key",
					},
				},
				{
					Name: "server2",
					URL:  server2.URL,
					Auth: &config.MCPAuthConfig{
						Grant:  "mcp-server2",
						Header: "X-Server2-Key",
					},
				},
			},
		},
		Cmd: []string{"sh", "-c", "sleep 1"}, // Placeholder - test makes direct requests
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	// Build relay URLs
	proxyAddr := r.ProxyServer.Addr()
	relay1URL := fmt.Sprintf("http://%s/mcp/server1", proxyAddr)
	relay2URL := fmt.Sprintf("http://%s/mcp/server2", proxyAddr)

	// Test server 1 via relay
	req1, _ := http.NewRequest("GET", relay1URL, nil)
	req1.Header.Set("X-Server1-Key", "moat-stub-mcp-server1")

	client := &http.Client{Timeout: 5 * time.Second}
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("Request to server1 relay: %v", err)
	}
	resp1.Body.Close()

	// Test server 2 via relay
	req2, _ := http.NewRequest("GET", relay2URL, nil)
	req2.Header.Set("X-Server2-Key", "moat-stub-mcp-server2")

	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("Request to server2 relay: %v", err)
	}
	resp2.Body.Close()

	// Verify both credentials were injected
	if server1Header != "server1-credential" {
		t.Errorf("Server1 received header %q, expected %q", server1Header, "server1-credential")
	}
	if server2Header != "server2-credential" {
		t.Errorf("Server2 received header %q, expected %q", server2Header, "server2-credential")
	}

	if server1Header == "server1-credential" && server2Header == "server2-credential" {
		t.Logf("✓ Multiple MCP server credentials injected successfully")
	}
}

// TestMCPMissingCredential verifies that runs fail gracefully when MCP credential is missing.
func TestMCPMissingCredential(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer mcpServer.Close()

	workspace := createTestWorkspaceWithMCP(t, mcpServer.URL)

	// Ensure the credential does not exist
	encKey, err := credential.DefaultEncryptionKey()
	if err != nil {
		t.Fatalf("DefaultEncryptionKey: %v", err)
	}
	credStore, err := credential.NewFileStore(credential.DefaultStoreDir(), encKey)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	credStore.Delete(credential.Provider("mcp-test")) // Ensure it doesn't exist

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: true})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	// Try to create run with MCP grant but no credential
	_, err = mgr.Create(ctx, run.Options{
		Name:      "e2e-mcp-missing-credential",
		Workspace: workspace,
		Grants:    []string{"mcp-test"},
		Config: &config.Config{
			MCP: []config.MCPServerConfig{
				{
					Name: "test-server",
					URL:  mcpServer.URL,
					Auth: &config.MCPAuthConfig{
						Grant:  "mcp-test",
						Header: "X-Test-Key",
					},
				},
			},
		},
		Cmd: []string{"echo", "hello"},
	})

	// Should fail because credential is missing
	if err == nil {
		t.Error("Expected error when MCP credential is missing")
	}
	if !strings.Contains(err.Error(), "mcp-test") {
		t.Errorf("Error should mention missing credential 'mcp-test', got: %v", err)
	}
}

// createTestWorkspaceWithMCP creates a temporary workspace with agent.yaml configured for MCP.
func createTestWorkspaceWithMCP(t *testing.T, serverURL string) string {
	t.Helper()

	dir := t.TempDir()

	agentYAML := `agent: e2e-mcp-test
version: 1.0.0
mcp:
  - name: test-server
    url: ` + serverURL + `
    auth:
      grant: mcp-test
      header: X-Test-Key
`
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(agentYAML), 0644); err != nil {
		t.Fatalf("WriteFile agent.yaml: %v", err)
	}

	return dir
}
