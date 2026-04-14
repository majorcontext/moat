package gcloud

import (
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestProviderName(t *testing.T) {
	p := New()
	if p.Name() != "gcloud" {
		t.Errorf("Name() = %q, want %q", p.Name(), "gcloud")
	}
}

func TestProviderRegistered(t *testing.T) {
	if provider.Get("gcloud") == nil {
		t.Error("gcloud provider not registered")
	}
}

func TestImpliedDependencies(t *testing.T) {
	p := New()
	deps := p.ImpliedDependencies()
	if len(deps) != 1 || deps[0] != "gcloud" {
		t.Errorf("ImpliedDependencies() = %v, want [gcloud]", deps)
	}
}
