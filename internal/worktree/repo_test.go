package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestResolveGitDir_RegularRepo(t *testing.T) {
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

	info, err := ResolveGitDir(tmpDir)
	if err != nil {
		t.Fatalf("ResolveGitDir() error = %v", err)
	}
	if info != nil {
		t.Errorf("ResolveGitDir() = %+v, want nil for regular repo", info)
	}
}

func TestResolveGitDir_NoGit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-nogit-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	info, err := ResolveGitDir(tmpDir)
	if err != nil {
		t.Fatalf("ResolveGitDir() error = %v", err)
	}
	if info != nil {
		t.Errorf("ResolveGitDir() = %+v, want nil for non-git dir", info)
	}
}

func TestResolveGitDir_Worktree(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	result, err := Resolve(repoDir, "github.com/acme/myrepo", "test-branch", "myapp")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	info, err := ResolveGitDir(result.WorkspacePath)
	if err != nil {
		t.Fatalf("ResolveGitDir() error = %v", err)
	}
	if info == nil {
		t.Fatal("ResolveGitDir() = nil, want non-nil for worktree")
	}

	// MainGitDir should be the repo's .git directory
	wantMainGitDir := filepath.Join(repoDir, ".git")
	if info.MainGitDir != wantMainGitDir {
		t.Errorf("MainGitDir = %q, want %q", info.MainGitDir, wantMainGitDir)
	}

	// WorktreeGitDir should be under the main .git/worktrees/ directory
	wantPrefix := filepath.Join(repoDir, ".git", "worktrees")
	if !strings.HasPrefix(info.WorktreeGitDir, wantPrefix) {
		t.Errorf("WorktreeGitDir = %q, want prefix %q", info.WorktreeGitDir, wantPrefix)
	}
}

func TestResolveGitDir_SubdirInvariant(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	result, err := Resolve(repoDir, "github.com/acme/myrepo", "subdir-test", "myapp")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	info, err := ResolveGitDir(result.WorkspacePath)
	if err != nil {
		t.Fatalf("ResolveGitDir() error = %v", err)
	}
	if info == nil {
		t.Fatal("ResolveGitDir() = nil, want non-nil for worktree")
	}

	// WorktreeGitDir must be a subdirectory of MainGitDir
	rel, err := filepath.Rel(info.MainGitDir, info.WorktreeGitDir)
	if err != nil {
		t.Fatalf("filepath.Rel() error = %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Errorf("WorktreeGitDir %q is not under MainGitDir %q (rel=%q)",
			info.WorktreeGitDir, info.MainGitDir, rel)
	}
}

func TestFindRepoRoot(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, out)
	}

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
