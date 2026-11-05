package daemon

import (
	"strings"
	"testing"
)

// TestBrandingMatchesMoatStubs guards the MCP relay contract: the agent's
// config carries "moat-stub-<grant>", and the proxy only substitutes the real
// credential for values with the branding's stub prefix. Gatekeeper's default
// prefix is "gatekeeper-stub-", so dropping the branding breaks MCP auth.
func TestBrandingMatchesMoatStubs(t *testing.T) {
	b := Branding()
	if b.MCPStubPrefix != "moat-stub-" {
		t.Fatalf("MCPStubPrefix = %q, want moat-stub-", b.MCPStubPrefix)
	}
	if b.Name != "Moat" || b.PolicyScheme != "Moat-Policy" {
		t.Errorf("Name/PolicyScheme = %q/%q, want Moat/Moat-Policy", b.Name, b.PolicyScheme)
	}
	if got := b.GrantHint("oauth:notion"); got != "Run: moat grant oauth notion" {
		t.Errorf("GrantHint(oauth:notion) = %q", got)
	}
	if got := b.GrantHint("mcp-context7"); got != "Run: moat grant mcp context7" {
		t.Errorf("GrantHint(mcp-context7) = %q", got)
	}
	if got := b.GrantHint("github"); got != "Run: moat grant github" {
		t.Errorf("GrantHint(github) = %q", got)
	}
	if got := b.HostHint(5432); !strings.Contains(got, "- 5432") {
		t.Errorf("HostHint(5432) = %q", got)
	}
}
