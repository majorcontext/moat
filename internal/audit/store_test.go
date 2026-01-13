package audit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_OpenClose(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "logs.db")

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	// Verify file was created
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("Database file not created: %v", err)
	}
}

func TestStore_Append_FirstEntry(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	entry, err := store.Append(EntryConsole, map[string]any{"line": "hello"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	if entry.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", entry.Sequence)
	}
	if entry.PrevHash != "" {
		t.Errorf("PrevHash = %q, want empty for first entry", entry.PrevHash)
	}
	if entry.Hash == "" {
		t.Error("Hash should not be empty")
	}
}

func TestStore_Append_ChainedEntries(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	e1, _ := store.Append(EntryConsole, map[string]any{"line": "first"})
	e2, _ := store.Append(EntryConsole, map[string]any{"line": "second"})
	e3, _ := store.Append(EntryNetwork, map[string]any{"url": "https://api.github.com"})

	if e2.PrevHash != e1.Hash {
		t.Errorf("e2.PrevHash = %s, want %s", e2.PrevHash, e1.Hash)
	}
	if e3.PrevHash != e2.Hash {
		t.Errorf("e3.PrevHash = %s, want %s", e3.PrevHash, e2.Hash)
	}
	if e3.Sequence != 3 {
		t.Errorf("e3.Sequence = %d, want 3", e3.Sequence)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "logs.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return store
}
