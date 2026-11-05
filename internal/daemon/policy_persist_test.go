package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	keeplib "github.com/majorcontext/keep"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/githubscope"
	_ "github.com/majorcontext/moat/internal/providers/github"
	"github.com/stretchr/testify/require"
)

func TestPoliciesSurviveRegistrationAndRestore(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	t.Setenv("MOAT_KEYRING_BACKEND", "file")
	key, err := credential.DefaultEncryptionKey()
	require.NoError(t, err)
	store, err := credential.NewProfileStore("", key)
	require.NoError(t, err)
	require.NoError(t, store.Save(credential.Credential{Provider: "github", Token: "review-test-token"}))
	policy, err := githubscope.RenderPolicy(githubscope.PolicyParams{Repos: []string{"acme/allowed"}, OtherRepos: githubscope.OtherReposRead})
	require.NoError(t, err)
	server := NewServer(filepath.Join(testSockDir(t), "s.sock"), 0)
	request := RegisterRequest{RunID: "scoped", Grants: []string{"github"}, Credentials: []CredentialSpec{{Host: "api.github.com", Header: "Authorization", Value: "Bearer review-test-token", Grant: "github"}}, PolicyYAML: map[string][]byte{"http": policy}, PolicyRuleSets: []PolicyRuleSetSpec{{Scope: "mcp", Mode: "enforce", Deny: []string{"delete_*"}}}}
	body, err := json.Marshal(request)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	server.handleRegisterRun(recorder, httptest.NewRequest("POST", "/v1/runs", bytes.NewReader(body)))
	require.Equal(t, 201, recorder.Code, recorder.Body.String())
	original := server.registry.List()
	require.Len(t, original, 1)
	defer original[0].CancelRefresh()
	defer original[0].Close()
	persister := NewRunPersister(filepath.Join(t.TempDir(), "runs.json"), server.registry)
	require.NoError(t, persister.Save())
	disk, err := os.ReadFile(persister.path)
	require.NoError(t, err)
	var legacy struct {
		Runs []PersistedRun `json:"runs"`
	}
	require.NoError(t, json.Unmarshal(disk, &legacy))
	require.Empty(t, legacy.Runs, "older daemons must not see policy-bearing auth tokens")
	persisted, err := persister.Load()
	require.NoError(t, err)
	require.Len(t, persisted, 1)
	require.True(t, persisted[0].PolicyRequired)
	require.Equal(t, request.PolicyYAML, persisted[0].PolicyYAML)
	require.Equal(t, request.PolicyRuleSets, persisted[0].PolicyRuleSets)
	restored := NewRegistry()
	require.Equal(t, 1, RestoreRuns(context.Background(), restored, persisted))
	rc, ok := restored.Lookup(persisted[0].AuthToken)
	require.True(t, ok)
	defer rc.CancelRefresh()
	defer rc.Close()
	for _, candidate := range []*RunContext{original[0], rc} {
		require.NotEmpty(t, candidate.Credentials["api.github.com"], "credential injection survives alongside the policy")
		for _, test := range []struct {
			repo     string
			decision keeplib.Decision
		}{{"other", keeplib.Deny}, {"allowed", keeplib.Allow}} {
			result, err := keeplib.SafeEvaluate(context.Background(), candidate.KeepEngines["http"], keeplib.NewHTTPCall("POST", "github.com", "/acme/"+test.repo+".git/git-receive-pack"), "http")
			require.NoError(t, err)
			require.Equal(t, test.decision, result.Decision)
		}
		result, err := keeplib.SafeEvaluate(context.Background(), candidate.KeepEngines["mcp"], keeplib.Call{Operation: "delete_item"}, "mcp")
		require.NoError(t, err)
		require.Equal(t, keeplib.Deny, result.Decision)
	}
}

func TestRestoreRejectsMissingOrInvalidPolicies(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	t.Setenv("MOAT_KEYRING_BACKEND", "file")
	registry := NewRegistry()
	require.Equal(t, 0, RestoreRuns(context.Background(), registry, []PersistedRun{
		{RunID: "legacy", AuthToken: "legacy", Grants: []string{"github"}},
		{RunID: "missing", AuthToken: "missing", PolicyRequired: true},
		{RunID: "invalid", AuthToken: "invalid", PolicyRequired: true, PolicyYAML: map[string][]byte{"http": []byte("not a policy")}},
	}))
	require.Empty(t, registry.List())
}

func TestCompilePoliciesRejectsScopeOverride(t *testing.T) {
	_, err := compilePolicies(map[string][]byte{"http": []byte("scope: http\nmode: enforce\nrules: [{name: deny-all, action: deny}]\n")}, []PolicyRuleSetSpec{{Scope: "http", Mode: "enforce", Deny: []string{"other"}}})
	require.ErrorContains(t, err, "multiple policies for scope")
}
