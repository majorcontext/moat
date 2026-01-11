package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewRunStore(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run-test123")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	if s.RunID() != "run-test123" {
		t.Errorf("RunID = %q, want %q", s.RunID(), "run-test123")
	}

	// Check directory was created
	runDir := filepath.Join(dir, "run-test123")
	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		t.Error("run directory was not created")
	}
}

func TestRunStoreMetadata(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewRunStore(dir, "run-test456")

	meta := Metadata{
		Agent:     "claude-code",
		Workspace: "/home/user/project",
		Grants:    []string{"github:repo"},
	}
	if err := s.SaveMetadata(meta); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	loaded, err := s.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if loaded.Agent != meta.Agent {
		t.Errorf("Agent = %q, want %q", loaded.Agent, meta.Agent)
	}
}

func TestRunStoreDir(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run-dirtest")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	expectedDir := filepath.Join(dir, "run-dirtest")
	if s.Dir() != expectedDir {
		t.Errorf("Dir = %q, want %q", s.Dir(), expectedDir)
	}
}

func TestDefaultBaseDir(t *testing.T) {
	baseDir := DefaultBaseDir()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	expected := filepath.Join(homeDir, ".agentops", "runs")
	if baseDir != expected {
		t.Errorf("DefaultBaseDir = %q, want %q", baseDir, expected)
	}
}

func TestLoadMetadataPreservesAllFields(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewRunStore(dir, "run-allfields")

	meta := Metadata{
		Agent:     "test-agent",
		Workspace: "/workspace",
		Grants:    []string{"grant1", "grant2"},
		Error:     "some error",
	}
	if err := s.SaveMetadata(meta); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	loaded, err := s.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}

	if loaded.Workspace != meta.Workspace {
		t.Errorf("Workspace = %q, want %q", loaded.Workspace, meta.Workspace)
	}
	if len(loaded.Grants) != len(meta.Grants) {
		t.Errorf("Grants length = %d, want %d", len(loaded.Grants), len(meta.Grants))
	}
	if loaded.Error != meta.Error {
		t.Errorf("Error = %q, want %q", loaded.Error, meta.Error)
	}
}
