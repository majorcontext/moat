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
