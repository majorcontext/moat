package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andybons/moat/internal/config"
)

// ProxyInjectedPlaceholder is a placeholder value for credentials that will be
// injected by the Moat proxy at runtime. The actual credential never reaches
// the container; instead, the proxy intercepts requests and adds the real
// Authorization header. This placeholder signals to MCP servers that a credential
// is expected without exposing the actual value.
const ProxyInjectedPlaceholder = "moat-proxy-injected"

// GeneratedConfig holds the paths to generated configuration files.
type GeneratedConfig struct {
	// SettingsPath is the path to the generated settings.json file
	SettingsPath string

	// MCPConfigPath is the path to the generated .mcp.json file (empty if no MCP config)
	MCPConfigPath string

	// TempDir is the temporary directory holding generated files
	TempDir string
}

// Cleanup removes all generated temporary files.
func (g *GeneratedConfig) Cleanup() error {
	if g.TempDir != "" {
		return os.RemoveAll(g.TempDir)
	}
	return nil
}

// GenerateContainerConfig generates Claude configuration files for a container.
// It creates:
// - settings.json with plugin configuration (marketplaces converted to directory sources)
// - .mcp.json with MCP server configuration (if any)
//
// The files are written to a temporary directory and should be mounted into the container.
func GenerateContainerConfig(settings *Settings, cfg *config.Config, cacheMountPath string, grants []string) (*GeneratedConfig, error) {
	// Create temporary directory for generated files
	tempDir, err := os.MkdirTemp("", "moat-claude-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp directory: %w", err)
	}

	// Clean up on failure
	success := false
	defer func() {
		if !success {
			os.RemoveAll(tempDir)
		}
	}()

	result := &GeneratedConfig{
		TempDir: tempDir,
	}

	// Generate settings.json
	settingsJSON, err := GenerateSettings(settings, cacheMountPath)
	if err != nil {
		return nil, fmt.Errorf("generating settings: %w", err)
	}

	settingsPath := filepath.Join(tempDir, "settings.json")
	if err := os.WriteFile(settingsPath, settingsJSON, 0644); err != nil {
		return nil, fmt.Errorf("writing settings.json: %w", err)
	}
	result.SettingsPath = settingsPath

	// Generate .mcp.json if MCP servers are configured
	if cfg != nil && len(cfg.Claude.MCP) > 0 {
		mcpJSON, err := GenerateMCPConfig(cfg.Claude.MCP, grants)
		if err != nil {
			return nil, fmt.Errorf("generating MCP config: %w", err)
		}

		mcpPath := filepath.Join(tempDir, ".mcp.json")
		if err := os.WriteFile(mcpPath, mcpJSON, 0644); err != nil {
			return nil, fmt.Errorf("writing .mcp.json: %w", err)
		}
		result.MCPConfigPath = mcpPath
	}

	success = true
	return result, nil
}

// GenerateSettings generates a settings.json for container use.
// Marketplace sources are converted to directory sources pointing to the cache mount.
func GenerateSettings(settings *Settings, cacheMountPath string) ([]byte, error) {
	if settings == nil {
		settings = &Settings{}
	}

	// Create container settings with directory sources
	containerSettings := &Settings{
		EnabledPlugins:         settings.EnabledPlugins,
		ExtraKnownMarketplaces: make(map[string]MarketplaceEntry),
	}

	// Convert all marketplaces to directory sources
	for name, entry := range settings.ExtraKnownMarketplaces {
		containerSettings.ExtraKnownMarketplaces[name] = ConvertToDirectorySource(name, entry, cacheMountPath)
	}

	return json.MarshalIndent(containerSettings, "", "  ")
}

// MCPConfig represents the .mcp.json file format.
type MCPConfig struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// MCPServerConfig represents a single MCP server configuration.
type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
}

// GenerateMCPConfig generates .mcp.json content from agent.yaml MCP configuration.
// Credentials are injected based on grants - the actual token value is set to a
// placeholder since the proxy handles injection at the network layer.
func GenerateMCPConfig(servers map[string]config.MCPServerSpec, grants []string) ([]byte, error) {
	mcpConfig := MCPConfig{
		MCPServers: make(map[string]MCPServerConfig),
	}

	grantSet := make(map[string]bool)
	for _, g := range grants {
		// Handle grants with scopes (e.g., "github:repo")
		base := strings.Split(g, ":")[0]
		grantSet[base] = true
		grantSet[g] = true
	}

	for name, spec := range servers {
		serverConfig := MCPServerConfig{
			Command: spec.Command,
			Args:    spec.Args,
			Cwd:     spec.Cwd,
		}

		// Copy env vars, resolving grant placeholders
		if len(spec.Env) > 0 || spec.Grant != "" {
			serverConfig.Env = make(map[string]string)
			for k, v := range spec.Env {
				serverConfig.Env[k] = v
			}
		}

		// If grant is specified, inject credential placeholder
		// The actual credential is injected by the proxy at the network layer
		if spec.Grant != "" {
			grantBase := strings.Split(spec.Grant, ":")[0]
			if !grantSet[grantBase] && !grantSet[spec.Grant] {
				return nil, fmt.Errorf("MCP server %q requires grant %q but it's not in the grants list", name, spec.Grant)
			}

			// Add environment variable placeholder for the grant
			// The proxy will inject the actual token at the network layer
			switch grantBase {
			case "github":
				serverConfig.Env["GITHUB_TOKEN"] = ProxyInjectedPlaceholder
			case "anthropic":
				serverConfig.Env["ANTHROPIC_API_KEY"] = ProxyInjectedPlaceholder
			}
		}

		mcpConfig.MCPServers[name] = serverConfig
	}

	return json.MarshalIndent(mcpConfig, "", "  ")
}

// RequiredMounts returns the container mounts needed for Claude plugin support.
func RequiredMounts(settings *Settings, generatedConfig *GeneratedConfig, cacheDir, containerHome string) []MountInfo {
	var mounts []MountInfo

	// Mount plugin cache (read-only)
	if settings != nil && settings.HasPluginsOrMarketplaces() {
		mounts = append(mounts, MountInfo{
			Source:   filepath.Join(cacheDir, "marketplaces"),
			Target:   "/moat/claude-plugins/marketplaces",
			ReadOnly: true,
		})
	}

	// Mount generated settings.json
	if generatedConfig != nil && generatedConfig.SettingsPath != "" {
		mounts = append(mounts, MountInfo{
			Source:   generatedConfig.SettingsPath,
			Target:   filepath.Join(containerHome, ".claude", "settings.json"),
			ReadOnly: true,
		})
	}

	return mounts
}

// MountInfo describes a mount configuration.
type MountInfo struct {
	Source   string
	Target   string
	ReadOnly bool
}

// WriteFileInContainer describes a file that should be written inside the container.
type WriteFileInContainer struct {
	Path    string
	Content []byte
	Mode    os.FileMode
}

// FilesToWrite returns files that should be written into the container workspace.
func FilesToWrite(generatedConfig *GeneratedConfig) ([]WriteFileInContainer, error) {
	var files []WriteFileInContainer

	// Write .mcp.json to workspace if MCP servers are configured
	if generatedConfig != nil && generatedConfig.MCPConfigPath != "" {
		content, err := os.ReadFile(generatedConfig.MCPConfigPath)
		if err != nil {
			return nil, fmt.Errorf("reading generated .mcp.json: %w", err)
		}
		files = append(files, WriteFileInContainer{
			Path:    "/workspace/.mcp.json",
			Content: content,
			Mode:    0644,
		})
	}

	return files, nil
}
