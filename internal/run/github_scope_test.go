package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	keeplib "github.com/majorcontext/keep"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/githubscope"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useTempGitHubScopeStore points the approval store at a temp file.
func useTempGitHubScopeStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), githubscope.StoreFileName)
	prev := githubScopeStorePath
	githubScopeStorePath = func() string { return path }
	t.Cleanup(func() { githubScopeStorePath = prev })
	return path
}

func gitWorkspace(t *testing.T, remote string) string {
	t.Helper()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = "+remote+"\n"), 0o644))
	return ws
}

func scopedConfig() *config.Config {
	return &config.Config{GitHub: config.GitHubConfig{Scope: githubscope.ScopeWorkspace}}
}

func TestCheckGitHubScopeConflicts(t *testing.T) {
	plan := &githubscope.Plan{Enabled: true}
	require.NoError(t, checkGitHubScopeConflicts(plan, []string{"github", "ssh:gitlab.com"}, scopedConfig()))

	err := checkGitHubScopeConflicts(plan, []string{"github", "ssh:github.com"}, scopedConfig())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh:github.com")

	cfg := scopedConfig()
	cfg.MCP = []config.MCPServerConfig{{Name: "gh", URL: "https://api.githubcopilot.com/mcp/", Auth: &config.MCPAuthConfig{Grant: "github"}}}
	err = checkGitHubScopeConflicts(plan, []string{"github"}, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `MCP server "gh"`)

	// Companion: an MCP server with another grant is fine.
	cfg.MCP[0].Auth.Grant = "linear"
	require.NoError(t, checkGitHubScopeConflicts(plan, []string{"github"}, cfg))

	cfg = scopedConfig()
	cfg.Agent = "copilot"
	err = checkGitHubScopeConflicts(plan, []string{"github"}, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Copilot")

	// A sticky plan explains where the scope came from and how to lift it.
	sticky := &githubscope.Plan{Enabled: true, Sticky: true, LockedAt: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}
	err = checkGitHubScopeConflicts(sticky, []string{"github"}, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "locked to its GitHub repositories on 2026-11-02")
	assert.Contains(t, err.Error(), "moat github forget --all")
}

func TestPrepareGitHubScope(t *testing.T) {
	path := useTempGitHubScopeStore(t)
	ws := gitWorkspace(t, "https://github.com/majorcontext/moat.git")

	// No github grant: nothing to scope, nothing recorded.
	plan, err := prepareGitHubScope(Options{Workspace: ws, Grants: []string{"claude"}, Config: scopedConfig()})
	require.NoError(t, err)
	assert.Nil(t, plan)
	_, statErr := os.Stat(path)
	require.ErrorIs(t, statErr, os.ErrNotExist, "no approvals recorded without the github grant")

	// Scope off and project never locked: not scoped.
	plan, err = prepareGitHubScope(Options{Workspace: ws, Grants: []string{"github"}, Config: &config.Config{}})
	require.NoError(t, err)
	assert.Nil(t, plan)

	// First scoped run records and locks.
	plan, err = prepareGitHubScope(Options{Workspace: ws, Grants: []string{"github"}, Config: scopedConfig()})
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.True(t, plan.FirstRun)
	assert.Equal(t, []string{"majorcontext/moat"}, plan.GrantedRepos())

	// moat.yaml drops the scope (or the config is missing entirely): still scoped.
	for _, cfg := range []*config.Config{{}, nil} {
		plan, err = prepareGitHubScope(Options{Workspace: ws, Grants: []string{"github"}, Config: cfg})
		require.NoError(t, err)
		require.NotNil(t, plan)
		assert.True(t, plan.Sticky)
		assert.False(t, plan.FirstRun)
	}

	// The copilot grant alias resolves to github and still hits the Copilot conflict.
	cfg := scopedConfig()
	cfg.Agent = "copilot"
	_, err = prepareGitHubScope(Options{Workspace: ws, Grants: []string{"github"}, Config: cfg})
	require.Error(t, err)
}

func TestPlanGitHubScopeCopilotGrantAlias(t *testing.T) {
	useTempGitHubScopeStore(t)
	ws := gitWorkspace(t, "https://github.com/majorcontext/moat.git")
	store, err := OpenGitHubScopeStore()
	require.NoError(t, err)
	defer store.Close()

	plan, err := PlanGitHubScope(store, ws, []string{"copilot"}, scopedConfig())
	require.NoError(t, err)
	assert.True(t, plan.Enabled, "the copilot grant carries the GitHub token, so it is scoped too")
}

func TestGitHubScopePolicyUsesLookupAndCache(t *testing.T) {
	useTempGitHubScopeStore(t)
	ws := gitWorkspace(t, "https://github.com/majorcontext/moat.git")
	plan, err := prepareGitHubScope(Options{Workspace: ws, Grants: []string{"github"}, Config: scopedConfig()})
	require.NoError(t, err)

	prev := githubLookupRepos
	t.Cleanup(func() { githubLookupRepos = prev })
	calls := 0
	githubLookupRepos = func(_ context.Context, token string, repos []string) (map[string]githubscope.RepoMeta, map[string]error) {
		calls++
		assert.Equal(t, "tok", token)
		assert.Equal(t, []string{"majorcontext/moat"}, repos)
		if calls == 1 {
			return map[string]githubscope.RepoMeta{"majorcontext/moat": {FullName: "majorcontext/moat", ID: 42, NodeIDs: []string{"R_kgDOMOAT"}}}, nil
		}
		return nil, map[string]error{"majorcontext/moat": errors.New("offline")}
	}

	for i := 0; i < 2; i++ {
		policy, err := githubScopePolicy(context.Background(), plan, "tok")
		require.NoError(t, err)
		// The second call fails the lookup and must fall back to the cache.
		assert.Contains(t, string(policy), "R_kgDOMOAT", "call %d", i+1)
		assert.Contains(t, string(policy), "/repositories/(42)", "call %d", i+1)
	}
	assert.Equal(t, 2, calls)

	// Companion: no token means no lookup, and the policy still renders.
	githubLookupRepos = func(context.Context, string, []string) (map[string]githubscope.RepoMeta, map[string]error) {
		t.Fatal("lookup must not run without a token")
		return nil, nil
	}
	_, err = githubScopePolicy(context.Background(), plan, "")
	require.NoError(t, err)
}

func renderedTestPolicy(t *testing.T) []byte {
	t.Helper()
	p, err := githubscope.RenderPolicy(githubscope.PolicyParams{Repos: []string{"majorcontext/moat"}, OtherRepos: githubscope.OtherReposRead})
	require.NoError(t, err)
	return p
}

func TestMergeGitHubScopePolicy(t *testing.T) {
	gh := renderedTestPolicy(t)

	t.Run("no user policy", func(t *testing.T) {
		yamls, sets, err := mergeGitHubScopePolicy(gh, nil, nil)
		require.NoError(t, err)
		assert.Empty(t, sets)
		assert.Equal(t, gh, yamls["http"])
	})

	t.Run("file policy merged", func(t *testing.T) {
		user := []byte("scope: http\nmode: enforce\nrules:\n  - name: deny-admin\n    match:\n      when: \"params.path == '/admin'\"\n    action: deny\n")
		yamls, _, err := mergeGitHubScopePolicy(gh, map[string][]byte{"http": user, "mcp-x": []byte("scope: mcp-x\n")}, nil)
		require.NoError(t, err)
		assert.Contains(t, string(yamls["http"]), "deny-admin")
		assert.Contains(t, string(yamls["http"]), "github-git-write")
		assert.Equal(t, "scope: mcp-x\n", string(yamls["mcp-x"]), "other scopes untouched")
	})

	t.Run("inline deny list merged", func(t *testing.T) {
		deny := []string{"delete api.example.com/*", "POST api.example.com/*"}
		sets := []daemon.PolicyRuleSetSpec{
			{Scope: "http", Mode: "enforce", Deny: deny},
			{Scope: "mcp-linear", Mode: "enforce", Deny: []string{"delete_issue"}},
		}
		yamls, rest, err := mergeGitHubScopePolicy(gh, nil, sets)
		require.NoError(t, err)
		assert.Equal(t, []daemon.PolicyRuleSetSpec{sets[1]}, rest, "the http rule set moves into the merged file")

		merged, err := keeplib.LoadFromBytes(yamls["http"])
		require.NoError(t, err)
		defer merged.Close()
		rs := keeplib.NewRuleSet("http", "enforce")
		rs.Deny(deny...)
		direct, err := rs.Compile()
		require.NoError(t, err)
		defer direct.Close()

		// Parity with the daemon's RuleSet path, including Keep's quirk that an
		// uppercase method in the pattern never matches (the operation is
		// lowercased, the pattern is not).
		for _, call := range []keeplib.Call{
			keeplib.NewHTTPCall("DELETE", "api.example.com", "/thing"),
			keeplib.NewHTTPCall("POST", "api.example.com", "/thing"),
			keeplib.NewHTTPCall("GET", "api.example.com", "/thing"),
		} {
			want, err := keeplib.SafeEvaluate(context.Background(), direct, call, "http")
			require.NoError(t, err)
			got, err := keeplib.SafeEvaluate(context.Background(), merged, call, "http")
			require.NoError(t, err)
			assert.Equal(t, want.Decision, got.Decision, call.Operation)
		}
		res, err := keeplib.SafeEvaluate(context.Background(), merged, keeplib.NewHTTPCall("DELETE", "api.example.com", "/thing"), "http")
		require.NoError(t, err)
		assert.Equal(t, keeplib.Deny, res.Decision)
		assert.Equal(t, "network-keep-policy-deny-1", res.Rule)
	})

	t.Run("inline audit deny list rejected", func(t *testing.T) {
		sets := []daemon.PolicyRuleSetSpec{{Scope: "http", Mode: "audit", Deny: []string{"DELETE api.example.com/*"}}}
		_, _, err := mergeGitHubScopePolicy(gh, nil, sets)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mode: enforce")
	})

	t.Run("audit-mode user policy rejected", func(t *testing.T) {
		user := []byte("scope: http\nrules:\n  - name: r\n    action: log\n")
		_, _, err := mergeGitHubScopePolicy(gh, map[string][]byte{"http": user}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mode: enforce")
	})

	t.Run("unpinned body rule rejected", func(t *testing.T) {
		user := []byte("scope: http\nmode: enforce\nrules:\n  - name: secrets\n    match:\n      when: \"params.body != null && hasSecrets(params.body)\"\n    action: deny\n")
		_, _, err := mergeGitHubScopePolicy(gh, map[string][]byte{"http": user}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "block git over HTTPS")
	})

	t.Run("pinned body rule accepted", func(t *testing.T) {
		user := []byte("scope: http\nmode: enforce\nrules:\n  - name: secrets\n    match:\n      operation: \"post httpbin.org/*\"\n      when: \"params.body != null && hasSecrets(params.body)\"\n    action: deny\n")
		_, _, err := mergeGitHubScopePolicy(gh, map[string][]byte{"http": user}, nil)
		require.NoError(t, err)
	})
}

func TestCheckGitHubScopeCapability(t *testing.T) {
	require.NoError(t, checkGitHubScopeCapability([]string{daemon.CapKeepPolicy, daemon.CapKeepScopedBody}))
	err := checkGitHubScopeCapability([]string{daemon.CapKeepPolicy, daemon.CapKeepBodyPolicy})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "moat proxy restart")
}
