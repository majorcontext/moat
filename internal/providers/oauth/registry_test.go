package oauth

import "testing"

func TestLookupServerURL(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"notion", "https://mcp.notion.com/mcp"},
		{"linear", "https://mcp.linear.app/mcp"},
		{"cloudflare", "https://mcp.cloudflare.com/mcp"},
		{"hubspot", "https://mcp.hubspot.com"},
		{"stripe", "https://mcp.stripe.com"},
		{"asana", "https://mcp.asana.com/mcp"},
		{"posthog", "https://mcp.posthog.com/mcp"},
		// context7 is in the catalog but uses an API-key grant, not OAuth —
		// it must not feed into OAuth discovery.
		{"context7", ""},
		{"nonexistent", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LookupServerURL(tt.name)
			if got != tt.want {
				t.Errorf("LookupServerURL(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
