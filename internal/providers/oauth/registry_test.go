package oauth

import "testing"

func TestLookupServerURL(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"notion", "https://mcp.notion.com"},
		{"linear", "https://mcp.linear.app"},
		{"cloudflare", "https://mcp.cloudflare.com"},
		{"hubspot", "https://mcp.hubspot.com"},
		{"stripe", "https://mcp.stripe.com"},
		{"asana", "https://mcp.asana.com"},
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

func TestRegistryNotEmpty(t *testing.T) {
	if len(registry) == 0 {
		t.Error("registry should not be empty")
	}
}
