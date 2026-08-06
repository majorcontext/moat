package routing

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// deadAddr returns an address nothing is listening on: a listener is opened to
// reserve a port, then closed. Beats hardcoding a port that CI might be using.
func deadAddr(t *testing.T) string {
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

// liveAddr starts a listener that stays open for the duration of the test.
func liveAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	rt, err := NewRouteTable(dir)
	if err != nil {
		t.Fatalf("NewRouteTable: %v", err)
	}
	if err := rt.Add("demo", map[string]string{"web": "127.0.0.1:3000"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// No temp file from the atomic rename may linger (unique routes-*.json.tmp).
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(leftovers) != 0 {
		t.Errorf("temp files should not remain after save: %v", leftovers)
	}
	// And the route must round-trip through a fresh table (reads from disk).
	rt2, _ := NewRouteTable(dir)
	if addr, ok := rt2.Lookup("demo", "web"); !ok || addr != "127.0.0.1:3000" {
		t.Errorf("route not persisted: addr=%q ok=%v", addr, ok)
	}
}

func TestRouteTable(t *testing.T) {
	dir := t.TempDir()
	rt, err := NewRouteTable(dir)
	if err != nil {
		t.Fatalf("NewRouteTable: %v", err)
	}

	// Add routes
	err = rt.Add("myapp", map[string]string{
		"web": "127.0.0.1:49152",
		"api": "127.0.0.1:49153",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Lookup
	addr, ok := rt.Lookup("myapp", "web")
	if !ok {
		t.Fatal("Lookup(myapp, web) not found")
	}
	if addr != "127.0.0.1:49152" {
		t.Errorf("Lookup(myapp, web) = %q, want 127.0.0.1:49152", addr)
	}

	// Remove
	err = rt.Remove("myapp")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, ok = rt.Lookup("myapp", "web")
	if ok {
		t.Error("Lookup after Remove should return false")
	}
}

func TestRouteTablePersistence(t *testing.T) {
	dir := t.TempDir()

	// Create and add routes
	rt1, _ := NewRouteTable(dir)
	rt1.Add("myapp", map[string]string{"web": "127.0.0.1:49152"})

	// Create new instance - should load from file
	rt2, err := NewRouteTable(dir)
	if err != nil {
		t.Fatalf("NewRouteTable: %v", err)
	}

	addr, ok := rt2.Lookup("myapp", "web")
	if !ok {
		t.Fatal("Route not persisted")
	}
	if addr != "127.0.0.1:49152" {
		t.Errorf("Lookup = %q, want 127.0.0.1:49152", addr)
	}
}

func TestRouteTableAgentExists(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)
	rt.Add("myapp", map[string]string{"web": "127.0.0.1:49152"})

	if !rt.AgentExists("myapp") {
		t.Error("AgentExists(myapp) = false, want true")
	}
	if rt.AgentExists("other") {
		t.Error("AgentExists(other) = true, want false")
	}
}

func TestRemoveIfStaleUnreachable(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)

	// Add a route pointing to a port nothing is listening on
	rt.Add("stale-agent", map[string]string{"web": "127.0.0.1:1"})

	if !rt.AgentExists("stale-agent") {
		t.Fatal("precondition: agent should exist")
	}

	removed := rt.RemoveIfStale("stale-agent")
	if !removed {
		t.Error("RemoveIfStale should return true for unreachable endpoint")
	}
	if rt.AgentExists("stale-agent") {
		t.Error("agent should be removed after RemoveIfStale")
	}
}

func TestRemoveIfStaleReachable(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)

	// Start a real listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	rt.Add("live-agent", map[string]string{"web": ln.Addr().String()})

	removed := rt.RemoveIfStale("live-agent")
	if removed {
		t.Error("RemoveIfStale should return false for reachable endpoint")
	}
	if !rt.AgentExists("live-agent") {
		t.Error("agent should still exist after RemoveIfStale")
	}
}

func TestRemoveIfStaleNotRegistered(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)

	removed := rt.RemoveIfStale("nonexistent")
	if removed {
		t.Error("RemoveIfStale should return false for unregistered agent")
	}
}

// --- issue #451: routes.json must survive an empty table ---

func TestRemoveKeepsFileWhenTableEmpties(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)
	if err := rt.Add("only-agent", map[string]string{"web": "127.0.0.1:3000"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := rt.Remove("only-agent"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// The file must remain — deleting it used to take still-running agents'
	// routes with it, orphaning them for the life of their container.
	data, err := os.ReadFile(filepath.Join(dir, "routes.json"))
	if err != nil {
		t.Fatalf("routes.json should survive an empty table: %v", err)
	}
	var routes map[string]map[string]string
	if err := json.Unmarshal(data, &routes); err != nil {
		t.Fatalf("routes.json should stay parseable: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("routes = %v, want empty", routes)
	}
	if agents := rt.Agents(); len(agents) != 0 {
		t.Errorf("Agents() = %v, want none", agents)
	}
}

// Companion to the case above: an agent removed while another is still
// registered must not disturb the survivor, and the survivor must be visible to
// a table instance in another process.
func TestRemoveKeepsOtherAgents(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)
	if err := rt.Add("long-lived", map[string]string{"web": "127.0.0.1:3000"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := rt.Add("short-lived", map[string]string{"web": "127.0.0.1:3001"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := rt.Remove("short-lived"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	other, _ := NewRouteTable(dir)
	if addr, ok := other.Lookup("long-lived", "web"); !ok || addr != "127.0.0.1:3000" {
		t.Errorf("survivor route lost: addr=%q ok=%v", addr, ok)
	}
	if other.AgentExists("short-lived") {
		t.Error("removed agent should be gone")
	}
}

// --- issue #451: concurrent read-modify-write across table instances ---

// Each RouteTable opens its own lock file descriptor, so this exercises the
// same flock path two moat processes take. Without the file lock, a table that
// reloads before another's save lands will write its stale copy back and drop
// the other's entry.
func TestConcurrentAddsDoNotLoseUpdates(t *testing.T) {
	dir := t.TempDir()
	const n = 8

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rt, err := NewRouteTable(dir)
			if err != nil {
				t.Errorf("NewRouteTable: %v", err)
				return
			}
			agent := fmt.Sprintf("agent-%d", i)
			if err := rt.Add(agent, map[string]string{"web": fmt.Sprintf("127.0.0.1:%d", 3000+i)}); err != nil {
				t.Errorf("Add(%s): %v", agent, err)
			}
		}(i)
	}
	wg.Wait()

	final, _ := NewRouteTable(dir)
	got := final.Agents()
	sort.Strings(got)
	if len(got) != n {
		t.Fatalf("Agents() = %v (%d), want %d entries", got, len(got), n)
	}
	for i := 0; i < n; i++ {
		want := fmt.Sprintf("127.0.0.1:%d", 3000+i)
		if addr, ok := final.Lookup(fmt.Sprintf("agent-%d", i), "web"); !ok || addr != want {
			t.Errorf("agent-%d: addr=%q ok=%v, want %q", i, addr, ok, want)
		}
	}
}

// Companion to the concurrent-add case: concurrent removals must delete only
// their own agent and leave every other entry intact.
func TestConcurrentRemovesKeepUntouchedAgents(t *testing.T) {
	dir := t.TempDir()
	const n = 8

	seed, _ := NewRouteTable(dir)
	for i := 0; i < n; i++ {
		if err := seed.Add(fmt.Sprintf("agent-%d", i), map[string]string{"web": fmt.Sprintf("127.0.0.1:%d", 3000+i)}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i += 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rt, err := NewRouteTable(dir)
			if err != nil {
				t.Errorf("NewRouteTable: %v", err)
				return
			}
			if err := rt.Remove(fmt.Sprintf("agent-%d", i)); err != nil {
				t.Errorf("Remove: %v", err)
			}
		}(i)
	}
	wg.Wait()

	final, _ := NewRouteTable(dir)
	for i := 0; i < n; i++ {
		exists := final.AgentExists(fmt.Sprintf("agent-%d", i))
		want := i%2 == 1
		if exists != want {
			t.Errorf("agent-%d exists = %v, want %v", i, exists, want)
		}
	}
}

// --- reload error handling ---

func TestAddAbortsOnUnreadableRoutesFile(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)

	// A directory where routes.json belongs makes ReadFile fail with something
	// other than "not exist" — the class of error that must abort a mutation
	// rather than silently overwrite the file with in-memory state.
	if err := os.Mkdir(filepath.Join(dir, "routes.json"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	if err := rt.Add("agent", map[string]string{"web": "127.0.0.1:3000"}); err == nil {
		t.Error("Add should fail when routes.json cannot be read")
	}
	if err := rt.Remove("agent"); err == nil {
		t.Error("Remove should fail when routes.json cannot be read")
	}
}

// Companion to the unreadable case: a file that exists but cannot be parsed is
// self-healing. No consumer can read it, so it is reset rather than preserved.
func TestAddResetsUnparseableRoutesFile(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)
	if err := os.WriteFile(filepath.Join(dir, "routes.json"), []byte("{ this is not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := rt.Add("agent", map[string]string{"web": "127.0.0.1:3000"}); err != nil {
		t.Fatalf("Add should recover from an unparseable file: %v", err)
	}

	final, _ := NewRouteTable(dir)
	if addr, ok := final.Lookup("agent", "web"); !ok || addr != "127.0.0.1:3000" {
		t.Errorf("route not written: addr=%q ok=%v", addr, ok)
	}
	if agents := final.Agents(); len(agents) != 1 {
		t.Errorf("Agents() = %v, want exactly the new agent", agents)
	}
}

// --- PruneUnreachable ---

func TestPruneUnreachableRemovesOnlyDeadRoutes(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)

	live := liveAddr(t)
	if err := rt.Add("alive", map[string]string{"web": live}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := rt.Add("dead", map[string]string{"web": deadAddr(t)}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Unreachable, but the caller has confirmed its container is running — the
	// service inside may simply not be listening yet.
	if err := rt.Add("starting", map[string]string{"web": deadAddr(t)}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Partially reachable: one live endpoint is enough to keep the agent.
	if err := rt.Add("partial", map[string]string{"web": live, "api": deadAddr(t)}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	pruned := rt.PruneUnreachable(map[string]struct{}{"starting": {}})
	if len(pruned) != 1 || pruned[0] != "dead" {
		t.Fatalf("pruned = %v, want [dead]", pruned)
	}

	for _, agent := range []string{"alive", "starting", "partial"} {
		if !rt.AgentExists(agent) {
			t.Errorf("agent %q should have survived the prune", agent)
		}
	}
	if rt.AgentExists("dead") {
		t.Error("dead agent should have been pruned")
	}
}

// Companion to the case above: when every route is reachable the sweep is a
// no-op and must not rewrite the table.
func TestPruneUnreachableKeepsAllLiveRoutes(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)
	if err := rt.Add("alive", map[string]string{"web": liveAddr(t)}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if pruned := rt.PruneUnreachable(nil); pruned != nil {
		t.Errorf("pruned = %v, want nil", pruned)
	}
	if !rt.AgentExists("alive") {
		t.Error("live agent should survive")
	}
}

func TestPruneUnreachableRemovesAgentWithNoEndpoints(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)
	if err := rt.Add("empty", map[string]string{}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if pruned := rt.PruneUnreachable(nil); len(pruned) != 1 || pruned[0] != "empty" {
		t.Fatalf("pruned = %v, want [empty]", pruned)
	}
}

// A route re-registered while its old endpoints were being probed must not be
// deleted — the new address was never tested.
func TestPruneUnreachableSkipsReregisteredRoutes(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)
	if err := rt.Add("restarted", map[string]string{"web": deadAddr(t)}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	probed := map[string]map[string]string{"restarted": {"web": "127.0.0.1:1"}}
	if removed := rt.removeUnchanged(probed); len(removed) != 0 {
		t.Errorf("removed = %v, want none (endpoints changed since probe)", removed)
	}
	if !rt.AgentExists("restarted") {
		t.Error("re-registered agent should survive")
	}
}
