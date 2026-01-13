package routing

import (
	"testing"
)

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

	// Lookup default (first service)
	_, ok = rt.LookupDefault("myapp")
	if !ok {
		t.Fatal("LookupDefault(myapp) not found")
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
