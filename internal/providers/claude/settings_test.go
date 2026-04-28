package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/majorcontext/moat/internal/config"
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

func TestLoadSettingsGitHubRepoFormat(t *testing.T) {
	// Claude Code's native settings.json uses {source: github, repo: owner/repo}.
	// LoadSettings must preserve that shape so strictKnownMarketplaces allowlist
	// matching (which compares source/repo and source/url as exact pairs) still
	// works inside the moat container.
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	content := `{
  "enabledPlugins": {
    "superpowers@superpowers-marketplace": true,
    "dev-skills@gp-claude-skills": true,
    "compound-engineering@compound-engineering-plugin": true
  },
  "extraKnownMarketplaces": {
    "superpowers-marketplace": {
      "source": {
        "source": "github",
        "repo": "obra/superpowers-marketplace"
      }
    },
    "gp-claude-skills": {
      "source": {
        "source": "github",
        "repo": "thegpvc/gp-claude-skills"
      }
    },
    "compound-engineering-plugin": {
      "source": {
        "source": "github",
        "repo": "EveryInc/compound-engineering-plugin"
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

	// All three marketplaces should be loaded
	if len(settings.ExtraKnownMarketplaces) != 3 {
		t.Fatalf("ExtraKnownMarketplaces = %d, want 3", len(settings.ExtraKnownMarketplaces))
	}

	// GitHub "repo" format must be preserved verbatim.
	superpowers := settings.ExtraKnownMarketplaces["superpowers-marketplace"]
	if superpowers.Source.Source != "github" {
		t.Errorf("superpowers.Source.Source = %q, want %q", superpowers.Source.Source, "github")
	}
	if superpowers.Source.Repo != "obra/superpowers-marketplace" {
		t.Errorf("superpowers.Source.Repo = %q, want %q", superpowers.Source.Repo, "obra/superpowers-marketplace")
	}
	if superpowers.Source.URL != "" {
		t.Errorf("superpowers.Source.URL should be empty for github source, got %q", superpowers.Source.URL)
	}

	gpSkills := settings.ExtraKnownMarketplaces["gp-claude-skills"]
	if gpSkills.Source.Source != "github" || gpSkills.Source.Repo != "thegpvc/gp-claude-skills" {
		t.Errorf("gp-claude-skills source = %+v, want {github, thegpvc/gp-claude-skills}", gpSkills.Source)
	}

	compound := settings.ExtraKnownMarketplaces["compound-engineering-plugin"]
	if compound.Source.Source != "github" || compound.Source.Repo != "EveryInc/compound-engineering-plugin" {
		t.Errorf("compound source = %+v, want {github, EveryInc/compound-engineering-plugin}", compound.Source)
	}

	// All plugins should be loaded
	if len(settings.EnabledPlugins) != 3 {
		t.Errorf("EnabledPlugins = %d, want 3", len(settings.EnabledPlugins))
	}
}

func TestLoadSettingsInvalidRepoFormat(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	content := `{
  "extraKnownMarketplaces": {
    "malicious": {
      "source": {
        "source": "github",
        "repo": "owner/repo; rm -rf /"
      }
    },
    "valid": {
      "source": {
        "source": "github",
        "repo": "owner/valid-repo"
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

	// Malicious entry should be removed, valid one preserved as-is.
	if _, ok := settings.ExtraKnownMarketplaces["malicious"]; ok {
		t.Error("malicious marketplace should have been removed")
	}

	if len(settings.ExtraKnownMarketplaces) != 1 {
		t.Errorf("ExtraKnownMarketplaces = %d, want 1", len(settings.ExtraKnownMarketplaces))
	}

	valid := settings.ExtraKnownMarketplaces["valid"]
	if valid.Source.Source != "github" {
		t.Errorf("valid.Source.Source = %q, want %q", valid.Source.Source, "github")
	}
	if valid.Source.Repo != "owner/valid-repo" {
		t.Errorf("valid.Source.Repo = %q, want %q", valid.Source.Repo, "owner/valid-repo")
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

	// Create moat.yaml config with overrides
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

	// project-plugin should be disabled (moat.yaml override)
	if result.EnabledPlugins["project-plugin@market"] {
		t.Error("project-plugin@market should be disabled by moat.yaml override")
	}

	// agent-plugin should be enabled
	if !result.EnabledPlugins["agent-plugin@market"] {
		t.Error("agent-plugin@market should be enabled")
	}
}

func TestLoadAllSettingsSkipHostSettings(t *testing.T) {
	// Set up a fake home with host-level settings that should be skipped.
	fakeHome := t.TempDir()
	claudeDir := filepath.Join(fakeHome, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	hostSettings := `{
  "enabledPlugins": { "host-plugin@host-market": true },
  "extraKnownMarketplaces": {
    "host-market": { "source": { "source": "git", "url": "https://example.com/host.git" } }
  }
}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(hostSettings), 0644); err != nil {
		t.Fatal(err)
	}

	// Set up workspace with project settings that should still load.
	workspace := t.TempDir()
	projClaudeDir := filepath.Join(workspace, ".claude")
	if err := os.MkdirAll(projClaudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	projSettings := `{ "enabledPlugins": { "proj-plugin@proj-market": true } }`
	if err := os.WriteFile(filepath.Join(projClaudeDir, "settings.json"), []byte(projSettings), 0644); err != nil {
		t.Fatal(err)
	}

	// Redirect HOME so LoadAllSettings would find the host settings.
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", "")

	// Without skip: host settings should be loaded.
	t.Setenv("MOAT_SKIP_HOST_CLAUDE_SETTINGS", "")
	result, err := LoadAllSettings(workspace, nil)
	if err != nil {
		t.Fatalf("LoadAllSettings: %v", err)
	}
	if !result.EnabledPlugins["host-plugin@host-market"] {
		t.Error("without skip: host-plugin should be loaded")
	}
	if !result.EnabledPlugins["proj-plugin@proj-market"] {
		t.Error("without skip: proj-plugin should be loaded")
	}

	// With skip: host settings should be skipped, project settings still load.
	t.Setenv("MOAT_SKIP_HOST_CLAUDE_SETTINGS", "1")
	result, err = LoadAllSettings(workspace, nil)
	if err != nil {
		t.Fatalf("LoadAllSettings: %v", err)
	}
	if result.EnabledPlugins["host-plugin@host-market"] {
		t.Error("with skip: host-plugin should NOT be loaded")
	}
	if _, ok := result.ExtraKnownMarketplaces["host-market"]; ok {
		t.Error("with skip: host-market should NOT be loaded")
	}
	if !result.EnabledPlugins["proj-plugin@proj-market"] {
		t.Error("with skip: proj-plugin should still be loaded")
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

	// github source should be preserved as {source: github, repo: owner/repo}
	ghMarket := settings.ExtraKnownMarketplaces["github-market"]
	if ghMarket.Source.Source != "github" {
		t.Errorf("github-market.Source.Source = %q, want %q", ghMarket.Source.Source, "github")
	}
	if ghMarket.Source.Repo != "acme/plugins" {
		t.Errorf("github-market.Source.Repo = %q, want %q", ghMarket.Source.Repo, "acme/plugins")
	}
	if ghMarket.Source.URL != "" {
		t.Errorf("github-market.Source.URL should be empty, got %q", ghMarket.Source.URL)
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
			"plugin-a@market-1": true,
			"plugin-b@market-1": true,
			"plugin-c@market-2": true,
			"plugin-d@market-3": false,
			"plugin-no-market":  true, // No @ separator
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

func TestLoadKnownMarketplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_marketplaces.json")

	// Content matching real ~/.claude/plugins/known_marketplaces.json format
	content := `{
  "claude-plugins-official": {
    "source": {
      "source": "github",
      "repo": "anthropics/claude-plugins-official"
    },
    "installLocation": "/Users/test/.claude/plugins/marketplaces/claude-plugins-official",
    "lastUpdated": "2026-01-24T00:50:41.204Z"
  },
  "aws-agent-skills": {
    "source": {
      "source": "github",
      "repo": "itsmostafa/aws-agent-skills"
    },
    "installLocation": "/Users/test/.claude/plugins/marketplaces/aws-agent-skills",
    "lastUpdated": "2026-01-24T00:50:43.196Z"
  }
}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := LoadKnownMarketplaces(path)
	if err != nil {
		t.Fatalf("LoadKnownMarketplaces: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("got %d marketplaces, want 2", len(result))
	}

	// github sources must be preserved as {source: github, repo: owner/repo}
	// so strictKnownMarketplaces matching against the host's registration form works.
	official := result["claude-plugins-official"]
	if official.Source.Source != "github" {
		t.Errorf("official.Source.Source = %q, want %q", official.Source.Source, "github")
	}
	if official.Source.Repo != "anthropics/claude-plugins-official" {
		t.Errorf("official.Source.Repo = %q, want %q", official.Source.Repo, "anthropics/claude-plugins-official")
	}
	if official.Source.URL != "" {
		t.Errorf("official.Source.URL should be empty for github source, got %q", official.Source.URL)
	}

	aws := result["aws-agent-skills"]
	if aws.Source.Source != "github" || aws.Source.Repo != "itsmostafa/aws-agent-skills" {
		t.Errorf("aws source = %+v, want {github, itsmostafa/aws-agent-skills}", aws.Source)
	}
}

func TestLoadKnownMarketplacesNotFound(t *testing.T) {
	result, err := LoadKnownMarketplaces("/nonexistent/known_marketplaces.json")
	if err != nil {
		t.Fatalf("LoadKnownMarketplaces should not error for missing file: %v", err)
	}
	if result != nil {
		t.Error("Expected nil result for missing file")
	}
}

func TestLoadKnownMarketplacesGitSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_marketplaces.json")

	// Test "git" source type (direct URL instead of "github" shorthand)
	content := `{
  "custom-marketplace": {
    "source": {
      "source": "git",
      "url": "https://gitlab.com/org/plugins.git"
    },
    "installLocation": "/Users/test/.claude/plugins/marketplaces/custom-marketplace",
    "lastUpdated": "2026-01-24T00:50:41.204Z"
  }
}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := LoadKnownMarketplaces(path)
	if err != nil {
		t.Fatalf("LoadKnownMarketplaces: %v", err)
	}

	if len(result) != 1 {
		t.Errorf("got %d marketplaces, want 1", len(result))
	}

	custom := result["custom-marketplace"]
	if custom.Source.Source != "git" {
		t.Errorf("custom.Source.Source = %q, want %q", custom.Source.Source, "git")
	}
	if custom.Source.URL != "https://gitlab.com/org/plugins.git" {
		t.Errorf("custom.Source.URL = %q, want %q", custom.Source.URL, "https://gitlab.com/org/plugins.git")
	}
}

func TestLoadKnownMarketplacesMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_marketplaces.json")

	// Invalid JSON
	content := `{ invalid json }`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadKnownMarketplaces(path)
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestLoadKnownMarketplacesInvalidRepoFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_marketplaces.json")

	// Repo with shell injection attempt (should be skipped)
	content := `{
  "malicious": {
    "source": {
      "source": "github",
      "repo": "owner/repo; rm -rf /"
    },
    "installLocation": "/test",
    "lastUpdated": "2026-01-24T00:50:41.204Z"
  },
  "valid": {
    "source": {
      "source": "github",
      "repo": "owner/valid-repo"
    },
    "installLocation": "/test",
    "lastUpdated": "2026-01-24T00:50:41.204Z"
  }
}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := LoadKnownMarketplaces(path)
	if err != nil {
		t.Fatalf("LoadKnownMarketplaces: %v", err)
	}

	// Malicious entry should be skipped, valid one kept
	if len(result) != 1 {
		t.Errorf("got %d marketplaces, want 1 (malicious skipped)", len(result))
	}

	if _, ok := result["malicious"]; ok {
		t.Error("malicious marketplace should have been skipped")
	}

	if _, ok := result["valid"]; !ok {
		t.Error("valid marketplace should be present")
	}
}

func TestLoadKnownMarketplacesEmptyURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_marketplaces.json")

	// Entry with empty repo (should be skipped)
	content := `{
  "empty-repo": {
    "source": {
      "source": "github",
      "repo": ""
    },
    "installLocation": "/test",
    "lastUpdated": "2026-01-24T00:50:41.204Z"
  },
  "empty-url": {
    "source": {
      "source": "git",
      "url": ""
    },
    "installLocation": "/test",
    "lastUpdated": "2026-01-24T00:50:41.204Z"
  }
}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := LoadKnownMarketplaces(path)
	if err != nil {
		t.Fatalf("LoadKnownMarketplaces: %v", err)
	}

	// Both entries should be skipped (empty URLs)
	if len(result) != 0 {
		t.Errorf("got %d marketplaces, want 0 (empty URLs skipped)", len(result))
	}
}

func TestMergeSettingsRawExtras(t *testing.T) {
	// Extras from moat-user source should be preserved.
	base := &Settings{
		EnabledPlugins: map[string]bool{"plugin@market": true},
	}
	override := &Settings{
		RawExtras: map[string]json.RawMessage{
			"statusLine": json.RawMessage(`{"command":"date"}`),
		},
	}

	result := MergeSettings(base, override, SourceMoatUser)
	if result.RawExtras == nil {
		t.Fatal("RawExtras should be preserved from moat-user source")
	}
	if _, ok := result.RawExtras["statusLine"]; !ok {
		t.Error("statusLine should be in RawExtras")
	}
}

func TestMergeSettingsRawExtrasDroppedFromNonMoatSources(t *testing.T) {
	// Extras from non-moat sources should be dropped.
	base := &Settings{}
	override := &Settings{
		RawExtras: map[string]json.RawMessage{
			"statusLine": json.RawMessage(`{"command":"date"}`),
		},
	}

	for _, source := range []SettingSource{SourceClaudeUser, SourceProject, SourceMoatYAML} {
		result := MergeSettings(base, override, source)
		if len(result.RawExtras) > 0 {
			t.Errorf("RawExtras should be dropped for source %s", source)
		}
	}
}

func TestMergeSettingsPreservesBaseExtrasWhenOverrideIsNonMoat(t *testing.T) {
	// Base extras (from a prior moat-user merge) should survive when
	// the override comes from a non-moat source (e.g., project settings).
	base := &Settings{
		RawExtras: map[string]json.RawMessage{
			"fromMoatUser": json.RawMessage(`"kept"`),
		},
	}
	override := &Settings{
		EnabledPlugins: map[string]bool{"project-plugin@market": true},
		RawExtras: map[string]json.RawMessage{
			"fromProject": json.RawMessage(`"dropped"`),
		},
	}

	result := MergeSettings(base, override, SourceProject)
	if _, ok := result.RawExtras["fromMoatUser"]; !ok {
		t.Error("base extras from prior moat-user merge should be preserved")
	}
	if _, ok := result.RawExtras["fromProject"]; ok {
		t.Error("override extras from project source should be dropped")
	}
}

func TestLoadSettingsPreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	content := `{
  "enabledPlugins": {
    "plugin@market": true
  },
  "statusLine": {
    "command": "node /home/user/.claude/moat/statusline.js"
  },
  "customUnknownField": "preserved"
}`
	if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	settings, err := LoadSettings(settingsPath)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}

	// Known fields should be parsed normally
	if !settings.EnabledPlugins["plugin@market"] {
		t.Error("plugin@market should be enabled")
	}

	// Unknown fields should be captured in RawExtras
	if settings.RawExtras == nil {
		t.Fatal("RawExtras should not be nil")
	}
	if _, ok := settings.RawExtras["statusLine"]; !ok {
		t.Error("statusLine should be in RawExtras")
	}
	if _, ok := settings.RawExtras["customUnknownField"]; !ok {
		t.Error("customUnknownField should be in RawExtras")
	}

	// Known fields should NOT appear in RawExtras
	if _, ok := settings.RawExtras["enabledPlugins"]; ok {
		t.Error("enabledPlugins should not be in RawExtras (it's a known field)")
	}
}

func TestSettingsRoundTripWithExtras(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	content := `{
  "enabledPlugins": {
    "plugin@market": true
  },
  "statusLine": {
    "command": "node ~/.claude/moat/statusline.js"
  },
  "customField": 42
}`
	if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	settings, err := LoadSettings(settingsPath)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}

	// Marshal back to JSON
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	// Parse the output and verify both known and unknown fields are present
	var output map[string]json.RawMessage
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("Unmarshal output: %v", err)
	}

	if _, ok := output["enabledPlugins"]; !ok {
		t.Error("enabledPlugins should be in output")
	}
	if _, ok := output["statusLine"]; !ok {
		t.Error("statusLine should be in output")
	}
	if _, ok := output["customField"]; !ok {
		t.Error("customField should be in output")
	}
}

func TestLoadAllSettingsPreservesMoatUserExtras(t *testing.T) {
	// Set up fake home with moat-user settings containing unknown fields.
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", "")
	t.Setenv("MOAT_SKIP_HOST_CLAUDE_SETTINGS", "")

	moatClaudeDir := filepath.Join(fakeHome, ".moat", "claude")
	if err := os.MkdirAll(moatClaudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	moatSettings := `{
  "enabledPlugins": { "moat-plugin@market": true },
  "statusLine": { "command": "date" },
  "customSetting": "from-moat-user"
}`
	if err := os.WriteFile(filepath.Join(moatClaudeDir, "settings.json"), []byte(moatSettings), 0644); err != nil {
		t.Fatal(err)
	}

	// Set up workspace with project settings that also have unknown fields.
	workspace := t.TempDir()
	projClaudeDir := filepath.Join(workspace, ".claude")
	if err := os.MkdirAll(projClaudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	projSettings := `{
  "enabledPlugins": { "proj-plugin@market": true },
  "projectOnlySetting": "should-be-dropped"
}`
	if err := os.WriteFile(filepath.Join(projClaudeDir, "settings.json"), []byte(projSettings), 0644); err != nil {
		t.Fatal(err)
	}

	// Apply moat.yaml overrides too.
	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Plugins: map[string]bool{"yaml-plugin@market": true},
		},
	}

	result, err := LoadAllSettings(workspace, cfg)
	if err != nil {
		t.Fatalf("LoadAllSettings: %v", err)
	}

	// All plugins from all sources should be present.
	if !result.EnabledPlugins["moat-plugin@market"] {
		t.Error("moat-plugin should be present")
	}
	if !result.EnabledPlugins["proj-plugin@market"] {
		t.Error("proj-plugin should be present")
	}
	if !result.EnabledPlugins["yaml-plugin@market"] {
		t.Error("yaml-plugin should be present")
	}

	// Moat-user extras should survive all merge layers.
	if result.RawExtras == nil {
		t.Fatal("RawExtras should not be nil")
	}
	if _, ok := result.RawExtras["statusLine"]; !ok {
		t.Error("statusLine from moat-user should survive")
	}
	if _, ok := result.RawExtras["customSetting"]; !ok {
		t.Error("customSetting from moat-user should survive")
	}

	// Project extras should NOT survive.
	if _, ok := result.RawExtras["projectOnlySetting"]; ok {
		t.Error("projectOnlySetting should be dropped (non-moat source)")
	}
}

func TestSettingsMarshalForContainerWrite(t *testing.T) {
	// Simulate what manager.go does: create Settings, set fields, marshal.
	settings := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin@market": true,
		},
		SkipDangerousModePermissionPrompt: true,
		RawExtras: map[string]json.RawMessage{
			"statusLine": json.RawMessage(`{"command":"node /home/user/.claude/moat/statusline.js"}`),
		},
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	// Verify the output is valid JSON with all fields
	var output map[string]json.RawMessage
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("Unmarshal output: %v", err)
	}

	if _, ok := output["enabledPlugins"]; !ok {
		t.Error("enabledPlugins missing from output")
	}
	if _, ok := output["skipDangerousModePermissionPrompt"]; !ok {
		t.Error("skipDangerousModePermissionPrompt missing from output")
	}
	if _, ok := output["statusLine"]; !ok {
		t.Error("statusLine missing from output")
	}
}

func TestSettingsJSONRoundTrip(t *testing.T) {
	// Verify that Settings serializes to valid Claude Code settings.json format
	// and can be loaded back via LoadSettings.
	settings := &Settings{
		EnabledPlugins: map[string]bool{
			"superpowers@superpowers-marketplace": true,
			"dev-skills@gp-claude-skills":         true,
		},
		ExtraKnownMarketplaces: map[string]MarketplaceEntry{
			"superpowers-marketplace": {
				Source: MarketplaceSource{
					Source: "git",
					URL:    "https://github.com/obra/superpowers-marketplace.git",
				},
			},
			"gp-claude-skills": {
				Source: MarketplaceSource{
					Source: "git",
					URL:    "https://github.com/thegpvc/gp-claude-skills.git",
				},
			},
		},
		// Source tracking fields should not be serialized
		PluginSources:      map[string]SettingSource{"superpowers@superpowers-marketplace": SourceClaudeUser},
		MarketplaceSources: map[string]SettingSource{"superpowers-marketplace": SourceClaudeUser},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	// Load back and verify
	loaded, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}

	if len(loaded.EnabledPlugins) != 2 {
		t.Errorf("EnabledPlugins = %d, want 2", len(loaded.EnabledPlugins))
	}
	if !loaded.EnabledPlugins["superpowers@superpowers-marketplace"] {
		t.Error("superpowers plugin should be enabled")
	}

	if len(loaded.ExtraKnownMarketplaces) != 2 {
		t.Errorf("ExtraKnownMarketplaces = %d, want 2", len(loaded.ExtraKnownMarketplaces))
	}
	sp := loaded.ExtraKnownMarketplaces["superpowers-marketplace"]
	if sp.Source.Source != "git" || sp.Source.URL != "https://github.com/obra/superpowers-marketplace.git" {
		t.Errorf("superpowers-marketplace source = %+v, unexpected", sp.Source)
	}

	// Source tracking should not survive serialization
	if loaded.PluginSources != nil {
		t.Error("PluginSources should not be serialized (json:\"-\")")
	}
	if loaded.MarketplaceSources != nil {
		t.Error("MarketplaceSources should not be serialized (json:\"-\")")
	}
}

// TestKnownSettingsKeysMatchesStruct uses reflection to verify that every
// JSON-tagged field on Settings is present in knownSettingsKeys, preventing
// the two from drifting apart silently.
func TestKnownSettingsKeysMatchesStruct(t *testing.T) {
	typ := reflect.TypeOf(Settings{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// Strip options like ",omitempty"
		jsonKey := tag
		if idx := len(tag); idx > 0 {
			if comma := indexOf(tag, ','); comma >= 0 {
				jsonKey = tag[:comma]
			}
		}
		if jsonKey == "-" {
			continue
		}
		if !knownSettingsKeys[jsonKey] {
			t.Errorf("Settings field %s has JSON key %q not present in knownSettingsKeys", field.Name, jsonKey)
		}
	}
}

func indexOf(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// TestMergeSettingsBaseNilDoesNotMutateOverride verifies that MergeSettings
// does not mutate the caller's override struct when base is nil.
func TestMergeSettingsBaseNilDoesNotMutateOverride(t *testing.T) {
	override := &Settings{
		EnabledPlugins: map[string]bool{
			"plugin-a@market": true,
		},
		ExtraKnownMarketplaces: map[string]MarketplaceEntry{
			"market": {Source: MarketplaceSource{Source: "git", URL: "https://example.com/repo.git"}},
		},
	}

	result := MergeSettings(nil, override, SourceMoatYAML)

	// Result should have the data
	if !result.EnabledPlugins["plugin-a@market"] {
		t.Error("result should contain plugin-a@market")
	}

	// Mutating result must not affect override
	result.EnabledPlugins["plugin-b@market"] = true
	result.PluginSources["plugin-b@market"] = SourceProject

	if _, exists := override.EnabledPlugins["plugin-b@market"]; exists {
		t.Error("mutating result should not affect override's EnabledPlugins")
	}
	if override.PluginSources != nil {
		t.Error("override.PluginSources should remain nil after merge")
	}
}
