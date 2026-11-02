package cli

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/githubscope"
	"github.com/majorcontext/moat/internal/run"
)

func githubTestWorkspace(t *testing.T, remotes map[string]string) string {
	t.Helper()
	t.Setenv("MOAT_HOME", t.TempDir())
	ws := t.TempDir()
	for dir, url := range remotes {
		d := filepath.Join(ws, dir)
		require.NoError(t, os.MkdirAll(filepath.Join(d, ".git"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(d, ".git", "config"),
			[]byte("[remote \"origin\"]\n\turl = "+url+"\n"), 0o644))
	}
	return ws
}

func scopedCfg() *config.Config {
	return &config.Config{GitHub: config.GitHubConfig{Scope: githubscope.ScopeWorkspace}}
}

func runPreflight(t *testing.T, ws string, grants []string, cfg *config.Config, interactive bool, input string) string {
	t.Helper()
	var out bytes.Buffer
	preflightGitHubScope(ws, grants, cfg, githubPreflight{
		out:         &out,
		in:          bufio.NewReader(strings.NewReader(input)),
		interactive: interactive,
		now:         time.Now,
	})
	return out.String()
}

// recordFirstRun simulates the first run's Create, which records approvals.
func recordFirstRun(t *testing.T, ws string) {
	t.Helper()
	store, err := run.OpenGitHubScopeStore()
	require.NoError(t, err)
	defer store.Close()
	plan, err := run.PlanGitHubScope(store, ws, []string{"github"}, scopedCfg())
	require.NoError(t, err)
	require.NoError(t, githubscope.RecordFirstRun(store, plan, time.Now()))
}

func grantedRepos(t *testing.T, ws string) []string {
	t.Helper()
	store, err := run.OpenGitHubScopeStore()
	require.NoError(t, err)
	defer store.Close()
	plan, err := run.PlanGitHubScope(store, ws, []string{"github"}, scopedCfg())
	require.NoError(t, err)
	return plan.GrantedRepos()
}

func TestGitHubPreflightFirstRun(t *testing.T) {
	ws := githubTestWorkspace(t, map[string]string{".": "https://github.com/majorcontext/moat.git"})
	out := runPreflight(t, ws, []string{"github"}, scopedCfg(), true, "")
	assert.Contains(t, out, "recorded for future runs")
	assert.Contains(t, out, "majorcontext/moat")
	assert.Contains(t, out, "Other repositories: read-only")
	assert.NotContains(t, out, "[y/N]", "the first run does not prompt")
}

func TestGitHubPreflightSilentWhenNotScoped(t *testing.T) {
	ws := githubTestWorkspace(t, map[string]string{".": "https://github.com/majorcontext/moat.git"})
	assert.Empty(t, runPreflight(t, ws, []string{"github"}, &config.Config{}, true, ""))
	assert.Empty(t, runPreflight(t, ws, []string{"claude"}, scopedCfg(), true, ""), "no github grant, nothing to scope")

	// A conflicting configuration is left for Create to report.
	cfg := scopedCfg()
	cfg.Agent = "copilot"
	assert.Empty(t, runPreflight(t, ws, []string{"github"}, cfg, true, ""))
}

func TestGitHubPreflightPromptsForNewRepos(t *testing.T) {
	ws := githubTestWorkspace(t, map[string]string{".": "https://github.com/majorcontext/moat.git"})
	recordFirstRun(t, ws)

	for dir, url := range map[string]string{"a": "https://github.com/acme/approve-me", "b": "https://github.com/acme/reject-me"} {
		require.NoError(t, os.MkdirAll(filepath.Join(ws, dir, ".git"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws, dir, ".git", "config"), []byte("[remote \"origin\"]\n\turl = "+url+"\n"), 0o644))
	}

	out := runPreflight(t, ws, []string{"github"}, scopedCfg(), true, "y\n\n")
	assert.Contains(t, out, "New since the last approved run")
	assert.Contains(t, out, "Allow this run to push to")
	assert.Contains(t, out, "moat github allow acme/reject-me")
	assert.Equal(t, []string{"acme/approve-me", "majorcontext/moat"}, grantedRepos(t, ws),
		"only the repository answered yes is approved; an empty answer is no")
}

func TestGitHubPreflightNonInteractiveDoesNotApprove(t *testing.T) {
	ws := githubTestWorkspace(t, map[string]string{".": "https://github.com/majorcontext/moat.git"})
	recordFirstRun(t, ws)
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "x", ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "x", ".git", "config"), []byte("[remote \"origin\"]\n\turl = https://github.com/acme/new\n"), 0o644))

	out := runPreflight(t, ws, []string{"github"}, scopedCfg(), false, "y\n")
	assert.Contains(t, out, "is new to this project and not approved")
	assert.Contains(t, out, "moat github allow acme/new")
	assert.NotContains(t, out, "[y/N]")
	assert.Equal(t, []string{"majorcontext/moat"}, grantedRepos(t, ws), "non-interactive runs never approve")
}

func TestGitHubPreflightSticky(t *testing.T) {
	ws := githubTestWorkspace(t, map[string]string{".": "https://github.com/majorcontext/moat.git"})
	recordFirstRun(t, ws)
	out := runPreflight(t, ws, []string{"github"}, &config.Config{}, true, "")
	assert.Contains(t, out, "moat.yaml no longer sets github.scope: workspace")
	assert.Contains(t, out, "moat github forget --all")
}

func TestWriteGitHubRepos(t *testing.T) {
	ws := githubTestWorkspace(t, map[string]string{".": "https://github.com/majorcontext/moat.git"})
	store, err := run.OpenGitHubScopeStore()
	require.NoError(t, err)
	defer store.Close()
	key, err := githubscope.ProjectKey(ws)
	require.NoError(t, err)

	// Scope off: lists what was found without calling it approved.
	plan, err := githubscope.BuildPlan(ws, githubscope.Settings{Scope: githubscope.ScopeWorkspace}, nil)
	require.NoError(t, err)
	plan.Enabled = false
	var buf bytes.Buffer
	writeGitHubRepos(&buf, key, plan)
	assert.Contains(t, buf.String(), "Scope:   any")
	assert.Contains(t, buf.String(), "found")
	assert.NotContains(t, buf.String(), "approved")

	// Scoped, after the first run, with a pending repository.
	recordFirstRun(t, ws)
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "x", ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "x", ".git", "config"), []byte("[remote \"origin\"]\n\turl = https://github.com/acme/new\n"), 0o644))
	existing, err := store.Get(key)
	require.NoError(t, err)
	plan, err = githubscope.BuildPlan(ws, githubscope.Settings{Scope: githubscope.ScopeWorkspace}, existing)
	require.NoError(t, err)
	buf.Reset()
	writeGitHubRepos(&buf, key, plan)
	out := buf.String()
	assert.Contains(t, out, "Scope:   workspace (since")
	assert.Regexp(t, `majorcontext/moat\s+approved\s+\./ \(origin\)`, out)
	assert.Regexp(t, `acme/new\s+pending\s+\./x \(origin\)`, out)
	assert.Contains(t, out, "moat github allow OWNER/REPO")
}
