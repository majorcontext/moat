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
	RunID           string
	Agent           string
	Workspace       string
	Grants          []Grant
	Services        []Service
	Ports           []Port
	NetworkPolicy   *NetworkPolicy
	MCPServers      []MCPServer
	HasDependencies bool // true when the config declares any dependencies
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
	AllowedHosts []AllowedHost
}

// AllowedHost describes a host that the run is allowed to access,
// optionally with per-path rules that restrict specific methods/paths.
type AllowedHost struct {
	Host  string
	Rules []string // human-readable rule summaries, e.g. "allow GET /repos/*"
}

// MCPServer describes an MCP server available to the agent.
type MCPServer struct {
	Name        string
	Description string
}

// Documentation base URL for machine-readable docs.
// URLs use .md suffixes to serve raw markdown (llms.txt convention) for agent
// consumption. Extension-less URLs serve HTML for human readers.
const docsBaseURL = "https://majorcontext.com/moat"

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
			// Check if any host has per-path rules.
			hasRules := false
			for _, h := range rc.NetworkPolicy.AllowedHosts {
				if len(h.Rules) > 0 {
					hasRules = true
					break
				}
			}
			if hasRules {
				// Use nested list so per-path rules are visible.
				b.WriteString("- Allowed hosts:\n")
				for _, h := range rc.NetworkPolicy.AllowedHosts {
					if len(h.Rules) == 0 {
						fmt.Fprintf(&b, "  - %s\n", h.Host)
					} else {
						fmt.Fprintf(&b, "  - %s (%d rules: %s)\n",
							h.Host, len(h.Rules), strings.Join(h.Rules, ", "))
					}
				}
			} else {
				// Simple comma-separated list when no per-path rules.
				hosts := make([]string, len(rc.NetworkPolicy.AllowedHosts))
				for i, h := range rc.NetworkPolicy.AllowedHosts {
					hosts[i] = h.Host
				}
				fmt.Fprintf(&b, "- Allowed hosts: %s\n", strings.Join(hosts, ", "))
			}
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

	// Documentation — always-present index plus context-specific references.
	b.WriteString("\n## Documentation\n\n")
	b.WriteString("For details on Moat configuration and capabilities:\n")
	fmt.Fprintf(&b, "- Index: %s/llms.txt\n", docsBaseURL)
	fmt.Fprintf(&b, "- moat.yaml reference: %s/reference/moat-yaml.md\n", docsBaseURL)
	if len(rc.Grants) > 0 {
		fmt.Fprintf(&b, "- Grants reference: %s/reference/grants.md\n", docsBaseURL)
	}
	if len(rc.Services) > 0 || rc.HasDependencies {
		fmt.Fprintf(&b, "- Dependencies reference: %s/reference/dependencies.md\n", docsBaseURL)
	}
	if len(rc.MCPServers) > 0 {
		fmt.Fprintf(&b, "- MCP servers guide: %s/guides/mcp.md\n", docsBaseURL)
	}
	if len(rc.Ports) > 0 {
		fmt.Fprintf(&b, "- Port exposure guide: %s/guides/ports.md\n", docsBaseURL)
	}
	if rc.NetworkPolicy != nil {
		fmt.Fprintf(&b, "- Networking concepts: %s/concepts/networking.md\n", docsBaseURL)
	}

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
