package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMCPServerConfigUnmarshal_BareString(t *testing.T) {
	var c struct {
		MCP []MCPServerConfig `yaml:"mcp"`
	}
	src := "mcp:\n  - linear\n  - name: acme\n    url: https://mcp.acme.com/mcp\n"
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(c.MCP) != 2 {
		t.Fatalf("got %d entries, want 2", len(c.MCP))
	}
	if c.MCP[0].Name != "linear" || c.MCP[0].URL != "" {
		t.Errorf("bare string entry = %+v, want {Name:linear}", c.MCP[0])
	}
	if c.MCP[1].Name != "acme" || c.MCP[1].URL != "https://mcp.acme.com/mcp" {
		t.Errorf("map entry = %+v, want name=acme url set", c.MCP[1])
	}
}
