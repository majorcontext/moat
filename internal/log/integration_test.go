//go:build integration

package log

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIntegration_FullLifecycle(t *testing.T) {
	tmpDir := t.TempDir()

	// Create some old files to test cleanup
	oldDate := time.Now().AddDate(0, 0, -20).Format("2006-01-02")
	oldFile := filepath.Join(tmpDir, oldDate+".jsonl")
	os.WriteFile(oldFile, []byte("old log"), 0644)

	// Initialize logger
	err := Init(Options{
		Verbose:       false,
		Interactive:   false,
		DebugDir:      tmpDir,
		RetentionDays: 14,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Old file should have been cleaned up
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old log file should have been cleaned up")
	}

	// Log some messages
	Debug("debug message", "key", "value")
	Info("info message")
	Warn("warn message")
	Error("error message")

	// Close to flush
	Close()

	// Verify today's file contains all messages
	today := time.Now().Format("2006-01-02")
	logFile := filepath.Join(tmpDir, today+".jsonl")
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}

	contentStr := string(content)
	for _, msg := range []string{"debug message", "info message", "warn message", "error message"} {
		if !strings.Contains(contentStr, msg) {
			t.Errorf("log file should contain %q", msg)
		}
	}

	// Verify symlink
	symlinkPath := filepath.Join(tmpDir, "latest")
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("reading symlink: %v", err)
	}
	if target != today+".jsonl" {
		t.Errorf("symlink should point to %s.jsonl, got %s", today, target)
	}
}
