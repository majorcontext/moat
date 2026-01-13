package audit

import (
	"testing"
	"time"
)

func TestNewEntry_AssignsSequenceAndHash(t *testing.T) {
	e := NewEntry(1, "", EntryConsole, map[string]any{"line": "hello"})

	if e.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", e.Sequence)
	}
	if e.Type != EntryConsole {
		t.Errorf("Type = %s, want %s", e.Type, EntryConsole)
	}
	if e.Hash == "" {
		t.Error("Hash should not be empty")
	}
	if e.PrevHash != "" {
		t.Error("PrevHash should be empty for first entry")
	}
}

func TestNewEntry_IncludesPrevHash(t *testing.T) {
	e1 := NewEntry(1, "", EntryConsole, map[string]any{"line": "first"})
	e2 := NewEntry(2, e1.Hash, EntryConsole, map[string]any{"line": "second"})

	if e2.PrevHash != e1.Hash {
		t.Errorf("PrevHash = %s, want %s", e2.PrevHash, e1.Hash)
	}
}

func TestEntry_HashIsConsistent(t *testing.T) {
	ts := time.Date(2026, 1, 12, 10, 0, 0, 0, time.UTC)
	data := map[string]any{"line": "test"}

	e1 := newEntryWithTimestamp(1, "", EntryConsole, data, ts)
	e2 := newEntryWithTimestamp(1, "", EntryConsole, data, ts)

	if e1.Hash != e2.Hash {
		t.Errorf("Hashes should be identical for same inputs")
	}
}

func TestEntry_HashChangesWithSequence(t *testing.T) {
	ts := time.Date(2026, 1, 12, 10, 0, 0, 0, time.UTC)
	data := map[string]any{"line": "test"}

	e1 := newEntryWithTimestamp(1, "", EntryConsole, data, ts)
	e2 := newEntryWithTimestamp(2, "", EntryConsole, data, ts)

	if e1.Hash == e2.Hash {
		t.Error("Different sequences should produce different hashes")
	}
}
