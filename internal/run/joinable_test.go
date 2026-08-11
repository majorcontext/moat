package run

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/majorcontext/moat/internal/deps"
	"github.com/majorcontext/moat/internal/storage"
)

func TestComputeJoinableAgents(t *testing.T) {
	tests := []struct {
		name          string
		initProviders []string
		depList       []deps.Dependency
		want          []string
	}{
		{
			name:          "staged with CLI dep is joinable",
			initProviders: []string{"claude"},
			depList:       []deps.Dependency{{Name: "claude-code"}},
			want:          []string{"claude"},
		},
		{
			name:          "staged without CLI dep is not joinable",
			initProviders: []string{"claude"},
			depList:       []deps.Dependency{{Name: "git"}},
			want:          []string{},
		},
		{
			name:          "CLI dep without staging is not joinable",
			initProviders: nil,
			depList:       []deps.Dependency{{Name: "claude-code"}},
			want:          []string{},
		},
		{
			name:          "dual agent",
			initProviders: []string{"claude", "codex"},
			depList:       []deps.Dependency{{Name: "claude-code"}, {Name: "codex-cli"}},
			want:          []string{"claude", "codex"},
		},
		{
			name:          "result is sorted regardless of input order",
			initProviders: []string{"codex", "claude"},
			depList:       []deps.Dependency{{Name: "codex-cli"}, {Name: "claude-code"}},
			want:          []string{"claude", "codex"},
		},
		{
			name:          "unknown provider name is ignored",
			initProviders: []string{"nonsense"},
			depList:       []deps.Dependency{{Name: "claude-code"}},
			want:          []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeJoinableAgents(tt.initProviders, tt.depList)
			if got == nil {
				t.Fatal("computeJoinableAgents must never return nil — nil means 'pre-upgrade run' downstream")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("computeJoinableAgents() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJoinableAgentsRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		value []string
	}{
		{"populated set survives", []string{"claude", "codex"}},
		{"empty set stays empty, not nil", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := storage.NewRunStore(dir, "run_test12345678")
			if err != nil {
				t.Fatalf("NewRunStore: %v", err)
			}
			if err := store.SaveMetadata(storage.Metadata{JoinableAgents: tt.value}); err != nil {
				t.Fatalf("SaveMetadata: %v", err)
			}
			got, err := store.LoadMetadata()
			if err != nil {
				t.Fatalf("LoadMetadata: %v", err)
			}
			if got.JoinableAgents == nil {
				t.Fatal("a persisted set must not load back as nil — nil means pre-upgrade")
			}
			if !reflect.DeepEqual(got.JoinableAgents, tt.value) {
				t.Errorf("JoinableAgents = %v, want %v", got.JoinableAgents, tt.value)
			}
		})
	}
}

func TestJoinableAgentsAbsentLoadsAsNil(t *testing.T) {
	// Companion to the round-trip: metadata written before this field existed
	// must load as nil so join can tell it apart from an empty set.
	dir := t.TempDir()
	path := filepath.Join(dir, "run_legacy12345678")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"name":"old-run","workspace":"/tmp","agent":"claude"}`
	if err := os.WriteFile(filepath.Join(path, "metadata.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewRunStore(dir, "run_legacy12345678")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	got, err := store.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if got.JoinableAgents != nil {
		t.Errorf("legacy metadata should load JoinableAgents as nil, got %v", got.JoinableAgents)
	}
}
