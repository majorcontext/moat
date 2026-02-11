package worktree

import (
	"os"
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

	result, err := Resolve(repoDir, "github.com/acme/myrepo", "to-clean", "myapp")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	err = Clean(repoDir, result.WorkspacePath)
	if err != nil {
		t.Fatalf("Clean() error = %v", err)
	}

	if _, err := os.Stat(result.WorkspacePath); !os.IsNotExist(err) {
		t.Error("worktree directory still exists after clean")
	}
}

func TestClean_NonExistentPath(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	// Path is under base but doesn't exist
	err = Clean(repoDir, wtBase+"/nonexistent/path")
	if err == nil {
		t.Error("Clean() expected error for nonexistent path, got nil")
	}
}

func TestClean_RefusesPathOutsideBase(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	// Create a directory outside the worktree base
	outsideDir, err := os.MkdirTemp("", "test-outside-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outsideDir)

	err = Clean(repoDir, outsideDir)
	if err == nil {
		t.Fatal("Clean() expected error for path outside base, got nil")
	}
	if _, statErr := os.Stat(outsideDir); os.IsNotExist(statErr) {
		t.Error("directory outside base was deleted")
	}
}

func TestClean_RefusesPathTraversal(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	err = Clean(repoDir, wtBase+"/repo/../../../../tmp")
	if err == nil {
		t.Fatal("Clean() expected error for traversal path, got nil")
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

func TestListWorktrees_SlashedBranch(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	repoID := "github.com/acme/myrepo"

	_, err = Resolve(repoDir, repoID, "feature/dark-mode", "")
	if err != nil {
		t.Fatalf("Resolve feature/dark-mode: %v", err)
	}
	_, err = Resolve(repoDir, repoID, "simple", "")
	if err != nil {
		t.Fatalf("Resolve simple: %v", err)
	}

	entries, err := ListWorktrees(repoID)
	if err != nil {
		t.Fatalf("ListWorktrees() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ListWorktrees() returned %d entries, want 2", len(entries))
	}

	branches := map[string]bool{}
	for _, e := range entries {
		branches[e.Branch] = true
	}
	if !branches["feature/dark-mode"] {
		t.Error("missing branch feature/dark-mode in entries")
	}
	if !branches["simple"] {
		t.Error("missing branch simple in entries")
	}
}

func TestListWorktrees_NoRepoDir(t *testing.T) {
	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	entries, err := ListWorktrees("nonexistent/repo")
	if err != nil {
		t.Fatalf("ListWorktrees() error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ListWorktrees() returned %d entries, want 0", len(entries))
	}
}
