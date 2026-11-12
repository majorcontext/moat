package run

import (
	"context"
	"fmt"
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

func TestCreateReservesCreatedAndStartingNames(t *testing.T) {
	for _, state := range []State{StateCreated, StateStarting} {
		for _, inherited := range []bool{false, true} {
			t.Run(string(state)+fmt.Sprint(inherited), func(t *testing.T) {
				routes, err := routing.NewRouteTable(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				// No runtime pool: these states must never query a runtime or reach Create.
				m := &Manager{routes: routes, runs: map[string]*Run{"existing": {Name: "project", State: state, inherited: inherited}}}
				if _, err := m.Create(context.Background(), Options{Name: "project"}); err == nil || !strings.Contains(err.Error(), "already running") {
					t.Fatalf("expected name reservation, got %v", err)
				}
			})
		}
	}
}

func TestInheritedRunNameReservationChecksRuntime(t *testing.T) {
	for _, tc := range []struct {
		name           string
		containerState string
		inherited      bool
		reserved       bool
	}{
		{"local running", "exited", false, true},
		{"inherited running", "running", true, true},
		{"inherited exited", "exited", true, false},
		{"inherited dead", "dead", true, false},
		{"inherited stopped", "stopped", true, false},
		{"inherited paused", "paused", true, true},
		{"inherited unknown", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &stubRuntime{states: map[string]string{}}
			if tc.containerState != "" {
				rt.states["c1"] = tc.containerState
			}
			m := &Manager{runtimePool: container.NewRuntimePoolWithDefault(rt)}
			r := &Run{ContainerID: "c1", State: StateRunning, inherited: tc.inherited}
			if got := m.runReservesName(context.Background(), r); got != tc.reserved {
				t.Fatalf("reserved = %v, want %v", got, tc.reserved)
			}
		})
	}
}

func TestInheritedNameCheckPreservesCanceledReservation(t *testing.T) {
	rt := &stubRuntime{states: map[string]string{"c1": "exited"}}
	m := &Manager{runtimePool: container.NewRuntimePoolWithDefault(rt)}
	r := &Run{ContainerID: "c1", State: StateRunning, inherited: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !m.runReservesName(ctx, r) {
		t.Fatal("canceled check released name")
	}
}
