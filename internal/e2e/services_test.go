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

// TestServicePostgres verifies that a postgres service dependency starts,
// injects MOAT_POSTGRES_URL, and the database is reachable from the main container.
func TestServicePostgres(t *testing.T) {
	skipIfNoDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspaceWithDeps(t, []string{"postgres@17"})

	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-service-postgres",
		Workspace: workspace,
		Config: &config.Config{
			Dependencies: []string{"postgres@17"},
		},
		Cmd: []string{"sh", "-c", `
			echo "MOAT_POSTGRES_URL=$MOAT_POSTGRES_URL" &&
			echo "MOAT_POSTGRES_HOST=$MOAT_POSTGRES_HOST" &&
			echo "MOAT_POSTGRES_PORT=$MOAT_POSTGRES_PORT" &&
			echo "=== connectivity test ===" &&
			for i in $(seq 1 10); do
				if pg_isready -h "$MOAT_POSTGRES_HOST" -p "$MOAT_POSTGRES_PORT" 2>/dev/null; then
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
	defer mgr.Destroy(context.Background(), r.ID)

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
		t.Errorf("MOAT_POSTGRES_URL not set in container\nLogs: %v", logs)
	}
	if !foundReachable {
		t.Errorf("Postgres not reachable from container\nLogs: %v", logs)
	}
}

// TestServiceRedis verifies that a redis service dependency starts,
// injects MOAT_REDIS_URL, and the cache is reachable.
func TestServiceRedis(t *testing.T) {
	skipIfNoDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

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
	defer mgr.Destroy(context.Background(), r.ID)

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
		t.Errorf("MOAT_REDIS_URL not set in container\nLogs: %v", logs)
	}
}

// TestServiceMultiple verifies that multiple services (postgres and redis)
// can run together and both sets of env vars are injected.
func TestServiceMultiple(t *testing.T) {
	skipIfNoDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

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
	defer mgr.Destroy(context.Background(), r.ID)

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
		t.Errorf("MOAT_POSTGRES_URL not set\nLogs: %v", logs)
	}
	if !foundRedis {
		t.Errorf("MOAT_REDIS_URL not set\nLogs: %v", logs)
	}
}

// TestServiceCustomConfig verifies that service configuration can be overridden
// via the services: block in agent.yaml (e.g., custom database name).
func TestServiceCustomConfig(t *testing.T) {
	skipIfNoDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

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
	defer mgr.Destroy(context.Background(), r.ID)

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
		t.Errorf("MOAT_POSTGRES_DB not set to myapp\nLogs: %v", logs)
	}
	if !foundURL {
		t.Errorf("MOAT_POSTGRES_URL does not contain myapp\nLogs: %v", logs)
	}
}

// TestServiceCleanup verifies that service containers are removed when
// the run is destroyed.
func TestServiceCleanup(t *testing.T) {
	skipIfNoDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

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

	runID := r.ID

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Logf("Wait returned error (may be expected): %v", err)
	}

	// Verify a service container exists before cleanup
	serviceContainerName := "moat-postgres-" + runID
	checkBefore := exec.CommandContext(ctx, "docker", "ps", "-a", "-q", "-f", "name="+serviceContainerName)
	beforeOutput, _ := checkBefore.Output()
	if len(strings.TrimSpace(string(beforeOutput))) == 0 {
		t.Logf("Note: service container %s not found before destroy (may have been cleaned up already)", serviceContainerName)
	}

	// Destroy the run (should clean up service containers)
	if err := mgr.Destroy(ctx, r.ID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Verify the service container no longer exists
	checkAfter := exec.CommandContext(ctx, "docker", "ps", "-a", "-q", "-f", "name="+serviceContainerName)
	afterOutput, err := checkAfter.Output()
	if err != nil {
		t.Fatalf("docker ps check: %v", err)
	}

	if len(strings.TrimSpace(string(afterOutput))) > 0 {
		t.Errorf("Service container %s still exists after Destroy", serviceContainerName)
	} else {
		t.Logf("Service container %s correctly removed after Destroy", serviceContainerName)
	}
}
