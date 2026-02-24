package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

func TestFindLatestSessionID(t *testing.T) {
	runStart := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("picks most recently modified UUID file", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionFile(t, dir, "aaaaaaaa-1111-2222-3333-444455556666", runStart.Add(1*time.Minute))
		writeSessionFile(t, dir, "bbbbbbbb-1111-2222-3333-444455556666", runStart.Add(5*time.Minute))

		got := findLatestSessionID(dir, runStart)
		if got != "bbbbbbbb-1111-2222-3333-444455556666" {
			t.Errorf("got %q, want most recent session", got)
		}
	})

	t.Run("ignores files before run started", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionFile(t, dir, "aaaaaaaa-1111-2222-3333-444455556666", runStart.Add(-1*time.Hour))

		got := findLatestSessionID(dir, runStart)
		if got != "" {
			t.Errorf("got %q, want empty (file predates run)", got)
		}
	})

	t.Run("ignores non-UUID files", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionFile(t, dir, "aaaaaaaa-1111-2222-3333-444455556666", runStart.Add(1*time.Minute))
		if err := os.WriteFile(filepath.Join(dir, "not-a-uuid.jsonl"), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}

		got := findLatestSessionID(dir, runStart)
		if got != "aaaaaaaa-1111-2222-3333-444455556666" {
			t.Errorf("got %q, want the UUID file", got)
		}
	})

	t.Run("ignores directories", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "aaaaaaaa-1111-2222-3333-444455556666"), 0755); err != nil {
			t.Fatal(err)
		}

		got := findLatestSessionID(dir, runStart)
		if got != "" {
			t.Errorf("got %q, want empty (directories ignored)", got)
		}
	})

	t.Run("empty directory", func(t *testing.T) {
		dir := t.TempDir()
		got := findLatestSessionID(dir, runStart)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("nonexistent directory", func(t *testing.T) {
		got := findLatestSessionID("/nonexistent/path", runStart)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("zero start time accepts all files", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionFile(t, dir, "aaaaaaaa-1111-2222-3333-444455556666", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))

		got := findLatestSessionID(dir, time.Time{})
		if got != "aaaaaaaa-1111-2222-3333-444455556666" {
			t.Errorf("got %q, want session ID", got)
		}
	})
}

func TestOnRunStopped_NoWorkspace(t *testing.T) {
	p := &OAuthProvider{}
	result := p.OnRunStopped(provider.RunStoppedContext{})
	if result != nil {
		t.Errorf("OnRunStopped() = %v, want nil for empty workspace", result)
	}
}

// writeSessionFile creates a fake Claude session JSONL file with the given
// modification time.
func writeSessionFile(t *testing.T, dir, uuid string, modTime time.Time) {
	t.Helper()
	path := filepath.Join(dir, uuid+".jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}
