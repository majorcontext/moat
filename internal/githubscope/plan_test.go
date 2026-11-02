package githubscope

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), StoreFileName))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func candidates(plan *Plan, pending bool) []string {
	list := plan.Granted
	if pending {
		list = plan.Pending
	}
	out := make([]string, 0, len(list))
	for _, c := range list {
		out = append(out, c.Repo)
	}
	return out
}

func buildPlan(t *testing.T, store *Store, ws string, s Settings) *Plan {
	t.Helper()
	key, err := ProjectKey(ws)
	require.NoError(t, err)
	existing, err := store.Get(key)
	require.NoError(t, err)
	plan, err := BuildPlan(ws, s, existing)
	require.NoError(t, err)
	return plan
}

var workspaceScope = Settings{Scope: ScopeWorkspace}

func TestPlanDisabled(t *testing.T) {
	store := openTestStore(t)
	ws := t.TempDir()
	writeRepo(t, ws, map[string]string{"origin": "https://github.com/majorcontext/moat"})

	for _, s := range []Settings{{}, {Scope: ScopeAny}} {
		plan := buildPlan(t, store, ws, s)
		assert.False(t, plan.Enabled)
		assert.Empty(t, plan.Granted)
	}
}

// TestPlanFirstRunThenNewRepo is the core cross-run scenario: the first run
// trusts what it finds; a repository that appears afterwards (cloned or
// re-pointed by an agent) is pending, not granted.
func TestPlanFirstRunThenNewRepo(t *testing.T) {
	store := openTestStore(t)
	ws := t.TempDir()
	writeRepo(t, ws, map[string]string{"origin": "https://github.com/majorcontext/moat"})
	writeRepo(t, filepath.Join(ws, "keep"), map[string]string{"origin": "https://github.com/majorcontext/keep"})

	plan := buildPlan(t, store, ws, workspaceScope)
	require.True(t, plan.Enabled)
	assert.True(t, plan.FirstRun)
	assert.False(t, plan.Sticky)
	assert.Equal(t, OtherReposRead, plan.OtherRepos)
	assert.Equal(t, []string{"majorcontext/keep", "majorcontext/moat"}, candidates(plan, false))
	assert.Empty(t, plan.Pending)

	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	require.NoError(t, RecordFirstRun(store, plan, now))
	proj, err := store.Get(plan.Project)
	require.NoError(t, err)
	require.NotNil(t, proj)
	assert.Equal(t, now, proj.LockedAt)
	assert.Equal(t, []string{"majorcontext/keep", "majorcontext/moat"}, proj.RepoNames())
	assert.Equal(t, "./ (origin)", proj.Repos["majorcontext/moat"].Source)

	// Between runs, the agent clones another repository and re-points a remote.
	writeRepo(t, filepath.Join(ws, "scratch"), map[string]string{"origin": "https://github.com/acme/payments"})
	writeRepo(t, filepath.Join(ws, "keep"), map[string]string{"origin": "https://github.com/acme/keep-fork"})

	plan = buildPlan(t, store, ws, workspaceScope)
	assert.False(t, plan.FirstRun)
	assert.Equal(t, []string{"majorcontext/moat"}, candidates(plan, false))
	assert.Equal(t, []string{"acme/keep-fork", "acme/payments"}, candidates(plan, true))
	assert.Equal(t, []string{"majorcontext/keep"}, plan.Missing)

	// RecordFirstRun is a no-op after the first run.
	require.NoError(t, RecordFirstRun(store, plan, now.Add(time.Hour)))
	proj, _ = store.Get(plan.Project)
	assert.Equal(t, []string{"majorcontext/keep", "majorcontext/moat"}, proj.RepoNames())

	// The user approves one of them.
	require.NoError(t, Approve(store, plan.Project, []Candidate{plan.Pending[1]}, now))
	plan = buildPlan(t, store, ws, workspaceScope)
	assert.Equal(t, []string{"acme/payments", "majorcontext/moat"}, candidates(plan, false))
	assert.Equal(t, []string{"acme/keep-fork"}, candidates(plan, true))
}

// TestPlanStickyLock covers D3: once locked, removing github.scope from
// moat.yaml (something an agent can do) keeps the scope on.
func TestPlanStickyLock(t *testing.T) {
	store := openTestStore(t)
	ws := t.TempDir()
	writeRepo(t, ws, map[string]string{"origin": "https://github.com/majorcontext/moat"})

	first := buildPlan(t, store, ws, Settings{Scope: ScopeWorkspace, OtherRepos: OtherReposNone, ExtraRepos: []string{"majorcontext/keep"}})
	assert.Equal(t, []string{"majorcontext/keep", "majorcontext/moat"}, candidates(first, false))
	require.NoError(t, RecordFirstRun(store, first, time.Now()))

	for _, s := range []Settings{{}, {Scope: ScopeAny}} {
		plan := buildPlan(t, store, ws, s)
		assert.True(t, plan.Enabled, "locked project stays scoped")
		assert.True(t, plan.Sticky)
		assert.Equal(t, OtherReposRead, plan.OtherRepos)
		// extra_repos are not honored once moat.yaml no longer asks for the scope.
		assert.Equal(t, []string{"majorcontext/moat"}, candidates(plan, false))
	}

	// Companion: still asking for the scope is not sticky.
	plan := buildPlan(t, store, ws, workspaceScope)
	assert.True(t, plan.Enabled)
	assert.False(t, plan.Sticky)

	// Reset removes the lock.
	existed, err := Reset(store, plan.Project)
	require.NoError(t, err)
	assert.True(t, existed)
	plan = buildPlan(t, store, ws, Settings{})
	assert.False(t, plan.Enabled)
}

func TestPlanExtraReposPendingAfterFirstRun(t *testing.T) {
	store := openTestStore(t)
	ws := t.TempDir()
	writeRepo(t, ws, map[string]string{"origin": "https://github.com/majorcontext/moat"})

	plan := buildPlan(t, store, ws, workspaceScope)
	require.NoError(t, RecordFirstRun(store, plan, time.Now()))

	// An extra_repos entry added to moat.yaml later must be approved.
	plan = buildPlan(t, store, ws, Settings{Scope: ScopeWorkspace, ExtraRepos: []string{"acme/other"}})
	assert.Equal(t, []string{"majorcontext/moat"}, candidates(plan, false))
	require.Len(t, plan.Pending, 1)
	assert.Equal(t, []string{SourceExtraRepos}, plan.Pending[0].Sources)
}

// TestApproveBeforeFirstRun: pre-approving a repository with `moat github
// allow` must not lock the project, or the workspace's own repositories
// would become pending on the first run.
func TestApproveBeforeFirstRun(t *testing.T) {
	store := openTestStore(t)
	ws := t.TempDir()
	writeRepo(t, ws, map[string]string{"origin": "https://github.com/majorcontext/moat"})
	key, err := ProjectKey(ws)
	require.NoError(t, err)

	require.NoError(t, Approve(store, key, []Candidate{{Repo: "majorcontext/keep", Sources: []string{SourceAllowCommand}}}, time.Now()))

	plan := buildPlan(t, store, ws, workspaceScope)
	assert.True(t, plan.FirstRun)
	assert.Equal(t, []string{"majorcontext/keep", "majorcontext/moat"}, candidates(plan, false))

	// Approving does not enable the scope on its own.
	plan = buildPlan(t, store, ws, Settings{})
	assert.False(t, plan.Enabled)
}

// TestAllowedReposGrantedWithoutCheckout: a repository approved with `moat
// github allow` is granted even when no checkout of it exists, while one
// approved from discovery is granted only while present.
func TestAllowedReposGrantedWithoutCheckout(t *testing.T) {
	store := openTestStore(t)
	ws := t.TempDir()
	writeRepo(t, ws, map[string]string{"origin": "https://github.com/majorcontext/moat"})
	writeRepo(t, filepath.Join(ws, "lib"), map[string]string{"origin": "https://github.com/acme/lib"})

	plan := buildPlan(t, store, ws, workspaceScope)
	require.NoError(t, RecordFirstRun(store, plan, time.Now()))
	require.NoError(t, Approve(store, plan.Project, []Candidate{{Repo: "acme/docs", Sources: []string{SourceAllowCommand}}}, time.Now()))

	require.NoError(t, os.RemoveAll(filepath.Join(ws, "lib")))
	plan = buildPlan(t, store, ws, workspaceScope)
	assert.Equal(t, []string{"acme/docs", "majorcontext/moat"}, candidates(plan, false))
	assert.Equal(t, []string{"acme/lib"}, plan.Missing)

	notFound, err := Forget(store, plan.Project, []string{"acme/docs", "acme/nope"})
	require.NoError(t, err)
	assert.Equal(t, []string{"acme/nope"}, notFound)
	plan = buildPlan(t, store, ws, workspaceScope)
	assert.Equal(t, []string{"majorcontext/moat"}, candidates(plan, false))
	assert.True(t, plan.Enabled, "forgetting repositories keeps the lock")
}

func TestPlanNoRepositories(t *testing.T) {
	store := openTestStore(t)
	ws := t.TempDir()
	plan := buildPlan(t, store, ws, workspaceScope)
	require.True(t, plan.Enabled)
	assert.Empty(t, plan.Granted)
	require.NoError(t, RecordFirstRun(store, plan, time.Now()))

	// The project is locked even with nothing approved, so a repository that
	// shows up later is pending rather than trusted as a first run.
	writeRepo(t, ws, map[string]string{"origin": "https://github.com/acme/late"})
	plan = buildPlan(t, store, ws, workspaceScope)
	assert.False(t, plan.FirstRun)
	assert.Equal(t, []string{"acme/late"}, candidates(plan, true))
}

func TestProjectKeyResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	a, err := ProjectKey(real)
	require.NoError(t, err)
	b, err := ProjectKey(link)
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestFindProject(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "moat.yaml"), []byte("agent: claude\n"), 0o644))
	sub := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	want, err := ProjectKey(root)
	require.NoError(t, err)
	got, err := FindProject(sub)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	// Companion: with no moat.yaml above, the directory itself is the project.
	bare := t.TempDir()
	want, err = ProjectKey(bare)
	require.NoError(t, err)
	got, err = FindProject(bare)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestStoreRejectsMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), StoreFileName)
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
	_, err := OpenStore(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete it to re-approve")
}

func TestStoreConcurrentApprovals(t *testing.T) {
	path := filepath.Join(t.TempDir(), StoreFileName)
	var wg sync.WaitGroup
	repos := []string{"a/one", "a/two", "a/three", "a/four", "a/five", "a/six"}
	for _, r := range repos {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			s, err := OpenStore(path)
			if !assert.NoError(t, err) {
				return
			}
			defer s.Close()
			assert.NoError(t, Approve(s, "/proj", []Candidate{{Repo: r}}, time.Now()))
		}(r)
	}
	wg.Wait()

	s, err := OpenStore(path)
	require.NoError(t, err)
	defer s.Close()
	p, err := s.Get("/proj")
	require.NoError(t, err)
	assert.Len(t, p.Repos, len(repos), "no approval may be lost to a concurrent write")
}

func TestSetRepoMeta(t *testing.T) {
	store := openTestStore(t)
	require.NoError(t, Approve(store, "/proj", []Candidate{{Repo: "a/b"}}, time.Now()))
	require.NoError(t, SetRepoMeta(store, "/proj", map[string]RepoMeta{
		"a/b":       {FullName: "a/b", ID: 7, NodeIDs: []string{"R_x"}},
		"not/there": {ID: 9},
	}))
	p, err := store.Get("/proj")
	require.NoError(t, err)
	assert.Equal(t, int64(7), p.Repos["a/b"].ID)
	assert.Len(t, p.Repos, 1, "metadata never adds approvals")
}
