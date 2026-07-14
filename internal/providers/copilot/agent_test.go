package copilot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func writeHostSettings(t *testing.T, settings map[string]any) {
	t.Helper()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("MOAT_HOME", t.TempDir())

	copilotDir := filepath.Join(fakeHome, ".copilot")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func stagedSettings(t *testing.T, cfg *provider.ContainerConfig) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cfg.StagingDir, "settings.json"))
	if err != nil {
		t.Fatalf("reading staged settings.json: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal staged settings: %v", err)
	}
	return got
}

func TestPrepareContainerStripsSettingsFromOpts(t *testing.T) {
	writeHostSettings(t, map[string]any{
		"model":       "gpt-5.4",
		"contextTier": "long_context",
		"effortLevel": "high",
		"theme":       "dim",
	})

	cfg, err := (&Provider{}).PrepareContainer(context.Background(), provider.PrepareOpts{
		CopilotModel:           "claude-opus-4.6",
		CopilotContext:         "default",
		CopilotReasoningEffort: "low",
	})
	if err != nil {
		t.Fatalf("PrepareContainer: %v", err)
	}
	t.Cleanup(cfg.Cleanup)

	got := stagedSettings(t, cfg)
	for _, key := range []string{"model", "contextTier", "effortLevel"} {
		if _, ok := got[key]; ok {
			t.Errorf("%s should be stripped when the PrepareOpts override is set", key)
		}
	}
	if _, ok := got["theme"]; !ok {
		t.Error("expected theme to remain")
	}
}

func TestPrepareContainerPreservesSettingsWithoutOverrides(t *testing.T) {
	writeHostSettings(t, map[string]any{
		"model":       "gpt-5.4",
		"contextTier": "long_context",
		"effortLevel": "high",
		"theme":       "dim",
	})

	cfg, err := (&Provider{}).PrepareContainer(context.Background(), provider.PrepareOpts{})
	if err != nil {
		t.Fatalf("PrepareContainer: %v", err)
	}
	t.Cleanup(cfg.Cleanup)

	got := stagedSettings(t, cfg)
	for _, key := range []string{"model", "contextTier", "effortLevel", "theme"} {
		if _, ok := got[key]; !ok {
			t.Errorf("%s should be preserved when no override is set", key)
		}
	}
}
