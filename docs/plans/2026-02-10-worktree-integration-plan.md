# Worktree Integration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add `moat wt` command and `--wt` flag to provider commands, consolidating branch creation, worktree creation, and agent execution into a single command.

**Architecture:** New `internal/worktree/` package handles all git worktree operations (repo detection, remote URL parsing, branch/worktree creation, cleanup). The CLI layer in `cmd/moat/cli/wt.go` registers the `moat wt` command with subcommands. Provider commands (claude, codex, gemini) gain a `--wt` flag that calls the same worktree resolution before proceeding normally. Run metadata gains a `Worktree` field to track worktree-managed runs.

**Tech Stack:** Go, Cobra CLI, `os/exec` for git commands, standard library for URL parsing.

---

### Task 1: Core worktree package — repo resolution

**Files:**
- Create: `internal/worktree/repo.go`
- Create: `internal/worktree/repo_test.go`

**Step 1: Write the failing tests**

```go
package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{
			name: "HTTPS URL",
			url:  "https://github.com/acme/myrepo.git",
			want: "github.com/acme/myrepo",
		},
		{
			name: "HTTPS URL without .git",
			url:  "https://github.com/acme/myrepo",
			want: "github.com/acme/myrepo",
		},
		{
			name: "SSH URL",
			url:  "git@github.com:acme/myrepo.git",
			want: "github.com/acme/myrepo",
		},
		{
			name: "SSH URL without .git",
			url:  "git@github.com:acme/myrepo",
			want: "github.com/acme/myrepo",
		},
		{
			name: "GitLab SSH",
			url:  "git@gitlab.com:team/project.git",
			want: "gitlab.com/team/project",
		},
		{
			name:    "empty URL",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRemoteURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRemoteURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseRemoteURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestResolveRepoID(t *testing.T) {
	// Create a temp git repo with a remote
	tmpDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = tmpDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	run("git", "init")
	run("git", "remote", "add", "origin", "https://github.com/acme/myrepo.git")

	repoID, err := ResolveRepoID(tmpDir)
	if err != nil {
		t.Fatalf("ResolveRepoID() error = %v", err)
	}
	if repoID != "github.com/acme/myrepo" {
		t.Errorf("ResolveRepoID() = %q, want %q", repoID, "github.com/acme/myrepo")
	}
}

func TestResolveRepoID_NoRemote(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, out)
	}

	repoID, err := ResolveRepoID(tmpDir)
	if err != nil {
		t.Fatalf("ResolveRepoID() error = %v", err)
	}
	want := "_local/" + filepath.Base(tmpDir)
	if repoID != want {
		t.Errorf("ResolveRepoID() = %q, want %q", repoID, want)
	}
}

func TestFindRepoRoot(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Resolve symlinks for macOS /var -> /private/var
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, out)
	}

	// Create a subdirectory
	subDir := filepath.Join(tmpDir, "a", "b", "c")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	root, err := FindRepoRoot(subDir)
	if err != nil {
		t.Fatalf("FindRepoRoot() error = %v", err)
	}
	if root != tmpDir {
		t.Errorf("FindRepoRoot() = %q, want %q", root, tmpDir)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test -run 'TestParseRemoteURL|TestResolveRepoID|TestFindRepoRoot' ./internal/worktree/ -v`
Expected: FAIL — package does not exist

**Step 3: Write minimal implementation**

```go
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
		// No remote origin — use local fallback
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
		// git@github.com:owner/repo.git -> github.com/owner/repo
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
```

**Step 4: Run tests to verify they pass**

Run: `go test -run 'TestParseRemoteURL|TestResolveRepoID|TestFindRepoRoot' ./internal/worktree/ -v`
Expected: PASS

**Step 5: Commit**

```
feat(worktree): add repo resolution and remote URL parsing
```

---

### Task 2: Core worktree package — Resolve function

**Files:**
- Create: `internal/worktree/worktree.go`
- Create: `internal/worktree/worktree_test.go`

**Step 1: Write the failing tests**

```go
package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "test-wt-*")
	if err != nil {
		t.Fatal(err)
	}
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = tmpDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	run("git", "init")
	run("git", "remote", "add", "origin", "https://github.com/acme/myrepo.git")

	// Need at least one commit for worktree operations
	readme := filepath.Join(tmpDir, "README.md")
	os.WriteFile(readme, []byte("# test"), 0644)
	run("git", "add", ".")
	run("git", "commit", "-m", "initial commit")

	return tmpDir
}

func TestResolve_CreatesNewBranchAndWorktree(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	result, err := Resolve(repoDir, "github.com/acme/myrepo", "new-feature", "myapp")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if result.Branch != "new-feature" {
		t.Errorf("Branch = %q, want %q", result.Branch, "new-feature")
	}
	if result.RunName != "myapp-new-feature" {
		t.Errorf("RunName = %q, want %q", result.RunName, "myapp-new-feature")
	}
	if result.Reused {
		t.Error("Reused = true, want false")
	}
	wantPath := filepath.Join(wtBase, "github.com/acme/myrepo", "new-feature")
	if result.WorkspacePath != wantPath {
		t.Errorf("WorkspacePath = %q, want %q", result.WorkspacePath, wantPath)
	}
	// Verify the worktree directory exists
	if _, err := os.Stat(result.WorkspacePath); os.IsNotExist(err) {
		t.Error("worktree directory was not created")
	}
}

func TestResolve_ReusesExistingWorktree(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	// Create first time
	_, err = Resolve(repoDir, "github.com/acme/myrepo", "existing", "myapp")
	if err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}

	// Resolve again — should reuse
	result, err := Resolve(repoDir, "github.com/acme/myrepo", "existing", "myapp")
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}
	if !result.Reused {
		t.Error("Reused = false, want true")
	}
}

func TestResolve_NoAgentName(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	result, err := Resolve(repoDir, "github.com/acme/myrepo", "feat-x", "")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.RunName != "feat-x" {
		t.Errorf("RunName = %q, want %q", result.RunName, "feat-x")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test -run 'TestResolve_' ./internal/worktree/ -v`
Expected: FAIL — `Resolve` not defined

**Step 3: Write minimal implementation**

```go
// Package worktree provides git worktree management for moat runs.
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Result holds the outcome of a worktree resolution.
type Result struct {
	WorkspacePath string // absolute path to worktree directory
	Branch        string // git branch name
	RunName       string // auto-generated run name ({agent}-{branch} or {branch})
	Reused        bool   // true if worktree already existed
	ActiveRunID   string // non-empty if a run is already active in this worktree
}

// Resolve ensures a branch and worktree exist for the given branch name.
// It creates them if necessary, reuses them if they already exist.
// repoRoot is the path to the main git repository.
// repoID is the normalized repo identifier (e.g., "github.com/acme/myrepo").
// agentName is optional — used for run name generation.
func Resolve(repoRoot, repoID, branch, agentName string) (*Result, error) {
	wtPath := filepath.Join(BasePath(), repoID, branch)

	// Generate run name
	runName := branch
	if agentName != "" {
		runName = agentName + "-" + branch
	}

	result := &Result{
		WorkspacePath: wtPath,
		Branch:        branch,
		RunName:       runName,
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
	// Check if branch exists
	cmd := exec.Command("git", "rev-parse", "--verify", branch)
	cmd.Dir = repoRoot
	if err := cmd.Run(); err == nil {
		return nil // Branch already exists
	}

	// Create branch from HEAD
	cmd = exec.Command("git", "branch", branch)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("creating branch: %w\n%s", err, out)
	}
	return nil
}
```

**Step 4: Run tests to verify they pass**

Run: `go test -run 'TestResolve_' ./internal/worktree/ -v`
Expected: PASS

**Step 5: Commit**

```
feat(worktree): add Resolve function for branch and worktree creation
```

---

### Task 3: Worktree cleanup

**Files:**
- Create: `internal/worktree/clean.go`
- Create: `internal/worktree/clean_test.go`

**Step 1: Write the failing tests**

```go
package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestClean_RemovesWorktree(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	// Create a worktree
	result, err := Resolve(repoDir, "github.com/acme/myrepo", "to-clean", "myapp")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	// Clean it
	err = Clean(repoDir, result.WorkspacePath)
	if err != nil {
		t.Fatalf("Clean() error = %v", err)
	}

	// Verify directory is gone
	if _, err := os.Stat(result.WorkspacePath); !os.IsNotExist(err) {
		t.Error("worktree directory still exists after clean")
	}
}

func TestClean_NonExistentPath(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	err := Clean(repoDir, "/nonexistent/path")
	if err == nil {
		t.Error("Clean() expected error for nonexistent path, got nil")
	}
}

func TestListWorktrees(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	repoID := "github.com/acme/myrepo"

	// Create two worktrees
	_, err = Resolve(repoDir, repoID, "feat-a", "")
	if err != nil {
		t.Fatalf("Resolve feat-a: %v", err)
	}
	_, err = Resolve(repoDir, repoID, "feat-b", "")
	if err != nil {
		t.Fatalf("Resolve feat-b: %v", err)
	}

	entries, err := ListWorktrees(repoID)
	if err != nil {
		t.Fatalf("ListWorktrees() error = %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("ListWorktrees() returned %d entries, want 2", len(entries))
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test -run 'TestClean|TestListWorktrees' ./internal/worktree/ -v`
Expected: FAIL — `Clean` and `ListWorktrees` not defined

**Step 3: Write minimal implementation**

```go
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// WorktreeEntry represents a managed worktree on disk.
type WorktreeEntry struct {
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
		pruneCmd.Run() // best effort
	}

	return nil
}

// ListWorktrees returns all managed worktree entries for a given repo ID.
func ListWorktrees(repoID string) ([]WorktreeEntry, error) {
	repoDir := filepath.Join(BasePath(), repoID)

	entries, err := os.ReadDir(repoDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []WorktreeEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		result = append(result, WorktreeEntry{
			Branch: e.Name(),
			Path:   filepath.Join(repoDir, e.Name()),
		})
	}
	return result, nil
}
```

**Step 4: Run tests to verify they pass**

Run: `go test -run 'TestClean|TestListWorktrees' ./internal/worktree/ -v`
Expected: PASS

**Step 5: Commit**

```
feat(worktree): add cleanup and listing functions
```

---

### Task 4: Add worktree metadata to run storage

**Files:**
- Modify: `internal/storage/storage.go`
- Modify: `internal/run/run.go`
- Modify: `internal/cli/types.go`

**Step 1: Add `Worktree` fields to `Metadata` and `ExecOptions`**

In `internal/storage/storage.go`, add to the `Metadata` struct (after the `Error` field, before service fields):

```go
	// Worktree fields (set when run was created via moat wt or --wt)
	WorktreeBranch string `json:"worktree_branch,omitempty"` // git branch name
	WorktreePath   string `json:"worktree_path,omitempty"`   // absolute path to worktree
	WorktreeRepoID string `json:"worktree_repo_id,omitempty"` // normalized repo identifier
```

In `internal/run/run.go`, add to the `Run` struct (after the `Workspace` field):

```go
	// Worktree tracking (set when created via moat wt or --wt flag)
	WorktreeBranch string
	WorktreePath   string
	WorktreeRepoID string
```

In `internal/run/run.go`, update `SaveMetadata()` to include the new fields:

```go
	return r.Store.SaveMetadata(storage.Metadata{
		// ... existing fields ...
		WorktreeBranch: r.WorktreeBranch,
		WorktreePath:   r.WorktreePath,
		WorktreeRepoID: r.WorktreeRepoID,
	})
```

In `internal/run/manager.go`, update `loadPersistedRuns()` to read the new fields from metadata (in the section that populates the Run struct from loaded metadata).

In `internal/cli/types.go`, add to `ExecOptions`:

```go
	// Worktree tracking (set by moat wt or --wt flag)
	WorktreeBranch string
	WorktreePath   string
	WorktreeRepoID string
```

In `cmd/moat/cli/exec.go`, pass worktree fields from `ExecOptions` into the run (after `manager.Create` returns):

```go
	if opts.WorktreeBranch != "" {
		r.WorktreeBranch = opts.WorktreeBranch
		r.WorktreePath = opts.WorktreePath
		r.WorktreeRepoID = opts.WorktreeRepoID
		r.SaveMetadata()
	}
```

**Step 2: Run existing tests to ensure no regressions**

Run: `go test ./internal/storage/ ./internal/run/ ./internal/cli/ -v`
Expected: PASS — all existing tests still pass

**Step 3: Commit**

```
feat(worktree): add worktree tracking fields to run metadata
```

---

### Task 5: `moat wt` CLI command

**Files:**
- Create: `cmd/moat/cli/wt.go`

**Step 1: Write the `moat wt` command**

```go
package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/majorcontext/moat/internal/worktree"
)

var wtFlags intcli.ExecFlags

var wtCmd = &cobra.Command{
	Use:   "wt <branch> [-- command]",
	Short: "Run agent in a git worktree",
	Long: `Create or reuse a git worktree for a branch and run an agent in it.

If the branch doesn't exist, it's created from HEAD. If the worktree doesn't
exist, it's created. If a run is already active in the worktree, attaches to it.

Agent configuration is read from agent.yaml in the current directory.

Worktrees are stored at ~/.moat/worktrees/<repo-id>/<branch>.
Override with MOAT_WORKTREE_BASE environment variable.

Examples:
  # Start agent on a new feature branch
  moat wt dark-mode

  # Start agent in background
  moat wt dark-mode -d

  # Start with a specific command
  moat wt dark-mode -- make test

  # List worktree-based runs
  moat wt list

  # Clean up stopped worktrees
  moat wt clean
  moat wt clean dark-mode`,
	Args: cobra.ArbitraryArgs,
	RunE: runWorktree,
}

func init() {
	rootCmd.AddCommand(wtCmd)
	intcli.AddExecFlags(wtCmd, &wtFlags)

	// List subcommand
	wtListCmd := &cobra.Command{
		Use:   "list",
		Short: "List worktree-based runs",
		RunE:  runWorktreeList,
	}
	wtCmd.AddCommand(wtListCmd)

	// Clean subcommand
	wtCleanCmd := &cobra.Command{
		Use:   "clean [branch]",
		Short: "Remove worktree directories for stopped runs",
		Long: `Remove worktree directories for stopped runs. Never deletes branches.

Without arguments, cleans all worktrees for the current repo whose runs are stopped.
With a branch name, cleans only that worktree.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runWorktreeClean,
	}
	wtCmd.AddCommand(wtCleanCmd)
}

func runWorktree(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("branch name required\n\nUsage: moat wt <branch> [-- command]")
	}

	branch := args[0]

	// Check for subcommand collision (list, clean handled by cobra)
	if branch == "list" || branch == "clean" {
		return nil
	}

	// Parse command after --
	var containerCmd []string
	dashIdx := cmd.ArgsLenAtDash()
	if dashIdx >= 0 {
		containerCmd = args[dashIdx:]
	}

	// Find git repo root
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("not inside a git repository: %w", err)
	}

	// Load agent.yaml from repo root
	cfg, err := config.Load(repoRoot)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("agent.yaml not found in %s\n\nmoat wt requires an agent.yaml to determine which agent to run.\nSee https://majorcontext.com/moat/reference/agent-yaml", repoRoot)
	}

	// Resolve repo ID
	repoID, err := worktree.ResolveRepoID(repoRoot)
	if err != nil {
		return fmt.Errorf("resolving repo identity: %w", err)
	}

	// Resolve worktree
	agentName := cfg.Name
	result, err := worktree.Resolve(repoRoot, repoID, branch, agentName)
	if err != nil {
		return fmt.Errorf("resolving worktree: %w", err)
	}

	// User feedback
	if result.Reused {
		ui.Info("Using existing worktree at %s", result.WorkspacePath)
	} else {
		ui.Info("Created worktree at %s", result.WorkspacePath)
	}

	// Check for active run
	if result.ActiveRunID != "" {
		ui.Info("Attaching to running session %s", result.ActiveRunID)
		// Attach to existing run
		manager, err := run.NewManager()
		if err != nil {
			return fmt.Errorf("creating run manager: %w", err)
		}
		defer manager.Close()
		r, err := manager.Get(result.ActiveRunID)
		if err != nil {
			return fmt.Errorf("getting run: %w", err)
		}
		return RunAttached(cmd.Context(), manager, r)
	}

	// Use run name from worktree resolution unless --name overrides
	if wtFlags.Name == "" {
		wtFlags.Name = result.RunName
	}

	log.Debug("starting worktree run",
		"branch", branch,
		"workspace", result.WorkspacePath,
		"run_name", wtFlags.Name,
	)

	if dryRun {
		fmt.Printf("Dry run — would start agent in worktree\n")
		fmt.Printf("Branch: %s\n", branch)
		fmt.Printf("Workspace: %s\n", result.WorkspacePath)
		fmt.Printf("Run name: %s\n", wtFlags.Name)
		return nil
	}

	ctx := context.Background()

	opts := intcli.ExecOptions{
		Flags:          wtFlags,
		Workspace:      result.WorkspacePath,
		Command:        containerCmd,
		Config:         cfg,
		Interactive:    !wtFlags.Detach,
		TTY:            !wtFlags.Detach,
		WorktreeBranch: branch,
		WorktreePath:   result.WorkspacePath,
		WorktreeRepoID: repoID,
	}

	_, err = ExecuteRun(ctx, opts)
	return err
}

func runWorktreeList(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("not inside a git repository: %w", err)
	}

	repoID, err := worktree.ResolveRepoID(repoRoot)
	if err != nil {
		return fmt.Errorf("resolving repo identity: %w", err)
	}

	// Find all runs with worktree metadata matching this repo
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	runs := manager.List()

	// Filter to worktree runs for this repo
	var wtRuns []*run.Run
	for _, r := range runs {
		if r.WorktreeRepoID == repoID {
			wtRuns = append(wtRuns, r)
		}
	}

	if len(wtRuns) == 0 {
		fmt.Println("No worktree runs found for this repository")
		return nil
	}

	sort.Slice(wtRuns, func(i, j int) bool {
		return wtRuns[i].CreatedAt.After(wtRuns[j].CreatedAt)
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "BRANCH\tRUN NAME\tSTATUS\tWORKTREE")
	for _, r := range wtRuns {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			r.WorktreeBranch, r.Name, r.State, r.WorktreePath)
	}
	return w.Flush()
}

func runWorktreeClean(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("not inside a git repository: %w", err)
	}

	repoID, err := worktree.ResolveRepoID(repoRoot)
	if err != nil {
		return fmt.Errorf("resolving repo identity: %w", err)
	}

	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	if len(args) > 0 {
		// Clean specific branch
		branch := args[0]
		wtPath := worktree.WorktreePath(repoID, branch)

		// Check for active run
		for _, r := range manager.List() {
			if r.WorktreePath == wtPath && r.State == run.StateRunning {
				return fmt.Errorf("cannot clean worktree for branch %q: run %s is still active. Stop it first with 'moat stop %s'", branch, r.Name, r.ID)
			}
		}

		if err := worktree.Clean(repoRoot, wtPath); err != nil {
			return err
		}
		ui.Info("Cleaned worktree for branch %s", branch)
		return nil
	}

	// Clean all stopped worktree runs for this repo
	entries, err := worktree.ListWorktrees(repoID)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		fmt.Println("No worktrees to clean")
		return nil
	}

	cleaned := 0
	for _, entry := range entries {
		// Check if any active run uses this worktree
		active := false
		for _, r := range manager.List() {
			if r.WorktreePath == entry.Path && r.State == run.StateRunning {
				active = true
				break
			}
		}
		if active {
			continue
		}

		if err := worktree.Clean(repoRoot, entry.Path); err != nil {
			ui.Warn("Failed to clean %s: %v", entry.Branch, err)
			continue
		}
		ui.Info("Cleaned worktree for branch %s", entry.Branch)
		cleaned++
	}

	if cleaned == 0 {
		fmt.Println("No stopped worktrees to clean")
	}
	return nil
}
```

**Step 2: Add `WorktreePath` helper to worktree package**

In `internal/worktree/repo.go`, add:

```go
// WorktreePath returns the expected path for a worktree given a repo ID and branch.
func WorktreePath(repoID, branch string) string {
	return filepath.Join(BasePath(), repoID, branch)
}
```

**Step 3: Verify it compiles**

Run: `go build ./cmd/moat/`
Expected: compiles successfully

**Step 4: Commit**

```
feat(worktree): add moat wt command with list and clean subcommands
```

---

### Task 6: Add `--wt` flag to provider commands

**Files:**
- Modify: `internal/cli/types.go`
- Modify: `internal/providers/claude/cli.go`
- Modify: `internal/providers/codex/cli.go`
- Modify: `internal/providers/gemini/cli.go`

**Step 1: Add shared worktree resolution helper to `internal/cli/`**

Create `internal/cli/worktree.go`:

```go
package cli

import (
	"fmt"
	"os"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/worktree"
)

// ResolveWorktreeWorkspace handles the --wt flag for provider commands.
// If wtBranch is empty, returns the original workspace unchanged.
// Otherwise, resolves the worktree and returns the updated workspace path,
// sets the run name on flags if not already set, and returns worktree metadata.
func ResolveWorktreeWorkspace(wtBranch, workspace string, flags *ExecFlags, cfg *config.Config) (string, *worktree.Result, error) {
	if wtBranch == "" {
		return workspace, nil, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, fmt.Errorf("getting current directory: %w", err)
	}

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return "", nil, fmt.Errorf("--wt requires a git repository: %w", err)
	}

	repoID, err := worktree.ResolveRepoID(repoRoot)
	if err != nil {
		return "", nil, fmt.Errorf("resolving repo identity: %w", err)
	}

	agentName := ""
	if cfg != nil {
		agentName = cfg.Name
	}

	result, err := worktree.Resolve(repoRoot, repoID, wtBranch, agentName)
	if err != nil {
		return "", nil, fmt.Errorf("resolving worktree: %w", err)
	}

	if flags.Name == "" {
		flags.Name = result.RunName
	}

	return result.WorkspacePath, result, nil
}
```

**Step 2: Add `--wt` flag to each provider**

In each provider's `cli.go` (claude, codex, gemini), add a package-level var:

```go
var claudeWtFlag string  // (or codexWtFlag, geminiWtFlag)
```

In each `RegisterCLI`, add the flag:

```go
claudeCmd.Flags().StringVar(&claudeWtFlag, "wt", "", "run in a git worktree for this branch")
```

In each `runXxx` function, after resolving workspace and loading config but before building grants, add:

```go
	// Handle --wt flag
	absPath, wtResult, err := cli.ResolveWorktreeWorkspace(claudeWtFlag, absPath, &claudeFlags, cfg)
	if err != nil {
		return err
	}
	if wtResult != nil {
		if wtResult.Reused {
			fmt.Fprintf(os.Stderr, "Using existing worktree at %s\n", wtResult.WorkspacePath)
		} else {
			fmt.Fprintf(os.Stderr, "Created worktree at %s\n", wtResult.WorkspacePath)
		}
		// Reload config from worktree path (it may have its own agent.yaml)
		if wtCfg, loadErr := config.Load(absPath); loadErr == nil && wtCfg != nil {
			cfg = wtCfg
		}
	}
```

And pass worktree metadata into `ExecOptions`:

```go
	opts := cli.ExecOptions{
		// ... existing fields ...
	}
	if wtResult != nil {
		opts.WorktreeBranch = wtResult.Branch
		opts.WorktreePath = wtResult.WorkspacePath
		opts.WorktreeRepoID = "" // Resolved from context; set properly below
	}
```

Actually, store the repoID in the `ResolveWorktreeWorkspace` result. Update `worktree.Result` to include `RepoID`:

```go
type Result struct {
	WorkspacePath string
	Branch        string
	RunName       string
	Reused        bool
	ActiveRunID   string
	RepoID        string // normalized repo identifier
}
```

Then in each provider:

```go
	if wtResult != nil {
		opts.WorktreeBranch = wtResult.Branch
		opts.WorktreePath = wtResult.WorkspacePath
		opts.WorktreeRepoID = wtResult.RepoID
	}
```

**Step 3: Verify it compiles**

Run: `go build ./cmd/moat/`
Expected: compiles successfully

**Step 4: Commit**

```
feat(worktree): add --wt flag to claude, codex, and gemini commands
```

---

### Task 7: Active run detection

**Files:**
- Modify: `internal/worktree/worktree.go`
- Modify: `internal/worktree/worktree_test.go`

The `Resolve` function currently doesn't check for active runs (it doesn't have access to the run manager). Instead, the CLI layer handles this.

**Step 1: Add `FindActiveRun` helper**

In `internal/cli/worktree.go`, add:

```go
// FindActiveRunForWorktree checks if there's an active run using the given worktree path.
// Returns the run ID if found, empty string otherwise.
func FindActiveRunForWorktree(wtPath string, runs []*run.Run) string {
	for _, r := range runs {
		if r.WorktreePath == wtPath && r.State == run.StateRunning {
			return r.ID
		}
	}
	return ""
}
```

Note: This requires importing `internal/run` in `internal/cli`, which may create an import cycle. If so, move this to `cmd/moat/cli/wt.go` as a local helper instead.

**Step 2: Wire up active run detection in `moat wt`**

In `cmd/moat/cli/wt.go`, in the `runWorktree` function, after resolving the worktree and before starting the run, add active run checking:

```go
	// Check for active run in this worktree
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	for _, r := range manager.List() {
		if r.WorktreePath == result.WorkspacePath && r.State == run.StateRunning {
			ui.Info("Attaching to running session %s (%s)", r.Name, r.ID)
			return RunAttached(ctx, manager, r)
		}
	}
```

**Step 3: Verify it compiles**

Run: `go build ./cmd/moat/`
Expected: compiles successfully

**Step 4: Commit**

```
feat(worktree): detect and reattach to active runs in worktrees
```

---

### Task 8: Run `make lint` and fix issues

**Step 1: Run lint**

Run: `make lint` (or `go vet ./...` if golangci-lint unavailable)
Fix any issues.

**Step 2: Run full test suite**

Run: `go test ./...`
Expected: PASS — no regressions

**Step 3: Commit fixes if any**

```
style(worktree): fix lint issues
```

---

### Task 9: Documentation

**Files:**
- Modify: `docs/content/reference/01-cli.md` — add `moat wt` command reference and `--wt` flag documentation
- Modify: `docs/content/reference/02-agent-yaml.md` — mention worktree naming behavior with the `name` field
- Modify: `docs/content/guides/06-ports.md` — update the existing worktree example to reference `moat wt`

**Step 1: Add `moat wt` to CLI reference**

Add a section for `moat wt` with usage, flags, subcommands (list, clean), and examples. Add `--wt` flag to the claude/codex/gemini command sections.

**Step 2: Update the worktree example in the ports guide**

Replace the manual worktree pattern in `docs/content/guides/06-ports.md` with the new `moat wt` equivalent.

**Step 3: Commit**

```
docs: add moat wt command and --wt flag documentation
```
