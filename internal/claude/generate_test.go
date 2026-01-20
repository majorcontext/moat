package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andybons/moat/internal/config"
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

	jsonBytes, err := GenerateSettings(settings, "/moat/claude-plugins")
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
	if market.Source.Path != "/moat/claude-plugins/marketplaces/market" {
		t.Errorf("market.Source.Path = %q, want %q", market.Source.Path, "/moat/claude-plugins/marketplaces/market")
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
	jsonBytes, err := GenerateSettings(nil, "/moat/claude-plugins")
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
	if github.Env["GITHUB_TOKEN"] != ProxyInjectedPlaceholder {
		t.Errorf("github.Env[GITHUB_TOKEN] = %q, want %q", github.Env["GITHUB_TOKEN"], ProxyInjectedPlaceholder)
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

	if result.MCPServers["github"].Env["GITHUB_TOKEN"] != ProxyInjectedPlaceholder {
		t.Error("github token should be injected even with scoped grant")
	}
}

func TestGenerateContainerConfig(t *testing.T) {
	settings := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin@market": true,
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

	result, err := GenerateContainerConfig(settings, cfg, "/moat/claude-plugins", nil)
	if err != nil {
		t.Fatalf("GenerateContainerConfig: %v", err)
	}
	defer result.Cleanup()

	// Check settings.json was created
	if result.SettingsPath == "" {
		t.Error("SettingsPath should be set")
	}
	if _, err := os.Stat(result.SettingsPath); err != nil {
		t.Errorf("settings.json should exist: %v", err)
	}

	// Check .mcp.json was created
	if result.MCPConfigPath == "" {
		t.Error("MCPConfigPath should be set when MCP servers configured")
	}
	if _, err := os.Stat(result.MCPConfigPath); err != nil {
		t.Errorf(".mcp.json should exist: %v", err)
	}

	// Verify settings.json content
	settingsContent, err := os.ReadFile(result.SettingsPath)
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
	}

	result, err := GenerateContainerConfig(settings, nil, "/moat/claude-plugins", nil)
	if err != nil {
		t.Fatalf("GenerateContainerConfig: %v", err)
	}
	defer result.Cleanup()

	// Settings should be created
	if result.SettingsPath == "" {
		t.Error("SettingsPath should be set")
	}

	// MCP config should NOT be created
	if result.MCPConfigPath != "" {
		t.Error("MCPConfigPath should be empty when no MCP servers configured")
	}
}

func TestGeneratedConfigCleanup(t *testing.T) {
	settings := &Settings{}
	result, err := GenerateContainerConfig(settings, nil, "/moat/claude-plugins", nil)
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
	settings := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin@market": true,
		},
	}

	generatedConfig := &GeneratedConfig{
		SettingsPath: "/tmp/moat-claude-123/settings.json",
	}

	mounts := RequiredMounts(settings, generatedConfig, "/home/user/.moat/claude/plugins", "/home/container")

	// Should have plugin cache mount
	var hasCacheMount bool
	for _, m := range mounts {
		if strings.Contains(m.Target, "claude-plugins") {
			hasCacheMount = true
			if !m.ReadOnly {
				t.Error("plugin cache mount should be read-only")
			}
		}
	}
	if !hasCacheMount {
		t.Error("should have plugin cache mount")
	}

	// Should have settings mount
	var hasSettingsMount bool
	for _, m := range mounts {
		if strings.Contains(m.Target, "settings.json") {
			hasSettingsMount = true
			if m.Target != filepath.Join("/home/container", ".claude", "settings.json") {
				t.Errorf("settings mount target = %q, want %q", m.Target, filepath.Join("/home/container", ".claude", "settings.json"))
			}
		}
	}
	if !hasSettingsMount {
		t.Error("should have settings mount")
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
		SettingsPath: "/tmp/settings.json",
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
