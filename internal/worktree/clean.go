package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Entry represents a managed worktree on disk.
type Entry struct {
	Branch string // branch name (directory name)
	Path   string // absolute path to worktree
}

// Clean removes a worktree directory and runs git worktree prune.
func Clean(repoRoot, wtPath string) error {
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		return fmt.Errorf("worktree path does not exist: %s", wtPath)
	}

	// Remove the worktree using git (handles lock files, etc.)
	cmd := exec.Command("git", "worktree", "remove", wtPath)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		// Fall back to manual removal + prune if git worktree remove fails
		if rmErr := os.RemoveAll(wtPath); rmErr != nil {
			return fmt.Errorf("removing worktree: %w (git error: %s)", rmErr, out)
		}
		pruneCmd := exec.Command("git", "worktree", "prune")
		pruneCmd.Dir = repoRoot
		_ = pruneCmd.Run() // best effort
	}

	return nil
}

// ListWorktrees returns all managed worktree entries for a given repo ID.
// It walks the directory tree to find worktrees, supporting branch names
// with slashes (e.g., feature/dark-mode) which create nested directories.
// A worktree is identified by the presence of a .git file (not directory)
// in its root, which git creates for worktrees.
func ListWorktrees(repoID string) ([]Entry, error) {
	repoDir := filepath.Join(BasePath(), repoID)

	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return nil, nil
	}

	var result []Entry
	err := filepath.WalkDir(repoDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable directories
		}
		// Skip the repo dir itself
		if path == repoDir {
			return nil
		}
		// Check for .git file (not directory) — the marker for a git worktree
		gitPath := filepath.Join(path, ".git")
		info, statErr := os.Stat(gitPath)
		if statErr != nil || info.IsDir() {
			return nil // not a worktree, keep walking
		}
		// This is a worktree. The branch name is the relative path from repoDir.
		rel, relErr := filepath.Rel(repoDir, path)
		if relErr != nil {
			return nil
		}
		result = append(result, Entry{
			Branch: rel,
			Path:   path,
		})
		// Don't descend into the worktree itself
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
