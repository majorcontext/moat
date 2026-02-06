package log

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInit_FileLogging(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize with file logging
	err := Init(Options{
		Verbose:     false,
		JSONFormat:  false,
		Interactive: false,
		DebugDir:    tmpDir,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Log something
	Info("test message", "key", "value")

	// Close to flush
	Close()

	// Verify file was written
	today := time.Now().Format("2006-01-02")
	logFile := filepath.Join(tmpDir, today+".jsonl")
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}

	if !strings.Contains(string(content), "test message") {
		t.Errorf("expected log file to contain 'test message', got: %s", content)
	}
}

func TestInit_StderrLevels(t *testing.T) {
	var stderr bytes.Buffer
	tmpDir := t.TempDir()

	// Initialize non-verbose, non-interactive
	if err := Init(Options{
		Verbose:     false,
		JSONFormat:  false,
		Interactive: false,
		DebugDir:    tmpDir,
		Stderr:      &stderr,
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	Debug("debug message")
	Info("info message")
	Warn("warn message")
	Error("error message")

	output := stderr.String()

	// Debug and Info should NOT appear on stderr
	if strings.Contains(output, "debug message") {
		t.Error("debug should not appear on stderr in non-verbose mode")
	}
	if strings.Contains(output, "info message") {
		t.Error("info should not appear on stderr in non-verbose mode")
	}

	// Warn and Error should NOT appear on stderr in default mode either
	// (user-facing output now uses the ui package, not slog)
	if strings.Contains(output, "warn message") {
		t.Error("warn should not appear on stderr in default mode")
	}
	if strings.Contains(output, "error message") {
		t.Error("error should not appear on stderr in default mode")
	}

	Close()
}

func TestInit_VerboseNonInteractive(t *testing.T) {
	var stderr bytes.Buffer
	tmpDir := t.TempDir()

	// Initialize verbose, non-interactive
	if err := Init(Options{
		Verbose:     true,
		JSONFormat:  false,
		Interactive: false,
		DebugDir:    tmpDir,
		Stderr:      &stderr,
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	Debug("debug message")
	Info("info message")

	output := stderr.String()

	// Both should appear in verbose mode
	if !strings.Contains(output, "debug message") {
		t.Error("debug should appear on stderr in verbose mode")
	}
	if !strings.Contains(output, "info message") {
		t.Error("info should appear on stderr in verbose mode")
	}

	Close()
}

func TestInit_InteractiveIgnoresVerbose(t *testing.T) {
	var stderr bytes.Buffer
	tmpDir := t.TempDir()

	// Initialize verbose + interactive (verbose should be ignored for stderr)
	if err := Init(Options{
		Verbose:     true,
		JSONFormat:  false,
		Interactive: true,
		DebugDir:    tmpDir,
		Stderr:      &stderr,
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	Debug("debug message")
	Info("info message")

	output := stderr.String()

	// Debug and Info should NOT appear even with verbose flag
	if strings.Contains(output, "debug message") {
		t.Error("debug should not appear on stderr in interactive mode")
	}
	if strings.Contains(output, "info message") {
		t.Error("info should not appear on stderr in interactive mode")
	}

	Close()
}

func TestSetRunContext(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize with file logging (JSON format for easy parsing)
	if err := Init(Options{
		DebugDir: tmpDir,
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Log without run context
	Info("before run")

	// Set run context
	SetRunContext(RunContext{
		RunID:     "test-run-123",
		RunName:   "my-project",
		Agent:     "claude-code",
		Workspace: "myapp",
		Image:     "moat-claude:latest",
		Grants:    []string{"github", "anthropic"},
	})

	// Log with run context
	Info("during run")

	// Close to flush
	Close()

	// Read log file
	today := time.Now().Format("2006-01-02")
	logFile := filepath.Join(tmpDir, today+".jsonl")
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 log lines, got %d", len(lines))
	}

	// First line should NOT have run context
	if strings.Contains(lines[0], "test-run-123") {
		t.Error("first log line should not have run_id")
	}

	// Second line should have all run context fields
	secondLine := lines[1]
	checks := []string{
		"test-run-123",     // run_id
		"my-project",       // run_name
		"claude-code",      // agent
		"myapp",            // workspace
		"moat-claude",      // image (partial match)
		"github,anthropic", // grants
	}
	for _, check := range checks {
		if !strings.Contains(secondLine, check) {
			t.Errorf("second log line should contain %q, got: %s", check, secondLine)
		}
	}
}
