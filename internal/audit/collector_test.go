package audit

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// createTestSocketPath creates a short socket path to avoid Unix socket path length limits.
// Unix domain sockets have a max path length of ~104 bytes on macOS.
func createTestSocketPath(t *testing.T) (socketPath, dbPath string, cleanup func()) {
	t.Helper()

	// Create a short temp directory for the socket
	socketDir, err := os.MkdirTemp("", "sock")
	if err != nil {
		t.Fatalf("creating socket temp dir: %v", err)
	}
	socketPath = filepath.Join(socketDir, "s")

	// Use t.TempDir() for the database (path length doesn't matter for SQLite)
	dbDir := t.TempDir()
	dbPath = filepath.Join(dbDir, "logs.db")

	cleanup = func() {
		os.RemoveAll(socketDir)
	}

	return socketPath, dbPath, cleanup
}

func TestCollector_UnixSocket_AcceptsWrites(t *testing.T) {
	socketPath, dbPath, cleanup := createTestSocketPath(t)
	defer cleanup()

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	collector := NewCollector(store)

	if err := collector.StartUnix(socketPath); err != nil {
		t.Fatalf("StartUnix: %v", err)
	}
	defer collector.Stop()

	// Connect as client
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Send a log message
	msg := CollectorMessage{
		Type: string(EntryConsole),
		Data: map[string]any{"line": "hello from agent"},
	}
	json.NewEncoder(conn).Encode(msg)

	// Give collector time to process
	time.Sleep(50 * time.Millisecond)

	// Verify entry was stored
	count, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Errorf("Count = %d, want 1", count)
	}

	entry, _ := store.Get(1)
	data := entry.Data.(map[string]any)
	if data["line"] != "hello from agent" {
		t.Errorf("line = %v, want 'hello from agent'", data["line"])
	}
}

func TestCollector_UnixSocket_MultipleMessages(t *testing.T) {
	socketPath, dbPath, cleanup := createTestSocketPath(t)
	defer cleanup()

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	collector := NewCollector(store)
	if err := collector.StartUnix(socketPath); err != nil {
		t.Fatalf("StartUnix: %v", err)
	}
	defer collector.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Send multiple messages
	for i := 0; i < 10; i++ {
		msg := CollectorMessage{
			Type: string(EntryConsole),
			Data: map[string]any{"line": i},
		}
		json.NewEncoder(conn).Encode(msg)
	}

	time.Sleep(100 * time.Millisecond)

	count, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 10 {
		t.Errorf("Count = %d, want 10", count)
	}
}
