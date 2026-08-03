package cli

import (
	"testing"

	"github.com/majorcontext/moat/internal/provider"

	// Blank imports so the agent-only providers are actually registered in the
	// test binary — otherwise the exclusion assertions below would pass
	// vacuously.
	_ "github.com/majorcontext/moat/internal/providers/copilot"
	_ "github.com/majorcontext/moat/internal/providers/pi"
)

// Agent-only providers register as CredentialProviders but their Grant()
// always errors, so listing them tells users to run a command that cannot work.
func TestGrantProviderInfosExcludesAgentOnly(t *testing.T) {
	// Checked by literal name so that dropping the marker from a provider
	// fails here rather than silently narrowing the test to whatever still
	// implements the interface.
	for _, name := range []string{"pi", "copilot"} {
		p := provider.Get(name)
		if p == nil {
			t.Fatalf("provider %q is not registered — this test would pass vacuously", name)
		}
		if _, ok := p.(provider.AgentOnlyProvider); !ok {
			t.Errorf("provider %q has no credential of its own and must implement provider.AgentOnlyProvider", name)
		}
	}

	excluded := map[string]bool{"pi": true, "copilot": true}
	for _, info := range grantProviderInfos() {
		if excluded[info.Name] {
			t.Errorf("agent-only provider %q must not be listed under 'moat grant providers'", info.Name)
		}
	}
}

// The structural half of the guard: whatever declares itself agent-only stays
// out of the listing, including providers added after this test was written.
func TestGrantProviderInfosExcludesEveryAgentOnlyMarker(t *testing.T) {
	markers := make(map[string]bool)
	for _, p := range provider.All() {
		if _, ok := p.(provider.AgentOnlyProvider); ok {
			markers[p.Name()] = true
		}
	}
	if len(markers) == 0 {
		t.Fatal("no registered provider implements AgentOnlyProvider — this test would pass vacuously")
	}

	for _, info := range grantProviderInfos() {
		if markers[info.Name] {
			t.Errorf("provider %q implements AgentOnlyProvider but is still listed", info.Name)
		}
	}
}

// Companion direction: the marker must not swallow providers that do have a
// credential of their own. Every agent provider that owns a grant stays listed.
func TestGrantProviderInfosKeepsCredentialOwningAgents(t *testing.T) {
	listed := make(map[string]bool)
	for _, info := range grantProviderInfos() {
		listed[info.Name] = true
	}

	// gemini owns its credential; claude and codex own theirs and are listed
	// under the names users type (codex as its openai alias).
	for _, name := range []string{"gemini", "claude", "openai"} {
		p := provider.Get(name)
		if p == nil {
			t.Fatalf("provider %q is not registered — this test would pass vacuously", name)
		}
		if _, ok := p.(provider.AgentOnlyProvider); ok {
			t.Errorf("provider %q owns a credential and must not be marked agent-only", name)
		}
		if !listed[name] {
			t.Errorf("credential-owning provider %q should be listed", name)
		}
	}
}

// Companion case: excluding agent-only providers and dropping the claude →
// anthropic rename must not drop real grants. `claude` (OAuth) and `anthropic`
// (API key) are separate credentials and both belong in the listing.
func TestGrantProviderInfosKeepsRealGrants(t *testing.T) {
	listed := make(map[string]bool)
	for _, info := range grantProviderInfos() {
		listed[info.Name] = true
	}

	for _, name := range []string{"claude", "anthropic", "github", "openai"} {
		if !listed[name] {
			t.Errorf("provider %q should be listed under 'moat grant providers'", name)
		}
	}
}

// A blank description column is a bug: the listing is how users discover what
// each grant is for.
func TestGrantProviderInfosAllDescribed(t *testing.T) {
	for _, info := range grantProviderInfos() {
		if info.Description == "" {
			t.Errorf("provider %q has no description — add one to goProviderDescriptions or implement DescribableProvider", info.Name)
		}
		if info.Type == "" {
			t.Errorf("provider %q has no type", info.Name)
		}
	}
}

// The claude provider was previously renamed to "anthropic" for display while
// AnthropicProvider also registered under that name, producing two rows.
func TestGrantProviderInfosNoDuplicates(t *testing.T) {
	seen := make(map[string]bool)
	for _, info := range grantProviderInfos() {
		if seen[info.Name] {
			t.Errorf("provider %q listed more than once", info.Name)
		}
		seen[info.Name] = true
	}
}

// Every listed name must be usable as 'moat grant <name>' — including
// alias-only names like "openai", which resolve through the registry alias.
func TestGrantProviderInfosNamesAreGrantable(t *testing.T) {
	for _, info := range grantProviderInfos() {
		if provider.Get(info.Name) == nil {
			t.Errorf("listed provider %q does not resolve via provider.Get — 'moat grant %s' would fail", info.Name, info.Name)
		}
	}
}
