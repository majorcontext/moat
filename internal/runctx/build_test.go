package runctx

import (
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/netrules"
)

func TestBuildFromConfig(t *testing.T) {
	cfg := &config.Config{
		Agent:        "claude",
		Grants:       []string{"github", "anthropic"},
		Dependencies: []string{"postgres@16", "redis", "node@20"},
		Network: config.NetworkConfig{
			Policy: "strict",
			Rules: []netrules.NetworkRuleEntry{
				{HostRules: netrules.HostRules{Host: "api.github.com"}},
				{HostRules: netrules.HostRules{Host: "*.npmjs.org"}},
			},
		},
		Ports: map[string]int{
			"api": 8080,
			"web": 3000,
		},
		MCP: []config.MCPServerConfig{
			{Name: "github", URL: "https://mcp.github.com"},
			{Name: "linear", URL: "https://mcp.linear.app"},
		},
	}

	rc := BuildFromConfig(cfg, "run-abc123")

	// RunID, Agent, Workspace
	if rc.RunID != "run-abc123" {
		t.Errorf("RunID = %q, want %q", rc.RunID, "run-abc123")
	}
	if rc.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", rc.Agent, "claude")
	}
	if rc.Workspace != "/workspace" {
		t.Errorf("Workspace = %q, want %q", rc.Workspace, "/workspace")
	}

	// HasDependencies
	if !rc.HasDependencies {
		t.Error("HasDependencies = false, want true")
	}

	// Grants
	if len(rc.Grants) != 2 {
		t.Fatalf("len(Grants) = %d, want 2", len(rc.Grants))
	}
	// Check github grant
	foundGithub := false
	foundAnthropic := false
	for _, g := range rc.Grants {
		switch g.Name {
		case "github":
			foundGithub = true
			if g.Description != "GitHub access via `gh` CLI. Credentials are auto-injected at the network layer." {
				t.Errorf("github grant description = %q", g.Description)
			}
		case "anthropic":
			foundAnthropic = true
			if g.Description != "Anthropic API access via proxy." {
				t.Errorf("anthropic grant description = %q", g.Description)
			}
		}
	}
	if !foundGithub {
		t.Error("missing github grant")
	}
	if !foundAnthropic {
		t.Error("missing anthropic grant")
	}

	// Services (only service-type deps: postgres and redis, NOT node)
	if len(rc.Services) != 2 {
		t.Fatalf("len(Services) = %d, want 2", len(rc.Services))
	}
	foundPostgres := false
	foundRedis := false
	for _, s := range rc.Services {
		switch s.Name {
		case "postgres":
			foundPostgres = true
			if s.Version != "16" {
				t.Errorf("postgres version = %q, want %q", s.Version, "16")
			}
			if s.EnvURL != "$MOAT_POSTGRES_URL" {
				t.Errorf("postgres EnvURL = %q, want %q", s.EnvURL, "$MOAT_POSTGRES_URL")
			}
		case "redis":
			foundRedis = true
			// redis has no version specified, should use default "7"
			if s.Version != "7" {
				t.Errorf("redis version = %q, want %q (default)", s.Version, "7")
			}
			if s.EnvURL != "$MOAT_REDIS_URL" {
				t.Errorf("redis EnvURL = %q, want %q", s.EnvURL, "$MOAT_REDIS_URL")
			}
		default:
			t.Errorf("unexpected service %q (node should not be a service)", s.Name)
		}
	}
	if !foundPostgres {
		t.Error("missing postgres service")
	}
	if !foundRedis {
		t.Error("missing redis service")
	}

	// Network policy
	if rc.NetworkPolicy == nil {
		t.Fatal("NetworkPolicy is nil, want non-nil")
	}
	if rc.NetworkPolicy.Policy != "strict" {
		t.Errorf("NetworkPolicy.Policy = %q, want %q", rc.NetworkPolicy.Policy, "strict")
	}
	if len(rc.NetworkPolicy.AllowedHosts) != 2 {
		t.Fatalf("len(AllowedHosts) = %d, want 2", len(rc.NetworkPolicy.AllowedHosts))
	}

	// Ports
	if len(rc.Ports) != 2 {
		t.Fatalf("len(Ports) = %d, want 2", len(rc.Ports))
	}
	foundAPI := false
	foundWeb := false
	for _, p := range rc.Ports {
		switch p.Name {
		case "api":
			foundAPI = true
			if p.ContainerPort != 8080 {
				t.Errorf("api ContainerPort = %d, want 8080", p.ContainerPort)
			}
			if p.EnvHostPort != "$MOAT_HOST_API" {
				t.Errorf("api EnvHostPort = %q, want %q", p.EnvHostPort, "$MOAT_HOST_API")
			}
		case "web":
			foundWeb = true
			if p.ContainerPort != 3000 {
				t.Errorf("web ContainerPort = %d, want 3000", p.ContainerPort)
			}
			if p.EnvHostPort != "$MOAT_HOST_WEB" {
				t.Errorf("web EnvHostPort = %q, want %q", p.EnvHostPort, "$MOAT_HOST_WEB")
			}
		}
	}
	if !foundAPI {
		t.Error("missing api port")
	}
	if !foundWeb {
		t.Error("missing web port")
	}

	// MCP servers
	if len(rc.MCPServers) != 2 {
		t.Fatalf("len(MCPServers) = %d, want 2", len(rc.MCPServers))
	}
	foundGithubMCP := false
	foundLinearMCP := false
	for _, m := range rc.MCPServers {
		switch m.Name {
		case "github":
			foundGithubMCP = true
			if m.Description != "Available via MCP relay at /mcp/github" {
				t.Errorf("github MCP description = %q", m.Description)
			}
		case "linear":
			foundLinearMCP = true
			if m.Description != "Available via MCP relay at /mcp/linear" {
				t.Errorf("linear MCP description = %q", m.Description)
			}
		}
	}
	if !foundGithubMCP {
		t.Error("missing github MCP server")
	}
	if !foundLinearMCP {
		t.Error("missing linear MCP server")
	}
}

func TestBuildFromConfig_noOptionalSections(t *testing.T) {
	cfg := &config.Config{
		Agent: "codex",
	}

	rc := BuildFromConfig(cfg, "run-minimal")

	if rc.RunID != "run-minimal" {
		t.Errorf("RunID = %q, want %q", rc.RunID, "run-minimal")
	}
	if rc.Agent != "codex" {
		t.Errorf("Agent = %q, want %q", rc.Agent, "codex")
	}
	if rc.Workspace != "/workspace" {
		t.Errorf("Workspace = %q, want %q", rc.Workspace, "/workspace")
	}

	// All optional fields should be empty/nil/false
	if rc.HasDependencies {
		t.Error("HasDependencies = true, want false")
	}
	if len(rc.Grants) != 0 {
		t.Errorf("len(Grants) = %d, want 0", len(rc.Grants))
	}
	if len(rc.Services) != 0 {
		t.Errorf("len(Services) = %d, want 0", len(rc.Services))
	}
	if len(rc.Ports) != 0 {
		t.Errorf("len(Ports) = %d, want 0", len(rc.Ports))
	}
	if rc.NetworkPolicy != nil {
		t.Errorf("NetworkPolicy = %+v, want nil", rc.NetworkPolicy)
	}
	if len(rc.MCPServers) != 0 {
		t.Errorf("len(MCPServers) = %d, want 0", len(rc.MCPServers))
	}
}

func TestBuildFromConfig_unknownGrant(t *testing.T) {
	cfg := &config.Config{
		Agent:  "claude",
		Grants: []string{"custom-provider"},
	}

	rc := BuildFromConfig(cfg, "run-unknown")

	if len(rc.Grants) != 1 {
		t.Fatalf("len(Grants) = %d, want 1", len(rc.Grants))
	}
	if rc.Grants[0].Name != "custom-provider" {
		t.Errorf("grant name = %q, want %q", rc.Grants[0].Name, "custom-provider")
	}
	// Unknown grants should get a generic fallback description
	if rc.Grants[0].Description == "" {
		t.Error("unknown grant should have a non-empty fallback description")
	}
	// Verify it's not one of the known descriptions
	if rc.Grants[0].Description == "GitHub access via `gh` CLI. Credentials are auto-injected at the network layer." {
		t.Error("unknown grant should not get a known grant description")
	}
}
