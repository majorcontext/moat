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

func TestStore_Get(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	original, _ := store.Append(EntryConsole, map[string]any{"line": "test"})

	retrieved, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if retrieved.Hash != original.Hash {
		t.Errorf("Hash = %s, want %s", retrieved.Hash, original.Hash)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	_, err := store.Get(999)
	if err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestStore_Count(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	if count := store.Count(); count != 0 {
		t.Errorf("Count = %d, want 0", count)
	}

	store.Append(EntryConsole, map[string]any{"line": "1"})
	store.Append(EntryConsole, map[string]any{"line": "2"})
	store.Append(EntryNetwork, map[string]any{"url": "http://example.com"})

	if count := store.Count(); count != 3 {
		t.Errorf("Count = %d, want 3", count)
	}
}

func TestStore_Range(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	for i := 0; i < 10; i++ {
		store.Append(EntryConsole, map[string]any{"line": i})
	}

	entries, err := store.Range(3, 7)
	if err != nil {
		t.Fatalf("Range: %v", err)
	}

	if len(entries) != 5 {
		t.Errorf("len(entries) = %d, want 5", len(entries))
	}
	if entries[0].Sequence != 3 {
		t.Errorf("First entry seq = %d, want 3", entries[0].Sequence)
	}
	if entries[4].Sequence != 7 {
		t.Errorf("Last entry seq = %d, want 7", entries[4].Sequence)
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
