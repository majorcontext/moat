package run

import (
	"context"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/container"
)

func TestManagerExecInteractive_RunNotFound(t *testing.T) {
	m := &Manager{runs: map[string]*Run{}}
	err := m.ExecInteractive(context.Background(), "run_missing", []string{"claude"}, container.ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v, want a 'not found' error", err)
	}
}

func TestManagerExecInteractive_NotRunning(t *testing.T) {
	r := &Run{ID: "run_stopped", State: StateStopped}
	m := &Manager{runs: map[string]*Run{"run_stopped": r}}
	err := m.ExecInteractive(context.Background(), "run_stopped", []string{"claude"}, container.ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("got %v, want a 'not running' error", err)
	}
}
