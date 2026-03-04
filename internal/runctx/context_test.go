package runctx

import (
	"strings"
	"testing"
)

func TestRender_minimal(t *testing.T) {
	rc := &RuntimeContext{
		RunID:     "abc123",
		Agent:     "claude",
		Workspace: "/workspace",
	}

	got := Render(rc)

	// Must contain the header.
	if !strings.Contains(got, "# Moat Environment") {
		t.Error("missing header")
	}

	// Must contain workspace section.
	if !strings.Contains(got, "## Workspace") {
		t.Error("missing Workspace section")
	}
	if !strings.Contains(got, "/workspace") {
		t.Error("missing workspace path")
	}

	// Must contain run metadata.
	if !strings.Contains(got, "## Run Metadata") {
		t.Error("missing Run Metadata section")
	}
	if !strings.Contains(got, "abc123") {
		t.Error("missing run ID")
	}
	if !strings.Contains(got, "claude") {
		t.Error("missing agent name")
	}

	// Optional sections must NOT appear.
	for _, section := range []string{
		"## Grants",
		"## Services",
		"## Network Policy",
		"## MCP Servers",
		"## Ports",
	} {
		if strings.Contains(got, section) {
			t.Errorf("minimal render should not contain %q", section)
		}
	}
}

func TestRender_full(t *testing.T) {
	rc := &RuntimeContext{
		RunID:     "run-xyz",
		Agent:     "codex",
		Workspace: "/workspace",
		Grants: []Grant{
			{Name: "github", Description: "GitHub access via `gh` CLI"},
		},
		Services: []Service{
			{Name: "postgres", Version: "17", EnvURL: "$MOAT_POSTGRES_URL"},
			{Name: "redis", Version: "7", EnvURL: "$MOAT_REDIS_URL"},
		},
		Ports: []Port{
			{Name: "api", ContainerPort: 8080, EnvHostPort: "$MOAT_HOST_API"},
		},
		NetworkPolicy: &NetworkPolicy{
			Policy:       "strict",
			AllowedHosts: []string{"api.github.com", "*.npmjs.org"},
		},
		MCPServers: []MCPServer{
			{Name: "github", Description: "GitHub tools (issues, PRs, search)"},
		},
	}

	got := Render(rc)

	// All sections must be present.
	for _, section := range []string{
		"# Moat Environment",
		"## Workspace",
		"## Grants",
		"## Services",
		"## Network Policy",
		"## MCP Servers",
		"## Ports",
		"## Run Metadata",
	} {
		if !strings.Contains(got, section) {
			t.Errorf("full render missing section %q", section)
		}
	}

	// Grant content.
	if !strings.Contains(got, "`github`") {
		t.Error("missing grant name")
	}
	if !strings.Contains(got, "GitHub access via `gh` CLI") {
		t.Error("missing grant description")
	}

	// Service display names.
	if !strings.Contains(got, "PostgreSQL 17") {
		t.Error("missing PostgreSQL display name with version")
	}
	if !strings.Contains(got, "Redis 7") {
		t.Error("missing Redis display name with version")
	}
	if !strings.Contains(got, "`$MOAT_POSTGRES_URL`") {
		t.Error("missing postgres env URL")
	}
	if !strings.Contains(got, "`$MOAT_REDIS_URL`") {
		t.Error("missing redis env URL")
	}

	// Network policy.
	if !strings.Contains(got, "strict") {
		t.Error("missing network policy value")
	}
	if !strings.Contains(got, "api.github.com") {
		t.Error("missing allowed host")
	}
	if !strings.Contains(got, "*.npmjs.org") {
		t.Error("missing wildcard allowed host")
	}

	// MCP servers.
	if !strings.Contains(got, "`github`") {
		t.Error("missing MCP server name")
	}
	if !strings.Contains(got, "GitHub tools (issues, PRs, search)") {
		t.Error("missing MCP server description")
	}

	// Ports.
	if !strings.Contains(got, "`api`") {
		t.Error("missing port name")
	}
	if !strings.Contains(got, "8080") {
		t.Error("missing container port")
	}
	if !strings.Contains(got, "`$MOAT_HOST_API`") {
		t.Error("missing host port env var")
	}

	// Run metadata.
	if !strings.Contains(got, "run-xyz") {
		t.Error("missing run ID in metadata")
	}
	if !strings.Contains(got, "codex") {
		t.Error("missing agent in metadata")
	}
}

func TestRender_omitsEmptySections(t *testing.T) {
	rc := &RuntimeContext{
		RunID:     "run-001",
		Agent:     "claude",
		Workspace: "/workspace",
		Grants: []Grant{
			{Name: "github", Description: "GitHub access"},
		},
	}

	got := Render(rc)

	// Grants section must be present.
	if !strings.Contains(got, "## Grants") {
		t.Error("missing Grants section")
	}

	// All other optional sections must be absent.
	for _, section := range []string{
		"## Services",
		"## Network Policy",
		"## MCP Servers",
		"## Ports",
	} {
		if strings.Contains(got, section) {
			t.Errorf("should not contain %q when only Grants is set", section)
		}
	}
}

func TestRender_networkPolicyWithoutAllowedHosts(t *testing.T) {
	rc := &RuntimeContext{
		RunID:     "run-np",
		Agent:     "claude",
		Workspace: "/workspace",
		NetworkPolicy: &NetworkPolicy{
			Policy: "permissive",
		},
	}

	got := Render(rc)

	// Network Policy section must be present.
	if !strings.Contains(got, "## Network Policy") {
		t.Error("missing Network Policy section")
	}
	if !strings.Contains(got, "permissive") {
		t.Error("missing policy value")
	}

	// Allowed hosts line must NOT be present.
	if strings.Contains(got, "Allowed hosts") {
		t.Error("Allowed hosts line should not appear when AllowedHosts is empty")
	}
}

func TestRender_serviceDisplayNames(t *testing.T) {
	tests := []struct {
		name    string
		service Service
		want    string
	}{
		{"postgres", Service{Name: "postgres", Version: "16", EnvURL: "$URL"}, "PostgreSQL 16"},
		{"mysql", Service{Name: "mysql", Version: "8", EnvURL: "$URL"}, "MySQL 8"},
		{"redis", Service{Name: "redis", Version: "7", EnvURL: "$URL"}, "Redis 7"},
		{"unknown", Service{Name: "minio", Version: "2024", EnvURL: "$URL"}, "minio 2024"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &RuntimeContext{
				RunID:     "r1",
				Agent:     "claude",
				Workspace: "/workspace",
				Services:  []Service{tt.service},
			}
			got := Render(rc)
			if !strings.Contains(got, tt.want) {
				t.Errorf("expected %q in output, got:\n%s", tt.want, got)
			}
		})
	}
}
