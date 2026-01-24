package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/credential"
)

func TestGenerateSettings(t *testing.T) {
	settings := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin-a@market": true,
			"plugin-b@market": false,
		},
		ExtraKnownMarketplaces: map[string]MarketplaceEntry{
			"market": {
				Source: MarketplaceSource{
					Source: "git",
					URL:    "git@github.com:org/plugins.git",
				},
			},
			"local": {
				Source: MarketplaceSource{
					Source: "directory",
					Path:   "/opt/plugins",
				},
			},
		},
	}

	jsonBytes, err := GenerateSettings(settings, ClaudePluginsPath)
	if err != nil {
		t.Fatalf("GenerateSettings: %v", err)
	}

	var result Settings
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	// Check plugins are preserved
	if len(result.EnabledPlugins) != 2 {
		t.Errorf("EnabledPlugins = %d, want 2", len(result.EnabledPlugins))
	}

	// Check git marketplace is converted to directory
	market := result.ExtraKnownMarketplaces["market"]
	if market.Source.Source != "directory" {
		t.Errorf("market.Source.Source = %q, want %q", market.Source.Source, "directory")
	}
	expectedMarketPath := ClaudePluginsPath + "/marketplaces/market"
	if market.Source.Path != expectedMarketPath {
		t.Errorf("market.Source.Path = %q, want %q", market.Source.Path, expectedMarketPath)
	}

	// Check directory marketplace keeps original path
	local := result.ExtraKnownMarketplaces["local"]
	if local.Source.Source != "directory" {
		t.Errorf("local.Source.Source = %q, want %q", local.Source.Source, "directory")
	}
	if local.Source.Path != "/opt/plugins" {
		t.Errorf("local.Source.Path = %q, want %q (unchanged)", local.Source.Path, "/opt/plugins")
	}
}

func TestGenerateSettingsNil(t *testing.T) {
	jsonBytes, err := GenerateSettings(nil, ClaudePluginsPath)
	if err != nil {
		t.Fatalf("GenerateSettings(nil): %v", err)
	}

	var result Settings
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	// Should produce valid empty settings
	if len(result.EnabledPlugins) > 0 {
		t.Error("EnabledPlugins should be empty for nil input")
	}
}

func TestGenerateInstalledPlugins(t *testing.T) {
	settings := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin-a@market":    true,
			"plugin-b@market":    false, // disabled - should not appear
			"other@different":    true,  // different marketplace - not in ExtraKnownMarketplaces
			"unknown@notmounted": true,  // marketplace not mounted - should be skipped
			"invalid-no-at":      true,  // invalid format - should be skipped
			"@invalid-empty":     true,  // invalid format - should be skipped
			"invalid-trailing@":  true,  // invalid format - should be skipped
		},
		// Only include marketplaces we actually mount
		ExtraKnownMarketplaces: map[string]MarketplaceEntry{
			"market": {
				Source: MarketplaceSource{Source: "git", URL: "https://github.com/org/market.git"},
			},
			"different": {
				Source: MarketplaceSource{Source: "git", URL: "https://github.com/org/different.git"},
			},
			// "notmounted" is NOT in this list, so plugins from it should be skipped
		},
	}

	jsonBytes, err := GenerateInstalledPlugins(settings, ClaudePluginsPath)
	if err != nil {
		t.Fatalf("GenerateInstalledPlugins: %v", err)
	}

	var result InstalledPluginsFile
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	// Check version
	if result.Version != 2 {
		t.Errorf("Version = %d, want 2", result.Version)
	}

	// Check enabled plugins from known marketplaces are included
	if _, ok := result.Plugins["plugin-a@market"]; !ok {
		t.Error("plugin-a@market should be in installed plugins")
	}
	if _, ok := result.Plugins["other@different"]; !ok {
		t.Error("other@different should be in installed plugins")
	}

	// Check disabled plugin is NOT included
	if _, ok := result.Plugins["plugin-b@market"]; ok {
		t.Error("plugin-b@market should NOT be in installed plugins (disabled)")
	}

	// Check plugins from unknown marketplaces are NOT included
	if _, ok := result.Plugins["unknown@notmounted"]; ok {
		t.Error("unknown@notmounted should NOT be in installed plugins (marketplace not configured)")
	}

	// Check invalid formats are NOT included
	if _, ok := result.Plugins["invalid-no-at"]; ok {
		t.Error("invalid-no-at should NOT be in installed plugins (invalid format)")
	}

	// Check install paths
	pluginA := result.Plugins["plugin-a@market"]
	if len(pluginA) != 1 {
		t.Fatalf("plugin-a should have 1 entry, got %d", len(pluginA))
	}
	expectedPath := ClaudePluginsPath + "/marketplaces/market/plugins/plugin-a"
	if pluginA[0].InstallPath != expectedPath {
		t.Errorf("InstallPath = %q, want %q", pluginA[0].InstallPath, expectedPath)
	}
	if pluginA[0].Scope != "user" {
		t.Errorf("Scope = %q, want %q", pluginA[0].Scope, "user")
	}
	if pluginA[0].Version != "unknown" {
		t.Errorf("Version = %q, want %q", pluginA[0].Version, "unknown")
	}
	if pluginA[0].InstalledAt == "" {
		t.Error("InstalledAt should be set")
	}
	if pluginA[0].LastUpdated == "" {
		t.Error("LastUpdated should be set")
	}
}

func TestGenerateInstalledPluginsNil(t *testing.T) {
	jsonBytes, err := GenerateInstalledPlugins(nil, ClaudePluginsPath)
	if err != nil {
		t.Fatalf("GenerateInstalledPlugins(nil): %v", err)
	}

	var result InstalledPluginsFile
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	if result.Version != 2 {
		t.Errorf("Version = %d, want 2", result.Version)
	}
	if len(result.Plugins) > 0 {
		t.Error("Plugins should be empty for nil input")
	}
}

func TestGenerateInstalledPluginsEmpty(t *testing.T) {
	settings := &Settings{
		EnabledPlugins: map[string]bool{},
	}

	jsonBytes, err := GenerateInstalledPlugins(settings, ClaudePluginsPath)
	if err != nil {
		t.Fatalf("GenerateInstalledPlugins: %v", err)
	}

	var result InstalledPluginsFile
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	if len(result.Plugins) > 0 {
		t.Error("Plugins should be empty for empty EnabledPlugins")
	}
}

func TestParsePluginKey(t *testing.T) {
	tests := []struct {
		key        string
		wantPlugin string
		wantMarket string
	}{
		{"plugin@market", "plugin", "market"},
		{"my-plugin@my-market", "my-plugin", "my-market"},
		{"a@b", "a", "b"},
		{"plugin@market@extra", "plugin@market", "extra"}, // last @ wins
		{"", "", ""},
		{"no-at-sign", "", ""},
		{"@empty-plugin", "", ""},
		{"empty-market@", "", ""},
	}

	for _, tt := range tests {
		plugin, market := parsePluginKey(tt.key)
		if plugin != tt.wantPlugin || market != tt.wantMarket {
			t.Errorf("parsePluginKey(%q) = (%q, %q), want (%q, %q)",
				tt.key, plugin, market, tt.wantPlugin, tt.wantMarket)
		}
	}
}

func TestGenerateMCPConfig(t *testing.T) {
	servers := map[string]config.MCPServerSpec{
		"github": {
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-github"},
			Grant:   "github",
		},
		"filesystem": {
			Command: "npx",
			Args:    []string{"-y", "@anthropic/mcp-server-filesystem", "/workspace"},
			Cwd:     "/workspace",
		},
		"custom": {
			Command: "python",
			Args:    []string{"-m", "my_server"},
			Env: map[string]string{
				"API_URL": "https://api.example.com",
			},
		},
	}

	grants := []string{"github", "anthropic"}

	jsonBytes, err := GenerateMCPConfig(servers, grants)
	if err != nil {
		t.Fatalf("GenerateMCPConfig: %v", err)
	}

	var result MCPConfig
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	// Check github server has token placeholder
	github := result.MCPServers["github"]
	if github.Command != "npx" {
		t.Errorf("github.Command = %q, want %q", github.Command, "npx")
	}
	if github.Env["GITHUB_TOKEN"] != credential.ProxyInjectedPlaceholder {
		t.Errorf("github.Env[GITHUB_TOKEN] = %q, want %q", github.Env["GITHUB_TOKEN"], credential.ProxyInjectedPlaceholder)
	}

	// Check filesystem server
	fs := result.MCPServers["filesystem"]
	if fs.Cwd != "/workspace" {
		t.Errorf("filesystem.Cwd = %q, want %q", fs.Cwd, "/workspace")
	}

	// Check custom server has its env preserved
	custom := result.MCPServers["custom"]
	if custom.Env["API_URL"] != "https://api.example.com" {
		t.Errorf("custom.Env[API_URL] = %q, want %q", custom.Env["API_URL"], "https://api.example.com")
	}
}

func TestGenerateMCPConfigMissingGrant(t *testing.T) {
	servers := map[string]config.MCPServerSpec{
		"github": {
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-github"},
			Grant:   "github", // Requires github grant
		},
	}

	grants := []string{"anthropic"} // Only anthropic, no github

	_, err := GenerateMCPConfig(servers, grants)
	if err == nil {
		t.Fatal("GenerateMCPConfig should error when required grant is missing")
	}
	if !strings.Contains(err.Error(), "github") {
		t.Errorf("error should mention missing grant: %v", err)
	}
}

func TestGenerateMCPConfigGrantWithScope(t *testing.T) {
	servers := map[string]config.MCPServerSpec{
		"github": {
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-github"},
			Grant:   "github",
		},
	}

	// Grant with scope should work
	grants := []string{"github:repo"}

	jsonBytes, err := GenerateMCPConfig(servers, grants)
	if err != nil {
		t.Fatalf("GenerateMCPConfig: %v", err)
	}

	var result MCPConfig
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	if result.MCPServers["github"].Env["GITHUB_TOKEN"] != credential.ProxyInjectedPlaceholder {
		t.Error("github token should be injected even with scoped grant")
	}
}

func TestGenerateContainerConfig(t *testing.T) {
	settings := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin@market": true,
		},
		ExtraKnownMarketplaces: map[string]MarketplaceEntry{
			"market": {
				Source: MarketplaceSource{Source: "git", URL: "https://github.com/org/market.git"},
			},
		},
	}

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			MCP: map[string]config.MCPServerSpec{
				"test": {
					Command: "echo",
					Args:    []string{"hello"},
				},
			},
		},
	}

	result, err := GenerateContainerConfig(settings, cfg, ClaudePluginsPath, nil)
	if err != nil {
		t.Fatalf("GenerateContainerConfig: %v", err)
	}
	defer result.Cleanup()

	// Check staging directory and settings.json were created
	if result.StagingDir == "" {
		t.Error("StagingDir should be set")
	}
	settingsPath := filepath.Join(result.StagingDir, "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Errorf("settings.json should exist: %v", err)
	}

	// Check installed_plugins.json was created
	installedPath := filepath.Join(result.StagingDir, "installed_plugins.json")
	if _, err := os.Stat(installedPath); err != nil {
		t.Errorf("installed_plugins.json should exist: %v", err)
	}

	// Check .mcp.json was created
	if result.MCPConfigPath == "" {
		t.Error("MCPConfigPath should be set when MCP servers configured")
	}
	if _, err := os.Stat(result.MCPConfigPath); err != nil {
		t.Errorf(".mcp.json should exist: %v", err)
	}

	// Verify settings.json content
	settingsContent, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}
	var parsedSettings Settings
	if err := json.Unmarshal(settingsContent, &parsedSettings); err != nil {
		t.Fatalf("parsing settings.json: %v", err)
	}
	if !parsedSettings.EnabledPlugins["plugin@market"] {
		t.Error("plugin should be enabled in generated settings")
	}

	// Verify installed_plugins.json content
	installedContent, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatalf("reading installed_plugins.json: %v", err)
	}
	var parsedInstalled InstalledPluginsFile
	if err := json.Unmarshal(installedContent, &parsedInstalled); err != nil {
		t.Fatalf("parsing installed_plugins.json: %v", err)
	}
	if parsedInstalled.Version != 2 {
		t.Errorf("installed_plugins.json version = %d, want 2", parsedInstalled.Version)
	}
	if _, ok := parsedInstalled.Plugins["plugin@market"]; !ok {
		t.Error("plugin@market should be in installed_plugins.json")
	}
	expectedInstallPath := ClaudePluginsPath + "/marketplaces/market/plugins/plugin"
	if parsedInstalled.Plugins["plugin@market"][0].InstallPath != expectedInstallPath {
		t.Errorf("install path = %q, want %q",
			parsedInstalled.Plugins["plugin@market"][0].InstallPath,
			expectedInstallPath)
	}

	// Verify .mcp.json content
	mcpContent, err := os.ReadFile(result.MCPConfigPath)
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}
	var parsedMCP MCPConfig
	if err := json.Unmarshal(mcpContent, &parsedMCP); err != nil {
		t.Fatalf("parsing .mcp.json: %v", err)
	}
	if parsedMCP.MCPServers["test"].Command != "echo" {
		t.Error("MCP server command should be preserved")
	}
}

func TestGenerateContainerConfigNoMCP(t *testing.T) {
	settings := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin@market": true,
		},
		ExtraKnownMarketplaces: map[string]MarketplaceEntry{
			"market": {
				Source: MarketplaceSource{Source: "git", URL: "https://github.com/org/market.git"},
			},
		},
	}

	result, err := GenerateContainerConfig(settings, nil, ClaudePluginsPath, nil)
	if err != nil {
		t.Fatalf("GenerateContainerConfig: %v", err)
	}
	defer result.Cleanup()

	// Settings should be created
	if result.StagingDir == "" {
		t.Error("StagingDir should be set")
	}
	settingsPath := filepath.Join(result.StagingDir, "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Errorf("settings.json should exist: %v", err)
	}

	// installed_plugins.json should be created (since plugins are configured)
	installedPath := filepath.Join(result.StagingDir, "installed_plugins.json")
	if _, err := os.Stat(installedPath); err != nil {
		t.Errorf("installed_plugins.json should exist: %v", err)
	}

	// MCP config should NOT be created
	if result.MCPConfigPath != "" {
		t.Error("MCPConfigPath should be empty when no MCP servers configured")
	}
}

func TestGenerateContainerConfigNoPlugins(t *testing.T) {
	settings := &Settings{
		EnabledPlugins: map[string]bool{}, // Empty
	}

	result, err := GenerateContainerConfig(settings, nil, ClaudePluginsPath, nil)
	if err != nil {
		t.Fatalf("GenerateContainerConfig: %v", err)
	}
	defer result.Cleanup()

	// Settings should be created
	settingsPath := filepath.Join(result.StagingDir, "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Errorf("settings.json should exist: %v", err)
	}

	// installed_plugins.json should NOT be created when no plugins
	installedPath := filepath.Join(result.StagingDir, "installed_plugins.json")
	if _, err := os.Stat(installedPath); !os.IsNotExist(err) {
		t.Error("installed_plugins.json should NOT exist when no plugins configured")
	}
}

func TestGeneratedConfigCleanup(t *testing.T) {
	settings := &Settings{}
	result, err := GenerateContainerConfig(settings, nil, ClaudePluginsPath, nil)
	if err != nil {
		t.Fatalf("GenerateContainerConfig: %v", err)
	}

	tempDir := result.TempDir
	if _, err := os.Stat(tempDir); err != nil {
		t.Fatalf("temp dir should exist: %v", err)
	}

	if err := result.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Error("temp dir should be removed after cleanup")
	}
}

func TestRequiredMounts(t *testing.T) {
	// Create a temp directory structure for the cache
	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "plugins")
	marketplacesDir := filepath.Join(cacheDir, "marketplaces")
	if err := os.MkdirAll(marketplacesDir, 0755); err != nil {
		t.Fatalf("failed to create marketplaces dir: %v", err)
	}

	settings := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin@market": true,
		},
	}

	generatedConfig := &GeneratedConfig{
		StagingDir: "/tmp/moat-claude-staging-123",
	}

	mounts := RequiredMounts(settings, generatedConfig, cacheDir, "/home/container")

	// Should have plugin cache mount at Claude's expected location
	var hasCacheMount bool
	for _, m := range mounts {
		if m.Target == ClaudeMarketplacesPath {
			hasCacheMount = true
			if !m.ReadOnly {
				t.Error("plugin cache mount should be read-only")
			}
		}
	}
	if !hasCacheMount {
		t.Errorf("should have plugin cache mount at %s", ClaudeMarketplacesPath)
	}

	// Should have staging directory mount for claude init
	var hasStagingMount bool
	for _, m := range mounts {
		if m.Target == ClaudeInitMountPath {
			hasStagingMount = true
			if m.Source != "/tmp/moat-claude-staging-123" {
				t.Errorf("staging mount source = %q, want %q", m.Source, "/tmp/moat-claude-staging-123")
			}
			if !m.ReadOnly {
				t.Error("staging mount should be read-only")
			}
		}
	}
	if !hasStagingMount {
		t.Error("should have staging directory mount")
	}
}

func TestRequiredMounts_NoMarketplacesDir(t *testing.T) {
	// Use a non-existent cache directory
	cacheDir := "/nonexistent/path/to/cache"

	settings := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin@market": true,
		},
	}

	generatedConfig := &GeneratedConfig{
		StagingDir: "/tmp/moat-claude-staging-123",
	}

	mounts := RequiredMounts(settings, generatedConfig, cacheDir, "/home/container")

	// Should NOT have plugin cache mount (directory doesn't exist)
	for _, m := range mounts {
		if m.Target == ClaudeMarketplacesPath {
			t.Error("should not have plugin cache mount when directory doesn't exist")
		}
	}

	// Should still have staging mount
	var hasStagingMount bool
	for _, m := range mounts {
		if m.Target == ClaudeInitMountPath {
			hasStagingMount = true
		}
	}
	if !hasStagingMount {
		t.Error("should have staging mount even when cache dir doesn't exist")
	}
}

func TestFilesToWrite(t *testing.T) {
	// Create a temp .mcp.json
	tempDir := t.TempDir()
	mcpPath := filepath.Join(tempDir, ".mcp.json")
	mcpContent := `{"mcpServers": {}}`
	if err := os.WriteFile(mcpPath, []byte(mcpContent), 0644); err != nil {
		t.Fatal(err)
	}

	generatedConfig := &GeneratedConfig{
		MCPConfigPath: mcpPath,
	}

	files, err := FilesToWrite(generatedConfig)
	if err != nil {
		t.Fatalf("FilesToWrite: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	if files[0].Path != "/workspace/.mcp.json" {
		t.Errorf("file path = %q, want %q", files[0].Path, "/workspace/.mcp.json")
	}
	if string(files[0].Content) != mcpContent {
		t.Errorf("file content = %q, want %q", string(files[0].Content), mcpContent)
	}
}

func TestFilesToWriteNoMCP(t *testing.T) {
	generatedConfig := &GeneratedConfig{
		StagingDir: "/tmp/moat-claude-staging-123",
		// MCPConfigPath is empty
	}

	files, err := FilesToWrite(generatedConfig)
	if err != nil {
		t.Fatalf("FilesToWrite: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("expected 0 files when no MCP, got %d", len(files))
	}
}
