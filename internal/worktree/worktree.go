package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Result holds the outcome of a worktree resolution.
type Result struct {
	WorkspacePath string // absolute path to worktree directory
	Branch        string // git branch name
	RunName       string // auto-generated run name ({agent}-{branch} or {branch})
	Reused        bool   // true if worktree already existed
	ActiveRunID   string // non-empty if a run is already active in this worktree
	RepoID        string // normalized repo identifier
}

// Resolve ensures a branch and worktree exist for the given branch name.
// It creates them if necessary, reuses them if they already exist.
func Resolve(repoRoot, repoID, branch, agentName string) (*Result, error) {
	wtPath := filepath.Join(BasePath(), repoID, branch)

	runName := branch
	if agentName != "" {
		runName = agentName + "-" + branch
	}

	result := &Result{
		WorkspacePath: wtPath,
		Branch:        branch,
		RunName:       runName,
		RepoID:        repoID,
	}

	// Check if worktree already exists
	if _, err := os.Stat(wtPath); err == nil {
		result.Reused = true
		return result, nil
	}

	// Ensure branch exists
	if err := ensureBranch(repoRoot, branch); err != nil {
		return nil, fmt.Errorf("ensuring branch %q: %w", branch, err)
	}

	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(wtPath), 0755); err != nil {
		return nil, fmt.Errorf("creating worktree parent directory: %w", err)
	}

	// Create worktree
	cmd := exec.Command("git", "worktree", "add", wtPath, branch)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("creating worktree: %w\n%s", err, out)
	}

	return result, nil
}

// ensureBranch creates the branch from HEAD if it doesn't already exist.
func ensureBranch(repoRoot, branch string) error {
	cmd := exec.Command("git", "rev-parse", "--verify", branch)
	cmd.Dir = repoRoot
	if err := cmd.Run(); err == nil {
		return nil
	}

	cmd = exec.Command("git", "branch", branch)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("creating branch: %w\n%s", err, out)
	}
	return nil
}
