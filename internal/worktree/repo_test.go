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
