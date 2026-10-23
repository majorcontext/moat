package pi

import (
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/spf13/cobra"
)

func TestPiSyncLogsFlag(t *testing.T) {
	original := piSyncLogsFlag
	t.Cleanup(func() { piSyncLogsFlag = original })
	for _, tc := range []struct {
		name string
		yaml bool
		args []string
		want bool
	}{
		{"default off", false, nil, false},
		{"preserve YAML true", true, nil, true},
		{"enable", false, []string{"--sync-logs"}, true},
		{"disable YAML true", true, []string{"--sync-logs=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := &cobra.Command{Use: "moat"}
			(&Provider{}).RegisterCLI(root)
			cmd, _, err := root.Find([]string{"pi"})
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Pi: config.PiConfig{SyncLogs: &tc.yaml}}
			configurePiLogSync(cmd, cfg)
			if cfg.ShouldSyncPiLogs() != tc.want {
				t.Fatalf("sync_logs = %v, want %v", cfg.ShouldSyncPiLogs(), tc.want)
			}
		})
	}
}
