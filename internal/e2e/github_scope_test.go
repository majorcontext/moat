//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestAuditFailureCleansOnlyRunBuildkitResources(t *testing.T) {
	skipIfNoDocker(t)
	skipIfNoPrivileged(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	shared := fmt.Sprintf("moat-review-shared-%d", time.Now().UnixNano())
	output, err := exec.CommandContext(ctx, "docker", "network", "create", shared).CombinedOutput()
	require.NoError(t, err, string(output))
	defer exec.Command("docker", "network", "rm", shared).Run()
	noSandbox := true
	manager, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &noSandbox})
	require.NoError(t, err)
	defer manager.Close()
	var runID string
	t.Cleanup(func() {
		if runID != "" {
			_ = exec.Command("docker", "rm", "-f", "moat-buildkit-"+runID, runID).Run()
			_ = exec.Command("docker", "network", "rm", "moat-"+runID).Run()
		}
	})
	since := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	_, err = manager.Create(ctx, run.Options{
		Name: "audit-cleanup", Workspace: createTestWorkspace(t), Cmd: []string{"true"},
		Config: &config.Config{Dependencies: []string{"docker:dind"}},
		OnRunIDAssigned: func(id, _ string) {
			runID = id
			// Force sqlite open to fail only after the main container and sidecar exist.
			require.NoError(t, os.MkdirAll(filepath.Join(config.GlobalConfigDir(), "runs", id, "audit.db"), 0o700))
		},
	})
	require.ErrorContains(t, err, "opening audit store")
	require.NotEmpty(t, runID)
	// Observe creation events, so the absence checks below cannot pass merely
	// because a reordered failure happened before resource allocation.
	until := time.Now().UTC().Format(time.RFC3339Nano)
	for _, resource := range []struct{ kind, name string }{{"container", "moat-buildkit-" + runID}, {"network", "moat-" + runID}} {
		events, eventErr := exec.CommandContext(ctx, "docker", "events", "--since", since, "--until", until, "--filter", "type="+resource.kind, "--filter", resource.kind+"="+resource.name, "--filter", "event=create").CombinedOutput()
		require.NoError(t, eventErr, string(events))
		require.NotEmpty(t, strings.TrimSpace(string(events)), "must observe %s creation before asserting cleanup", resource.name)
	}
	output, err = exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", "name=^moat-buildkit-"+runID+"$").CombinedOutput()
	require.NoError(t, err, string(output))
	require.Empty(t, strings.TrimSpace(string(output)), "the run's BuildKit sidecar must be removed")
	require.Error(t, exec.CommandContext(ctx, "docker", "network", "inspect", "moat-"+runID).Run(), "the run's network must be removed")
	require.NoError(t, exec.CommandContext(ctx, "docker", "network", "inspect", shared).Run(), "unrelated networks must survive")
	require.NoDirExists(t, filepath.Join(config.GlobalConfigDir(), "runs", runID))
}
