package run

import (
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestGrantToCommandExported(t *testing.T) {
	cases := map[string]string{
		"github":       "github",
		"oauth:notion": "oauth notion",
		"mcp:render":   "mcp render",
	}
	for grant, want := range cases {
		if got := GrantToCommand(grant); got != want {
			t.Errorf("GrantToCommand(%q) = %q, want %q", grant, got, want)
		}
	}
}

func TestAppendMCPGrantsExported(t *testing.T) {
	cfg := &config.Config{MCP: []config.MCPServerConfig{
		{Name: "render", Auth: &config.MCPAuthConfig{Grant: "mcp:render"}},
	}}
	got := AppendMCPGrants([]string{"github"}, cfg)
	want := []string{"github", "mcp:render"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("AppendMCPGrants = %v, want %v", got, want)
	}
}
