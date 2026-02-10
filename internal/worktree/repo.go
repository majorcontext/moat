// Package worktree provides git worktree management for moat runs.
package worktree

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FindRepoRoot returns the root of the git repository containing dir.
func FindRepoRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveRepoID returns a normalized repository identifier.
// Uses the origin remote URL if available, otherwise falls back to _local/<dirname>.
func ResolveRepoID(repoRoot string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "_local/" + filepath.Base(repoRoot), nil
	}
	return ParseRemoteURL(strings.TrimSpace(string(out)))
}

// ParseRemoteURL normalizes a git remote URL to host/owner/repo format.
// Handles both HTTPS and SSH URLs, strips .git suffix.
func ParseRemoteURL(rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("empty remote URL")
	}

	// Handle SSH format: git@host:owner/repo.git
	if strings.HasPrefix(rawURL, "git@") {
		rawURL = strings.TrimPrefix(rawURL, "git@")
		parts := strings.SplitN(rawURL, ":", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid SSH URL: %s", rawURL)
		}
		host := parts[0]
		path := strings.TrimSuffix(parts[1], ".git")
		return host + "/" + path, nil
	}

	// Handle HTTPS format
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	return u.Host + "/" + path, nil
}

// BasePath returns the root directory for worktrees.
// Checks MOAT_WORKTREE_BASE env var, defaults to ~/.moat/worktrees.
func BasePath() string {
	if base := os.Getenv("MOAT_WORKTREE_BASE"); base != "" {
		return base
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".moat", "worktrees")
	}
	return filepath.Join(home, ".moat", "worktrees")
}

// Path returns the expected path for a worktree given a repo ID and branch.
func Path(repoID, branch string) string {
	return filepath.Join(BasePath(), repoID, branch)
}
