package run

import (
	"context"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/routing"
	"github.com/majorcontext/moat/internal/storage"
)

func TestCreatePreservesLiveNameWhileServerIsDown(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	routes, err := routing.NewRouteTable(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.Add("project", map[string]string{"web": deadTestAddr(t)}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{routes: routes, runs: map[string]*Run{
		"live": {Name: "project", State: StateRunning},
	}}
	if _, err := m.Create(context.Background(), Options{Name: "project"}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected name collision, got %v", err)
	}
	if !routes.AgentExists("project") {
		t.Fatal("removed live run's route while its service was unavailable")
	}
}

func TestCleanupResourcesPreservesReplacementRoutes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		owner     string
		hostPorts map[string]int
		wantRoute bool
	}{
		{"own registration", "old", map[string]int{"web": 50001}, false},
		{"replacement registration", "new", map[string]int{"web": 50002}, true},
		{"legacy run without bindings", "old", nil, true},
		{"replacement with identical ports", "new", map[string]int{"web": 50001}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MOAT_HOME", t.TempDir())
			routes, err := routing.NewRouteTable(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := routes.AddOwned("project", tc.owner, map[string]string{"web": "127.0.0.1:50001"}); err != nil {
				t.Fatal(err)
			}
			m := &Manager{routes: routes, runtimePool: container.NewRuntimePoolWithDefault(&stubRuntime{})}
			r := &Run{Name: "project", HostPorts: tc.hostPorts, ContainerID: "old"}
			m.cleanupResources(context.Background(), r)
			if got := routes.AgentExists("project"); got != tc.wantRoute {
				t.Fatalf("route exists = %v, want %v", got, tc.wantRoute)
			}
		})
	}
}

func TestHostPortsSurviveMetadataRoundTrip(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), "run_123456789abc")
	if err != nil {
		t.Fatal(err)
	}
	r := &Run{Name: "project", ContainerID: "container", State: StateStopped, HostPorts: map[string]int{"web": 50001}, Store: store}
	if err := r.SaveMetadata(); err != nil {
		t.Fatal(err)
	}
	m := &Manager{runs: make(map[string]*Run)}
	if err := m.loadPersistedRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.runs["run_123456789abc"].HostPorts["web"]; got != 50001 {
		t.Fatalf("persisted host port = %d, want 50001", got)
	}
}

func TestCreatePreservesLiveNameWithoutRoute(t *testing.T) {
	routes, err := routing.NewRouteTable(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{routes: routes, runs: map[string]*Run{"live": {Name: "project", State: StateRunning}}}
	if _, err := m.Create(context.Background(), Options{Name: "project"}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected name collision, got %v", err)
	}
}
