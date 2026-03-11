package runctx

import (
	"fmt"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/deps"
)

// grantDescriptions maps known grant names to human-friendly descriptions.
var grantDescriptions = map[string]string{
	"github":    "GitHub access via `gh` CLI. Credentials are auto-injected at the network layer.",
	"anthropic": "Anthropic API access via proxy.",
	"openai":    "OpenAI API access via proxy.",
	"gemini":    "Google Gemini API access via proxy.",
	"aws":       "AWS credentials via IAM role assumption.",
	"telegram":  "Telegram Bot API access.",
}

// BuildFromConfig constructs a RuntimeContext from a moat config and run ID.
func BuildFromConfig(cfg *config.Config, runID string) *RuntimeContext {
	rc := &RuntimeContext{
		RunID:           runID,
		Agent:           cfg.Agent,
		Workspace:       "/workspace",
		HasDependencies: len(cfg.Dependencies) > 0,
	}

	// Grants.
	for _, name := range cfg.Grants {
		desc, ok := grantDescriptions[name]
		if !ok {
			desc = fmt.Sprintf("Credential grant %q.", name)
		}
		rc.Grants = append(rc.Grants, Grant{
			Name:        name,
			Description: desc,
		})
	}

	// Services from dependencies (only TypeService entries).
	for _, depStr := range cfg.Dependencies {
		dep, err := deps.Parse(depStr)
		if err != nil {
			continue
		}
		spec, ok := deps.GetSpec(dep.Name)
		if !ok {
			continue
		}
		if spec.Type != deps.TypeService {
			continue
		}
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		envURL := fmt.Sprintf("$MOAT_%s_URL", strings.ToUpper(dep.Name))
		rc.Services = append(rc.Services, Service{
			Name:    dep.Name,
			Version: version,
			EnvURL:  envURL,
		})
	}

	// Network policy.
	if cfg.Network.Policy != "" {
		np := &NetworkPolicy{
			Policy: cfg.Network.Policy,
		}
		for _, entry := range cfg.Network.Rules {
			np.AllowedHosts = append(np.AllowedHosts, entry.Host)
		}
		rc.NetworkPolicy = np
	}

	// Ports (sorted by name for deterministic output).
	if len(cfg.Ports) > 0 {
		names := make([]string, 0, len(cfg.Ports))
		for name := range cfg.Ports {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			port := cfg.Ports[name]
			envHostPort := fmt.Sprintf("$MOAT_HOST_%s", strings.ToUpper(name))
			rc.Ports = append(rc.Ports, Port{
				Name:          name,
				ContainerPort: port,
				EnvHostPort:   envHostPort,
			})
		}
	}

	// MCP servers. Use relay description rather than raw URL to avoid
	// exposing URLs that may contain embedded credentials or internal endpoints.
	for _, mcp := range cfg.MCP {
		rc.MCPServers = append(rc.MCPServers, MCPServer{
			Name:        mcp.Name,
			Description: fmt.Sprintf("Available via MCP relay at /mcp/%s", mcp.Name),
		})
	}

	return rc
}
