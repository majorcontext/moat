//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andybons/moat/internal/run"
)

// TestLogsCapturedInAttachedMode verifies that logs are captured to logs.jsonl
// when a run completes in attached (non-interactive) mode.
// This is the standard flow: moat run (without -d)
func TestLogsCapturedInAttachedMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	// Create and start a run that outputs logs and exits
	r, err := mgr.Create(ctx, run.Options{
		Name:      "test-logs-attached",
		Workspace: workspace,
		Cmd:       []string{"sh", "-c", "echo 'test log line 1'; echo 'test log line 2'"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	// Start and wait for completion (simulating attached mode)
	if err := mgr.Start(ctx, r.ID, run.StartOptions{StreamLogs: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Verify logs.jsonl exists and contains the logs
	logsPath := filepath.Join(r.Store.Dir(), "logs.jsonl")
	if _, err := os.Stat(logsPath); os.IsNotExist(err) {
		t.Fatalf("logs.jsonl does not exist at %s", logsPath)
	}

	logs, err := r.Store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	if len(logs) < 2 {
		t.Errorf("expected at least 2 log entries, got %d", len(logs))
	}

	// Verify log content (use Contains for flexibility with ANSI codes, etc.)
	foundLine1 := false
	foundLine2 := false
	for _, entry := range logs {
		t.Logf("Log entry: %q", entry.Line)
		if entry.Line == "test log line 1" {
			foundLine1 = true
		}
		if entry.Line == "test log line 2" {
			foundLine2 = true
		}
	}

	if !foundLine1 {
		t.Errorf("expected to find 'test log line 1' in logs, got %d entries", len(logs))
	}
	if !foundLine2 {
		t.Errorf("expected to find 'test log line 2' in logs, got %d entries", len(logs))
	}
}

// TestLogsCapturedInDetachedMode verifies that logs are captured to logs.jsonl
// when a run is started in detached mode and later completes.
// This is the critical bug: moat run -d doesn't capture logs.
func TestLogsCapturedInDetachedMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	// Create and start a detached run
	r, err := mgr.Create(ctx, run.Options{
		Name:      "test-logs-detached",
		Workspace: workspace,
		Cmd:       []string{"sh", "-c", "echo 'detached log line 1'; echo 'detached log line 2'; sleep 1"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	// Start without waiting (detached mode)
	if err := mgr.Start(ctx, r.ID, run.StartOptions{StreamLogs: false}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for container to complete
	time.Sleep(3 * time.Second)

	// Verify the run has stopped
	currentRun, err := mgr.Get(r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if currentRun.State != run.StateStopped {
		t.Logf("Warning: run state is %s, expected stopped. Waiting longer...", currentRun.State)
		time.Sleep(3 * time.Second)
	}

	// Verify logs.jsonl exists and contains the logs
	logsPath := filepath.Join(r.Store.Dir(), "logs.jsonl")
	if _, err := os.Stat(logsPath); os.IsNotExist(err) {
		t.Fatalf("logs.jsonl does not exist at %s (BUG: detached mode doesn't capture logs)", logsPath)
	}

	logs, err := r.Store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	if len(logs) < 2 {
		t.Errorf("expected at least 2 log entries, got %d", len(logs))
	}
}

// TestLogsCapturedInInteractiveMode verifies that logs are captured to logs.jsonl
// when a run is started in interactive mode and exits normally.
// This is the bug reported by the user: interactive runs don't capture logs.
func TestLogsCapturedInInteractiveMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	// Create an interactive run (TTY enabled)
	r, err := mgr.Create(ctx, run.Options{
		Name:        "test-logs-interactive",
		Workspace:   workspace,
		Cmd:         []string{"sh", "-c", "echo 'interactive log 1'; echo 'interactive log 2'"},
		Interactive: true,
		TTY:         true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	// StartAttached simulates interactive mode
	// We can't truly test interactive mode in automated tests, but we can
	// verify that StartAttached captures logs when the container exits
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- mgr.StartAttached(ctx, r.ID, os.Stdin, os.Stdout, os.Stderr)
	}()

	// Wait for completion
	select {
	case err := <-doneCh:
		if err != nil && ctx.Err() == nil {
			t.Fatalf("StartAttached failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("StartAttached timed out")
	}

	// Verify logs.jsonl exists and contains the logs
	logsPath := filepath.Join(r.Store.Dir(), "logs.jsonl")
	if _, err := os.Stat(logsPath); os.IsNotExist(err) {
		t.Fatalf("logs.jsonl does not exist at %s (BUG: interactive mode doesn't capture logs)", logsPath)
	}

	logs, err := r.Store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	if len(logs) < 2 {
		t.Errorf("expected at least 2 log entries, got %d", len(logs))
	}
}

// TestLogsCapturedAfterStop verifies that logs are captured when a run is
// explicitly stopped (not just natural exit).
func TestLogsCapturedAfterStop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	// Create a long-running process that outputs logs
	r, err := mgr.Create(ctx, run.Options{
		Name:      "test-logs-stop",
		Workspace: workspace,
		Cmd:       []string{"sh", "-c", "echo 'log before stop'; sleep 30; echo 'should not see this'"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	// Start the run
	if err := mgr.Start(ctx, r.ID, run.StartOptions{StreamLogs: false}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give it time to output the first log
	time.Sleep(2 * time.Second)

	// Stop the run
	if err := mgr.Stop(ctx, r.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Verify logs.jsonl exists
	logsPath := filepath.Join(r.Store.Dir(), "logs.jsonl")
	if _, err := os.Stat(logsPath); os.IsNotExist(err) {
		t.Fatalf("logs.jsonl does not exist at %s (BUG: stop doesn't capture logs)", logsPath)
	}

	logs, err := r.Store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	if len(logs) < 1 {
		t.Errorf("expected at least 1 log entry, got %d", len(logs))
	}

	// Verify we got the first log but not the second
	foundFirst := false
	foundSecond := false
	for _, entry := range logs {
		t.Logf("Log entry: %q", entry.Line)
		if entry.Line == "log before stop" {
			foundFirst = true
		}
		if entry.Line == "should not see this" {
			foundSecond = true
		}
	}

	if !foundFirst {
		t.Errorf("expected to find 'log before stop' in logs, got %d entries", len(logs))
	}
	if foundSecond {
		t.Error("should not have 'should not see this' in logs (process was stopped)")
	}
}

// TestLogsAlwaysExistForAudit verifies that logs.jsonl exists even for
// runs that produce no output. This is important for audit trail completeness.
func TestLogsAlwaysExistForAudit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	// Create a run that produces no output
	r, err := mgr.Create(ctx, run.Options{
		Name:      "test-logs-empty",
		Workspace: workspace,
		Cmd:       []string{"true"}, // exits immediately with no output
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID, run.StartOptions{StreamLogs: false}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Verify logs.jsonl exists (even if empty)
	logsPath := filepath.Join(r.Store.Dir(), "logs.jsonl")
	if _, err := os.Stat(logsPath); os.IsNotExist(err) {
		t.Fatalf("logs.jsonl does not exist at %s (should exist for audit even if empty)", logsPath)
	}

	// Reading empty logs should not error
	logs, err := r.Store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs on empty logs: %v", err)
	}

	// Empty slice or nil is acceptable when file exists but is empty
	// The important part is that the file was created (verified above)
	t.Logf("ReadLogs returned %d entries (nil slice is ok for empty file)", len(logs))
}
