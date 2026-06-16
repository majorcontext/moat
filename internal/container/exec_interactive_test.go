package container

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDockerExecInteractive is an integration test for DockerRuntime.ExecInteractive.
// It requires a running Docker daemon and is skipped when -short is passed.
func TestDockerExecInteractive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	rt, err := NewDockerRuntime(false)
	if err != nil {
		t.Fatalf("failed to create Docker runtime: %v", err)
	}
	defer rt.Close()

	// Start a long-lived container that creates moatuser first.
	// ExecInteractive hard-codes User:"moatuser", so the image must have that user.
	// Alpine does not ship moatuser, so we add it in the container entrypoint.
	containerName := "test-moat-exec-interactive-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	containerID, err := rt.CreateContainer(ctx, Config{
		Name:  containerName,
		Image: "alpine:latest",
		Cmd:   []string{"sh", "-c", "adduser -D moatuser && sleep 60"},
	})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	defer rt.RemoveContainer(ctx, containerID)

	if err := rt.StartContainer(ctx, containerID); err != nil {
		t.Fatalf("StartContainer failed: %v", err)
	}
	defer rt.StopContainer(ctx, containerID)

	t.Run("TTY output contains expected string", func(t *testing.T) {
		var buf bytes.Buffer
		err := rt.ExecInteractive(ctx, containerID, []string{"sh", "-c", "echo HELLO_INTERACTIVE"}, ExecOptions{
			Stdout: &buf,
			TTY:    true,
		})
		if err != nil {
			t.Fatalf("ExecInteractive failed: %v", err)
		}
		// In TTY mode the terminal driver may translate \n → \r\n, so use Contains
		// rather than exact equality.
		if !strings.Contains(buf.String(), "HELLO_INTERACTIVE") {
			t.Errorf("output %q does not contain HELLO_INTERACTIVE", buf.String())
		}
	})

	t.Run("exit code propagation via ExecError", func(t *testing.T) {
		var buf bytes.Buffer
		err := rt.ExecInteractive(ctx, containerID, []string{"sh", "-c", "exit 7"}, ExecOptions{
			Stdout: &buf,
			TTY:    false,
		})
		if err == nil {
			t.Fatal("expected non-nil error for exit code 7, got nil")
		}
		var execErr *ExecError
		if !errors.As(err, &execErr) {
			t.Fatalf("expected *ExecError, got %T: %v", err, err)
		}
		if execErr.ExitCode != 7 {
			t.Errorf("ExitCode = %d, want 7", execErr.ExitCode)
		}
	})
}
