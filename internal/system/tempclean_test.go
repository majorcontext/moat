package system

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindOrphanedTempDirs(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	oldTmpDir := os.Getenv("TMPDIR")
	os.Setenv("TMPDIR", tmpDir)
	defer os.Setenv("TMPDIR", oldTmpDir)

	// Create test directories
	oldDir := filepath.Join(tmpDir, "agentops-aws-old")
	recentDir := filepath.Join(tmpDir, "agentops-aws-recent")
	claudeOldDir := filepath.Join(tmpDir, "moat-claude-staging-old")

	if err := os.Mkdir(oldDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(recentDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(claudeOldDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Make old directories appear old (2 hours ago)
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(claudeOldDir, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		minAge      time.Duration
		wantCount   int
		wantPattern string
	}{
		{
			name:      "find directories older than 1 hour",
			minAge:    1 * time.Hour,
			wantCount: 2, // oldDir and claudeOldDir
		},
		{
			name:      "find directories older than 3 hours",
			minAge:    3 * time.Hour,
			wantCount: 0, // none are that old
		},
		{
			name:      "find directories older than 30 minutes",
			minAge:    30 * time.Minute,
			wantCount: 2, // oldDir and claudeOldDir
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orphaned, err := FindOrphanedTempDirs(tt.minAge)
			if err != nil {
				t.Fatalf("FindOrphanedTempDirs() error = %v", err)
			}

			if len(orphaned) != tt.wantCount {
				t.Errorf("FindOrphanedTempDirs() found %d directories, want %d", len(orphaned), tt.wantCount)
			}

			// Verify the directories have the expected patterns
			for _, dir := range orphaned {
				if dir.Pattern == "" {
					t.Error("orphaned directory missing pattern")
				}
				if dir.Description == "" {
					t.Error("orphaned directory missing description")
				}
			}
		})
	}
}

func TestCleanOrphanedTempDirs(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test directories
	testDirs := []string{
		filepath.Join(tmpDir, "test-1"),
		filepath.Join(tmpDir, "test-2"),
	}

	for _, dir := range testDirs {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		// Make them old
		oldTime := time.Now().Add(-2 * time.Hour)
		if err := os.Chtimes(dir, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}

	orphaned := []OrphanedTempDir{
		{
			Path:        testDirs[0],
			Pattern:     "test-*",
			Description: "test directories",
			ModTime:     time.Now().Add(-2 * time.Hour),
		},
		{
			Path:        testDirs[1],
			Pattern:     "test-*",
			Description: "test directories",
			ModTime:     time.Now().Add(-2 * time.Hour),
		},
	}

	// Test successful cleanup
	if err := CleanOrphanedTempDirs(orphaned, 1*time.Hour); err != nil {
		t.Errorf("CleanOrphanedTempDirs() error = %v", err)
	}

	// Verify directories were removed
	for _, dir := range testDirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("directory %s still exists after cleanup", dir)
		}
	}
}

func TestCleanOrphanedTempDirs_SkipsRecentlyModified(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test directory
	testDir := filepath.Join(tmpDir, "test-recent")
	if err := os.Mkdir(testDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Make it appear old initially
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(testDir, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	orphaned := []OrphanedTempDir{
		{
			Path:        testDir,
			Pattern:     "test-*",
			Description: "test directories",
			ModTime:     oldTime,
		},
	}

	// Touch the directory to make it recent (simulating a race condition)
	if err := os.Chtimes(testDir, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}

	// Attempt cleanup with 1 hour minimum age
	if err := CleanOrphanedTempDirs(orphaned, 1*time.Hour); err != nil {
		t.Errorf("CleanOrphanedTempDirs() error = %v", err)
	}

	// Verify directory was NOT removed (it's recent now)
	if _, err := os.Stat(testDir); os.IsNotExist(err) {
		t.Error("recently modified directory was incorrectly removed")
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{"zero bytes", 0, "0 B"},
		{"small bytes", 512, "512 B"},
		{"kilobytes", 1024, "1.0 KiB"},
		{"megabytes", 1024 * 1024, "1.0 MiB"},
		{"gigabytes", 1024 * 1024 * 1024, "1.0 GiB"},
		{"mixed", 1536, "1.5 KiB"},
		{"large", 5 * 1024 * 1024 * 1024, "5.0 GiB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatSize(tt.bytes)
			if got != tt.want {
				t.Errorf("FormatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestDirSize(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a directory with some files
	if err := os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte("world"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a subdirectory with a file
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "file3.txt"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	size, err := dirSize(tmpDir)
	if err != nil {
		t.Errorf("dirSize() error = %v", err)
	}

	// Expected: 5 + 5 + 4 = 14 bytes
	expectedSize := int64(14)
	if size != expectedSize {
		t.Errorf("dirSize() = %d, want %d", size, expectedSize)
	}
}

func TestPluralSuffix(t *testing.T) {
	tests := []struct {
		count    int
		singular string
		plural   string
		want     string
	}{
		{0, "y", "ies", "ies"},
		{1, "y", "ies", "y"},
		{2, "y", "ies", "ies"},
		{100, "y", "ies", "ies"},
	}

	for _, tt := range tests {
		got := pluralSuffix(tt.count, tt.singular, tt.plural)
		if got != tt.want {
			t.Errorf("pluralSuffix(%d, %q, %q) = %q, want %q",
				tt.count, tt.singular, tt.plural, got, tt.want)
		}
	}
}
