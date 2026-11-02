package githubscope

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRemoteURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"https://github.com/majorcontext/moat.git", "majorcontext/moat", true},
		{"https://github.com/MajorContext/Moat", "majorcontext/moat", true},
		{"https://github.com/majorcontext/moat/", "majorcontext/moat", true},
		{"https://x-access-token:abc@github.com/majorcontext/moat.git", "majorcontext/moat", true},
		{"https://github.com:443/majorcontext/moat.git", "majorcontext/moat", true},
		{"http://github.com/majorcontext/moat", "majorcontext/moat", true},
		{"git://github.com/majorcontext/moat.git", "majorcontext/moat", true},
		{"ssh://git@github.com/majorcontext/moat.git", "majorcontext/moat", true},
		{"ssh://git@github.com:22/majorcontext/moat.git", "majorcontext/moat", true},
		{"git+ssh://git@github.com/majorcontext/moat.git", "majorcontext/moat", true},
		{"git@github.com:majorcontext/moat.git", "majorcontext/moat", true},
		{"github.com:majorcontext/moat", "majorcontext/moat", true},
		{"git@GitHub.com:majorcontext/moat.git", "majorcontext/moat", true},
		{"https://github.com/my.org-x/repo_name.v2.git", "", false}, // dots are not valid in owners
		{"https://github.com/my-org/repo_name.v2.git", "my-org/repo_name.v2", true},

		{"https://gitlab.com/majorcontext/moat.git", "", false},
		{"https://github.com.evil.com/majorcontext/moat.git", "", false},
		{"https://evilgithub.com/majorcontext/moat.git", "", false},
		{"git@gitlab.com:majorcontext/moat.git", "", false},
		{"https://github.com/majorcontext", "", false},
		{"https://github.com/majorcontext/moat/tree/main", "", false},
		{"https://github.com/majorcontext/..", "", false},
		{"file:///home/u/moat", "", false},
		{"/home/u/moat", "", false},
		{"../moat", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := ParseRemoteURL(c.in)
		assert.Equal(t, c.ok, ok, c.in)
		assert.Equal(t, c.want, got, c.in)
	}
}

func TestNormalizeRepo(t *testing.T) {
	got, err := NormalizeRepo("MajorContext/Moat.git")
	require.NoError(t, err)
	assert.Equal(t, "majorcontext/moat", got)

	for _, bad := range []string{"", "moat", "a/b/c", "/moat", "owner/", "-owner/x", "owner/..", "own er/x", "owner/re po"} {
		_, err := NormalizeRepo(bad)
		assert.Error(t, err, bad)
	}
}

func TestParseRemoteURLs(t *testing.T) {
	cfg := `
[core]
	repositoryformatversion = 0
	url = https://github.com/not/a-remote.git
[remote "origin"]
	url = https://github.com/majorcontext/moat.git ; trailing comment
	fetch = +refs/heads/*:refs/remotes/origin/*
[remote "My Fork"]
	URL = "git@github.com:someone/moat.git"
	pushurl = git@github.com:someone/moat-push.git # comment
[remote.legacy]
	url = https://github.com/legacy/repo
[branch "main"]
	remote = origin
# [remote "commented"]
;	url = https://github.com/commented/out.git
`
	got := parseRemoteURLs([]byte(cfg))
	assert.Equal(t, map[string][]string{
		"origin":  {"https://github.com/majorcontext/moat.git"},
		"My Fork": {"git@github.com:someone/moat.git", "git@github.com:someone/moat-push.git"},
		"legacy":  {"https://github.com/legacy/repo"},
	}, got)
}

// writeRepo creates dir/.git/config with the given remotes (name → url).
func writeRepo(t *testing.T, dir string, remotes map[string]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("[core]\n\tbare = false\n")
	for name, url := range remotes {
		b.WriteString("[remote \"" + name + "\"]\n\turl = " + url + "\n")
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(b.String()), 0o644))
}

func repoNames(res DiscoverResult) []string {
	out := make([]string, 0, len(res.Repos))
	for _, f := range res.Repos {
		out = append(out, f.Repo)
	}
	return out
}

func TestDiscover(t *testing.T) {
	ws := t.TempDir()
	writeRepo(t, ws, map[string]string{"origin": "https://github.com/majorcontext/moat.git"})
	writeRepo(t, filepath.Join(ws, "keep"), map[string]string{
		"origin":   "git@github.com:majorcontext/keep.git",
		"upstream": "https://github.com/MajorContext/Keep",
		"gitlab":   "https://gitlab.com/majorcontext/keep.git",
	})
	writeRepo(t, filepath.Join(ws, "services", "api"), map[string]string{"origin": "https://github.com/acme/api"})

	// Package-manager checkouts are skipped.
	writeRepo(t, filepath.Join(ws, "node_modules", "dep"), map[string]string{"origin": "https://github.com/third/dep.git"})
	writeRepo(t, filepath.Join(ws, "vendor", "lib"), map[string]string{"origin": "https://github.com/third/lib.git"})
	writeRepo(t, filepath.Join(ws, ".build", "checkouts", "swift"), map[string]string{"origin": "https://github.com/third/swift.git"})
	writeRepo(t, filepath.Join(ws, ".terraform", "modules", "m"), map[string]string{"origin": "https://github.com/third/tf.git"})

	// Beyond maxDepth.
	writeRepo(t, filepath.Join(ws, "a", "b", "c", "d", "e"), map[string]string{"origin": "https://github.com/too/deep.git"})

	// A linked worktree: .git file → gitdir with commondir → main config.
	main := filepath.Join(t.TempDir(), "main")
	writeRepo(t, main, map[string]string{"origin": "https://github.com/acme/worktree-main.git"})
	wtGitdir := filepath.Join(main, ".git", "worktrees", "wt")
	require.NoError(t, os.MkdirAll(wtGitdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wtGitdir, "commondir"), []byte("../..\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "wt"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "wt", ".git"), []byte("gitdir: "+wtGitdir+"\n"), 0o644))

	// A submodule: .git file → relative gitdir with its own config.
	modDir := filepath.Join(ws, ".git", "modules", "sub")
	require.NoError(t, os.MkdirAll(modDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "config"), []byte("[remote \"origin\"]\n\turl = https://github.com/acme/sub.git\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "sub", ".git"), []byte("gitdir: ../.git/modules/sub\n"), 0o644))

	// A symlinked .git is ignored.
	linked := filepath.Join(ws, "linked")
	require.NoError(t, os.MkdirAll(linked, 0o755))
	other := t.TempDir()
	writeRepo(t, other, map[string]string{"origin": "https://github.com/acme/symlinked.git"})
	require.NoError(t, os.Symlink(filepath.Join(other, ".git"), filepath.Join(linked, ".git")))

	res, err := Discover(ws)
	require.NoError(t, err)
	assert.False(t, res.Truncated)
	assert.Equal(t, []string{
		"acme/api",
		"acme/sub",
		"acme/worktree-main",
		"majorcontext/keep",
		"majorcontext/moat",
	}, repoNames(res))

	for _, f := range res.Repos {
		switch f.Repo {
		case "majorcontext/moat":
			assert.Equal(t, []string{"./ (origin)"}, f.Sources)
		case "majorcontext/keep":
			assert.Equal(t, []string{"./keep (origin, upstream)"}, f.Sources)
		case "acme/api":
			assert.Equal(t, []string{"./services/api (origin)"}, f.Sources)
		}
	}
}

func TestDiscoverEmptyAndMalformed(t *testing.T) {
	ws := t.TempDir()
	res, err := Discover(ws)
	require.NoError(t, err)
	assert.Empty(t, res.Repos)

	// A garbled config and a bad .git file yield nothing rather than an error.
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "bad", ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "bad", ".git", "config"), []byte("[remote \"x\"\n\x00\x01url"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "badfile"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "badfile", ".git"), []byte("not a gitdir line"), 0o644))
	res, err = Discover(ws)
	require.NoError(t, err)
	assert.Empty(t, res.Repos)

	_, err = Discover(filepath.Join(ws, "missing"))
	assert.Error(t, err)
}

func TestRemoteConfigMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	cases := map[string]string{
		"same line":                "[remote \"origin\"] url = https://github.com/acme/app\n",
		"continued value":          "[remote \"origin\"]\nurl = https://github.com/acme/\\\napp\n",
		"odd backslashes":          "[remote \"origin\"]\nurl = https://github.com/acme/\\\\\\\napp\n",
		"comment is not continued": "[remote \"origin\"]\nurl = https://github.com/acme/one # comment \\\nurl = https://github.com/acme/two\n",
		"bracket in comment":       "[remote \"origin\"] # ]\nurl = https://github.com/acme/app\n",
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
			out, err := exec.Command("git", "config", "--file", path, "--get-all", "remote.origin.url").Output()
			require.NoError(t, err)
			assert.Equal(t, strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"), parseRemoteURLs([]byte(data))["origin"])
		})
	}
}
