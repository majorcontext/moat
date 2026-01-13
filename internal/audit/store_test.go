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

	e1, err := store.Append(EntryConsole, map[string]any{"line": "first"})
	if err != nil {
		t.Fatalf("Append e1: %v", err)
	}
	e2, err := store.Append(EntryConsole, map[string]any{"line": "second"})
	if err != nil {
		t.Fatalf("Append e2: %v", err)
	}
	e3, err := store.Append(EntryNetwork, map[string]any{"url": "https://api.github.com"})
	if err != nil {
		t.Fatalf("Append e3: %v", err)
	}

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

func TestStore_PersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "logs.db")

	// First session: create entries
	store1, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	e1, err := store1.Append(EntryConsole, map[string]any{"line": "first"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	e2, err := store1.Append(EntryConsole, map[string]any{"line": "second"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	store1.Close()

	// Second session: reopen and continue chain
	store2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer store2.Close()

	e3, err := store2.Append(EntryConsole, map[string]any{"line": "third"})
	if err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}

	// Verify chain continues correctly
	if e3.Sequence != 3 {
		t.Errorf("e3.Sequence = %d, want 3", e3.Sequence)
	}
	if e3.PrevHash != e2.Hash {
		t.Errorf("e3.PrevHash = %s, want %s (chain broken)", e3.PrevHash, e2.Hash)
	}

	// Silence unused variable warning
	_ = e1
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
