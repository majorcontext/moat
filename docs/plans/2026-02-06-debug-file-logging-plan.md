# Debug File Logging Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add always-on debug file logging to `~/.moat/debug/` with daily rotation, configurable retention, and TUI-safe stderr behavior.

**Architecture:** New `internal/log/file.go` handles file writing, rotation, and cleanup. Modified `log.Init()` creates a multi-handler slog setup (stderr + file). Stderr shows Warn+Error by default; `--verbose` in non-interactive mode adds Debug+Info.

**Tech Stack:** Go stdlib only (`log/slog`, `os`, `path/filepath`, `time`, `encoding/json`, `sync`)

---

## Task 1: Add Debug Config to GlobalConfig

**Files:**
- Modify: `internal/config/global.go`
- Test: `internal/config/global_test.go`

**Step 1: Write the failing test**

Create test file `internal/config/global_test.go` (if not exists, add to existing):

```go
func TestLoadGlobal_DebugConfig(t *testing.T) {
	// Create temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(configPath, []byte("debug:\n  retention_days: 7\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Override home dir for test
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create .moat directory and move config
	moatDir := filepath.Join(tmpDir, ".moat")
	os.MkdirAll(moatDir, 0755)
	os.Rename(configPath, filepath.Join(moatDir, "config.yaml"))

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal failed: %v", err)
	}

	if cfg.Debug.RetentionDays != 7 {
		t.Errorf("expected RetentionDays=7, got %d", cfg.Debug.RetentionDays)
	}
}

func TestDefaultGlobalConfig_DebugDefaults(t *testing.T) {
	cfg := DefaultGlobalConfig()
	if cfg.Debug.RetentionDays != 14 {
		t.Errorf("expected default RetentionDays=14, got %d", cfg.Debug.RetentionDays)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestLoadGlobal_DebugConfig ./internal/config/ -v`
Expected: FAIL with "cfg.Debug undefined"

**Step 3: Write minimal implementation**

In `internal/config/global.go`, add DebugConfig struct and field:

```go
// DebugConfig holds debug logging settings.
type DebugConfig struct {
	RetentionDays int `yaml:"retention_days"`
}

// GlobalConfig holds global Moat settings from ~/.moat/config.yaml.
type GlobalConfig struct {
	Proxy ProxyConfig `yaml:"proxy"`
	Debug DebugConfig `yaml:"debug"`
}

// DefaultGlobalConfig returns the default global configuration.
func DefaultGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		Proxy: ProxyConfig{
			Port: 8080,
		},
		Debug: DebugConfig{
			RetentionDays: 14,
		},
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test -run TestLoadGlobal_DebugConfig ./internal/config/ -v`
Expected: PASS

Run: `go test -run TestDefaultGlobalConfig_DebugDefaults ./internal/config/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/global.go internal/config/global_test.go
git commit -m "feat(config): add debug.retention_days to global config"
```

---

## Task 2: Create FileWriter for Debug Logging

**Files:**
- Create: `internal/log/file.go`
- Create: `internal/log/file_test.go`

**Step 1: Write the failing test**

Create `internal/log/file_test.go`:

```go
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
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestFileWriter ./internal/log/ -v`
Expected: FAIL with "undefined: NewFileWriter"

**Step 3: Write minimal implementation**

Create `internal/log/file.go`:

```go
package log

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileWriter manages daily log file rotation and symlink updates.
type FileWriter struct {
	dir      string
	mu       sync.Mutex
	file     *os.File
	currDate string
}

// NewFileWriter creates a FileWriter that writes to dir/YYYY-MM-DD.jsonl.
func NewFileWriter(dir string) (*FileWriter, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating debug log dir: %w", err)
	}

	fw := &FileWriter{dir: dir}
	if err := fw.rotate(); err != nil {
		return nil, err
	}
	return fw, nil
}

// Write implements io.Writer. It handles daily rotation.
func (fw *FileWriter) Write(p []byte) (n int, err error) {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if today != fw.currDate {
		if err := fw.rotateLocked(); err != nil {
			return 0, err
		}
	}

	return fw.file.Write(p)
}

// Close closes the underlying file.
func (fw *FileWriter) Close() error {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.file != nil {
		return fw.file.Close()
	}
	return nil
}

func (fw *FileWriter) rotate() error {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.rotateLocked()
}

func (fw *FileWriter) rotateLocked() error {
	if fw.file != nil {
		fw.file.Close()
	}

	today := time.Now().Format("2006-01-02")
	filename := today + ".jsonl"
	path := filepath.Join(fw.dir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}

	fw.file = f
	fw.currDate = today

	// Update symlink atomically
	fw.updateSymlink(filename)

	return nil
}

func (fw *FileWriter) updateSymlink(target string) {
	symlinkPath := filepath.Join(fw.dir, "latest")
	tmpPath := symlinkPath + ".tmp"

	// Remove temp if exists, create new symlink, rename
	os.Remove(tmpPath)
	if err := os.Symlink(target, tmpPath); err != nil {
		return // Best effort
	}
	os.Rename(tmpPath, symlinkPath)
}
```

**Step 4: Run test to verify it passes**

Run: `go test -run TestFileWriter ./internal/log/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/log/file.go internal/log/file_test.go
git commit -m "feat(log): add FileWriter for daily rotated debug logs"
```

---

## Task 3: Add Cleanup Function

**Files:**
- Modify: `internal/log/file.go`
- Modify: `internal/log/file_test.go`

**Step 1: Write the failing test**

Add to `internal/log/file_test.go`:

```go
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
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestCleanup ./internal/log/ -v`
Expected: FAIL with "undefined: Cleanup"

**Step 3: Write minimal implementation**

Add to `internal/log/file.go`:

```go
import (
	"regexp"
	// ... existing imports
)

// datePattern matches YYYY-MM-DD.jsonl filenames.
var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.jsonl$`)

// Cleanup removes log files older than retentionDays.
func Cleanup(dir string, retentionDays int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // Directory doesn't exist or can't be read
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !datePattern.MatchString(name) {
			continue // Not a log file
		}

		// Parse date from filename
		dateStr := name[:10] // "YYYY-MM-DD"
		fileDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue // Malformed, skip
		}

		if fileDate.Before(cutoff) {
			os.Remove(filepath.Join(dir, name))
		}
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test -run TestCleanup ./internal/log/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/log/file.go internal/log/file_test.go
git commit -m "feat(log): add Cleanup for log file retention"
```

---

## Task 4: Create Multi-Handler slog Setup

**Files:**
- Modify: `internal/log/log.go`
- Create: `internal/log/log_test.go`

**Step 1: Write the failing test**

Create `internal/log/log_test.go`:

```go
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
	Init(Options{
		Verbose:     false,
		JSONFormat:  false,
		Interactive: false,
		DebugDir:    tmpDir,
		Stderr:      &stderr,
	})

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

	// Warn and Error SHOULD appear
	if !strings.Contains(output, "warn message") {
		t.Error("warn should appear on stderr")
	}
	if !strings.Contains(output, "error message") {
		t.Error("error should appear on stderr")
	}

	Close()
}

func TestInit_VerboseNonInteractive(t *testing.T) {
	var stderr bytes.Buffer
	tmpDir := t.TempDir()

	// Initialize verbose, non-interactive
	Init(Options{
		Verbose:     true,
		JSONFormat:  false,
		Interactive: false,
		DebugDir:    tmpDir,
		Stderr:      &stderr,
	})

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
	Init(Options{
		Verbose:     true,
		JSONFormat:  false,
		Interactive: true,
		DebugDir:    tmpDir,
		Stderr:      &stderr,
	})

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
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestInit ./internal/log/ -v`
Expected: FAIL (Init signature mismatch, Options undefined)

**Step 3: Write minimal implementation**

Replace `internal/log/log.go` with:

```go
package log

import (
	"io"
	"log/slog"
	"os"
)

var (
	logger     *slog.Logger
	fileWriter *FileWriter
)

// Options configures the logger.
type Options struct {
	Verbose       bool      // Enable debug output on stderr (non-interactive only)
	JSONFormat    bool      // Use JSON format for stderr
	Interactive   bool      // Interactive mode (suppresses debug on stderr)
	DebugDir      string    // Directory for debug log files (empty = disabled)
	RetentionDays int       // Days to keep old log files (0 = use default 14)
	Stderr        io.Writer // Override stderr (for testing)
}

// Init initializes the global logger with file and stderr handlers.
func Init(opts Options) error {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	// Determine stderr log level
	stderrLevel := slog.LevelWarn // Default: Warn and Error only
	if opts.Verbose && !opts.Interactive {
		stderrLevel = slog.LevelDebug // Verbose in non-interactive: all levels
	}

	// Create stderr handler
	stderrOpts := &slog.HandlerOptions{Level: stderrLevel}
	var stderrHandler slog.Handler
	if opts.JSONFormat {
		stderrHandler = slog.NewJSONHandler(stderr, stderrOpts)
	} else {
		stderrHandler = slog.NewTextHandler(stderr, stderrOpts)
	}

	// Create file handler if debug dir specified
	var handlers []slog.Handler
	handlers = append(handlers, stderrHandler)

	if opts.DebugDir != "" {
		// Run cleanup first
		retention := opts.RetentionDays
		if retention <= 0 {
			retention = 14
		}
		Cleanup(opts.DebugDir, retention)

		fw, err := NewFileWriter(opts.DebugDir)
		if err != nil {
			// Log to stderr and continue without file logging
			slog.New(stderrHandler).Warn("failed to initialize debug file logging", "error", err)
		} else {
			fileWriter = fw
			// File handler always captures all levels
			fileOpts := &slog.HandlerOptions{Level: slog.LevelDebug}
			fileHandler := slog.NewJSONHandler(fw, fileOpts)
			handlers = append(handlers, fileHandler)
		}
	}

	// Create multi-handler logger
	logger = slog.New(&multiHandler{handlers: handlers})
	slog.SetDefault(logger)

	return nil
}

// Close cleans up resources (closes file writer).
func Close() {
	if fileWriter != nil {
		fileWriter.Close()
		fileWriter = nil
	}
}

// multiHandler fans out to multiple handlers.
type multiHandler struct {
	handlers []slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		newHandlers[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: newHandlers}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		newHandlers[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: newHandlers}
}

// Debug logs a debug message.
func Debug(msg string, args ...any) {
	logger.Debug(msg, args...)
}

// Info logs an info message.
func Info(msg string, args ...any) {
	logger.Info(msg, args...)
}

// Warn logs a warning message.
func Warn(msg string, args ...any) {
	logger.Warn(msg, args...)
}

// Error logs an error message.
func Error(msg string, args ...any) {
	logger.Error(msg, args...)
}

// With returns a logger with additional context.
func With(args ...any) *slog.Logger {
	return logger.With(args...)
}

// SetOutput sets the output writer (for testing).
func SetOutput(w io.Writer) {
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger = slog.New(handler)
	slog.SetDefault(logger)
}

func init() {
	// Default logger until Init is called
	logger = slog.Default()
}
```

Note: Add `"context"` to imports for the multiHandler methods.

**Step 4: Run test to verify it passes**

Run: `go test -run TestInit ./internal/log/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/log/log.go internal/log/log_test.go
git commit -m "feat(log): add multi-handler logging with file and stderr"
```

---

## Task 5: Update CLI to Use New Log Init

**Files:**
- Modify: `cmd/moat/cli/root.go`

**Step 1: Identify the change needed**

The current `root.go` calls `log.Init(verbose, jsonOut)`. We need to:
1. Change to `log.Init(log.Options{...})`
2. Load global config to get retention days
3. Pass debug dir from global config dir

**Step 2: Make the change**

In `cmd/moat/cli/root.go`, update imports and PersistentPreRun:

```go
import (
	"path/filepath"

	"github.com/majorcontext/moat/internal/config"
	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/spf13/cobra"
)

// ... existing vars ...

var rootCmd = &cobra.Command{
	// ... existing fields ...
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Load global config for debug settings
		globalCfg, _ := config.LoadGlobal()
		debugDir := filepath.Join(config.GlobalConfigDir(), "debug")

		// Check if this is an interactive command
		interactive := false
		if cmd.Flags().Lookup("interactive") != nil {
			interactive, _ = cmd.Flags().GetBool("interactive")
		}

		log.Init(log.Options{
			Verbose:       verbose,
			JSONFormat:    jsonOut,
			Interactive:   interactive,
			DebugDir:      debugDir,
			RetentionDays: globalCfg.Debug.RetentionDays,
		})

		// Sync dry-run state to internal/cli package for providers
		intcli.DryRun = dryRun
	},
}
```

**Step 3: Verify build succeeds**

Run: `go build ./cmd/moat/`
Expected: Success

**Step 4: Manual test**

Run: `./moat --help`
Check: `~/.moat/debug/` directory created with today's log file

**Step 5: Commit**

```bash
git add cmd/moat/cli/root.go
git commit -m "feat(cli): integrate debug file logging in root command"
```

---

## Task 6: Add Integration Test

**Files:**
- Create: `internal/log/integration_test.go`

**Step 1: Write integration test**

Create `internal/log/integration_test.go`:

```go
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
```

**Step 2: Run integration test**

Run: `go test -tags=integration -run TestIntegration ./internal/log/ -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/log/integration_test.go
git commit -m "test(log): add integration test for debug file logging"
```

---

## Task 7: Run Full Test Suite and Lint

**Step 1: Run all tests**

Run: `go test ./...`
Expected: All tests pass

**Step 2: Run linter**

Run: `make lint` or `go vet ./...`
Expected: No errors

**Step 3: Fix any issues**

If lint errors, fix and amend commit.

**Step 4: Final commit if needed**

```bash
git add -A
git commit -m "fix(log): address lint issues"
```

---

## Summary

| Task | Description | Files |
|------|-------------|-------|
| 1 | Add Debug config to GlobalConfig | `internal/config/global.go`, `global_test.go` |
| 2 | Create FileWriter | `internal/log/file.go`, `file_test.go` |
| 3 | Add Cleanup function | `internal/log/file.go`, `file_test.go` |
| 4 | Create multi-handler slog setup | `internal/log/log.go`, `log_test.go` |
| 5 | Update CLI to use new log.Init | `cmd/moat/cli/root.go` |
| 6 | Add integration test | `internal/log/integration_test.go` |
| 7 | Run full test suite and lint | — |
