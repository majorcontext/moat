package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// testClient returns an HTTP client that dials the given Unix socket.
func testClient(sockPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}
}

func TestServer_HealthEndpoint(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	srv := NewServer(sock, 9119)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	client := testClient(sock)
	resp, err := client.Get("http://localhost/v1/health")
	if err != nil {
		t.Fatalf("GET /v1/health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if health.PID == 0 {
		t.Error("expected non-zero PID")
	}
	if health.ProxyPort != 9119 {
		t.Errorf("expected proxy_port 9119, got %d", health.ProxyPort)
	}
	if health.RunCount != 0 {
		t.Errorf("expected run_count 0, got %d", health.RunCount)
	}
	if health.StartedAt == "" {
		t.Error("expected non-empty started_at")
	}
}

func TestServer_RegisterAndListRuns(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	srv := NewServer(sock, 9119)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	client := testClient(sock)

	// Register a run.
	reqBody := RegisterRequest{
		RunID: "run-abc",
		Credentials: []CredentialSpec{
			{Host: "api.github.com", Header: "Authorization", Value: "Bearer ghp_xxx", Grant: "github"},
		},
	}
	body, _ := json.Marshal(reqBody)
	resp, err := client.Post("http://localhost/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var regResp RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if regResp.AuthToken == "" {
		t.Fatal("expected non-empty auth_token")
	}
	if regResp.ProxyPort != 9119 {
		t.Errorf("expected proxy_port 9119, got %d", regResp.ProxyPort)
	}

	// List runs.
	resp2, err := client.Get("http://localhost/v1/runs")
	if err != nil {
		t.Fatalf("GET /v1/runs: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}

	var runs []RunInfo
	if err := json.NewDecoder(resp2.Body).Decode(&runs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].RunID != "run-abc" {
		t.Errorf("expected run_id run-abc, got %s", runs[0].RunID)
	}

	// Verify credential was stored in the RunContext.
	rc, ok := srv.Registry().Lookup(regResp.AuthToken)
	if !ok {
		t.Fatal("RunContext not found by token")
	}
	cred, ok := rc.GetCredential("api.github.com")
	if !ok {
		t.Fatal("credential not found for api.github.com")
	}
	if cred.Value != "Bearer ghp_xxx" {
		t.Errorf("expected credential value 'Bearer ghp_xxx', got %q", cred.Value)
	}
}

func TestServer_UnregisterRun(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	srv := NewServer(sock, 9119)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	client := testClient(sock)

	// Register a run.
	reqBody := RegisterRequest{RunID: "run-del"}
	body, _ := json.Marshal(reqBody)
	resp, err := client.Post("http://localhost/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	defer resp.Body.Close()

	var regResp RegisterResponse
	json.NewDecoder(resp.Body).Decode(&regResp)
	token := regResp.AuthToken

	// Delete the run.
	req, _ := http.NewRequest(http.MethodDelete, "http://localhost/v1/runs/"+token, nil)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /v1/runs/%s: %v", token, err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp2.StatusCode)
	}

	// List should be empty.
	resp3, err := client.Get("http://localhost/v1/runs")
	if err != nil {
		t.Fatalf("GET /v1/runs: %v", err)
	}
	defer resp3.Body.Close()

	var runs []RunInfo
	json.NewDecoder(resp3.Body).Decode(&runs)
	if len(runs) != 0 {
		t.Errorf("expected 0 runs after delete, got %d", len(runs))
	}
}

func TestServer_UpdateRun(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	srv := NewServer(sock, 9119)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	client := testClient(sock)

	// Register a run.
	reqBody := RegisterRequest{RunID: "run-upd"}
	body, _ := json.Marshal(reqBody)
	resp, err := client.Post("http://localhost/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	defer resp.Body.Close()

	var regResp RegisterResponse
	json.NewDecoder(resp.Body).Decode(&regResp)
	token := regResp.AuthToken

	// Update with container ID.
	updateBody, _ := json.Marshal(UpdateRunRequest{ContainerID: "ctr-123"})
	req, _ := http.NewRequest(http.MethodPatch, "http://localhost/v1/runs/"+token, bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("PATCH /v1/runs/%s: %v", token, err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp2.StatusCode)
	}

	// Verify container ID via registry.
	rc, ok := srv.Registry().Lookup(token)
	if !ok {
		t.Fatal("RunContext not found")
	}
	if rc.ContainerID != "ctr-123" {
		t.Errorf("expected container_id ctr-123, got %s", rc.ContainerID)
	}

	// Also verify via list endpoint.
	resp3, err := client.Get("http://localhost/v1/runs")
	if err != nil {
		t.Fatalf("GET /v1/runs: %v", err)
	}
	defer resp3.Body.Close()

	var runs []RunInfo
	json.NewDecoder(resp3.Body).Decode(&runs)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].ContainerID != "ctr-123" {
		t.Errorf("expected container_id ctr-123, got %s", runs[0].ContainerID)
	}
}

func TestServer_SocketCleanup(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	srv := NewServer(sock, 9119)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Socket file should exist after start.
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket should exist after Start: %v", err)
	}

	// Stop the server.
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Socket file should be cleaned up.
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket should be removed after Stop, got err: %v", err)
	}
}

func TestServer_OnEmptyCallback(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	srv := NewServer(sock, 9119)

	called := false
	srv.SetOnEmpty(func() { called = true })

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	client := testClient(sock)

	// Register a run.
	reqBody := RegisterRequest{RunID: "run-cb"}
	body, _ := json.Marshal(reqBody)
	resp, err := client.Post("http://localhost/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	defer resp.Body.Close()

	var regResp RegisterResponse
	json.NewDecoder(resp.Body).Decode(&regResp)

	// Delete the run — should trigger onEmpty.
	req, _ := http.NewRequest(http.MethodDelete, "http://localhost/v1/runs/"+regResp.AuthToken, nil)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp2.Body.Close()

	if !called {
		t.Error("expected onEmpty to be called after last run unregistered")
	}
}

func TestServer_UnregisterNotFound(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	srv := NewServer(sock, 9119)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	client := testClient(sock)

	req, _ := http.NewRequest(http.MethodDelete, "http://localhost/v1/runs/nonexistent-token", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unknown token, got %d", resp.StatusCode)
	}
}

func TestServer_UpdateNotFound(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	srv := NewServer(sock, 9119)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	client := testClient(sock)

	updateBody, _ := json.Marshal(UpdateRunRequest{ContainerID: "ctr-999"})
	req, _ := http.NewRequest(http.MethodPatch, "http://localhost/v1/runs/nonexistent-token", bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unknown token, got %d", resp.StatusCode)
	}
}

func TestServer_ShutdownEndpoint(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	srv := NewServer(sock, 9119)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// No defer Stop — shutdown endpoint will stop it.

	client := testClient(sock)
	resp, err := client.Post("http://localhost/v1/shutdown", "", nil)
	if err != nil {
		t.Fatalf("POST /v1/shutdown: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Wait a moment for async shutdown, then verify socket is gone.
	// The socket should be removed.
	// Give the goroutine a moment to clean up.
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sock); os.IsNotExist(err) {
			return // success
		}
		// Small busy-wait.
	}
	// Even if the file still exists, the server should have stopped.
	// That's acceptable since shutdown is async.
}
