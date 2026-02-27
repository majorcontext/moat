package daemon

import (
	"context"
	"path/filepath"
	"testing"
)

func TestClient_Health(t *testing.T) {
	dir := testSockDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	srv := NewServer(sockPath, 9100)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())

	client := NewClient(sockPath)
	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.ProxyPort != 9100 {
		t.Errorf("expected port 9100, got %d", health.ProxyPort)
	}
	if health.RunCount != 0 {
		t.Errorf("expected 0 runs, got %d", health.RunCount)
	}
	if health.PID == 0 {
		t.Error("expected non-zero PID")
	}
	if health.StartedAt == "" {
		t.Error("expected non-empty started_at")
	}
}

func TestClient_RegisterAndUnregister(t *testing.T) {
	dir := testSockDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	srv := NewServer(sockPath, 9100)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())

	client := NewClient(sockPath)

	// Register
	resp, err := client.RegisterRun(context.Background(), RegisterRequest{
		RunID: "run_1",
		Credentials: []CredentialSpec{
			{Host: "api.github.com", Header: "Authorization", Value: "token abc"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.AuthToken == "" {
		t.Error("expected auth token")
	}
	if resp.ProxyPort != 9100 {
		t.Errorf("expected port 9100, got %d", resp.ProxyPort)
	}

	// Update container ID
	if err := client.UpdateRun(context.Background(), resp.AuthToken, "container_xyz"); err != nil {
		t.Fatal(err)
	}

	// Verify via list
	runs, err := client.ListRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].ContainerID != "container_xyz" {
		t.Errorf("expected container_xyz, got %s", runs[0].ContainerID)
	}

	// Unregister
	if err := client.UnregisterRun(context.Background(), resp.AuthToken); err != nil {
		t.Fatal(err)
	}
	runs, _ = client.ListRuns(context.Background())
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestClient_UpdateRunNotFound(t *testing.T) {
	dir := testSockDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	srv := NewServer(sockPath, 9100)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())

	client := NewClient(sockPath)
	err := client.UpdateRun(context.Background(), "nonexistent-token", "ctr-999")
	if err == nil {
		t.Fatal("expected error for nonexistent token")
	}
	if err.Error() != "run not found" {
		t.Errorf("expected 'run not found', got %q", err.Error())
	}
}

func TestClient_UnregisterRunNotFound(t *testing.T) {
	dir := testSockDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	srv := NewServer(sockPath, 9100)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())

	client := NewClient(sockPath)
	err := client.UnregisterRun(context.Background(), "nonexistent-token")
	if err == nil {
		t.Fatal("expected error for nonexistent token")
	}
}

func TestClient_Shutdown(t *testing.T) {
	dir := testSockDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	srv := NewServer(sockPath, 9100)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	// No defer Stop — shutdown endpoint will stop it.

	client := NewClient(sockPath)
	err := client.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
}

func TestClient_ConnectionRefused(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "nonexistent.sock")

	client := NewClient(sockPath)

	// Health should fail with connection error
	_, err := client.Health(context.Background())
	if err == nil {
		t.Fatal("expected error when daemon not running")
	}

	// RegisterRun should fail
	_, err = client.RegisterRun(context.Background(), RegisterRequest{RunID: "run_1"})
	if err == nil {
		t.Fatal("expected error when daemon not running")
	}

	// ListRuns should fail
	_, err = client.ListRuns(context.Background())
	if err == nil {
		t.Fatal("expected error when daemon not running")
	}
}

func TestClient_MultipleRuns(t *testing.T) {
	dir := testSockDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	srv := NewServer(sockPath, 9100)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())

	client := NewClient(sockPath)

	// Register two runs
	resp1, err := client.RegisterRun(context.Background(), RegisterRequest{RunID: "run_a"})
	if err != nil {
		t.Fatal(err)
	}
	resp2, err := client.RegisterRun(context.Background(), RegisterRequest{RunID: "run_b"})
	if err != nil {
		t.Fatal(err)
	}

	// Tokens should be different
	if resp1.AuthToken == resp2.AuthToken {
		t.Error("expected different auth tokens for different runs")
	}

	// List should show both
	runs, err := client.ListRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}

	// Unregister first, verify second still exists
	if err := client.UnregisterRun(context.Background(), resp1.AuthToken); err != nil {
		t.Fatal(err)
	}
	runs, err = client.ListRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run after unregister, got %d", len(runs))
	}
	if runs[0].RunID != "run_b" {
		t.Errorf("expected remaining run to be run_b, got %s", runs[0].RunID)
	}
}
