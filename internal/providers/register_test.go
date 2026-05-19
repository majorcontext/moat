package providers

import (
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestKiroProviderRegistered(t *testing.T) {
	if provider.Get("kiro") == nil {
		t.Fatal("kiro provider not registered")
	}
	if provider.GetAgent("kiro") == nil {
		t.Fatal("kiro provider does not implement AgentProvider")
	}
}
