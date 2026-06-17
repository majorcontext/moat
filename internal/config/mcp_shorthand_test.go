package config

import (
	"os"
	"path/filepath"
	"strings"
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

func loadConfigFromString(t *testing.T, src string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(src), 0o644); err != nil {
		t.Fatalf("write moat.yaml: %v", err)
	}
	return Load(dir)
}

func TestResolveMCPShorthand_BareNameResolves(t *testing.T) {
	cfg, err := loadConfigFromString(t, "mcp:\n  - linear\n")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	m := cfg.MCP[0]
	if m.URL != "https://mcp.linear.app/mcp" {
		t.Errorf("url = %q, want linear MCP url", m.URL)
	}
	if m.Auth == nil || m.Auth.Grant != "oauth:linear" || m.Auth.Header != "Authorization" {
		t.Errorf("auth = %+v, want oauth:linear / Authorization", m.Auth)
	}
}

func TestResolveMCPShorthand_PolicyPreserved(t *testing.T) {
	cfg, err := loadConfigFromString(t, "mcp:\n  - name: linear\n    policy: linear-readonly\n")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	m := cfg.MCP[0]
	if m.URL != "https://mcp.linear.app/mcp" {
		t.Errorf("url not resolved: %q", m.URL)
	}
	if m.Policy == nil {
		t.Error("policy was dropped during resolution")
	}
}

func TestResolveMCPShorthand_ExplicitFieldsWin(t *testing.T) {
	src := "mcp:\n  - name: linear\n    url: https://custom.example.com/mcp\n"
	cfg, err := loadConfigFromString(t, src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MCP[0].URL != "https://custom.example.com/mcp" {
		t.Errorf("explicit url overridden: %q", cfg.MCP[0].URL)
	}
}

func TestResolveMCPShorthand_UnknownNameErrors(t *testing.T) {
	_, err := loadConfigFromString(t, "mcp:\n  - bogusservice\n")
	if err == nil {
		t.Fatal("expected error for unknown bare name, got nil")
	}
	if !strings.Contains(err.Error(), "bogusservice") || !strings.Contains(err.Error(), "known name") {
		t.Errorf("error = %q, want it to name the service and known names", err.Error())
	}
}

func TestResolveMCPShorthand_CustomFullEntryUntouched(t *testing.T) {
	src := "mcp:\n  - name: acme\n    url: https://mcp.acme.com/mcp\n    auth:\n      grant: oauth:acme\n      header: Authorization\n"
	cfg, err := loadConfigFromString(t, src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MCP[0].Auth.Grant != "oauth:acme" {
		t.Errorf("custom auth mutated: %+v", cfg.MCP[0].Auth)
	}
}
