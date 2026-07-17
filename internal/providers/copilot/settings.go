package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
)

// allowedSettings lists the settings keys that are safe and useful to carry
// over from the host into a moat container. Settings that conflict with moat's
// proxy/network layer, don't work inside containers, or pose security concerns
// are excluded.
//
// Note: statusLine is intentionally excluded from host settings because
// statusLine.command executes arbitrary commands inside the container. It is
// only allowed from ~/.moat/copilot/settings.json (explicit user opt-in).
//
// Excluded after analysis:
//   - keepAlive: requires host-level caffeinate/power API — no-op in container
//   - copyOnSelect: requires OSC 52 clipboard write — moat uses its own
//     clipboard bridging (xclip/Ctrl+V), OSC 52 does not reach host clipboard
//   - notifications: requires host notification daemon — unreachable from container
//   - hooks: security concern — arbitrary command execution
//   - proxyUrl, allowedUrls, deniedUrls: moat manages network/proxy
//   - trustedFolders: hardcoded to /workspace
//   - ide.*: no IDE in container
var allowedSettings = map[string]bool{
	"contextTier":         true,
	"effortLevel":         true,
	"footer":              true,
	"includeCoAuthoredBy": true,
	"model":               true,
	"mouse":               true,
	"subagents":           true,
	"tabs":                true,
	"theme":               true,
}

// moatOnlySettings lists settings keys that are allowed only from the
// moat-specific override file (~/.moat/copilot/settings.json), not from the
// host's ~/.copilot/settings.json. These settings can execute arbitrary
// commands or have security implications that require explicit user opt-in.
var moatOnlySettings = map[string]bool{
	"statusLine": true,
}

// loadHostSettings reads Copilot's host user settings from COPILOT_HOME when
// set, or ~/.copilot/settings.json otherwise.
func loadHostSettings() (map[string]json.RawMessage, error) {
	if copilotHome := os.Getenv("COPILOT_HOME"); copilotHome != "" {
		return loadSettingsFile(filepath.Join(copilotHome, "settings.json"))
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Debug("cannot determine home directory, skipping host Copilot settings",
			"error", err,
		)
		return nil, nil
	}
	return loadSettingsFile(filepath.Join(homeDir, ".copilot", "settings.json"))
}

// loadMoatSettings reads <MOAT_HOME>/copilot/settings.json for moat-specific
// user overrides.
func loadMoatSettings() (map[string]json.RawMessage, error) {
	return loadSettingsFile(filepath.Join(config.GlobalConfigDir(), "copilot", "settings.json"))
}

// loadSettingsFile reads a JSON settings file into a raw map.
// Returns nil, nil if the file does not exist.
func loadSettingsFile(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		log.Warn("ignoring malformed Copilot settings",
			"path", path,
			"error", err,
		)
		return nil, nil
	}
	return raw, nil
}

// normalizeSettingsAliases maps legacy Copilot setting names to their current
// keys before source precedence is applied.
func normalizeSettingsAliases(raw map[string]json.RawMessage) map[string]json.RawMessage {
	if raw == nil {
		return nil
	}
	if _, ok := raw["theme"]; !ok {
		if v, ok := raw["colorMode"]; ok {
			raw["theme"] = v
		}
	}
	delete(raw, "colorMode")
	return raw
}

// MergeOpts provides override values that take precedence over settings.json.
// These correspond to CLI flags or moat.yaml copilot.* fields that are passed
// as --flags to the Copilot CLI (which naturally override settings.json).
type MergeOpts struct {
	ModelOverride   string
	ContextOverride string
	EffortOverride  string
}

// MergeSettings builds the container settings.json by:
// 1. Reading COPILOT_HOME/settings.json or ~/.copilot/settings.json (host defaults)
// 2. Reading <MOAT_HOME>/copilot/settings.json (moat user overrides)
// 3. Filtering to the allowlist
// 4. Stripping settings overridden by moat.yaml/CLI flags
//
// Returns nil, nil when no settings need to be written.
func MergeSettings(opts MergeOpts) ([]byte, error) {
	if os.Getenv("MOAT_SKIP_HOST_COPILOT_SETTINGS") == "1" {
		return nil, nil
	}

	host, err := loadHostSettings()
	if err != nil {
		return nil, err
	}
	host = normalizeSettingsAliases(host)

	moat, err := loadMoatSettings()
	if err != nil {
		return nil, err
	}
	moat = normalizeSettingsAliases(moat)

	if host == nil && moat == nil {
		return nil, nil
	}

	// Start with host, overlay moat overrides
	merged := make(map[string]json.RawMessage)
	for k, v := range host {
		merged[k] = v
	}
	for k, v := range moat {
		merged[k] = v
	}

	// Filter to allowlist
	result := make(map[string]json.RawMessage)
	for k, v := range merged {
		if !allowedSettings[k] {
			continue
		}
		result[k] = v
	}

	// Add moat-only settings (only from ~/.moat/copilot/settings.json)
	for k, v := range moat {
		if moatOnlySettings[k] {
			result[k] = v
		}
	}

	// Strip settings that are overridden by moat.yaml or CLI flags.
	if opts.ModelOverride != "" {
		delete(result, "model")
	}
	if opts.ContextOverride != "" {
		delete(result, "contextTier")
	}
	if opts.EffortOverride != "" {
		delete(result, "effortLevel")
	}

	if len(result) == 0 {
		return nil, nil
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
