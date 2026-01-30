package system

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TempDirPattern represents a temporary directory pattern created by moat
type TempDirPattern struct {
	Pattern     string
	Description string
}

// MoatTempPatterns lists all temporary directory patterns created by moat
var MoatTempPatterns = []TempDirPattern{
	{Pattern: "agentops-aws-*", Description: "AWS credential helper directories"},
	{Pattern: "moat-claude-staging-*", Description: "Claude configuration staging directories"},
	{Pattern: "moat-codex-staging-*", Description: "Codex configuration staging directories"},
}

// OrphanedTempDir represents a temporary directory that may be orphaned
type OrphanedTempDir struct {
	Path        string
	Pattern     string
	Description string
	ModTime     time.Time
	Size        int64
}

// FindOrphanedTempDirs scans /tmp for moat temporary directories that are likely orphaned
func FindOrphanedTempDirs(minAge time.Duration) ([]OrphanedTempDir, error) {
	var orphaned []OrphanedTempDir
	tmpDir := os.TempDir()
	cutoff := time.Now().Add(-minAge)

	for _, pattern := range MoatTempPatterns {
		matches, err := filepath.Glob(filepath.Join(tmpDir, pattern.Pattern))
		if err != nil {
			continue
		}

		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				continue
			}

			// Skip if directory was modified recently (still in use)
			if info.ModTime().After(cutoff) {
				continue
			}

			// Calculate directory size
			size, _ := dirSize(match)

			orphaned = append(orphaned, OrphanedTempDir{
				Path:        match,
				Pattern:     pattern.Pattern,
				Description: pattern.Description,
				ModTime:     info.ModTime(),
				Size:        size,
			})
		}
	}

	return orphaned, nil
}

// CleanOrphanedTempDirs removes the specified orphaned temporary directories
// Re-verifies age before deletion to prevent race conditions
func CleanOrphanedTempDirs(dirs []OrphanedTempDir, minAge time.Duration) error {
	var errs []string
	var skipped []string
	cutoff := time.Now().Add(-minAge)

	for _, dir := range dirs {
		// Re-check age before deletion to avoid TOCTOU race condition
		// A new moat run could have started between scan and deletion
		if info, err := os.Stat(dir.Path); err == nil {
			if info.ModTime().After(cutoff) {
				skipped = append(skipped, dir.Path)
				continue
			}
		}

		if err := os.RemoveAll(dir.Path); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", dir.Path, err))
		}
	}

	if len(skipped) > 0 {
		fmt.Fprintf(os.Stderr, "Skipped %d director%s (modified since scan):\n",
			len(skipped), pluralSuffix(len(skipped), "y", "ies"))
		for _, path := range skipped {
			fmt.Fprintf(os.Stderr, "  %s\n", path)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to remove some directories:\n  %s", strings.Join(errs, "\n  "))
	}

	return nil
}

func pluralSuffix(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

// dirSize calculates the total size of a directory recursively
func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// FormatSize formats a byte size into a human-readable string
func FormatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
