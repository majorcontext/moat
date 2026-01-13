package audit

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegration_FullWorkflow(t *testing.T) {
	// Create a short socket path to avoid Unix socket path length limits.
	// Unix domain sockets have a max path length of ~104 bytes on macOS.
	socketDir, err := os.MkdirTemp("", "sock")
	if err != nil {
		t.Fatalf("creating socket temp dir: %v", err)
	}
	defer os.RemoveAll(socketDir)
	socketPath := filepath.Join(socketDir, "s")

	// Use t.TempDir() for the database (path length doesn't matter for SQLite)
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "logs.db")

	// 1. Create store
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// 2. Start collector
	collector := NewCollector(store)
	if err := collector.StartUnix(socketPath); err != nil {
		t.Fatalf("StartUnix: %v", err)
	}

	// 3. Simulate agent writing logs
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Write console logs
	for i := 0; i < 5; i++ {
		msg := CollectorMessage{Type: "console", Data: map[string]any{"line": i}}
		json.NewEncoder(conn).Encode(msg)
	}

	// Write network request
	msg := CollectorMessage{
		Type: "network",
		Data: map[string]any{
			"method":      "GET",
			"url":         "https://api.github.com/user",
			"status_code": 200,
			"duration_ms": 150,
		},
	}
	json.NewEncoder(conn).Encode(msg)

	// Write credential event
	msg = CollectorMessage{
		Type: "credential",
		Data: map[string]any{
			"name":   "github",
			"action": "injected",
			"host":   "api.github.com",
		},
	}
	json.NewEncoder(conn).Encode(msg)

	conn.Close()
	time.Sleep(100 * time.Millisecond)

	// 4. Stop collector
	collector.Stop()

	// 5. Verify entries (use proper error handling for Count)
	count, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 7 {
		t.Errorf("Count = %d, want 7", count)
	}

	// 6. Verify chain integrity
	result, err := store.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !result.Valid {
		t.Errorf("Chain should be valid: %s", result.Error)
	}

	// 7. Close and reopen store
	store.Close()

	store2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("Reopen store: %v", err)
	}
	defer store2.Close()

	// 8. Verify chain still valid after reopen
	result2, err := store2.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain after reopen: %v", err)
	}
	if !result2.Valid {
		t.Errorf("Chain should still be valid after reopen: %s", result2.Error)
	}

	// 9. Add more entries after reopen
	store2.AppendConsole("after reopen")

	count2, err := store2.Count()
	if err != nil {
		t.Fatalf("Count after reopen: %v", err)
	}
	if count2 != 8 {
		t.Errorf("Count after reopen = %d, want 8", count2)
	}

	// 10. Final verification
	result3, err := store2.VerifyChain()
	if err != nil {
		t.Fatalf("Final VerifyChain: %v", err)
	}
	if !result3.Valid {
		t.Errorf("Chain should be valid after adding: %s", result3.Error)
	}
}
