# Claude Settings Passthrough Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `~/.moat/claude/settings.json` a full passthrough so unknown fields survive the merge/write cycle, while keeping whitelist filtering for `~/.claude/settings.json`.

**Architecture:** Add a `RawExtras map[string]json.RawMessage` field to `Settings` that captures unknown JSON fields. `LoadSettings` populates it for all sources, but only the moat-user source (`~/.moat/claude/settings.json`) carries extras through to the final written output. `MergeSettings` propagates extras from the moat-user layer. The write path in `manager.go` includes extras when serializing `settings.json` to the container staging directory.

**Tech Stack:** Go, `encoding/json`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/providers/claude/settings.go` | Modify | Add `RawExtras` to `Settings`, custom JSON marshal/unmarshal, merge logic |
| `internal/providers/claude/settings_test.go` | Modify | Tests for extras capture, merge, serialization |
| `internal/run/manager.go` | Verify | Confirm settings write path already handles the new field (it uses `json.MarshalIndent` on `Settings`) |
| `docs/content/guides/01-claude-code.md` | Modify | Document that `~/.moat/claude/settings.json` is a full passthrough |

---

### Task 1: Add RawExtras field and custom unmarshal to Settings

**Files:**
- Modify: `internal/providers/claude/settings.go:30-49`
- Test: `internal/providers/claude/settings_test.go`

The `Settings` struct currently uses standard `json.Unmarshal` which silently drops unknown fields. We need custom unmarshal to capture them, and custom marshal to re-emit them.

- [ ] **Step 1: Write failing test for unknown field capture**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestLoadSettingsPreservesUnknownFields ./internal/providers/claude/ -v`
Expected: FAIL — `RawExtras` field doesn't exist

- [ ] **Step 3: Add RawExtras field to Settings struct**

In `internal/providers/claude/settings.go`, add the field to `Settings`:

```go
// Settings represents Claude's native settings.json format.
// This is the format used by Claude Code in .claude/settings.json files.
type Settings struct {
	// EnabledPlugins maps "plugin-name@marketplace" to enabled/disabled state
	EnabledPlugins map[string]bool `json:"enabledPlugins,omitempty"`

	// ExtraKnownMarketplaces defines additional plugin marketplaces
	ExtraKnownMarketplaces map[string]MarketplaceEntry `json:"extraKnownMarketplaces,omitempty"`

	// SkipDangerousModePermissionPrompt suppresses the bypass-permissions warning
	// that Claude Code shows when launched with --dangerously-skip-permissions.
	// Set to true for container runs since the container provides isolation.
	SkipDangerousModePermissionPrompt bool `json:"skipDangerousModePermissionPrompt,omitempty"`

	// RawExtras holds unknown JSON fields from settings files.
	// Only extras from the moat-user source (~/.moat/claude/settings.json)
	// are preserved through merge and written to the container.
	// This allows users to pass arbitrary Claude Code settings without
	// needing a code change for each new field.
	RawExtras map[string]json.RawMessage `json:"-"`

	// PluginSources tracks where each plugin setting came from (not serialized)
	PluginSources map[string]SettingSource `json:"-"`

	// MarketplaceSources tracks where each marketplace setting came from (not serialized)
	MarketplaceSources map[string]SettingSource `json:"-"`
}
```

- [ ] **Step 4: Implement custom UnmarshalJSON on Settings**

Add below the struct definition in `settings.go`. The set of known keys must match the JSON tags on `Settings` fields.

```go
// knownSettingsKeys lists the JSON keys that map to explicit Settings fields.
// Everything else is captured in RawExtras.
var knownSettingsKeys = map[string]bool{
	"enabledPlugins":                    true,
	"extraKnownMarketplaces":            true,
	"skipDangerousModePermissionPrompt": true,
}

// UnmarshalJSON implements custom unmarshaling to capture unknown fields in RawExtras.
func (s *Settings) UnmarshalJSON(data []byte) error {
	// First, unmarshal known fields using an alias to avoid recursion.
	type settingsAlias Settings
	var alias settingsAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*s = Settings(alias)

	// Then, unmarshal the full object to find unknown keys.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	for key, val := range raw {
		if !knownSettingsKeys[key] {
			if s.RawExtras == nil {
				s.RawExtras = make(map[string]json.RawMessage)
			}
			s.RawExtras[key] = val
		}
	}

	return nil
}
```

- [ ] **Step 5: Implement custom MarshalJSON on Settings**

```go
// MarshalJSON implements custom marshaling that includes RawExtras fields.
func (s Settings) MarshalJSON() ([]byte, error) {
	// Build a map of known fields.
	m := make(map[string]any)

	if len(s.EnabledPlugins) > 0 {
		m["enabledPlugins"] = s.EnabledPlugins
	}
	if len(s.ExtraKnownMarketplaces) > 0 {
		m["extraKnownMarketplaces"] = s.ExtraKnownMarketplaces
	}
	if s.SkipDangerousModePermissionPrompt {
		m["skipDangerousModePermissionPrompt"] = true
	}

	// Merge extras (known fields take precedence if there's a conflict).
	for key, val := range s.RawExtras {
		if !knownSettingsKeys[key] {
			m[key] = val
		}
	}

	return json.Marshal(m)
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test -run TestLoadSettingsPreservesUnknownFields ./internal/providers/claude/ -v`
Expected: PASS

- [ ] **Step 7: Run all existing settings tests to check for regressions**

Run: `go test ./internal/providers/claude/ -v -run 'TestLoadSettings|TestMerge|TestSettingsJSON'`
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
git add internal/providers/claude/settings.go internal/providers/claude/settings_test.go
git commit -m "feat(claude): capture unknown fields in Settings via RawExtras

Add custom JSON marshal/unmarshal to Settings so that unknown fields
from settings.json files are preserved in RawExtras. This prepares
for making ~/.moat/claude/settings.json a full passthrough."
```

---

### Task 2: Propagate RawExtras only from moat-user source through merge

**Files:**
- Modify: `internal/providers/claude/settings.go:204-269` (MergeSettings)
- Modify: `internal/providers/claude/settings.go:271-338` (LoadAllSettings)
- Test: `internal/providers/claude/settings_test.go`

Only extras from `SourceMoatUser` should survive the merge chain. Extras from `~/.claude/settings.json` (host) or project settings should be dropped — those files have historically caused problems when unknown fields leak into containers.

- [ ] **Step 1: Write failing tests for merge propagation**

```go
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
	// This is the critical multi-layer merge scenario from LoadAllSettings:
	// moat-user extras land in base, then project/moat.yaml overrides are applied.
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestMergeSettingsRawExtras' ./internal/providers/claude/ -v`
Expected: FAIL — merge doesn't handle RawExtras yet

- [ ] **Step 3: Update MergeSettings to propagate RawExtras from moat-user only**

In `MergeSettings`, after the existing marketplace merge logic, add:

```go
	// Propagate RawExtras only from the moat-user source.
	// Other sources (host ~/.claude/settings.json, project, moat.yaml)
	// are filtered to known fields only.
	if overrideSource == SourceMoatUser && len(override.RawExtras) > 0 {
		if result.RawExtras == nil {
			result.RawExtras = make(map[string]json.RawMessage)
		}
		for k, v := range override.RawExtras {
			result.RawExtras[k] = v
		}
	}
	// Preserve base extras (from earlier moat-user merge)
	if len(base.RawExtras) > 0 {
		if result.RawExtras == nil {
			result.RawExtras = make(map[string]json.RawMessage)
		}
		for k, v := range base.RawExtras {
			if _, exists := result.RawExtras[k]; !exists {
				result.RawExtras[k] = v
			}
		}
	}
```

Also handle the nil-base case. **Replace** the existing `if base == nil` branch (lines 213-228 of `settings.go`) with the following — it's the same source-tracking logic plus a new RawExtras guard:

```go
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
		// Drop extras from non-moat sources
		if override != nil && overrideSource != SourceMoatUser {
			override.RawExtras = nil
		}
		return override
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run 'TestMergeSettingsRawExtras' ./internal/providers/claude/ -v`
Expected: PASS

- [ ] **Step 5: Run all settings tests**

Run: `go test ./internal/providers/claude/ -v -run 'TestLoadSettings|TestMerge|TestSettingsJSON|TestLoadAll|TestConfig'`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/providers/claude/settings.go internal/providers/claude/settings_test.go
git commit -m "feat(claude): propagate RawExtras only from moat-user settings source

MergeSettings now carries RawExtras through the merge chain only when
the override source is SourceMoatUser. Extras from host ~/.claude/
settings.json, project settings, and moat.yaml are dropped."
```

---

### Task 3: Write integration test for round-trip with extras

**Files:**
- Test: `internal/providers/claude/settings_test.go`

Verify the full flow: load settings with extras → merge → marshal → the output contains both known and unknown fields.

- [ ] **Step 1: Write the round-trip test**

```go
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
```

- [ ] **Step 2: Write end-to-end test for LoadAllSettings with extras**

This tests the critical path: moat-user extras survive through project and moat.yaml merge layers.

```go
func TestLoadAllSettingsPreservesMoatUserExtras(t *testing.T) {
	// Set up fake home with moat-user settings containing unknown fields.
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
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
```

- [ ] **Step 3: Run tests to verify they pass**

Run: `go test -run 'TestSettingsRoundTripWithExtras|TestLoadAllSettingsPreservesMoatUserExtras' ./internal/providers/claude/ -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/providers/claude/settings_test.go
git commit -m "test(claude): add round-trip and end-to-end tests for settings extras"
```

---

### Task 4: Verify manager.go write path and add integration test

**Files:**
- Verify: `internal/run/manager.go:1629-1650`
- Test: `internal/providers/claude/settings_test.go`

The write path in `manager.go` already uses `json.MarshalIndent(claudeSettings, "", "  ")`. Since we added custom `MarshalJSON`, this should automatically include `RawExtras`. Verify with a focused test.

- [ ] **Step 1: Write test simulating the manager write path**

```go
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
```

- [ ] **Step 2: Run test**

Run: `go test -run TestSettingsMarshalForContainerWrite ./internal/providers/claude/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/providers/claude/settings_test.go
git commit -m "test(claude): verify settings marshal includes RawExtras for container write"
```

---

### Task 5: Update documentation

**Files:**
- Modify: `docs/content/guides/01-claude-code.md`

- [ ] **Step 1: Read the current guide**

Run: Read `docs/content/guides/01-claude-code.md` to find the right section.

- [ ] **Step 2: Add documentation about settings passthrough**

Add a section explaining that `~/.moat/claude/settings.json` is a full passthrough — any valid Claude Code setting placed in this file will be forwarded into the container. In contrast, `~/.claude/settings.json` on the host is filtered to known fields only (plugins and marketplaces).

Example content to add:

```markdown
## User settings

`~/.moat/claude/settings.json` is your personal Claude Code settings layer for moat containers. Any field you add to this file is forwarded into the container's `~/.claude/settings.json` as-is — you don't need to wait for moat to add explicit support for new Claude Code settings.

In contrast, moat only reads plugin and marketplace fields from your host `~/.claude/settings.json`. This prevents host-specific settings from leaking into containers.
```

- [ ] **Step 3: Commit**

```bash
git add docs/content/guides/01-claude-code.md
git commit -m "docs(claude): document settings passthrough for ~/.moat/claude/settings.json"
```

---

### Task 6: Lint and final verification

- [ ] **Step 1: Run linter**

Run: `make lint`
Expected: No errors

- [ ] **Step 2: Run full test suite for affected packages**

Run: `go test ./internal/providers/claude/ ./internal/config/ -v -race`
Expected: All PASS

- [ ] **Step 3: Fix any issues and commit**
