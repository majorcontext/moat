package githubscope

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	keeplib "github.com/majorcontext/keep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	wsNodeID     = "R_kgDOAAAAAQ"
	wsLegacyID   = "MDEwOlJlcG9zaXRvcnkxMTE="
	otherNodeID  = "R_kgDOZZZZZQ"
	wsRepoNumber = 111
)

func testParams(other string) PolicyParams {
	return PolicyParams{
		Repos:      []string{"majorcontext/moat", "majorcontext/keep"},
		RepoIDs:    []int64{wsRepoNumber, 222},
		NodeIDs:    []string{wsNodeID, wsLegacyID},
		OtherRepos: other,
	}
}

func loadPolicy(t *testing.T, p PolicyParams) *keeplib.Engine {
	t.Helper()
	data, err := RenderPolicy(p)
	require.NoError(t, err)
	require.NoError(t, keeplib.ValidateRuleBytes(data), string(data))
	eng, err := keeplib.LoadFromBytes(data)
	require.NoError(t, err)
	t.Cleanup(eng.Close)
	return eng
}

// evalHTTP builds the call the way gatekeeper does: the body is only parsed
// and attached when a body rule applies to the request's operation.
func evalHTTP(t *testing.T, eng *keeplib.Engine, method, host, path, body string) keeplib.EvalResult {
	t.Helper()
	path, query, _ := strings.Cut(path, "?")
	call := keeplib.NewHTTPCall(method, host, path)
	if eng.RequiresBodyFor(PolicyScope, call) {
		var parsed any
		if body != "" {
			require.NoError(t, json.Unmarshal([]byte(body), &parsed), body)
		}
		call = keeplib.NewHTTPCallWithBody(method, host, path, parsed)
	}
	call.Params["query"] = query
	res, err := keeplib.SafeEvaluate(context.Background(), eng, call, PolicyScope)
	require.NoError(t, err)
	return res
}

func gql(t *testing.T, query string, variables map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	require.NoError(t, err)
	return string(b)
}

type httpCase struct {
	name                 string
	method, host, path   string
	body                 string
	wantDeny             bool
	wantRule             string // checked when wantDeny
	wantMessageSubstring string
}

func runCases(t *testing.T, eng *keeplib.Engine, cases []httpCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := evalHTTP(t, eng, c.method, c.host, c.path, c.body)
			if c.wantDeny {
				assert.Equal(t, keeplib.Deny, res.Decision, "expected deny")
				if c.wantRule != "" {
					assert.Equal(t, c.wantRule, res.Rule)
				}
				if c.wantMessageSubstring != "" {
					assert.Contains(t, res.Message, c.wantMessageSubstring)
				}
				return
			}
			assert.Equal(t, keeplib.Allow, res.Decision, "expected allow, denied by %q: %s", res.Rule, res.Message)
		})
	}
}

func TestPolicyGitOverHTTPS(t *testing.T) {
	eng := loadPolicy(t, testParams(OtherReposRead))
	runCases(t, eng, []httpCase{
		{name: "push to workspace repo", method: "POST", host: "github.com", path: "/majorcontext/moat.git/git-receive-pack"},
		{name: "push to workspace repo without .git", method: "POST", host: "github.com", path: "/majorcontext/keep/git-receive-pack"},
		{name: "push matches case-insensitively", method: "POST", host: "github.com", path: "/MajorContext/Moat.git/git-receive-pack"},
		{
			name: "push to other repo", method: "POST", host: "github.com", path: "/acme/other.git/git-receive-pack",
			wantDeny: true, wantRule: "github-git-write", wantMessageSubstring: "majorcontext/keep, majorcontext/moat",
		},
		{
			name: "push to repo sharing a name prefix", method: "POST", host: "github.com", path: "/majorcontext/moat-evil.git/git-receive-pack",
			wantDeny: true, wantRule: "github-git-write",
		},
		{
			name: "push to repo sharing an owner prefix", method: "POST", host: "github.com", path: "/majorcontext/moat.gitx/git-receive-pack",
			wantDeny: true, wantRule: "github-git-write",
		},
		{
			name: "dot-segment escape from workspace path", method: "POST", host: "github.com", path: "/majorcontext/moat.git/../../acme/other.git/git-receive-pack",
			wantDeny: true, wantRule: "github-path-hygiene",
		},
		{
			name: "empty segment", method: "POST", host: "github.com", path: "//acme/other.git/git-receive-pack",
			wantDeny: true, wantRule: "github-path-hygiene",
		},
		{name: "fetch ref advertisement of other repo", method: "GET", host: "github.com", path: "/acme/other.git/info/refs?service=git-upload-pack"},
		{
			name: "push ref advertisement of other repo", method: "GET", host: "github.com", path: "/acme/other.git/info/refs?service=git-receive-pack",
			wantDeny: true, wantRule: "github-git-push-start", wantMessageSubstring: "Push blocked",
		},
		{name: "push ref advertisement of workspace repo", method: "GET", host: "github.com", path: "/majorcontext/moat.git/info/refs?service=git-receive-pack"},
		{name: "fetch from other repo", method: "POST", host: "github.com", path: "/acme/other.git/git-upload-pack"},
		{
			name: "LFS download from other repo", method: "POST", host: "github.com", path: "/acme/other.git/info/lfs/objects/batch",
			body: `{"operation":"download","objects":[{"oid":"x","size":1}]}`,
		},
		{
			name: "LFS upload to other repo", method: "POST", host: "github.com", path: "/acme/other.git/info/lfs/objects/batch",
			body: `{"operation":"upload","objects":[{"oid":"x","size":1}]}`, wantDeny: true, wantRule: "github-lfs-upload",
		},
		{
			name: "LFS batch with no body", method: "POST", host: "github.com", path: "/acme/other.git/info/lfs/objects/batch",
			wantDeny: true, wantRule: "github-lfs-upload",
		},
		{
			name: "LFS upload to workspace repo", method: "POST", host: "github.com", path: "/majorcontext/moat.git/info/lfs/objects/batch",
			body: `{"operation":"upload","objects":[{"oid":"x","size":1}]}`,
		},
		{
			name: "LFS batch under an extra segment is not a read", method: "POST", host: "github.com", path: "/acme/other.git/x/info/lfs/objects/batch",
			wantDeny: true, wantRule: "github-git-write",
		},
		{
			name: "upload-pack under an extra segment is not a read", method: "POST", host: "github.com", path: "/acme/other.git/x/git-upload-pack",
			wantDeny: true, wantRule: "github-git-write",
		},
		{
			name: "LFS lock on other repo", method: "POST", host: "github.com", path: "/acme/other.git/info/lfs/locks",
			wantDeny: true, wantRule: "github-git-write",
		},
		{name: "unrelated host untouched", method: "POST", host: "gitlab.com", path: "/acme/other.git/git-receive-pack"},
	})
}

func TestPolicyREST(t *testing.T) {
	eng := loadPolicy(t, testParams(OtherReposRead))
	runCases(t, eng, []httpCase{
		{name: "contents write in workspace repo", method: "PUT", host: "api.github.com", path: "/repos/majorcontext/moat/contents/README.md"},
		{name: "open PR in workspace repo", method: "POST", host: "api.github.com", path: "/repos/majorcontext/keep/pulls"},
		{name: "merge PR in workspace repo", method: "PUT", host: "api.github.com", path: "/repos/majorcontext/moat/pulls/7/merge"},
		{name: "write by numeric repo ID", method: "POST", host: "api.github.com", path: "/repositories/111/pulls"},
		{
			name: "contents write in other repo", method: "PUT", host: "api.github.com", path: "/repos/acme/other/contents/README.md",
			wantDeny: true, wantRule: "github-api-write",
		},
		{
			name: "ref delete in other repo", method: "DELETE", host: "api.github.com", path: "/repos/acme/other/git/refs/heads/main",
			wantDeny: true, wantRule: "github-api-write",
		},
		{
			name: "merge in other repo", method: "PUT", host: "api.github.com", path: "/repos/acme/other/pulls/1/merge",
			wantDeny: true, wantRule: "github-api-write",
		},
		{
			name: "repo sharing a name prefix", method: "POST", host: "api.github.com", path: "/repos/majorcontext/moatx/issues",
			wantDeny: true, wantRule: "github-api-write",
		},
		{
			name: "write by unknown numeric ID", method: "POST", host: "api.github.com", path: "/repositories/999/pulls",
			wantDeny: true, wantRule: "github-api-write",
		},
		{name: "add SSH key", method: "POST", host: "api.github.com", path: "/user/keys", wantDeny: true, wantRule: "github-api-write"},
		{name: "create gist", method: "POST", host: "api.github.com", path: "/gists", wantDeny: true, wantRule: "github-api-write"},
		{name: "create repository", method: "POST", host: "api.github.com", path: "/user/repos", wantDeny: true, wantRule: "github-api-write"},
		{
			name: "fork workspace repo", method: "POST", host: "api.github.com", path: "/repos/majorcontext/moat/forks",
			wantDeny: true, wantRule: "github-new-repository",
		},
		{
			name: "generate from workspace template", method: "POST", host: "api.github.com", path: "/repos/majorcontext/moat/generate",
			wantDeny: true, wantRule: "github-new-repository",
		},
		{name: "render markdown", method: "POST", host: "api.github.com", path: "/markdown"},
		{name: "read other repo", method: "GET", host: "api.github.com", path: "/repos/acme/other/contents/README.md"},
		{name: "code search", method: "GET", host: "api.github.com", path: "/search/code"},
		{name: "current user", method: "GET", host: "api.github.com", path: "/user"},
		{
			name: "dot-segment escape", method: "PUT", host: "api.github.com", path: "/repos/majorcontext/moat/../../repos/acme/other/contents/x",
			wantDeny: true, wantRule: "github-path-hygiene",
		},
	})
}

func TestPolicyGraphQL(t *testing.T) {
	eng := loadPolicy(t, testParams(OtherReposRead))
	const host, path = "api.github.com", "/graphql"
	createPR := `mutation($input: CreatePullRequestInput!) { createPullRequest(input: $input) { pullRequest { url } } }`
	commit := `mutation($input: CreateCommitOnBranchInput!) { createCommitOnBranch(input: $input) { commit { oid } } }`

	runCases(t, eng, []httpCase{
		{name: "query", method: "POST", host: host, path: path, body: gql(t, `query { viewer { login } }`, nil)},
		{
			name: "query other repo", method: "POST", host: host, path: path,
			body: gql(t, `query { repository(owner: "acme", name: "other") { id } }`, nil),
		},
		{
			name: "gh pr create in workspace repo", method: "POST", host: host, path: path,
			body: gql(t, createPR, map[string]any{"input": map[string]any{"repositoryId": wsNodeID, "baseRefName": "main", "headRefName": "x", "title": "t"}}),
		},
		{
			name: "legacy node ID for workspace repo", method: "POST", host: host, path: path,
			body: gql(t, createPR, map[string]any{"input": map[string]any{"repositoryId": wsLegacyID}}),
		},
		{
			name: "PR in other repo", method: "POST", host: host, path: path,
			body:     gql(t, createPR, map[string]any{"input": map[string]any{"repositoryId": otherNodeID}}),
			wantDeny: true, wantRule: "github-graphql-repository-id",
		},
		{
			name: "node ID differing only in case", method: "POST", host: host, path: path,
			body:     gql(t, createPR, map[string]any{"input": map[string]any{"repositoryId": strings.ToLower(wsNodeID)}}),
			wantDeny: true, wantRule: "github-graphql-repository-id",
		},
		{
			name: "inline literal repositoryId", method: "POST", host: host, path: path,
			body:     gql(t, `mutation { createRef(input: {repositoryId: "`+otherNodeID+`", name: "refs/heads/x", oid: "abc"}) { ref { id } } }`, nil),
			wantDeny: true, wantRule: "github-graphql-repository-id",
		},
		{
			name: "variable default value", method: "POST", host: host, path: path,
			body:     gql(t, `mutation($id: ID = "`+otherNodeID+`") { createRef(input: {repositoryId: $id, name: "refs/heads/x", oid: "abc"}) { ref { id } } }`, nil),
			wantDeny: true, wantRule: "github-graphql-repository-id",
		},
		{
			name: "createRef in workspace repo", method: "POST", host: host, path: path,
			body: gql(t, `mutation { createRef(input: {repositoryId: "`+wsNodeID+`", name: "refs/heads/x", oid: "abc"}) { ref { id } } }`, nil),
		},
		{
			name: "commit on workspace branch by name", method: "POST", host: host, path: path,
			body: gql(t, commit, map[string]any{"input": map[string]any{"branch": map[string]any{"repositoryNameWithOwner": "MajorContext/Moat", "branchName": "x"}}}),
		},
		{
			name: "commit on other repo by name", method: "POST", host: host, path: path,
			body:     gql(t, commit, map[string]any{"input": map[string]any{"branch": map[string]any{"repositoryNameWithOwner": "acme/other", "branchName": "x"}}}),
			wantDeny: true, wantRule: "github-graphql-repository-name",
		},
		{
			name: "commit by branch node ID", method: "POST", host: host, path: path,
			body:     gql(t, commit, map[string]any{"input": map[string]any{"branch": map[string]any{"id": "REF_x"}}}),
			wantDeny: true, wantRule: "github-graphql-code-change",
		},
		{
			name: "commit naming workspace but carrying a branch node ID", method: "POST", host: host, path: path,
			body: gql(t, commit, map[string]any{"input": map[string]any{"branch": map[string]any{
				"repositoryNameWithOwner": "majorcontext/moat", "branchName": "x", "id": "REF_other",
			}}}),
			wantDeny: true, wantRule: "github-graphql-code-change",
		},
		{
			name: "merge PR by node ID", method: "POST", host: host, path: path,
			body: gql(t, `mutation($input: MergePullRequestInput!) { mergePullRequest(input: $input) { clientMutationId } }`,
				map[string]any{"input": map[string]any{"pullRequestId": "PR_x"}}),
			wantDeny: true, wantRule: "github-graphql-code-change", wantMessageSubstring: "gh api -X PUT",
		},
		{
			name: "aliased merge", method: "POST", host: host, path: path,
			body:     gql(t, `mutation { m: mergePullRequest(input: {pullRequestId: "PR_x"}) { clientMutationId } }`, nil),
			wantDeny: true, wantRule: "github-graphql-code-change",
		},
		{
			name: "merge hidden in a fragment", method: "POST", host: host, path: path,
			body:     gql(t, `mutation { ...F } fragment F on Mutation { mergePullRequest(input: {pullRequestId: "PR_x"}) { clientMutationId } }`, nil),
			wantDeny: true, wantRule: "github-graphql-code-change",
		},
		{
			name: "mutation next to the selected query operation", method: "POST", host: host, path: path,
			body:     `{"operationName":"Q","query":"query Q { viewer { login } } mutation M { deleteRef(input: {refId: \"REF_x\"}) { clientMutationId } }"}`,
			wantDeny: true, wantRule: "github-graphql-code-change",
		},
		{
			name: "comment by subject node ID is allowed", method: "POST", host: host, path: path,
			body: gql(t, `mutation { addComment(input: {subjectId: "PR_x", body: "hi"}) { clientMutationId } }`, nil),
		},
		{
			name: "PR edit by node ID is allowed", method: "POST", host: host, path: path,
			body: gql(t, `mutation($i: UpdatePullRequestInput!) { updatePullRequest(input: $i) { clientMutationId } }`,
				map[string]any{"i": map[string]any{"pullRequestId": "PR_x", "title": "t"}}),
		},
		{
			name: "comment review is allowed", method: "POST", host: host, path: path,
			body: gql(t, `mutation { addPullRequestReview(input: {pullRequestId: "PR_x", event: COMMENT, body: "b"}) { clientMutationId } }`, nil),
		},
		{
			name: "approving review is denied", method: "POST", host: host, path: path,
			body:     gql(t, `mutation { addPullRequestReview(input: {pullRequestId: "PR_x", event: APPROVE}) { clientMutationId } }`, nil),
			wantDeny: true, wantRule: "github-graphql-approve",
		},
		{
			name: "approve via variables is denied", method: "POST", host: host, path: path,
			body: gql(t, `mutation($i: SubmitPullRequestReviewInput!) { submitPullRequestReview(input: $i) { clientMutationId } }`,
				map[string]any{"i": map[string]any{"pullRequestReviewId": "PRR_x", "event": "APPROVE"}}),
			wantDeny: true, wantRule: "github-graphql-approve",
		},
		{
			name: "branch protection deletion by node ID is denied", method: "POST", host: host, path: path,
			body:     gql(t, `mutation { deleteBranchProtectionRule(input: {branchProtectionRuleId: "BPR_x"}) { clientMutationId } }`, nil),
			wantDeny: true, wantRule: "github-graphql-unattributed",
		},
		{
			name: "unknown future mutation is denied", method: "POST", host: host, path: path,
			body:     gql(t, `mutation { someNewMutation(input: {id: "X_1"}) { clientMutationId } }`, nil),
			wantDeny: true, wantRule: "github-graphql-unattributed",
		},
		{
			name: "settings change on workspace repo by repositoryId is allowed", method: "POST", host: host, path: path,
			body: gql(t, `mutation { updateRepository(input: {repositoryId: "`+wsNodeID+`", hasWikiEnabled: false}) { clientMutationId } }`, nil),
		},
		{
			name: "settings change on other repo by repositoryId is denied", method: "POST", host: host, path: path,
			body:     gql(t, `mutation { updateRepository(input: {repositoryId: "`+otherNodeID+`", hasWikiEnabled: false}) { clientMutationId } }`, nil),
			wantDeny: true, wantRule: "github-graphql-repository-id",
		},
		{
			name: "create repository", method: "POST", host: host, path: path,
			body:     gql(t, `mutation { createRepository(input: {name: "x", visibility: PRIVATE}) { repository { id } } }`, nil),
			wantDeny: true, wantRule: "github-graphql-new-repository",
		},
		{
			name: "unparseable query", method: "POST", host: host, path: path, body: `{"query":"mutation {"}`,
			wantDeny: true, wantRule: "github-graphql-invalid",
		},
		{
			name: "batched array body", method: "POST", host: host, path: path, body: `[{"query":"query { viewer { login } }"}]`,
			wantDeny: true, wantRule: "github-graphql-invalid",
		},
		{
			name: "empty body", method: "POST", host: host, path: path, body: "",
			wantDeny: true, wantRule: "github-graphql-invalid",
		},
		{
			name: "graphql with trailing slash is not exempt", method: "POST", host: host, path: "/graphql/",
			body: gql(t, `query { viewer { login } }`, nil), wantDeny: true, wantRule: "github-api-write",
		},
	})
}

func TestPolicyOtherReposNone(t *testing.T) {
	eng := loadPolicy(t, testParams(OtherReposNone))
	runCases(t, eng, []httpCase{
		{name: "read workspace repo", method: "GET", host: "api.github.com", path: "/repos/majorcontext/moat/contents/README.md"},
		{name: "fetch workspace repo", method: "POST", host: "github.com", path: "/majorcontext/moat.git/git-upload-pack"},
		{name: "read workspace repo by ID", method: "GET", host: "api.github.com", path: "/repositories/111"},
		{name: "current user", method: "GET", host: "api.github.com", path: "/user"},
		{
			name: "read other repo", method: "GET", host: "api.github.com", path: "/repos/acme/other/contents/README.md",
			wantDeny: true, wantRule: "github-other-repo-read",
		},
		{
			name: "read other repo by ID", method: "GET", host: "api.github.com", path: "/repositories/999",
			wantDeny: true, wantRule: "github-other-repo-read",
		},
		{
			name: "fetch other repo", method: "POST", host: "github.com", path: "/acme/other.git/git-upload-pack",
			wantDeny: true, wantRule: "github-other-repo-read",
		},
		{
			name: "ref advertisement of other repo", method: "GET", host: "github.com", path: "/acme/other.git/info/refs",
			wantDeny: true, wantRule: "github-other-repo-read",
		},
		{
			name: "code search", method: "GET", host: "api.github.com", path: "/search/code",
			wantDeny: true, wantRule: "github-other-repo-read",
		},
	})
}

// TestPolicyNoApprovedRepos covers a project with no GitHub remotes: every
// write is denied, reads still work.
func TestPolicyNoApprovedRepos(t *testing.T) {
	eng := loadPolicy(t, PolicyParams{OtherRepos: OtherReposRead})
	runCases(t, eng, []httpCase{
		{
			name: "push", method: "POST", host: "github.com", path: "/majorcontext/moat.git/git-receive-pack",
			wantDeny: true, wantRule: "github-git-write", wantMessageSubstring: "no repositories",
		},
		{
			name: "REST write", method: "POST", host: "api.github.com", path: "/repos/majorcontext/moat/pulls",
			wantDeny: true, wantRule: "github-api-write",
		},
		{
			name: "ID-addressed write", method: "POST", host: "api.github.com", path: "/repositories/1/pulls",
			wantDeny: true, wantRule: "github-api-write",
		},
		{name: "fetch", method: "POST", host: "github.com", path: "/majorcontext/moat.git/git-upload-pack"},
		{name: "read", method: "GET", host: "api.github.com", path: "/repos/majorcontext/moat"},
	})
}

// TestPolicyBodyOnlyForGraphQL guards the gatekeeper contract: only GraphQL
// requests are buffered, so git's non-JSON pack bodies are never inspected.
func TestPolicyBodyOnlyForGraphQL(t *testing.T) {
	eng := loadPolicy(t, testParams(OtherReposRead))
	assert.True(t, eng.RequiresBodyFor(PolicyScope, keeplib.NewHTTPCall("POST", "api.github.com", "/graphql")))
	assert.False(t, eng.RequiresBodyFor(PolicyScope, keeplib.NewHTTPCall("POST", "github.com", "/majorcontext/moat.git/git-receive-pack")))
	assert.False(t, eng.RequiresBodyFor(PolicyScope, keeplib.NewHTTPCall("PUT", "api.github.com", "/repos/majorcontext/moat/contents/x")))
}

func TestPolicyTemplateLoadsAsIs(t *testing.T) {
	require.NoError(t, keeplib.ValidateRuleBytes(PolicyTemplate()))
}

func TestRenderPolicyRejectsBadInput(t *testing.T) {
	_, err := RenderPolicy(PolicyParams{Repos: []string{"not a repo"}})
	require.Error(t, err)
	_, err = RenderPolicy(PolicyParams{NodeIDs: []string{`R_x"`}})
	require.Error(t, err)
	_, err = RenderPolicy(PolicyParams{OtherRepos: "write"})
	require.Error(t, err)

	var many []string
	for i := 0; i < 200; i++ {
		many = append(many, "some-organization/repository-"+strings.Repeat("x", 5)+string(rune('a'+i%26))+strings.Repeat("y", i/26))
	}
	_, err = RenderPolicy(PolicyParams{Repos: many})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "moat github forget")
}

func TestMergeHTTPPolicy(t *testing.T) {
	github, err := RenderPolicy(testParams(OtherReposRead))
	require.NoError(t, err)

	user := []byte(`
scope: http
mode: enforce
defs:
  blocked_paths: '["/admin"]'
rules:
  - name: deny-admin
    match:
      when: "params.host == 'api.example.com' && params.path in blocked_paths"
    action: deny
    message: "admin blocked"
`)
	merged, err := MergeHTTPPolicy(github, user)
	require.NoError(t, err)
	eng, err := keeplib.LoadFromBytes(merged)
	require.NoError(t, err)
	defer eng.Close()

	runCases(t, eng, []httpCase{
		{name: "user rule still applies", method: "GET", host: "api.example.com", path: "/admin", wantDeny: true, wantRule: "deny-admin"},
		{name: "github rule still applies", method: "POST", host: "github.com", path: "/acme/other.git/git-receive-pack", wantDeny: true, wantRule: "github-git-write"},
		{name: "workspace push still allowed", method: "POST", host: "github.com", path: "/majorcontext/moat.git/git-receive-pack"},
		{name: "other host allowed", method: "GET", host: "api.example.com", path: "/ok"},
	})

	for name, bad := range map[string]string{
		"implicit audit mode": "scope: http\nrules:\n  - name: r\n    action: log\n",
		"audit mode":          "scope: http\nmode: audit_only\nrules:\n  - name: r\n    action: log\n",
		"on_error open":       "scope: http\nmode: enforce\non_error: open\nrules:\n  - name: r\n    action: log\n",
		"case sensitive":      "scope: http\nmode: enforce\ncase_sensitive: true\nrules:\n  - name: r\n    action: log\n",
		"rule name clash":     "scope: http\nmode: enforce\nrules:\n  - name: github-git-write\n    action: log\n",
		"def name clash":      "scope: http\nmode: enforce\ndefs:\n  gh_ws_repos: '[]'\nrules:\n  - name: r\n    action: log\n",
		"not a mapping":       "- a\n- b\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := MergeHTTPPolicy(github, []byte(bad))
			assert.Error(t, err)
		})
	}
}

func TestPlanPolicyParamsUsesCachedIdentity(t *testing.T) {
	plan := &Plan{
		OtherRepos: OtherReposRead,
		Granted:    []Candidate{{Repo: "old-owner/app"}, {Repo: "majorcontext/keep"}},
	}
	project := &Project{Repos: map[string]Approval{
		"old-owner/app": {FullName: "New-Owner/App", ID: 5, NodeIDs: []string{"R_a", "MDEw"}},
	}}
	p := PlanPolicyParams(plan, project, nil)
	assert.ElementsMatch(t, []string{"old-owner/app", "new-owner/app", "majorcontext/keep"}, p.Repos)
	assert.Equal(t, []int64{5}, p.RepoIDs)
	assert.ElementsMatch(t, []string{"R_a", "MDEw"}, p.NodeIDs)

	// Fresh lookups take precedence over the cache, and fill uncached repos.
	p = PlanPolicyParams(plan, project, map[string]RepoMeta{
		"old-owner/app":     {FullName: "newest/app", ID: 6, NodeIDs: []string{"R_b"}},
		"majorcontext/keep": {FullName: "majorcontext/keep", ID: 7},
	})
	assert.ElementsMatch(t, []string{"old-owner/app", "newest/app", "majorcontext/keep"}, p.Repos)
	assert.ElementsMatch(t, []int64{6, 7}, p.RepoIDs)
	assert.Equal(t, []string{"R_b"}, p.NodeIDs)

	// Companion: with no stored identity, names alone still populate.
	p = PlanPolicyParams(plan, nil, nil)
	assert.ElementsMatch(t, []string{"old-owner/app", "majorcontext/keep"}, p.Repos)
	assert.Empty(t, p.RepoIDs)
	assert.Empty(t, p.NodeIDs)
}

func TestMergeHTTPPolicyRejectsUnsupportedSettings(t *testing.T) {
	gh, err := RenderPolicy(testParams(OtherReposRead))
	require.NoError(t, err)
	for _, setting := range []string{"typo: true", "version: v2", "scope: other", "packs: []", "profile: mine"} {
		_, err := MergeHTTPPolicy(gh, []byte("mode: enforce\nrules: []\n"+setting+"\n"))
		require.Error(t, err, setting)
	}
	_, err = MergeHTTPPolicy(gh, []byte("version: v1\nscope: http\nmode: enforce\nrules: []\n"))
	require.NoError(t, err)
}
