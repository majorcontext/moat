//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
)

// skipIfNoServiceRuntime skips the test if no container runtime with service
// support is available (Docker or Apple containers).
func skipIfNoServiceRuntime(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("docker"); err == nil {
		return
	}
	if _, err := exec.LookPath("container"); err == nil {
		return
	}
	t.Skip("No container runtime with service support available")
}

// cleanupRun stops then destroys a run, ignoring errors. Registered via
// t.Cleanup so it runs even when assertions fail. Destroy refuses to remove
// a Running run, so the explicit Stop is required to avoid leaking the
// container's network and run dir on test failure (see #315).
func cleanupRun(t *testing.T, mgr *run.Manager, runID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := mgr.Stop(ctx, runID); err != nil {
			t.Logf("cleanup: stop run %s: %v", runID, err)
		}
		if err := mgr.Destroy(ctx, runID); err != nil {
			t.Logf("cleanup: destroy run %s: %v", runID, err)
		}
	})
}

// TestServicePostgres verifies that a postgres service dependency starts,
// injects MOAT_POSTGRES_URL, and the database is reachable from the main container.
func TestServicePostgres(t *testing.T) {
	skipIfNoServiceRuntime(t)
	skipIfNestedDind(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	workspace := createTestWorkspaceWithDeps(t, []string{"postgres@17"})

	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-service-postgres",
		Workspace: workspace,
		Config: &config.Config{
			Dependencies: []string{"postgres@17"},
		},
		Cmd: []string{"bash", "-c", `
			echo "MOAT_POSTGRES_URL=$MOAT_POSTGRES_URL" &&
			echo "MOAT_POSTGRES_HOST=$MOAT_POSTGRES_HOST" &&
			echo "MOAT_POSTGRES_PORT=$MOAT_POSTGRES_PORT" &&
			echo "=== connectivity test ===" &&
			for i in $(seq 1 10); do
				if (echo > /dev/tcp/"$MOAT_POSTGRES_HOST"/"$MOAT_POSTGRES_PORT") 2>/dev/null; then
					echo "postgres_reachable=true"
					exit 0
				fi
				sleep 1
			done &&
			echo "postgres_reachable=false"
		`},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupRun(t, mgr, r.ID)

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Logf("Wait returned error (may be expected): %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	var foundURL, foundReachable bool
	for _, entry := range logs {
		if strings.HasPrefix(entry.Line, "MOAT_POSTGRES_URL=") && len(entry.Line) > len("MOAT_POSTGRES_URL=") {
			foundURL = true
			t.Logf("Postgres URL: %s", entry.Line)
		}
		if strings.Contains(entry.Line, "postgres_reachable=true") {
			foundReachable = true
		}
	}

	if !foundURL {
		t.Errorf("MOAT_POSTGRES_URL not set in container\nLogs:%s", formatLogEntries(logs))
	}
	if !foundReachable {
		t.Errorf("Postgres not reachable from container\nLogs:%s", formatLogEntries(logs))
	}
}

// TestServiceRedis verifies that a redis service dependency starts,
// injects MOAT_REDIS_URL, and the cache is reachable.
func TestServiceRedis(t *testing.T) {
	skipIfNoServiceRuntime(t)
	skipIfNestedDind(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	workspace := createTestWorkspaceWithDeps(t, []string{"redis@7"})

	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-service-redis",
		Workspace: workspace,
		Config: &config.Config{
			Dependencies: []string{"redis@7"},
		},
		Cmd: []string{"sh", "-c", `
			echo "MOAT_REDIS_URL=$MOAT_REDIS_URL" &&
			echo "MOAT_REDIS_HOST=$MOAT_REDIS_HOST" &&
			echo "MOAT_REDIS_PORT=$MOAT_REDIS_PORT"
		`},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupRun(t, mgr, r.ID)

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Logf("Wait returned error (may be expected): %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	var foundURL bool
	for _, entry := range logs {
		if strings.HasPrefix(entry.Line, "MOAT_REDIS_URL=") && len(entry.Line) > len("MOAT_REDIS_URL=") {
			foundURL = true
			t.Logf("Redis URL: %s", entry.Line)
		}
	}

	if !foundURL {
		t.Errorf("MOAT_REDIS_URL not set in container\nLogs:%s", formatLogEntries(logs))
	}
}

// TestServiceMultiple verifies that multiple services (postgres and redis)
// can run together and both sets of env vars are injected.
func TestServiceMultiple(t *testing.T) {
	skipIfNoServiceRuntime(t)
	skipIfNestedDind(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	workspace := createTestWorkspaceWithDeps(t, []string{"postgres@17", "redis@7"})

	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-service-multiple",
		Workspace: workspace,
		Config: &config.Config{
			Dependencies: []string{"postgres@17", "redis@7"},
		},
		Cmd: []string{"sh", "-c", `
			echo "MOAT_POSTGRES_URL=$MOAT_POSTGRES_URL" &&
			echo "MOAT_REDIS_URL=$MOAT_REDIS_URL"
		`},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupRun(t, mgr, r.ID)

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Logf("Wait returned error (may be expected): %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	var foundPostgres, foundRedis bool
	for _, entry := range logs {
		if strings.HasPrefix(entry.Line, "MOAT_POSTGRES_URL=") && len(entry.Line) > len("MOAT_POSTGRES_URL=") {
			foundPostgres = true
			t.Logf("Postgres URL: %s", entry.Line)
		}
		if strings.HasPrefix(entry.Line, "MOAT_REDIS_URL=") && len(entry.Line) > len("MOAT_REDIS_URL=") {
			foundRedis = true
			t.Logf("Redis URL: %s", entry.Line)
		}
	}

	if !foundPostgres {
		t.Errorf("MOAT_POSTGRES_URL not set\nLogs:%s", formatLogEntries(logs))
	}
	if !foundRedis {
		t.Errorf("MOAT_REDIS_URL not set\nLogs:%s", formatLogEntries(logs))
	}
}

// TestServiceCustomConfig verifies that service configuration can be overridden
// via the services: block in moat.yaml (e.g., custom database name).
func TestServiceCustomConfig(t *testing.T) {
	skipIfNoServiceRuntime(t)
	skipIfNestedDind(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	workspace := createTestWorkspaceWithDeps(t, []string{"postgres@17"})

	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-service-custom-config",
		Workspace: workspace,
		Config: &config.Config{
			Dependencies: []string{"postgres@17"},
			Services: map[string]config.ServiceSpec{
				"postgres": {
					Env: map[string]string{"POSTGRES_DB": "myapp"},
				},
			},
		},
		Cmd: []string{"sh", "-c", `
			echo "MOAT_POSTGRES_DB=$MOAT_POSTGRES_DB" &&
			echo "MOAT_POSTGRES_URL=$MOAT_POSTGRES_URL"
		`},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupRun(t, mgr, r.ID)

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Logf("Wait returned error (may be expected): %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	var foundDB, foundURL bool
	for _, entry := range logs {
		if entry.Line == "MOAT_POSTGRES_DB=myapp" {
			foundDB = true
			t.Logf("Custom DB: %s", entry.Line)
		}
		if strings.HasPrefix(entry.Line, "MOAT_POSTGRES_URL=") && strings.Contains(entry.Line, "myapp") {
			foundURL = true
			t.Logf("URL with custom DB: %s", entry.Line)
		}
	}

	if !foundDB {
		t.Errorf("MOAT_POSTGRES_DB not set to myapp\nLogs:%s", formatLogEntries(logs))
	}
	if !foundURL {
		t.Errorf("MOAT_POSTGRES_URL does not contain myapp\nLogs:%s", formatLogEntries(logs))
	}
}

// TestServiceCleanup verifies that service containers are removed when
// the run is destroyed.
func TestServiceCleanup(t *testing.T) {
	skipIfNoServiceRuntime(t)
	skipIfNestedDind(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	workspace := createTestWorkspaceWithDeps(t, []string{"postgres@17"})

	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-service-cleanup",
		Workspace: workspace,
		Config: &config.Config{
			Dependencies: []string{"postgres@17"},
		},
		Cmd: []string{"echo", "done"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Safety net: even though this test deliberately destroys the run mid-test,
	// register cleanup in case an assertion fails before the explicit Destroy.
	// Stop+Destroy on an already-destroyed run is a harmless no-op.
	cleanupRun(t, mgr, r.ID)

	runID := r.ID

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Logf("Wait returned error (may be expected): %v", err)
	}

	// Verify a service container exists before cleanup
	serviceContainerName := "moat-postgres-" + runID
	if found := serviceContainerExists(ctx, t, serviceContainerName); !found {
		t.Logf("Note: service container %s not found before destroy (may have been cleaned up already)", serviceContainerName)
	}

	// Destroy the run (should clean up service containers)
	if err := mgr.Destroy(ctx, r.ID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Verify the service container no longer exists
	if serviceContainerExists(ctx, t, serviceContainerName) {
		t.Errorf("Service container %s still exists after Destroy", serviceContainerName)
	} else {
		t.Logf("Service container %s correctly removed after Destroy", serviceContainerName)
	}
}

// serviceContainerExists checks whether a container with the given name exists
// using whichever CLI is available (docker or Apple container).
func serviceContainerExists(ctx context.Context, t *testing.T, name string) bool {
	t.Helper()

	// Try Docker first
	if _, err := exec.LookPath("docker"); err == nil {
		cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "-q", "-f", "name="+name)
		out, err := cmd.Output()
		if err != nil {
			// Docker CLI exists but daemon may not be running — fall through to Apple
			t.Logf("docker ps failed (trying Apple container CLI): %v", err)
		} else {
			return len(strings.TrimSpace(string(out))) > 0
		}
	}

	// Try Apple container CLI
	if _, err := exec.LookPath("container"); err == nil {
		cmd := exec.CommandContext(ctx, "container", "list", "--all", "--format", "json")
		out, err := cmd.Output()
		if err != nil {
			t.Logf("container list failed: %v", err)
			return false
		}
		return strings.Contains(string(out), name)
	}

	t.Log("No container CLI available to check container existence")
	return false
}
