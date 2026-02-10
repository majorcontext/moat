package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
)

// mockProvider is a minimal CredentialProvider for testing.
type mockProvider struct {
	name string
}

func (m *mockProvider) Name() string                                { return m.name }
func (m *mockProvider) Grant(context.Context) (*Credential, error)  { return nil, nil }
func (m *mockProvider) ConfigureProxy(ProxyConfigurer, *Credential) {}
func (m *mockProvider) ContainerEnv(*Credential) []string           { return nil }
func (m *mockProvider) ContainerMounts(*Credential, string) ([]MountConfig, string, error) {
	return nil, "", nil
}
func (m *mockProvider) Cleanup(string)                {}
func (m *mockProvider) ImpliedDependencies() []string { return nil }

// mockAgentProvider implements both CredentialProvider and AgentProvider.
type mockAgentProvider struct {
	mockProvider
}

func (m *mockAgentProvider) PrepareContainer(context.Context, PrepareOpts) (*ContainerConfig, error) {
	return nil, nil
}
func (m *mockAgentProvider) RegisterCLI(root *cobra.Command) {}

// mockEndpointProvider implements both CredentialProvider and EndpointProvider.
type mockEndpointProvider struct {
	mockProvider
}

func (m *mockEndpointProvider) RegisterEndpoints(mux *http.ServeMux, cred *Credential) {}

func TestRegistry(t *testing.T) {
	Clear() // Start fresh
	defer Clear()

	t.Run("register and get", func(t *testing.T) {
		p := &mockProvider{name: "test"}
		Register(p)

		got := Get("test")
		if got == nil {
			t.Fatal("expected provider, got nil")
		}
		if got.Name() != "test" {
			t.Errorf("expected name 'test', got %q", got.Name())
		}
	})

	t.Run("get unknown returns nil", func(t *testing.T) {
		got := Get("unknown")
		if got != nil {
			t.Errorf("expected nil for unknown provider, got %v", got)
		}
	})

	t.Run("names returns sorted list", func(t *testing.T) {
		Clear()
		Register(&mockProvider{name: "zeta"})
		Register(&mockProvider{name: "alpha"})
		Register(&mockProvider{name: "beta"})

		names := Names()
		if len(names) != 3 {
			t.Fatalf("expected 3 names, got %d", len(names))
		}
		if names[0] != "alpha" || names[1] != "beta" || names[2] != "zeta" {
			t.Errorf("expected sorted names, got %v", names)
		}
	})
}

func TestRegisterAlias(t *testing.T) {
	Clear()
	defer Clear()

	// Register a provider
	Register(&mockProvider{name: "original"})

	// Register an alias
	RegisterAlias("alias", "original")

	// Get by alias should return the same provider
	got := Get("alias")
	if got == nil {
		t.Fatal("expected provider via alias, got nil")
	}
	if got.Name() != "original" {
		t.Errorf("expected name 'original', got %q", got.Name())
	}

	// Alias for non-existent provider should not register
	RegisterAlias("bad-alias", "nonexistent")
	if Get("bad-alias") != nil {
		t.Error("alias to nonexistent provider should return nil")
	}
}

func TestGetAgent(t *testing.T) {
	Clear()
	defer Clear()

	// Register a regular provider (not an agent)
	Register(&mockProvider{name: "regular"})

	// Register an agent provider
	Register(&mockAgentProvider{mockProvider{name: "agent"}})

	// GetAgent for regular provider should return nil
	if GetAgent("regular") != nil {
		t.Error("GetAgent() for non-agent should return nil")
	}

	// GetAgent for agent provider should return the agent
	agent := GetAgent("agent")
	if agent == nil {
		t.Fatal("GetAgent() for agent provider should not return nil")
	}
	if agent.Name() != "agent" {
		t.Errorf("GetAgent() name = %q, want 'agent'", agent.Name())
	}

	// GetAgent for unknown provider should return nil
	if GetAgent("unknown") != nil {
		t.Error("GetAgent() for unknown should return nil")
	}
}

func TestGetEndpoint(t *testing.T) {
	Clear()
	defer Clear()

	// Register a regular provider (not an endpoint)
	Register(&mockProvider{name: "regular"})

	// Register an endpoint provider
	Register(&mockEndpointProvider{mockProvider{name: "endpoint"}})

	// GetEndpoint for regular provider should return nil
	if GetEndpoint("regular") != nil {
		t.Error("GetEndpoint() for non-endpoint should return nil")
	}

	// GetEndpoint for endpoint provider should return the endpoint
	ep := GetEndpoint("endpoint")
	if ep == nil {
		t.Fatal("GetEndpoint() for endpoint provider should not return nil")
	}
	if ep.Name() != "endpoint" {
		t.Errorf("GetEndpoint() name = %q, want 'endpoint'", ep.Name())
	}

	// GetEndpoint for unknown provider should return nil
	if GetEndpoint("unknown") != nil {
		t.Error("GetEndpoint() for unknown should return nil")
	}
}

func TestAll(t *testing.T) {
	Clear()
	defer Clear()

	// Empty registry
	all := All()
	if len(all) != 0 {
		t.Errorf("All() on empty registry = %d, want 0", len(all))
	}

	// Register some providers
	Register(&mockProvider{name: "a"})
	Register(&mockProvider{name: "b"})
	Register(&mockAgentProvider{mockProvider{name: "c"}})

	all = All()
	if len(all) != 3 {
		t.Errorf("All() = %d providers, want 3", len(all))
	}
}

func TestAgents(t *testing.T) {
	Clear()
	defer Clear()

	// Register mix of providers
	Register(&mockProvider{name: "regular1"})
	Register(&mockAgentProvider{mockProvider{name: "agent1"}})
	Register(&mockProvider{name: "regular2"})
	Register(&mockAgentProvider{mockProvider{name: "agent2"}})
	Register(&mockEndpointProvider{mockProvider{name: "endpoint1"}})

	agents := Agents()
	if len(agents) != 2 {
		t.Errorf("Agents() = %d, want 2", len(agents))
	}

	// Verify they're actually agent providers
	for _, a := range agents {
		if a.Name() != "agent1" && a.Name() != "agent2" {
			t.Errorf("unexpected agent: %s", a.Name())
		}
	}
}
