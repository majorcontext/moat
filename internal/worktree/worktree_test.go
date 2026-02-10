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

	_, err = Resolve(repoDir, "github.com/acme/myrepo", "existing", "myapp")
	if err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}

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
