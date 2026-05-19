package credential

import "testing"

func TestProviderKiroIsKnown(t *testing.T) {
	if ProviderKiro != "kiro" {
		t.Errorf("ProviderKiro = %q, want %q", ProviderKiro, "kiro")
	}
	if !IsKnownProvider(ProviderKiro) {
		t.Error("IsKnownProvider(ProviderKiro) = false, want true")
	}
	found := false
	for _, p := range KnownProviders() {
		if p == ProviderKiro {
			found = true
		}
	}
	if !found {
		t.Error("KnownProviders() does not include ProviderKiro")
	}
}
