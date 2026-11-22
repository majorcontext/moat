//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/run"
)

// TestRunRecordsGuestExitStatus verifies that both runtimes report the
// guest's exit status the same way, for foreground and background starts.
// Apple's inspect output carries no status, so it comes from the launch
// process.
func TestRunRecordsGuestExitStatus(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		for _, interactive := range []bool{false, true} {
			for _, code := range []int{17, 0} {
				t.Run(fmt.Sprintf("interactive=%v/exit=%d", interactive, code), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
					defer cancel()
					mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
					if err != nil {
						t.Fatalf("NewManager: %v", err)
					}
					defer mgr.Close()

					r, err := mgr.Create(ctx, run.Options{
						Name:        "test-exit-status",
						Workspace:   createTestWorkspace(t),
						Cmd:         []string{"sh", "-c", fmt.Sprintf("echo ran; exit %d", code)},
						Interactive: interactive,
					})
					if err != nil {
						t.Fatalf("Create: %v", err)
					}
					defer mgr.Destroy(context.Background(), r.ID)

					var result error
					if interactive {
						var out bytes.Buffer
						result = mgr.StartAttached(ctx, r.ID, nil, &out, &out, container.TTYSize{})
					} else {
						if err := mgr.Start(ctx, r.ID); err != nil {
							t.Fatalf("Start: %v", err)
						}
						result = mgr.Wait(ctx, r.ID)
					}

					if code == 0 {
						if result != nil || r.GetState() != run.StateStopped {
							t.Errorf("clean exit: result = %v, state = %s; want nil, stopped", result, r.GetState())
						}
						return
					}
					want := fmt.Sprintf("exit code %d", code)
					if result == nil || !strings.Contains(result.Error(), want) {
						t.Errorf("result = %v, want %q", result, want)
					}
					if r.GetState() != run.StateFailed {
						t.Errorf("state = %s, want failed", r.GetState())
					}
				})
			}
		}
	})
}
