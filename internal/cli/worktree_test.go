package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "test-wt-cli-*")
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

func TestResolveWorktreeWorkspace_EmptyBranch(t *testing.T) {
	flags := &ExecFlags{}
	cfg := &config.Config{}

	result, err := ResolveWorktreeWorkspace("", "/some/workspace", flags, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Workspace != "/some/workspace" {
		t.Errorf("Workspace = %q, want %q", result.Workspace, "/some/workspace")
	}
	if result.Config != cfg {
		t.Error("Config should be the same object when no worktree is resolved")
	}
	if result.Result != nil {
		t.Error("Result should be nil when wtBranch is empty")
	}
}

func TestResolveWorktreeWorkspace_SetsRunName(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	// Change to repo dir so FindRepoRoot works
	oldDir, _ := os.Getwd()
	os.Chdir(repoDir)
	defer os.Chdir(oldDir)

	flags := &ExecFlags{}
	cfg := &config.Config{Name: "myagent"}

	result, err := ResolveWorktreeWorkspace("test-branch", repoDir, flags, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if flags.Name != "myagent-test-branch" {
		t.Errorf("flags.Name = %q, want %q", flags.Name, "myagent-test-branch")
	}
	if result.Result == nil {
		t.Fatal("Result should not be nil")
	}
	if result.Result.Branch != "test-branch" {
		t.Errorf("Branch = %q, want %q", result.Result.Branch, "test-branch")
	}
}

func TestResolveWorktreeWorkspace_DoesNotOverrideName(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	oldDir, _ := os.Getwd()
	os.Chdir(repoDir)
	defer os.Chdir(oldDir)

	flags := &ExecFlags{Name: "custom-name"}
	cfg := &config.Config{Name: "myagent"}

	_, err = ResolveWorktreeWorkspace("test-branch2", repoDir, flags, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if flags.Name != "custom-name" {
		t.Errorf("flags.Name = %q, want %q (should not override)", flags.Name, "custom-name")
	}
}

func TestResolveWorktreeWorkspace_ActiveRunError(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	oldDir, _ := os.Getwd()
	os.Chdir(repoDir)
	defer os.Chdir(oldDir)

	// First resolve to create the worktree
	flags := &ExecFlags{}
	cfg := &config.Config{}
	result, err := ResolveWorktreeWorkspace("active-branch", repoDir, flags, cfg)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	wtPath := result.Workspace

	// Set up the mock CheckWorktreeActive to return active run
	oldCheck := CheckWorktreeActive
	CheckWorktreeActive = func(path string) (string, string) {
		if path == wtPath {
			return "test-run", "run-123"
		}
		return "", ""
	}
	defer func() { CheckWorktreeActive = oldCheck }()

	// Second resolve should error because there's an active run
	flags2 := &ExecFlags{}
	_, err = ResolveWorktreeWorkspace("active-branch", repoDir, flags2, cfg)
	if err == nil {
		t.Fatal("expected error for active run, got nil")
	}
}

func TestResolveWorktreeWorkspace_NilCheckWorktreeActive(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	oldDir, _ := os.Getwd()
	os.Chdir(repoDir)
	defer os.Chdir(oldDir)

	// First resolve to create the worktree
	flags := &ExecFlags{}
	cfg := &config.Config{}
	_, err = ResolveWorktreeWorkspace("nil-check-branch", repoDir, flags, cfg)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Set CheckWorktreeActive to nil
	oldCheck := CheckWorktreeActive
	CheckWorktreeActive = nil
	defer func() { CheckWorktreeActive = oldCheck }()

	// Second resolve should succeed even though worktree is reused
	flags2 := &ExecFlags{}
	_, err = ResolveWorktreeWorkspace("nil-check-branch", repoDir, flags2, cfg)
	if err != nil {
		t.Fatalf("second resolve with nil CheckWorktreeActive should succeed: %v", err)
	}
}
