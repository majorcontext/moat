// Package claude handles Claude Code plugin and settings management.
package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/andybons/moat/internal/config"
)

// SettingSource identifies where a setting came from.
type SettingSource string

const (
	SourceClaudeUser   SettingSource = "~/.claude/settings.json"
	SourceMoatUser     SettingSource = "~/.moat/claude/settings.json"
	SourceProject      SettingSource = ".claude/settings.json"
	SourceAgentYAML    SettingSource = "agent.yaml"
	SourceUnknown      SettingSource = "unknown"
)

// Settings represents Claude's native settings.json format.
// This is the format used by Claude Code in .claude/settings.json files.
type Settings struct {
	// EnabledPlugins maps "plugin-name@marketplace" to enabled/disabled state
	EnabledPlugins map[string]bool `json:"enabledPlugins,omitempty"`

	// ExtraKnownMarketplaces defines additional plugin marketplaces
	ExtraKnownMarketplaces map[string]MarketplaceEntry `json:"extraKnownMarketplaces,omitempty"`

	// PluginSources tracks where each plugin setting came from (not serialized)
	PluginSources map[string]SettingSource `json:"-"`

	// MarketplaceSources tracks where each marketplace setting came from (not serialized)
	MarketplaceSources map[string]SettingSource `json:"-"`
}

// MarketplaceEntry represents a marketplace in Claude's settings format.
type MarketplaceEntry struct {
	Source MarketplaceSource `json:"source"`
}

// MarketplaceSource defines the source location for a marketplace.
type MarketplaceSource struct {
	// Source is the type: "git", "github", or "directory"
	Source string `json:"source"`

	// URL is the git URL (for source: git or github)
	URL string `json:"url,omitempty"`

	// Path is the local directory path (for source: directory)
	Path string `json:"path,omitempty"`
}

// LoadSettings loads a single Claude settings.json file.
// Returns nil, nil if the file doesn't exist.
func LoadSettings(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}

	return &settings, nil
}

// MergeSettings merges two settings objects with override taking precedence.
// This implements the merge rules:
// - enabledPlugins: Union all sources; later overrides earlier for same plugin
// - extraKnownMarketplaces: Union all sources; later overrides earlier for same name
// The overrideSource is used to track where override settings came from.
func MergeSettings(base, override *Settings, overrideSource SettingSource) *Settings {
	if base == nil && override == nil {
		return &Settings{}
	}
	if base == nil {
		// Initialize source tracking for override
		if override != nil && override.PluginSources == nil {
			override.PluginSources = make(map[string]SettingSource)
			for k := range override.EnabledPlugins {
				override.PluginSources[k] = overrideSource
			}
		}
		if override != nil && override.MarketplaceSources == nil {
			override.MarketplaceSources = make(map[string]SettingSource)
			for k := range override.ExtraKnownMarketplaces {
				override.MarketplaceSources[k] = overrideSource
			}
		}
		return override
	}
	if override == nil {
		return base
	}

	result := &Settings{
		EnabledPlugins:         make(map[string]bool),
		ExtraKnownMarketplaces: make(map[string]MarketplaceEntry),
		PluginSources:          make(map[string]SettingSource),
		MarketplaceSources:     make(map[string]SettingSource),
	}

	// Copy base plugins and sources
	for k, v := range base.EnabledPlugins {
		result.EnabledPlugins[k] = v
		if base.PluginSources != nil {
			result.PluginSources[k] = base.PluginSources[k]
		}
	}
	// Override with later values
	for k, v := range override.EnabledPlugins {
		result.EnabledPlugins[k] = v
		result.PluginSources[k] = overrideSource
	}

	// Copy base marketplaces and sources
	for k, v := range base.ExtraKnownMarketplaces {
		result.ExtraKnownMarketplaces[k] = v
		if base.MarketplaceSources != nil {
			result.MarketplaceSources[k] = base.MarketplaceSources[k]
		}
	}
	// Override with later values
	for k, v := range override.ExtraKnownMarketplaces {
		result.ExtraKnownMarketplaces[k] = v
		result.MarketplaceSources[k] = overrideSource
	}

	return result
}

// LoadAllSettings loads and merges settings from all sources.
// Merge precedence (lowest to highest):
// 1. ~/.claude/settings.json (Claude's native user settings)
// 2. ~/.moat/claude/settings.json (user defaults for moat runs)
// 3. <workspace>/.claude/settings.json (project defaults)
// 4. agent.yaml claude.* fields (run overrides)
func LoadAllSettings(workspacePath string, cfg *config.Config) (*Settings, error) {
	var result *Settings

	homeDir, err := os.UserHomeDir()
	if err == nil {
		// 1. Load Claude's native user settings from ~/.claude/settings.json
		claudeUserSettingsPath := filepath.Join(homeDir, ".claude", "settings.json")
		claudeUserSettings, err := LoadSettings(claudeUserSettingsPath)
		if err != nil {
			return nil, err
		}
		result = MergeSettings(result, claudeUserSettings, SourceClaudeUser)

		// 2. Load moat-specific user defaults from ~/.moat/claude/settings.json
		moatUserSettingsPath := filepath.Join(homeDir, ".moat", "claude", "settings.json")
		moatUserSettings, err := LoadSettings(moatUserSettingsPath)
		if err != nil {
			return nil, err
		}
		result = MergeSettings(result, moatUserSettings, SourceMoatUser)
	}

	// 3. Load project settings from <workspace>/.claude/settings.json
	projectSettingsPath := filepath.Join(workspacePath, ".claude", "settings.json")
	projectSettings, err := LoadSettings(projectSettingsPath)
	if err != nil {
		return nil, err
	}
	result = MergeSettings(result, projectSettings, SourceProject)

	// 4. Apply agent.yaml overrides
	if cfg != nil {
		agentOverrides := ConfigToSettings(cfg)
		result = MergeSettings(result, agentOverrides, SourceAgentYAML)
	}

	// Ensure we always return a non-nil result
	if result == nil {
		result = &Settings{}
	}

	return result, nil
}

// ConfigToSettings converts agent.yaml claude config to Settings format.
func ConfigToSettings(cfg *config.Config) *Settings {
	if cfg == nil {
		return nil
	}

	settings := &Settings{}

	// Convert plugins map
	if len(cfg.Claude.Plugins) > 0 {
		settings.EnabledPlugins = make(map[string]bool)
		for k, v := range cfg.Claude.Plugins {
			settings.EnabledPlugins[k] = v
		}
	}

	// Convert marketplaces
	if len(cfg.Claude.Marketplaces) > 0 {
		settings.ExtraKnownMarketplaces = make(map[string]MarketplaceEntry)
		for name, spec := range cfg.Claude.Marketplaces {
			entry := MarketplaceEntry{
				Source: MarketplaceSource{
					Source: spec.Source,
				},
			}

			switch spec.Source {
			case "github":
				// Convert github owner/repo to git URL
				entry.Source.Source = "git"
				entry.Source.URL = "https://github.com/" + spec.Repo + ".git"
			case "git":
				entry.Source.URL = spec.URL
			case "directory":
				entry.Source.Path = spec.Path
			}

			settings.ExtraKnownMarketplaces[name] = entry
		}
	}

	return settings
}

// HasPluginsOrMarketplaces returns true if the settings contain any plugins or marketplaces.
func (s *Settings) HasPluginsOrMarketplaces() bool {
	if s == nil {
		return false
	}
	return len(s.EnabledPlugins) > 0 || len(s.ExtraKnownMarketplaces) > 0
}

// GetMarketplaceNames returns the names of all marketplaces referenced in settings.
// This includes marketplaces from extraKnownMarketplaces and those inferred from plugin names.
func (s *Settings) GetMarketplaceNames() []string {
	if s == nil {
		return nil
	}

	seen := make(map[string]bool)

	// Add explicit marketplaces
	for name := range s.ExtraKnownMarketplaces {
		seen[name] = true
	}

	// Extract marketplace names from plugin names (format: "plugin@marketplace")
	for plugin := range s.EnabledPlugins {
		if idx := strings.LastIndexByte(plugin, '@'); idx >= 0 {
			marketplace := plugin[idx+1:]
			seen[marketplace] = true
		}
	}

	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	return result
}

