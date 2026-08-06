package run

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/routing"
	"github.com/majorcontext/moat/internal/storage"
)

// portStubRuntime adds port-binding lookup to stubRuntime, which panics on it.
// bindings maps container ID -> container port -> host port; bindErr, when set,
// makes every lookup fail.
type portStubRuntime struct {
	*stubRuntime
	bindings map[string]map[int]int
	bindErr  error
}

func (p *portStubRuntime) GetPortBindings(_ context.Context, id string) (map[int]int, error) {
	if p.bindErr != nil {
		return nil, p.bindErr
	}
	b, ok := p.bindings[id]
	if !ok {
		return nil, fmt.Errorf("no bindings for %q", id)
	}
	return b, nil
}

// reconcileFixture builds a Manager wired to a temp route table and the given
// runtime, and persists the supplied run metadata to an isolated MOAT_HOME —
// the reconciler reads candidates from disk, not from the manager's in-memory
// map.
func reconcileFixture(t *testing.T, rt container.Runtime, runs ...storage.Metadata) (*Manager, *routing.RouteTable) {
	t.Helper()
	t.Setenv("MOAT_HOME", t.TempDir())

	baseDir := storage.DefaultBaseDir()
	for i, meta := range runs {
		store, err := storage.NewRunStore(baseDir, fmt.Sprintf("run_%012d", i))
		if err != nil {
			t.Fatalf("NewRunStore: %v", err)
		}
		if err := store.SaveMetadata(meta); err != nil {
			t.Fatalf("SaveMetadata: %v", err)
		}
	}

	routes, err := routing.NewRouteTable(t.TempDir())
	if err != nil {
		t.Fatalf("NewRouteTable: %v", err)
	}
	return &Manager{
		runtimePool: container.NewRuntimePoolWithDefault(rt),
		routes:      routes,
		runs:        make(map[string]*Run),
	}, routes
}

// deadTestAddr returns an address nothing is listening on.
func deadTestAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return addr
}

// TestReconcileRoutesReRegistersRunningRun covers the core of issue #451: a
// container that is still up but has no entry in routes.json gets one back.
func TestReconcileRoutesReRegistersRunningRun(t *testing.T) {
	rt := &portStubRuntime{
		stubRuntime: &stubRuntime{states: map[string]string{"c1": "running"}},
		bindings:    map[string]map[int]int{"c1": {4321: 53624}},
	}
	m, routes := reconcileFixture(t, rt, storage.Metadata{
		Name:        "long-lived",
		ContainerID: "c1",
		State:       string(StateRunning),
		Ports:       map[string]int{"www": 4321},
	})

	m.reconcileRoutes(context.Background())

	addr, ok := routes.Lookup("long-lived", "www")
	if !ok || addr != "127.0.0.1:53624" {
		t.Errorf("route not rebuilt: addr=%q ok=%v", addr, ok)
	}
}

// Companion to the case above: a run whose container is confirmed gone has its
// leftover, unreachable route pruned instead of rebuilt.
func TestReconcileRoutesPrunesStoppedRunRoute(t *testing.T) {
	rt := &portStubRuntime{
		stubRuntime: &stubRuntime{states: map[string]string{"c1": "exited"}},
		bindings:    map[string]map[int]int{},
	}
	m, routes := reconcileFixture(t, rt, storage.Metadata{
		Name:        "gone",
		ContainerID: "c1",
		State:       string(StateRunning), // persisted state is stale; the runtime is authoritative
		Ports:       map[string]int{"www": 4321},
	})
	if err := routes.Add("gone", map[string]string{"www": deadTestAddr(t)}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	m.reconcileRoutes(context.Background())

	if routes.AgentExists("gone") {
		t.Error("route for a stopped container should have been pruned")
	}
}

// A container that is running but whose service has not started listening yet
// must keep its route — an unreachable probe is not evidence the agent is gone.
func TestReconcileRoutesKeepsRunningRunThatIsNotListening(t *testing.T) {
	rt := &portStubRuntime{
		stubRuntime: &stubRuntime{states: map[string]string{"c1": "running"}},
		bindings:    map[string]map[int]int{"c1": {4321: 53624}},
	}
	m, routes := reconcileFixture(t, rt, storage.Metadata{
		Name:        "starting",
		ContainerID: "c1",
		State:       string(StateRunning),
		Ports:       map[string]int{"www": 4321},
	})

	// 53624 has nothing listening on it in this test, so the rebuilt route is
	// unreachable — and must survive the prune sweep anyway.
	m.reconcileRoutes(context.Background())

	if !routes.AgentExists("starting") {
		t.Error("route for a running container should survive an unreachable probe")
	}
}

// When the container state cannot be read at all, the existing route is left
// alone rather than pruned on an unconfirmed check.
func TestReconcileRoutesPreservesUnverifiableRun(t *testing.T) {
	// stubRuntime returns an error for container IDs it does not know.
	rt := &portStubRuntime{
		stubRuntime: &stubRuntime{states: map[string]string{}},
		bindings:    map[string]map[int]int{},
	}
	m, routes := reconcileFixture(t, rt, storage.Metadata{
		Name:        "unverifiable",
		ContainerID: "c1",
		State:       string(StateRunning),
		Ports:       map[string]int{"www": 4321},
	})
	if err := routes.Add("unverifiable", map[string]string{"www": deadTestAddr(t)}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	m.reconcileRoutes(context.Background())

	if !routes.AgentExists("unverifiable") {
		t.Error("route should survive when the container state is unknown")
	}
}

// A running container whose port bindings cannot be read keeps whatever route
// it already had, rather than losing it to a failed lookup.
func TestReconcileRoutesPreservesRouteWhenBindingLookupFails(t *testing.T) {
	rt := &portStubRuntime{
		stubRuntime: &stubRuntime{states: map[string]string{"c1": "running"}},
		bindErr:     fmt.Errorf("runtime hiccup"),
	}
	m, routes := reconcileFixture(t, rt, storage.Metadata{
		Name:        "binding-error",
		ContainerID: "c1",
		State:       string(StateRunning),
		Ports:       map[string]int{"www": 4321},
	})
	stale := deadTestAddr(t)
	if err := routes.Add("binding-error", map[string]string{"www": stale}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	m.reconcileRoutes(context.Background())

	if addr, ok := routes.Lookup("binding-error", "www"); !ok || addr != stale {
		t.Errorf("route should be untouched: addr=%q ok=%v, want %q", addr, ok, stale)
	}
}

// Routes with no corresponding run — hand-added entries, or runs from another
// credential profile — are judged purely on reachability.
func TestReconcileRoutesJudgesForeignRoutesByReachability(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	rt := &portStubRuntime{stubRuntime: &stubRuntime{states: map[string]string{}}}
	m, routes := reconcileFixture(t, rt)
	if err := routes.Add("foreign-live", map[string]string{"www": ln.Addr().String()}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := routes.Add("foreign-dead", map[string]string{"www": deadTestAddr(t)}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	m.reconcileRoutes(context.Background())

	if !routes.AgentExists("foreign-live") {
		t.Error("a foreign route that is serving traffic must be preserved")
	}
	if routes.AgentExists("foreign-dead") {
		t.Error("a foreign route that is unreachable should be pruned")
	}
}

// Runs without published ports are not candidates and must not be probed or
// registered.
func TestReconcileRoutesSkipsRunsWithoutPorts(t *testing.T) {
	rt := &portStubRuntime{
		// GetPortBindings would fail for c1; reaching it at all is the bug.
		stubRuntime: &stubRuntime{states: map[string]string{"c1": "running"}},
	}
	m, routes := reconcileFixture(t, rt, storage.Metadata{
		Name:        "portless",
		ContainerID: "c1",
		State:       string(StateRunning),
	})

	m.reconcileRoutes(context.Background())

	if routes.AgentExists("portless") {
		t.Error("a run with no published ports should not get a route")
	}
}

// A run persisted in a terminal state is not a candidate at all: its route is
// judged purely on reachability, like any other orphan.
func TestReconcileRoutesSkipsTerminalRuns(t *testing.T) {
	rt := &portStubRuntime{
		// Reaching the runtime for a stopped run at all is the bug.
		stubRuntime: &stubRuntime{states: map[string]string{"c1": "running"}},
	}
	m, routes := reconcileFixture(t, rt, storage.Metadata{
		Name:        "finished",
		ContainerID: "c1",
		State:       string(StateStopped),
		Ports:       map[string]int{"www": 4321},
	})
	if err := routes.Add("finished", map[string]string{"www": deadTestAddr(t)}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	m.reconcileRoutes(context.Background())

	if routes.AgentExists("finished") {
		t.Error("an unreachable route for a terminal run should be pruned")
	}
}
