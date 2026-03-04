// Package runctx provides runtime context types and rendering for agent
// instruction files.
package runctx

import (
	"fmt"
	"strings"
)

// RuntimeContext holds the information about a moat run that is rendered
// into agent instruction files (CLAUDE.md, AGENTS.md, etc.).
type RuntimeContext struct {
	RunID         string
	Agent         string
	Workspace     string
	Grants        []Grant
	Services      []Service
	Ports         []Port
	NetworkPolicy *NetworkPolicy
	MCPServers    []MCPServer
}

// Grant describes a credential grant available inside the container.
type Grant struct {
	Name        string
	Description string
}

// Service describes an infrastructure service available to the run.
type Service struct {
	Name    string
	Version string
	EnvURL  string
}

// Port describes a port mapping from the container to the host.
type Port struct {
	Name          string
	ContainerPort int
	EnvHostPort   string
}

// NetworkPolicy describes the network access policy for the run.
type NetworkPolicy struct {
	Policy       string
	AllowedHosts []string
}

// MCPServer describes an MCP server available to the agent.
type MCPServer struct {
	Name        string
	Description string
}

// serviceDisplayNames maps internal service names to human-friendly display names.
var serviceDisplayNames = map[string]string{
	"postgres": "PostgreSQL",
	"mysql":    "MySQL",
	"redis":    "Redis",
}

// Render produces a markdown document describing the runtime context.
// Sections for optional fields (Grants, Services, etc.) are only included
// when their corresponding slice is non-empty or pointer is non-nil.
func Render(rc *RuntimeContext) string {
	var b strings.Builder

	// Header.
	b.WriteString("# Moat Environment\n\n")
	b.WriteString("You are running inside a Moat sandbox — an isolated container with\n")
	b.WriteString("credential injection and network controls.\n")

	// Workspace.
	b.WriteString("\n## Workspace\n\n")
	fmt.Fprintf(&b, "- Path: %s\n", rc.Workspace)
	b.WriteString("- Mount: read-write\n")

	// Grants.
	if len(rc.Grants) > 0 {
		b.WriteString("\n## Grants\n\n")
		for _, g := range rc.Grants {
			fmt.Fprintf(&b, "- `%s` — %s\n", g.Name, g.Description)
		}
	}

	// Services.
	if len(rc.Services) > 0 {
		b.WriteString("\n## Services\n\n")
		for _, s := range rc.Services {
			displayName := serviceDisplayName(s.Name)
			fmt.Fprintf(&b, "- %s %s available at `%s`\n", displayName, s.Version, s.EnvURL)
		}
	}

	// Network Policy.
	if rc.NetworkPolicy != nil {
		b.WriteString("\n## Network Policy\n\n")
		fmt.Fprintf(&b, "- Policy: %s\n", rc.NetworkPolicy.Policy)
		if len(rc.NetworkPolicy.AllowedHosts) > 0 {
			fmt.Fprintf(&b, "- Allowed hosts: %s\n", strings.Join(rc.NetworkPolicy.AllowedHosts, ", "))
		}
	}

	// MCP Servers.
	if len(rc.MCPServers) > 0 {
		b.WriteString("\n## MCP Servers\n\n")
		for _, m := range rc.MCPServers {
			fmt.Fprintf(&b, "- `%s` — %s\n", m.Name, m.Description)
		}
	}

	// Ports.
	if len(rc.Ports) > 0 {
		b.WriteString("\n## Ports\n\n")
		for _, p := range rc.Ports {
			fmt.Fprintf(&b, "- `%s` (%d) — host port at `%s`\n", p.Name, p.ContainerPort, p.EnvHostPort)
		}
	}

	// Run Metadata.
	b.WriteString("\n## Run Metadata\n\n")
	fmt.Fprintf(&b, "- Run ID: %s\n", rc.RunID)
	fmt.Fprintf(&b, "- Agent: %s\n", rc.Agent)

	return b.String()
}

// serviceDisplayName returns the human-friendly display name for a service.
// Unknown services are returned as-is.
func serviceDisplayName(name string) string {
	if display, ok := serviceDisplayNames[name]; ok {
		return display
	}
	return name
}
