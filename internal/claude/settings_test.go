package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andybons/moat/internal/config"
)

func TestLoadSettings(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	content := `{
  "enabledPlugins": {
    "typescript-lsp@official": true,
    "debug-tool@acme": false
  },
  "extraKnownMarketplaces": {
    "acme": {
      "source": {
        "source": "git",
        "url": "git@github.com:acme/plugins.git"
      }
    }
  }
}`
	if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	settings, err := LoadSettings(settingsPath)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}

	if len(settings.EnabledPlugins) != 2 {
		t.Errorf("EnabledPlugins = %d, want 2", len(settings.EnabledPlugins))
	}
	if !settings.EnabledPlugins["typescript-lsp@official"] {
		t.Error("typescript-lsp@official should be enabled")
	}
	if settings.EnabledPlugins["debug-tool@acme"] {
		t.Error("debug-tool@acme should be disabled")
	}

	if len(settings.ExtraKnownMarketplaces) != 1 {
		t.Errorf("ExtraKnownMarketplaces = %d, want 1", len(settings.ExtraKnownMarketplaces))
	}
	acme := settings.ExtraKnownMarketplaces["acme"]
	if acme.Source.Source != "git" {
		t.Errorf("acme.Source.Source = %q, want %q", acme.Source.Source, "git")
	}
	if acme.Source.URL != "git@github.com:acme/plugins.git" {
		t.Errorf("acme.Source.URL = %q, want %q", acme.Source.URL, "git@github.com:acme/plugins.git")
	}
}

func TestLoadSettingsNotFound(t *testing.T) {
	settings, err := LoadSettings("/nonexistent/settings.json")
	if err != nil {
		t.Fatalf("LoadSettings should not error for missing file: %v", err)
	}
	if settings != nil {
		t.Error("Expected nil settings for missing file")
	}
}

func TestMergeSettings(t *testing.T) {
	base := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin-a@market-1": true,
			"plugin-b@market-1": true,
		},
		ExtraKnownMarketplaces: map[string]MarketplaceEntry{
			"market-1": {
				Source: MarketplaceSource{
					Source: "git",
					URL:    "https://example.com/market-1.git",
				},
			},
		},
	}

	override := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin-b@market-1": false, // Override existing
			"plugin-c@market-2": true,  // Add new
		},
		ExtraKnownMarketplaces: map[string]MarketplaceEntry{
			"market-2": {
				Source: MarketplaceSource{
					Source: "directory",
					Path:   "/opt/plugins",
				},
			},
		},
	}

	result := MergeSettings(base, override, SourceProject)

	// Check plugins
	if len(result.EnabledPlugins) != 3 {
		t.Errorf("EnabledPlugins = %d, want 3", len(result.EnabledPlugins))
	}
	if !result.EnabledPlugins["plugin-a@market-1"] {
		t.Error("plugin-a@market-1 should be enabled (from base)")
	}
	if result.EnabledPlugins["plugin-b@market-1"] {
		t.Error("plugin-b@market-1 should be disabled (override)")
	}
	if !result.EnabledPlugins["plugin-c@market-2"] {
		t.Error("plugin-c@market-2 should be enabled (from override)")
	}

	// Check marketplaces
	if len(result.ExtraKnownMarketplaces) != 2 {
		t.Errorf("ExtraKnownMarketplaces = %d, want 2", len(result.ExtraKnownMarketplaces))
	}
	if result.ExtraKnownMarketplaces["market-1"].Source.URL != "https://example.com/market-1.git" {
		t.Error("market-1 should be preserved from base")
	}
	if result.ExtraKnownMarketplaces["market-2"].Source.Path != "/opt/plugins" {
		t.Error("market-2 should be added from override")
	}

	// Check source tracking
	if result.PluginSources["plugin-b@market-1"] != SourceProject {
		t.Errorf("plugin-b source = %q, want %q", result.PluginSources["plugin-b@market-1"], SourceProject)
	}
	if result.MarketplaceSources["market-2"] != SourceProject {
		t.Errorf("market-2 source = %q, want %q", result.MarketplaceSources["market-2"], SourceProject)
	}
}

func TestMergeSettingsNilHandling(t *testing.T) {
	t.Run("both nil", func(t *testing.T) {
		result := MergeSettings(nil, nil, SourceUnknown)
		if result == nil {
			t.Fatal("result should not be nil")
		}
	})

	t.Run("base nil", func(t *testing.T) {
		override := &Settings{
			EnabledPlugins: map[string]bool{"plugin@market": true},
		}
		result := MergeSettings(nil, override, SourceProject)
		// When base is nil, override is returned with sources initialized
		if result.EnabledPlugins["plugin@market"] != true {
			t.Error("plugin should be enabled")
		}
		if result.PluginSources["plugin@market"] != SourceProject {
			t.Errorf("source should be %q", SourceProject)
		}
	})

	t.Run("override nil", func(t *testing.T) {
		base := &Settings{
			EnabledPlugins: map[string]bool{"plugin@market": true},
		}
		result := MergeSettings(base, nil, SourceUnknown)
		if result != base {
			t.Error("should return base when override is nil")
		}
	})
}

func TestLoadAllSettings(t *testing.T) {
	// Set up workspace with project settings
	workspace := t.TempDir()
	claudeDir := filepath.Join(workspace, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	projectSettings := `{
  "enabledPlugins": {
    "project-plugin@market": true
  }
}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(projectSettings), 0644); err != nil {
		t.Fatal(err)
	}

	// Create agent.yaml config with overrides
	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Plugins: map[string]bool{
				"project-plugin@market": false, // Override project setting
				"agent-plugin@market":   true,
			},
		},
	}

	result, err := LoadAllSettings(workspace, cfg)
	if err != nil {
		t.Fatalf("LoadAllSettings: %v", err)
	}

	// project-plugin should be disabled (agent.yaml override)
	if result.EnabledPlugins["project-plugin@market"] {
		t.Error("project-plugin@market should be disabled by agent.yaml override")
	}

	// agent-plugin should be enabled
	if !result.EnabledPlugins["agent-plugin@market"] {
		t.Error("agent-plugin@market should be enabled")
	}
}

func TestLoadAllSettingsNoConfig(t *testing.T) {
	workspace := t.TempDir()

	result, err := LoadAllSettings(workspace, nil)
	if err != nil {
		t.Fatalf("LoadAllSettings: %v", err)
	}

	if result == nil {
		t.Fatal("result should not be nil")
	}
}

func TestConfigToSettings(t *testing.T) {
	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Plugins: map[string]bool{
				"plugin-a@market": true,
				"plugin-b@market": false,
			},
			Marketplaces: map[string]config.MarketplaceSpec{
				"github-market": {
					Source: "github",
					Repo:   "acme/plugins",
				},
				"git-market": {
					Source: "git",
					URL:    "git@github.com:org/plugins.git",
				},
				"dir-market": {
					Source: "directory",
					Path:   "/opt/plugins",
				},
			},
		},
	}

	settings := ConfigToSettings(cfg)

	// Check plugins
	if len(settings.EnabledPlugins) != 2 {
		t.Errorf("EnabledPlugins = %d, want 2", len(settings.EnabledPlugins))
	}
	if !settings.EnabledPlugins["plugin-a@market"] {
		t.Error("plugin-a should be enabled")
	}

	// Check marketplaces
	if len(settings.ExtraKnownMarketplaces) != 3 {
		t.Errorf("ExtraKnownMarketplaces = %d, want 3", len(settings.ExtraKnownMarketplaces))
	}

	// github source should be converted to git with HTTPS URL
	ghMarket := settings.ExtraKnownMarketplaces["github-market"]
	if ghMarket.Source.Source != "git" {
		t.Errorf("github-market.Source.Source = %q, want %q", ghMarket.Source.Source, "git")
	}
	if ghMarket.Source.URL != "https://github.com/acme/plugins.git" {
		t.Errorf("github-market.Source.URL = %q, want %q", ghMarket.Source.URL, "https://github.com/acme/plugins.git")
	}

	// git source should be preserved
	gitMarket := settings.ExtraKnownMarketplaces["git-market"]
	if gitMarket.Source.URL != "git@github.com:org/plugins.git" {
		t.Errorf("git-market.Source.URL = %q, want %q", gitMarket.Source.URL, "git@github.com:org/plugins.git")
	}

	// directory source should be preserved
	dirMarket := settings.ExtraKnownMarketplaces["dir-market"]
	if dirMarket.Source.Source != "directory" {
		t.Errorf("dir-market.Source.Source = %q, want %q", dirMarket.Source.Source, "directory")
	}
	if dirMarket.Source.Path != "/opt/plugins" {
		t.Errorf("dir-market.Source.Path = %q, want %q", dirMarket.Source.Path, "/opt/plugins")
	}
}

func TestConfigToSettingsNil(t *testing.T) {
	settings := ConfigToSettings(nil)
	if settings != nil {
		t.Error("ConfigToSettings(nil) should return nil")
	}
}

func TestHasPluginsOrMarketplaces(t *testing.T) {
	tests := []struct {
		name     string
		settings *Settings
		want     bool
	}{
		{
			name:     "nil settings",
			settings: nil,
			want:     false,
		},
		{
			name:     "empty settings",
			settings: &Settings{},
			want:     false,
		},
		{
			name: "has plugins",
			settings: &Settings{
				EnabledPlugins: map[string]bool{"plugin@market": true},
			},
			want: true,
		},
		{
			name: "has marketplaces",
			settings: &Settings{
				ExtraKnownMarketplaces: map[string]MarketplaceEntry{
					"market": {},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.settings.HasPluginsOrMarketplaces()
			if got != tt.want {
				t.Errorf("HasPluginsOrMarketplaces() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetMarketplaceNames(t *testing.T) {
	settings := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin-a@market-1":  true,
			"plugin-b@market-1":  true,
			"plugin-c@market-2":  true,
			"plugin-d@market-3":  false,
			"plugin-no-market":   true, // No @ separator
		},
		ExtraKnownMarketplaces: map[string]MarketplaceEntry{
			"market-1":     {},
			"market-extra": {},
		},
	}

	names := settings.GetMarketplaceNames()

	// Should have: market-1 (from plugins and marketplaces), market-2, market-3, market-extra
	expected := map[string]bool{
		"market-1":     true,
		"market-2":     true,
		"market-3":     true,
		"market-extra": true,
	}

	if len(names) != len(expected) {
		t.Errorf("GetMarketplaceNames() returned %d names, want %d", len(names), len(expected))
	}

	for _, name := range names {
		if !expected[name] {
			t.Errorf("unexpected marketplace name: %s", name)
		}
	}
}

func TestGetMarketplaceNamesNil(t *testing.T) {
	var settings *Settings
	names := settings.GetMarketplaceNames()
	if names != nil {
		t.Error("GetMarketplaceNames() on nil should return nil")
	}
}
