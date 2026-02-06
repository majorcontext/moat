package log

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileWriter_Write(t *testing.T) {
	tmpDir := t.TempDir()

	fw, err := NewFileWriter(tmpDir)
	if err != nil {
		t.Fatalf("NewFileWriter failed: %v", err)
	}
	defer fw.Close()

	// Write a log line
	_, err = fw.Write([]byte(`{"msg":"test"}`))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Verify file exists with today's date
	today := time.Now().Format("2006-01-02")
	logFile := filepath.Join(tmpDir, today+".jsonl")
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Errorf("expected log file %s to exist", logFile)
	}

	// Verify content
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if !strings.Contains(string(content), `{"msg":"test"}`) {
		t.Errorf("expected content to contain test message, got: %s", content)
	}
}

func TestFileWriter_LatestSymlink(t *testing.T) {
	tmpDir := t.TempDir()

	fw, err := NewFileWriter(tmpDir)
	if err != nil {
		t.Fatalf("NewFileWriter failed: %v", err)
	}
	defer fw.Close()

	// Write something to create the file
	fw.Write([]byte(`{"msg":"test"}`))

	// Verify symlink exists
	symlinkPath := filepath.Join(tmpDir, "latest")
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("reading symlink: %v", err)
	}

	today := time.Now().Format("2006-01-02")
	expected := today + ".jsonl"
	if target != expected {
		t.Errorf("expected symlink to point to %s, got %s", expected, target)
	}
}

func TestCleanup(t *testing.T) {
	tmpDir := t.TempDir()

	// Create old log files
	oldDate := time.Now().AddDate(0, 0, -20).Format("2006-01-02")
	oldFile := filepath.Join(tmpDir, oldDate+".jsonl")
	os.WriteFile(oldFile, []byte("old"), 0644)

	// Create recent log file
	recentDate := time.Now().AddDate(0, 0, -5).Format("2006-01-02")
	recentFile := filepath.Join(tmpDir, recentDate+".jsonl")
	os.WriteFile(recentFile, []byte("recent"), 0644)

	// Create non-log file (should be ignored)
	otherFile := filepath.Join(tmpDir, "other.txt")
	os.WriteFile(otherFile, []byte("other"), 0644)

	// Run cleanup with 14 day retention
	Cleanup(tmpDir, 14)

	// Old file should be deleted
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("expected old file %s to be deleted", oldFile)
	}

	// Recent file should remain
	if _, err := os.Stat(recentFile); os.IsNotExist(err) {
		t.Errorf("expected recent file %s to remain", recentFile)
	}

	// Non-log file should remain
	if _, err := os.Stat(otherFile); os.IsNotExist(err) {
		t.Errorf("expected other file %s to remain", otherFile)
	}
}
