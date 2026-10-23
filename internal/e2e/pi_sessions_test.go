//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/run"
)

// Exercise the actual Create mount path without a model call or a Pi install.
// Pi writes JSONL under cwd-specific subdirectories of ~/.pi/agent/sessions.
func TestPiSessionSync_E2E(t *testing.T) {
	moatHome := t.TempDir()
	t.Setenv("MOAT_HOME", moatHome)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	noSandbox := true
	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &noSandbox})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	workspace, otherWorkspace := createTestWorkspace(t), createTestWorkspace(t)
	on, off := true, false
	for _, tc := range []struct {
		name      string
		workspace string
		sync      *bool
		existing  bool
		wantFiles int
	}{
		{"enabled", workspace, &on, false, 1},
		{"enabled-again", workspace, &on, true, 1},
		{"other-workspace", otherWorkspace, &on, false, 2},
		{"disabled", workspace, &off, false, 2},
		{"default", workspace, nil, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A different workspace, disabled sync, and the default must each
			// see an empty sessions directory rather than the first run's data.
			script := `set -eu
file="$HOME/.pi/agent/sessions/--workspace--/session-e2e.jsonl"
`
			if tc.existing {
				script += `test -s "$file"` + "\n"
			} else {
				script += `test ! -e "$file"` + "\n"
			}
			script += `
mkdir -p "$(dirname "$file")"
printf '%s\n' '{"type":"session","cwd":"/workspace"}' > "$file"
`
			r, err := mgr.Create(ctx, run.Options{
				Name: "e2e-pi-sync-" + tc.name, Workspace: tc.workspace,
				Config: &config.Config{Agent: "pi", Pi: config.PiConfig{SyncLogs: tc.sync}},
				Cmd:    []string{"sh", "-c", script},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer mgr.Destroy(context.Background(), r.ID)
			if err := mgr.Start(ctx, r.ID); err != nil {
				t.Fatal(err)
			}
			if err := mgr.Wait(ctx, r.ID); err != nil {
				t.Fatal(err)
			}
			if err := mgr.Destroy(ctx, r.ID); err != nil {
				t.Fatal(err)
			}
			matches, err := filepath.Glob(filepath.Join(moatHome, "pi", "sessions", "*", "--workspace--", "session-e2e.jsonl"))
			if err != nil || len(matches) != tc.wantFiles {
				t.Fatalf("host transcripts after removal = %v, %v; want %d", matches, err, tc.wantFiles)
			}
			for _, file := range matches {
				data, err := os.ReadFile(file)
				if err != nil || string(data) != "{\"type\":\"session\",\"cwd\":\"/workspace\"}\n" {
					t.Fatalf("transcript changed: %q, %v", data, err)
				}
			}
		})
	}
}
