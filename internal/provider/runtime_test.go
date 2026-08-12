package provider_test

import (
	"testing"

	"github.com/majorcontext/moat/internal/provider"
	_ "github.com/majorcontext/moat/internal/providers/claude"
	_ "github.com/majorcontext/moat/internal/providers/codex"
	_ "github.com/majorcontext/moat/internal/providers/copilot"
	_ "github.com/majorcontext/moat/internal/providers/gemini"
)

func TestAgentRuntimeCredentialGrantIsStatic(t *testing.T) {
	tests := []struct {
		agent string
		want  string
	}{
		{"claude", "claude"},
		{"codex", "openai"}, // NOT "codex" — the credential lives under openai
		{"copilot", "github"},
		{"gemini", "gemini"},
	}
	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			a := provider.GetAgent(tt.agent)
			if a == nil {
				t.Fatalf("agent %q not registered", tt.agent)
			}
			rt, ok := a.(provider.AgentRuntime)
			if !ok {
				t.Fatalf("agent %q should implement AgentRuntime", tt.agent)
			}
			// Static: the answer must not depend on the credential store.
			if got := rt.CredentialGrant(); got != tt.want {
				t.Errorf("CredentialGrant() = %q, want %q", got, tt.want)
			}
			if len(rt.DefaultDependencies()) == 0 {
				t.Errorf("agent %q should declare CLI dependencies", tt.agent)
			}
			if len(rt.NetworkHosts()) == 0 {
				t.Errorf("agent %q should declare network hosts", tt.agent)
			}
		})
	}
}

func TestPiDoesNotImplementAgentRuntime(t *testing.T) {
	// Companion: pi's backend grant is resolved per-invocation from flags,
	// config, and the store, so there is no static answer. agents: rejects it.
	a := provider.GetAgent("pi")
	if a == nil {
		t.Skip("pi provider not registered in this build")
	}
	if _, ok := a.(provider.AgentRuntime); ok {
		t.Error("pi must not implement AgentRuntime — its grant has no static value")
	}
}
