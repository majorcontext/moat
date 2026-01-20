package snapshot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSnapshotID(t *testing.T) {
	id := NewID()
	if len(id) != 13 { // "snap_" + 8 hex chars
		t.Errorf("expected ID length 13, got %d: %s", len(id), id)
	}
	if id[:5] != "snap_" {
		t.Errorf("expected prefix 'snap_', got %s", id[:5])
	}
}

func TestSnapshotIDUniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := NewID()
		if ids[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}

func TestSnapshotTypes(t *testing.T) {
	tests := []struct {
		typ  Type
		want string
	}{
		{TypePreRun, "pre-run"},
		{TypeGit, "git"},
		{TypeBuild, "build"},
		{TypeIdle, "idle"},
		{TypeManual, "manual"},
		{TypeSafety, "safety"},
	}
	for _, tt := range tests {
		if tt.typ.String() != tt.want {
			t.Errorf("Type.String() = %s, want %s", tt.typ.String(), tt.want)
		}
	}
}

func TestSnapshotMetadata(t *testing.T) {
	meta := Metadata{
		ID:        "snap_abc123",
		Type:      TypePreRun,
		CreatedAt: time.Now(),
		Backend:   "apfs",
	}
	if meta.ID != "snap_abc123" {
		t.Errorf("unexpected ID: %s", meta.ID)
	}
}

func TestSnapshotMetadataJSON(t *testing.T) {
	sizeDelta := int64(1024)
	original := Metadata{
		ID:        "snap_abc12345",
		Type:      TypeGit,
		Label:     "before refactor",
		Backend:   "apfs",
		CreatedAt: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		SizeDelta: &sizeDelta,
		NativeRef: "apfs://snapshot/123",
	}

	// Marshal to JSON
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal metadata: %v", err)
	}

	// Unmarshal back
	var restored Metadata
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("failed to unmarshal metadata: %v", err)
	}

	// Verify fields
	if restored.ID != original.ID {
		t.Errorf("ID mismatch: got %s, want %s", restored.ID, original.ID)
	}
	if restored.Type != original.Type {
		t.Errorf("Type mismatch: got %s, want %s", restored.Type, original.Type)
	}
	if restored.Label != original.Label {
		t.Errorf("Label mismatch: got %s, want %s", restored.Label, original.Label)
	}
	if restored.Backend != original.Backend {
		t.Errorf("Backend mismatch: got %s, want %s", restored.Backend, original.Backend)
	}
	if !restored.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("CreatedAt mismatch: got %v, want %v", restored.CreatedAt, original.CreatedAt)
	}
	if restored.SizeDelta == nil || *restored.SizeDelta != *original.SizeDelta {
		t.Errorf("SizeDelta mismatch: got %v, want %v", restored.SizeDelta, original.SizeDelta)
	}
	if restored.NativeRef != original.NativeRef {
		t.Errorf("NativeRef mismatch: got %s, want %s", restored.NativeRef, original.NativeRef)
	}
}

func TestSnapshotMetadataJSONOmitEmpty(t *testing.T) {
	// Test that optional fields are omitted when empty
	meta := Metadata{
		ID:        "snap_12345678",
		Type:      TypeManual,
		Backend:   "archive",
		CreatedAt: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("failed to marshal metadata: %v", err)
	}

	// Check that omitempty fields are not present
	jsonStr := string(data)
	if strings.Contains(jsonStr, "label") {
		t.Errorf("expected label to be omitted, got: %s", jsonStr)
	}
	if strings.Contains(jsonStr, "size_delta") {
		t.Errorf("expected size_delta to be omitted, got: %s", jsonStr)
	}
	if strings.Contains(jsonStr, "native_ref") {
		t.Errorf("expected native_ref to be omitted, got: %s", jsonStr)
	}
}
