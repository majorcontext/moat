package provider_test

import (
	"testing"

	"github.com/majorcontext/moat/internal/provider"
	// Import all provider packages to trigger init() registration.
	_ "github.com/majorcontext/moat/internal/providers"
)

func TestProvidersSatisfyCredentialProvider(t *testing.T) {
	providers := provider.All()
	if len(providers) == 0 {
		t.Skip("no providers registered (may need provider imports)")
	}
	for _, p := range providers {
		// CredentialProvider is a composite — every registered provider must satisfy it.
		var _ provider.CredentialProvider = p

		// ProxyProvider is always satisfied (embedded in CredentialProvider).
		var _ provider.ProxyProvider = p

		t.Logf("%s: satisfies CredentialProvider and ProxyProvider", p.Name())
	}
}

func TestProxyProviderSubset(t *testing.T) {
	// Verify ProxyProvider is a strict subset of CredentialProvider.
	providers := provider.All()
	if len(providers) == 0 {
		t.Skip("no providers registered")
	}
	for _, p := range providers {
		pp, ok := p.(provider.ProxyProvider)
		if !ok {
			t.Errorf("%s: does not implement ProxyProvider", p.Name())
			continue
		}
		if pp.Name() != p.Name() {
			t.Errorf("name mismatch: ProxyProvider.Name()=%q, CredentialProvider.Name()=%q", pp.Name(), p.Name())
		}
	}
}
