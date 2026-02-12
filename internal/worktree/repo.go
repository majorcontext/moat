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

// GitDirInfo holds the resolved git directory paths for a worktree workspace.
type GitDirInfo struct {
	// MainGitDir is the absolute path to the main .git directory (shared objects, refs).
	MainGitDir string
	// WorktreeGitDir is the absolute path to the worktree-specific git dir
	// (e.g., main-repo/.git/worktrees/<branch>). Always a subdirectory of MainGitDir.
	WorktreeGitDir string
}

// ResolveGitDir detects whether workspace is a git worktree and returns
// the paths needed to make git operations work inside a container.
// Returns (nil, nil) if workspace is not a worktree (regular repo or no .git).
func ResolveGitDir(workspace string) (*GitDirInfo, error) {
	dotGit := filepath.Join(workspace, ".git")

	fi, err := os.Lstat(dotGit)
	if err != nil {
		// No .git at all — not a git repo or bare checkout
		return nil, nil
	}
	if fi.IsDir() {
		// Regular repo with .git directory — not a worktree
		return nil, nil
	}

	// .git is a file — this is a worktree. Parse the gitdir reference.
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return nil, fmt.Errorf("reading .git file: %w", err)
	}

	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir: ") {
		return nil, fmt.Errorf("unexpected .git file content: %q", line)
	}
	gitdir := strings.TrimPrefix(line, "gitdir: ")

	// Resolve relative paths against the workspace directory
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(workspace, gitdir)
	}
	gitdir = filepath.Clean(gitdir)

	// Resolve the main .git directory via commondir file
	mainGitDir := resolveMainGitDir(gitdir)

	// Validate the main .git directory
	if _, err := os.Stat(filepath.Join(mainGitDir, "HEAD")); err != nil {
		return nil, fmt.Errorf("main git dir missing HEAD: %w", err)
	}

	return &GitDirInfo{
		MainGitDir:     mainGitDir,
		WorktreeGitDir: gitdir,
	}, nil
}

// resolveMainGitDir finds the main .git directory from a worktree gitdir path.
// It reads the commondir file if present, otherwise falls back to ../../ relative to gitdir.
func resolveMainGitDir(gitdir string) string {
	commondirFile := filepath.Join(gitdir, "commondir")
	data, err := os.ReadFile(commondirFile)
	if err == nil {
		commondir := strings.TrimSpace(string(data))
		if !filepath.IsAbs(commondir) {
			commondir = filepath.Join(gitdir, commondir)
		}
		return filepath.Clean(commondir)
	}

	// Fallback: worktree gitdirs are typically at <main>/.git/worktrees/<branch>
	return filepath.Clean(filepath.Join(gitdir, "..", ".."))
}
