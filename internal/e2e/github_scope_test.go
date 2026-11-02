//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/githubscope"
	"github.com/majorcontext/moat/internal/run"
	"github.com/stretchr/testify/require"
)

func TestGitHubScopeCreateCommit(t *testing.T) {
	skipIfNoDocker(t)
	_, cleanup := setupTestCredential(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	noSandbox := true
	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &noSandbox})
	require.NoError(t, err)
	defer mgr.Close()
	ws := createTestWorkspace(t)
	cfg := &config.Config{GitHub: config.GitHubConfig{Scope: githubscope.ScopeWorkspace}}
	// Bypass YAML validation to force Docker itself to reject container creation
	// after the policy has been rendered and registered with the daemon.
	cfg.Container.Memory = 1
	opts := run.Options{Name: "scope-create-commit", Workspace: ws, Grants: []string{"github"}, Config: cfg, Cmd: []string{"echo", "ok"}}
	_, err = mgr.Create(ctx, opts)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "creating container"), err.Error())
	store, err := run.OpenGitHubScopeStore()
	require.NoError(t, err)
	defer store.Close()
	key, err := githubscope.ProjectKey(ws)
	require.NoError(t, err)
	project, err := store.Get(key)
	require.NoError(t, err)
	require.Nil(t, project, "failed container creation must not lock the project")

	cfg.Container.Memory = 0
	r, err := mgr.Create(ctx, opts)
	require.NoError(t, err)
	defer mgr.Destroy(context.Background(), r.ID)
	project, err = store.Get(key)
	require.NoError(t, err)
	require.NotNil(t, project)
	require.False(t, project.LockedAt.IsZero(), "successful creation commits the scope")
}
