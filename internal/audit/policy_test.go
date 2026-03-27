package audit

import (
	"testing"
)

func TestAppend_PolicyDecisionData(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	data := PolicyDecisionData{
		Scope:     "mcp",
		Operation: "tools/call",
		Decision:  "allow",
		Rule:      "allow-read-tools",
		Message:   "matched allow rule",
	}

	entry, err := store.Append(EntryPolicy, &data)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	if entry.Type != EntryPolicy {
		t.Errorf("Type = %q, want %q", entry.Type, EntryPolicy)
	}
	if entry.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", entry.Sequence)
	}
	if !entry.Verify() {
		t.Error("entry hash verification failed")
	}
}

func TestAppendPolicy(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	data := PolicyDecisionData{
		Scope:     "network",
		Operation: "CONNECT",
		Decision:  "deny",
		Rule:      "deny-external",
		Message:   "blocked outbound connection",
	}

	entry, err := store.AppendPolicy(data)
	if err != nil {
		t.Fatalf("AppendPolicy: %v", err)
	}

	if entry.Type != EntryPolicy {
		t.Errorf("Type = %q, want %q", entry.Type, EntryPolicy)
	}
	if entry.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", entry.Sequence)
	}
	if !entry.Verify() {
		t.Error("entry hash verification failed")
	}

	// Verify round-trip: read back from store and check fields.
	got, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Type != EntryPolicy {
		t.Errorf("round-trip Type = %q, want %q", got.Type, EntryPolicy)
	}
	if !got.Verify() {
		t.Error("round-trip hash verification failed")
	}
}

func TestAppendPolicy_OmitsEmptyFields(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	data := PolicyDecisionData{
		Scope:     "mcp",
		Operation: "tools/call",
		Decision:  "allow",
		// Rule and Message intentionally empty — should be omitted from JSON.
	}

	entry, err := store.AppendPolicy(data)
	if err != nil {
		t.Fatalf("AppendPolicy: %v", err)
	}

	if entry.Type != EntryPolicy {
		t.Errorf("Type = %q, want %q", entry.Type, EntryPolicy)
	}
	if !entry.Verify() {
		t.Error("entry hash verification failed")
	}
}
