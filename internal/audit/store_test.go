package audit

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

	count, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Errorf("Count = %d, want 0", count)
	}

	store.Append(EntryConsole, map[string]any{"line": "1"})
	store.Append(EntryConsole, map[string]any{"line": "2"})
	store.Append(EntryNetwork, map[string]any{"url": "http://example.com"})

	count, err = store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 3 {
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

func TestStore_VerifyChain_Empty(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	result, err := store.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !result.Valid {
		t.Error("Empty chain should be valid")
	}
}

func TestStore_VerifyChain_Valid(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	for i := 0; i < 100; i++ {
		store.Append(EntryConsole, map[string]any{"line": i})
	}

	result, err := store.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !result.Valid {
		t.Errorf("Valid chain should verify: %s", result.Error)
	}
	if result.EntryCount != 100 {
		t.Errorf("EntryCount = %d, want 100", result.EntryCount)
	}
}

func TestStore_VerifyChain_TamperedEntry(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	for i := 0; i < 10; i++ {
		store.Append(EntryConsole, map[string]any{"line": i})
	}

	// Directly tamper with entry 5 in the database
	_, err := store.db.Exec(`UPDATE entries SET data = '{"line": "TAMPERED"}' WHERE seq = 5`)
	if err != nil {
		t.Fatalf("tampering failed: %v", err)
	}

	result, err := store.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if result.Valid {
		t.Error("Tampered chain should not verify")
	}
	if result.FirstInvalidSeq != 5 {
		t.Errorf("FirstInvalidSeq = %d, want 5", result.FirstInvalidSeq)
	}
}

func TestStore_VerifyChain_BrokenLink(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	for i := 0; i < 10; i++ {
		store.Append(EntryConsole, map[string]any{"line": i})
	}

	// Break the chain by modifying prev_hash
	_, err := store.db.Exec(`UPDATE entries SET prev_hash = 'wrong' WHERE seq = 5`)
	if err != nil {
		t.Fatalf("tampering failed: %v", err)
	}

	result, err := store.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if result.Valid {
		t.Error("Broken chain should not verify")
	}
	if result.FirstInvalidSeq != 5 {
		t.Errorf("FirstInvalidSeq = %d, want 5", result.FirstInvalidSeq)
	}
}

func TestStore_AppendConsole(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	entry, err := store.AppendConsole("hello world")
	if err != nil {
		t.Fatalf("AppendConsole: %v", err)
	}

	if entry.Type != EntryConsole {
		t.Errorf("Type = %s, want %s", entry.Type, EntryConsole)
	}

	data := entry.Data.(*ConsoleData)
	if data.Line != "hello world" {
		t.Errorf("Line = %s, want 'hello world'", data.Line)
	}
}

func TestStore_AppendNetwork(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	entry, err := store.AppendNetwork(NetworkData{
		Method:         "GET",
		URL:            "https://api.github.com/user",
		StatusCode:     200,
		DurationMs:     150,
		CredentialUsed: "github",
	})
	if err != nil {
		t.Fatalf("AppendNetwork: %v", err)
	}

	if entry.Type != EntryNetwork {
		t.Errorf("Type = %s, want %s", entry.Type, EntryNetwork)
	}

	data := entry.Data.(*NetworkData)
	if data.URL != "https://api.github.com/user" {
		t.Errorf("URL = %s, want 'https://api.github.com/user'", data.URL)
	}
}

func TestStore_AppendCredential(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	entry, err := store.AppendCredential(CredentialData{
		Name:   "github",
		Action: "injected",
		Host:   "api.github.com",
	})
	if err != nil {
		t.Fatalf("AppendCredential: %v", err)
	}

	if entry.Type != EntryCredential {
		t.Errorf("Type = %s, want %s", entry.Type, EntryCredential)
	}

	data := entry.Data.(*CredentialData)
	if data.Name != "github" {
		t.Errorf("Name = %s, want 'github'", data.Name)
	}
	if data.Action != "injected" {
		t.Errorf("Action = %s, want 'injected'", data.Action)
	}
	if data.Host != "api.github.com" {
		t.Errorf("Host = %s, want 'api.github.com'", data.Host)
	}
}

func TestStore_MerkleRoot_Empty(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	root := store.MerkleRoot()
	if root != "" {
		t.Errorf("MerkleRoot = %q, want empty for empty store", root)
	}
}

func TestStore_MerkleRoot_AfterAppend(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	store.Append(EntryConsole, map[string]any{"line": "test"})

	root := store.MerkleRoot()
	if root == "" {
		t.Error("MerkleRoot should not be empty after append")
	}
}

func TestStore_MerkleRoot_ChangesWithEntries(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	store.Append(EntryConsole, map[string]any{"line": "first"})
	root1 := store.MerkleRoot()

	store.Append(EntryConsole, map[string]any{"line": "second"})
	root2 := store.MerkleRoot()

	if root1 == root2 {
		t.Error("MerkleRoot should change when entries are added")
	}
}

func TestStore_MerkleRoot_PersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create store and add entries
	store1, _ := OpenStore(dbPath)
	store1.Append(EntryConsole, map[string]any{"line": "test"})
	root1 := store1.MerkleRoot()
	store1.Close()

	// Reopen and check root
	store2, _ := OpenStore(dbPath)
	defer store2.Close()
	root2 := store2.MerkleRoot()

	if root1 != root2 {
		t.Errorf("MerkleRoot changed after reopen: %q != %q", root1, root2)
	}
}

func TestStore_ProveEntry(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	// Add several entries
	for i := 0; i < 5; i++ {
		store.Append(EntryConsole, map[string]any{"line": i})
	}

	// Generate proof for entry 3
	proof, err := store.ProveEntry(3)
	if err != nil {
		t.Fatalf("ProveEntry: %v", err)
	}

	if proof.EntrySeq != 3 {
		t.Errorf("EntrySeq = %d, want 3", proof.EntrySeq)
	}
	if proof.RootHash != store.MerkleRoot() {
		t.Error("Proof root should match store's merkle root")
	}
	if !proof.Verify() {
		t.Error("Proof should verify")
	}
}

func TestStore_ProveEntry_NotFound(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	store.Append(EntryConsole, map[string]any{"line": "test"})

	_, err := store.ProveEntry(999)
	if err == nil {
		t.Error("Expected error for non-existent entry")
	}
}

func TestStore_SaveRekorProof(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))
	defer store.Close()

	// Add an entry first
	store.AppendConsole("test log")

	proof := &RekorProof{
		LogIndex:  12345,
		LogID:     "c0d23d6ad406973f",
		TreeSize:  98765432,
		RootHash:  "abc123",
		Hashes:    []string{"def456", "789abc"},
		Timestamp: time.Now().UTC(),
		EntryUUID: "entry-uuid-123",
	}

	err := store.SaveRekorProof(1, proof)
	if err != nil {
		t.Fatalf("SaveRekorProof: %v", err)
	}
}

func TestStore_LoadRekorProofs(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))
	defer store.Close()

	store.AppendConsole("test 1")
	store.AppendConsole("test 2")

	proof1 := &RekorProof{LogIndex: 100, LogID: "id1", EntryUUID: "uuid1", Timestamp: time.Now().UTC()}
	proof2 := &RekorProof{LogIndex: 200, LogID: "id2", EntryUUID: "uuid2", Timestamp: time.Now().UTC()}

	store.SaveRekorProof(1, proof1)
	store.SaveRekorProof(2, proof2)

	proofs, err := store.LoadRekorProofs()
	if err != nil {
		t.Fatalf("LoadRekorProofs: %v", err)
	}

	if len(proofs) != 2 {
		t.Errorf("got %d proofs, want 2", len(proofs))
	}
}
