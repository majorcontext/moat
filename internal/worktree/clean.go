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
func ListWorktrees(repoID string) ([]Entry, error) {
	repoDir := filepath.Join(BasePath(), repoID)

	entries, err := os.ReadDir(repoDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []Entry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		result = append(result, Entry{
			Branch: e.Name(),
			Path:   filepath.Join(repoDir, e.Name()),
		})
	}
	return result, nil
}
