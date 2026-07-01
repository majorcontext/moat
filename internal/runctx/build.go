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

// BuildOptions carries resolved run facts that are not derivable from the
// moat.yaml Config alone — they come from CLI overrides or runtime resolution
// at container-creation time.
type BuildOptions struct {
	// WorkspaceMode is the resolved workspace mode ("bind"/"volume"). Empty is
	// treated as bind. Sourced from the resolved run options, not cfg, because a
	// --workspace-mode CLI flag can override the moat.yaml value.
	WorkspaceMode config.WorkspaceMode
	// DockerMode is the resolved Docker mode ("host"/"dind"). Empty means Docker
	// is not enabled for this run.
	DockerMode deps.DockerMode
}

// BuildFromConfig constructs a RuntimeContext from a moat config, run ID, and
// resolved run options.
func BuildFromConfig(cfg *config.Config, runID string, opts BuildOptions) *RuntimeContext {
	workspaceMode := opts.WorkspaceMode
	if workspaceMode == "" {
		workspaceMode = config.WorkspaceModeBind
	}
	rc := &RuntimeContext{
		RunID:           runID,
		Agent:           cfg.Agent,
		Workspace:       "/workspace",
		WorkspaceMode:   string(workspaceMode),
		HasDependencies: len(cfg.Dependencies) > 0,
	}

	if opts.DockerMode != "" {
		rc.Docker = &Docker{Mode: string(opts.DockerMode)}
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

	// Dependencies: split into services (their own section) and installed tools.
	// Docker deps are skipped here — Docker is surfaced via opts.DockerMode.
	for _, depStr := range cfg.Dependencies {
		dep, err := deps.Parse(depStr)
		if err != nil {
			continue
		}
		// Docker deps (docker:host/docker:dind) are surfaced via opts.DockerMode,
		// not as tools. parseDockerDep sets DockerMode but leaves Type unset, so
		// key off the mode rather than the install type.
		if dep.DockerMode != "" {
			continue
		}
		if spec, ok := deps.GetSpec(dep.Name); ok && spec.Type == deps.TypeService {
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
			continue
		}
		// Anything else declared is an installed tool.
		rc.Tools = append(rc.Tools, depStr)
	}

	// Network policy.
	if cfg.Network.Policy != "" {
		np := &NetworkPolicy{
			Policy: cfg.Network.Policy,
		}
		for _, entry := range cfg.Network.Rules {
			ah := AllowedHost{Host: entry.Host}
			for _, r := range entry.Rules {
				ah.Rules = append(ah.Rules, fmt.Sprintf("%s %s %s", r.Action, r.Method, r.PathPattern))
			}
			np.AllowedHosts = append(np.AllowedHosts, ah)
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
