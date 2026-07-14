package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeSettings_HostSettingsCarriedOver(t *testing.T) {
	// Create a fake home with settings.json
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	hostSettings := map[string]any{
		"theme": "dim",
		"mouse": false,
		"tabs":  map[string]any{"enabled": true},
	}
	data, _ := json.Marshal(hostSettings)
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil settings output")
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// All allowed keys should be present
	for _, key := range []string{"theme", "mouse", "tabs"} {
		if _, ok := got[key]; !ok {
			t.Errorf("expected key %q in merged settings", key)
		}
	}
}

func TestMergeSettings_UsesCopilotHomeWhenSet(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	homeCopilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(homeCopilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	homeData, _ := json.Marshal(map[string]any{"theme": "dim"})
	if err := os.WriteFile(filepath.Join(homeCopilotDir, "settings.json"), homeData, 0o600); err != nil {
		t.Fatal(err)
	}

	copilotHome := t.TempDir()
	t.Setenv("COPILOT_HOME", copilotHome)
	copilotHomeData, _ := json.Marshal(map[string]any{"theme": "high-contrast"})
	if err := os.WriteFile(filepath.Join(copilotHome, "settings.json"), copilotHomeData, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	var theme string
	if err := json.Unmarshal(got["theme"], &theme); err != nil {
		t.Fatalf("unmarshal theme: %v", err)
	}
	if theme != "high-contrast" {
		t.Errorf("theme = %q, want %q", theme, "high-contrast")
	}
}

func TestMergeSettings_CopilotHomeDoesNotFallBackToDefaultHome(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	homeCopilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(homeCopilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	homeData, _ := json.Marshal(map[string]any{"theme": "dim"})
	if err := os.WriteFile(filepath.Join(homeCopilotDir, "settings.json"), homeData, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("COPILOT_HOME", t.TempDir())

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil when COPILOT_HOME has no settings file, got %s", result)
	}
}

func TestMergeSettings_LegacyColorModeNormalizedToTheme(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"colorMode": "high-contrast"})
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if _, ok := got["colorMode"]; ok {
		t.Error("legacy colorMode should be normalized to theme")
	}
	var theme string
	if err := json.Unmarshal(got["theme"], &theme); err != nil {
		t.Fatalf("unmarshal theme: %v", err)
	}
	if theme != "high-contrast" {
		t.Errorf("theme = %q, want %q", theme, "high-contrast")
	}
}

func TestMergeSettings_ThemePreferredOverLegacyColorMode(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{
		"theme":     "github",
		"colorMode": "dim",
	})
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	var theme string
	if err := json.Unmarshal(got["theme"], &theme); err != nil {
		t.Fatalf("unmarshal theme: %v", err)
	}
	if theme != "github" {
		t.Errorf("theme = %q, want %q", theme, "github")
	}
	if _, ok := got["colorMode"]; ok {
		t.Error("legacy colorMode should not be written when theme is present")
	}
}

func TestMergeSettings_MoatLegacyColorModeOverridesHostTheme(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	moatHome := t.TempDir()
	t.Setenv("MOAT_HOME", moatHome)

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hostData, _ := json.Marshal(map[string]any{"theme": "github"})
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), hostData, 0o600); err != nil {
		t.Fatal(err)
	}

	moatCopilotDir := filepath.Join(moatHome, "copilot")
	if err := os.MkdirAll(moatCopilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	moatData, _ := json.Marshal(map[string]any{"colorMode": "dim"})
	if err := os.WriteFile(filepath.Join(moatCopilotDir, "settings.json"), moatData, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	var theme string
	if err := json.Unmarshal(got["theme"], &theme); err != nil {
		t.Fatalf("unmarshal theme: %v", err)
	}
	if theme != "dim" {
		t.Errorf("theme = %q, want %q", theme, "dim")
	}
}

func TestMergeSettings_DisallowedKeysFiltered(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	hostSettings := map[string]any{
		"theme":          "dim",
		"proxyUrl":       "http://evil.proxy:8080",
		"allowedUrls":    []string{"https://example.com"},
		"deniedUrls":     []string{"https://bad.com"},
		"notifications":  true,
		"hooks":          map[string]any{"pre-commit": "echo pwned"},
		"trustedFolders": []string{"/home/user/secrets"},
	}
	data, _ := json.Marshal(hostSettings)
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil settings (theme is allowed)")
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// theme should be present
	if _, ok := got["theme"]; !ok {
		t.Error("expected theme in result")
	}

	// Disallowed keys must not appear
	for _, key := range []string{"proxyUrl", "allowedUrls", "deniedUrls", "notifications", "hooks", "trustedFolders"} {
		if _, ok := got[key]; ok {
			t.Errorf("disallowed key %q should not be in merged settings", key)
		}
	}
}

func TestMergeSettings_MoatOverridesHost(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	moatHome := t.TempDir()
	t.Setenv("MOAT_HOME", moatHome)

	// Host settings
	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hostData, _ := json.Marshal(map[string]any{"theme": "github", "mouse": true})
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), hostData, 0o600); err != nil {
		t.Fatal(err)
	}

	// Moat overrides
	moatCopilotDir := filepath.Join(moatHome, "copilot")
	if err := os.MkdirAll(moatCopilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	moatData, _ := json.Marshal(map[string]any{"theme": "high-contrast"})
	if err := os.WriteFile(filepath.Join(moatCopilotDir, "settings.json"), moatData, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Moat override should win
	var theme string
	if err := json.Unmarshal(got["theme"], &theme); err != nil {
		t.Fatalf("unmarshal theme: %v", err)
	}
	if theme != "high-contrast" {
		t.Errorf("theme = %q, want %q", theme, "high-contrast")
	}

	// Host-only key should also be present
	if _, ok := got["mouse"]; !ok {
		t.Error("expected mouse from host settings")
	}
}

func TestMergeSettings_CLIFlagStripsModelFields(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	hostSettings := map[string]any{
		"model": "gpt-5.4",
		"theme": "dim",
	}
	data, _ := json.Marshal(hostSettings)
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{
		ModelOverride: "claude-opus-4.6",
	})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// model should be stripped (CLI flag wins)
	if _, ok := got["model"]; ok {
		t.Error("model should be stripped when ModelOverride is set")
	}

	// Non-model settings should remain
	if _, ok := got["theme"]; !ok {
		t.Error("expected theme to remain")
	}
}

func TestMergeSettings_ModelPreservedWhenNoCLIOverride(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	hostSettings := map[string]any{
		"model": "gpt-5.4",
	}
	data, _ := json.Marshal(hostSettings)
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	// No CLI overrides
	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if _, ok := got["model"]; !ok {
		t.Error("model should be preserved when no CLI override")
	}
}

func TestMergeSettings_NilWhenNoFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MOAT_HOME", t.TempDir())

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil when no settings files exist, got %s", result)
	}
}

func TestMergeSettings_SkipEnvVar(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())
	t.Setenv("MOAT_SKIP_HOST_COPILOT_SETTINGS", "1")

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"theme": "dim"})
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}
	if result != nil {
		t.Error("expected nil when MOAT_SKIP_HOST_COPILOT_SETTINGS=1")
	}
}

func TestMergeSettings_MalformedJSONIgnored(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write invalid JSON
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), []byte("{not valid json!!!"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings should not error on malformed JSON: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for malformed JSON, got %s", result)
	}
}

func TestMergeSettings_ModelStrippedOthersPreserved(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	hostSettings := map[string]any{
		"model":       "gpt-5.4",
		"effortLevel": "high",
		"contextTier": "long_context",
		"theme":       "dim",
	}
	data, _ := json.Marshal(hostSettings)
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{
		ModelOverride: "claude-opus-4.6",
	})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// model should be stripped (CLI override)
	if _, ok := got["model"]; ok {
		t.Error("model should be stripped when ModelOverride is set")
	}

	// effortLevel and contextTier pass through when their overrides are unset
	if _, ok := got["effortLevel"]; !ok {
		t.Error("effortLevel should be preserved")
	}
	if _, ok := got["contextTier"]; !ok {
		t.Error("contextTier should be preserved")
	}

	// theme should always be present
	if _, ok := got["theme"]; !ok {
		t.Error("expected theme to remain")
	}
}

func TestMergeSettings_ContextTierStrippedWhenOverridden(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	hostSettings := map[string]any{
		"contextTier": "long_context",
		"theme":       "dim",
	}
	data, _ := json.Marshal(hostSettings)
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{
		ContextOverride: "default",
	})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if _, ok := got["contextTier"]; ok {
		t.Error("contextTier should be stripped when ContextOverride is set")
	}
	if _, ok := got["theme"]; !ok {
		t.Error("expected theme to remain")
	}
}

func TestMergeSettings_EffortLevelStrippedWhenOverridden(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	hostSettings := map[string]any{
		"effortLevel": "high",
		"theme":       "dim",
	}
	data, _ := json.Marshal(hostSettings)
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{
		EffortOverride: "low",
	})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if _, ok := got["effortLevel"]; ok {
		t.Error("effortLevel should be stripped when EffortOverride is set")
	}
	if _, ok := got["theme"]; !ok {
		t.Error("expected theme to remain")
	}
}

func TestMergeSettings_HostStatusLineDropped(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	hostSettings := map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "echo pwned",
		},
		"theme": "dim",
	}
	data, _ := json.Marshal(hostSettings)
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// statusLine from host must NOT appear
	if _, ok := got["statusLine"]; ok {
		t.Error("statusLine from host settings should be dropped (security: executes commands in container)")
	}

	// theme should still be present
	if _, ok := got["theme"]; !ok {
		t.Error("expected theme from host settings")
	}
}

func TestMergeSettings_MoatStatusLinePreserved(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	moatHome := t.TempDir()
	t.Setenv("MOAT_HOME", moatHome)

	// Host has no statusLine
	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hostData, _ := json.Marshal(map[string]any{"theme": "dim"})
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), hostData, 0o600); err != nil {
		t.Fatal(err)
	}

	// Moat settings have statusLine (explicit user opt-in)
	moatCopilotDir := filepath.Join(moatHome, "copilot")
	if err := os.MkdirAll(moatCopilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	moatData, _ := json.Marshal(map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "/home/user/.moat/statusline.sh",
		},
	})
	if err := os.WriteFile(filepath.Join(moatCopilotDir, "settings.json"), moatData, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// statusLine from moat settings SHOULD be present
	if _, ok := got["statusLine"]; !ok {
		t.Error("statusLine from ~/.moat/copilot/settings.json should be preserved")
	}

	// theme from host should also be present
	if _, ok := got["theme"]; !ok {
		t.Error("expected theme from host settings")
	}
}

func TestMergeSettings_StatusLineConflictMoatWins(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	moatHome := t.TempDir()
	t.Setenv("MOAT_HOME", moatHome)

	// Host has statusLine
	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hostData, _ := json.Marshal(map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "echo host-version",
		},
		"theme": "dim",
	})
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), hostData, 0o600); err != nil {
		t.Fatal(err)
	}

	// Moat also has statusLine
	moatCopilotDir := filepath.Join(moatHome, "copilot")
	if err := os.MkdirAll(moatCopilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	moatData, _ := json.Marshal(map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "echo moat-version",
		},
	})
	if err := os.WriteFile(filepath.Join(moatCopilotDir, "settings.json"), moatData, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeSettings(MergeOpts{})
	if err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// statusLine should be moat's version (host's is never considered)
	raw, ok := got["statusLine"]
	if !ok {
		t.Fatal("expected statusLine from moat settings")
	}
	var sl map[string]any
	if err := json.Unmarshal(raw, &sl); err != nil {
		t.Fatalf("unmarshal statusLine: %v", err)
	}
	if sl["command"] != "echo moat-version" {
		t.Errorf("statusLine.command = %q, want %q", sl["command"], "echo moat-version")
	}
}
