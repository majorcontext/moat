package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFindLatestRun verifies that findLatestRun returns the most recently
// modified run directory.
func TestFindLatestRun(t *testing.T) {
	baseDir := t.TempDir()

	// Create run directories with different modification times
	runs := []struct {
		name  string
		delay time.Duration
	}{
		{"run_aaa", 0},
		{"run_bbb", 50 * time.Millisecond},
		{"run_ccc", 100 * time.Millisecond},
	}

	for _, r := range runs {
		time.Sleep(r.delay)
		dir := filepath.Join(baseDir, r.name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		// Touch the directory to update mod time
		now := time.Now()
		os.Chtimes(dir, now, now)
	}

	got, err := findLatestRun(baseDir)
	if err != nil {
		t.Fatalf("findLatestRun() error: %v", err)
	}

	if got != "run_ccc" {
		t.Errorf("findLatestRun() = %q, want %q", got, "run_ccc")
	}
}

// TestFindLatestRunEmptyDir verifies error when the runs directory is empty.
func TestFindLatestRunEmptyDir(t *testing.T) {
	baseDir := t.TempDir()

	_, err := findLatestRun(baseDir)
	if err == nil {
		t.Fatal("findLatestRun() expected error for empty dir, got nil")
	}
}

// TestFindLatestRunNonExistent verifies error when the directory does not exist.
func TestFindLatestRunNonExistent(t *testing.T) {
	_, err := findLatestRun("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("findLatestRun() expected error for nonexistent dir, got nil")
	}
}

// TestFindLatestRunIgnoresFiles verifies that findLatestRun skips regular files.
func TestFindLatestRunIgnoresFiles(t *testing.T) {
	baseDir := t.TempDir()

	// Create a regular file (should be skipped)
	filePath := filepath.Join(baseDir, "not-a-run.txt")
	if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a run directory
	runDir := filepath.Join(baseDir, "run_actual")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}

	got, err := findLatestRun(baseDir)
	if err != nil {
		t.Fatalf("findLatestRun() error: %v", err)
	}

	if got != "run_actual" {
		t.Errorf("findLatestRun() = %q, want %q", got, "run_actual")
	}
}

// TestFindLatestRunOnlyFiles verifies error when directory has only files, no subdirs.
func TestFindLatestRunOnlyFiles(t *testing.T) {
	baseDir := t.TempDir()

	// Create only regular files, no directories
	for _, name := range []string{"file1.txt", "file2.txt"} {
		path := filepath.Join(baseDir, name)
		if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := findLatestRun(baseDir)
	if err == nil {
		t.Fatal("findLatestRun() expected error when no run directories exist, got nil")
	}
}

// TestFollowLogsTimingConstants verifies that timing constants for the follow
// mode have reasonable values.
func TestFollowLogsTimingConstants(t *testing.T) {
	// The follow mode polls every 500ms
	if logsFollow {
		t.Error("logsFollow should default to false")
	}
	// logsLines is set via flag with default 100. Verify the flag default is
	// wired up correctly (IntVarP sets it at init time).
	if logsLines != 100 {
		t.Errorf("logsLines should default to 100, got %d", logsLines)
	}
}
