package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPiSyncLogsConfig(t *testing.T) {
	for _, value := range []string{"", "true", "false"} {
		t.Run("value="+value, func(t *testing.T) {
			dir := t.TempDir()
			contents := "agent: pi\ngrants: [anthropic, openai, lunaroute]\n"
			if value != "" {
				contents += "pi:\n  sync_logs: " + value + "\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ShouldSyncPiLogs() != (value == "true") {
				t.Fatalf("pi.sync_logs=%q resolved to %v", value, cfg.ShouldSyncPiLogs())
			}
		})
	}
}
