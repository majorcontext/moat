package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/credential"
)

// GeneratedConfig holds the paths to generated configuration files.
type GeneratedConfig struct {
	// StagingDir is the directory containing all files to copy to ~/.claude at container startup.
	// This directory is mounted to /moat/claude-init and copied by moat-init script.
	StagingDir string

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
// It creates a staging directory containing:
// - settings.json with plugin configuration (marketplaces converted to directory sources)
// - installed_plugins.json with plugin installation records
// - .mcp.json with MCP server configuration (if any)
//
// The staging directory is mounted to /moat/claude-init and copied to ~/.claude
// at container startup by the moat-init script.
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
		TempDir:    tempDir,
		StagingDir: tempDir, // Staging dir is the temp dir itself
	}

	// Generate settings.json directly in the staging directory
	// This will be copied to ~/.claude/settings.json by moat-init
	settingsJSON, err := GenerateSettings(settings, cacheMountPath)
	if err != nil {
		return nil, fmt.Errorf("generating settings: %w", err)
	}

	settingsPath := filepath.Join(tempDir, "settings.json")
	if err := os.WriteFile(settingsPath, settingsJSON, 0644); err != nil {
		return nil, fmt.Errorf("writing settings.json: %w", err)
	}

	// Generate installed_plugins.json if plugins are configured
	// This will be copied to ~/.claude/plugins/installed_plugins.json by moat-init
	if settings != nil && len(settings.EnabledPlugins) > 0 {
		installedJSON, err := GenerateInstalledPlugins(settings, cacheMountPath)
		if err != nil {
			return nil, fmt.Errorf("generating installed plugins: %w", err)
		}

		installedPath := filepath.Join(tempDir, "installed_plugins.json")
		if err := os.WriteFile(installedPath, installedJSON, 0644); err != nil {
			return nil, fmt.Errorf("writing installed_plugins.json: %w", err)
		}
	}

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

// GenerateInstalledPlugins generates an installed_plugins.json for container use.
// This file tells Claude Code which plugins are "installed" and where to find them.
// Without this file, plugins in enabledPlugins won't be loaded.
//
// Only plugins from marketplaces defined in ExtraKnownMarketplaces are included,
// because those are the only ones we mount into the container. Plugins from
// other sources (like the user's host ~/.claude/settings.json) are not included
// since their marketplaces aren't available in the container.
func GenerateInstalledPlugins(settings *Settings, cacheMountPath string) ([]byte, error) {
	installedPlugins := &InstalledPluginsFile{
		Version: 2,
		Plugins: make(map[string][]InstalledPlugin),
	}

	if settings == nil || len(settings.EnabledPlugins) == 0 {
		return json.MarshalIndent(installedPlugins, "", "  ")
	}

	// Build set of marketplaces we actually have mounted
	knownMarketplaces := make(map[string]bool)
	for name := range settings.ExtraKnownMarketplaces {
		knownMarketplaces[name] = true
	}

	now := time.Now().UTC().Format(time.RFC3339)

	for pluginKey, enabled := range settings.EnabledPlugins {
		if !enabled {
			continue
		}

		// Parse plugin@marketplace format
		pluginName, marketplace := parsePluginKey(pluginKey)
		if pluginName == "" || marketplace == "" {
			continue
		}

		// Only include plugins from marketplaces we configure and mount
		// Skip plugins from host settings that reference marketplaces we don't have
		if !knownMarketplaces[marketplace] {
			continue
		}

		// Build install path pointing to our mounted marketplace
		// Format: {cacheMountPath}/marketplaces/{marketplace}/plugins/{plugin}
		// Claude Code marketplaces have a plugins/ subdirectory containing the actual plugins
		installPath := filepath.Join(cacheMountPath, "marketplaces", marketplace, "plugins", pluginName)

		installedPlugins.Plugins[pluginKey] = []InstalledPlugin{
			{
				Scope:       "user",
				InstallPath: installPath,
				Version:     "unknown",
				InstalledAt: now,
				LastUpdated: now,
			},
		}
	}

	return json.MarshalIndent(installedPlugins, "", "  ")
}

// parsePluginKey splits "plugin@marketplace" into its components.
// Returns empty strings if the format is invalid.
// Uses LastIndexByte so that malformed keys like "plugin@market@extra" parse as
// plugin="plugin@market", marketplace="extra". This provides more predictable
// behavior when processing unvalidated settings data - the marketplace name is
// always the part after the final @. Invalid keys are filtered out later by
// checking against known marketplaces.
func parsePluginKey(key string) (plugin, marketplace string) {
	idx := strings.LastIndexByte(key, '@')
	if idx <= 0 || idx >= len(key)-1 {
		return "", ""
	}
	return key[:idx], key[idx+1:]
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
				serverConfig.Env["GITHUB_TOKEN"] = credential.ProxyInjectedPlaceholder
			case "anthropic":
				serverConfig.Env["ANTHROPIC_API_KEY"] = credential.ProxyInjectedPlaceholder
			}
		}

		mcpConfig.MCPServers[name] = serverConfig
	}

	return json.MarshalIndent(mcpConfig, "", "  ")
}

// ClaudeInitMountPath is the path where the Claude staging directory is mounted.
// The moat-init script reads from this path and copies files to ~/.claude.
const ClaudeInitMountPath = "/moat/claude-init"

// ClaudePluginsPath is the base path for Claude plugins in the container.
// This matches Claude Code's expected location at ~/.claude/plugins.
// We use the absolute path for moatuser since that's our standard container user.
const ClaudePluginsPath = "/home/moatuser/.claude/plugins"

// ClaudeMarketplacesPath is the path where marketplaces are mounted in the container.
const ClaudeMarketplacesPath = ClaudePluginsPath + "/marketplaces"

// RequiredMounts returns the container mounts needed for Claude plugin support.
func RequiredMounts(settings *Settings, generatedConfig *GeneratedConfig, cacheDir, containerHome string) []MountInfo {
	var mounts []MountInfo

	// Mount plugin cache (read-only) - only if directory exists
	if settings != nil && settings.HasPluginsOrMarketplaces() {
		marketplacesDir := filepath.Join(cacheDir, "marketplaces")
		if _, err := os.Stat(marketplacesDir); err == nil {
			mounts = append(mounts, MountInfo{
				Source:   marketplacesDir,
				Target:   ClaudeMarketplacesPath,
				ReadOnly: true,
			})
		}
	}

	// Mount staging directory for Claude init
	// The moat-init script copies files from here to ~/.claude at container startup
	if generatedConfig != nil && generatedConfig.StagingDir != "" {
		mounts = append(mounts, MountInfo{
			Source:   generatedConfig.StagingDir,
			Target:   ClaudeInitMountPath,
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
