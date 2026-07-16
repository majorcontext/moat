package moatinit

import (
	"strings"
	"testing"
)

// TestRunFailsClosedWithoutExecPhase pins the incomplete-build contract:
// until the exec-dispatch phase lands, Run must refuse to fall through to
// the user command (which would silently run it as root, skipping the
// privilege-drop contract) — it exits 1 with a loud FATAL instead.
func TestRunFailsClosedWithoutExecPhase(t *testing.T) {
	var stderr strings.Builder
	sys := NewSys()
	ctx := &Context{
		Sys:    sys,
		Cfg:    LoadConfig(sys),
		Argv:   []string{"true"},
		Stderr: &stderr,
	}
	code := Run(ctx)
	if code != 1 {
		t.Errorf("Run() = %d, want 1 (fail closed)", code)
	}
	if !strings.Contains(stderr.String(), "FATAL: moat-init reached the end of its phase list") {
		t.Errorf("missing fail-closed FATAL message, got: %q", stderr.String())
	}
}
