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
