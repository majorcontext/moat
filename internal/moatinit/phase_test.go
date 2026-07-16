package moatinit

import (
	"os"
	"strings"
	"testing"
)

// TestRunDrivesToExecDispatch runs the full pipeline end-to-end (empty
// config: every setup phase no-ops) and asserts it terminates in the exec
// handoff, not the fail-closed FATAL. A fake Exec records the handoff —
// the real one would replace the test process.
func TestRunDrivesToExecDispatch(t *testing.T) {
	ts := newTestSys(t, 1000, false)
	ctx, stderr := newTestContext(ts, Config{Home: "/tmp/h"})
	ctx.Argv = []string{"echo", "hello"}

	code := Run(ctx)
	if code != 0 {
		t.Fatalf("Run() = %d, want 0 (handoff), stderr: %q", code, stderr.String())
	}
	if len(ts.execs) != 1 {
		t.Fatalf("execs = %d, want 1", len(ts.execs))
	}
	if got := strings.Join(ts.execs[0].argv, " "); got != "echo hello" {
		t.Errorf("exec argv = %q", got)
	}
	if strings.Contains(stderr.String(), "FATAL") {
		t.Errorf("pipeline hit the fail-closed FATAL: %q", stderr.String())
	}
}

// TestRunPhaseOrder pins the global ordering invariant (X-ORDER-GLOBAL):
// the fixed top-to-bottom sequence with its hard dependencies — extra-hosts
// first, populate before workspace-mcp-json before pre-run-hook, exec last.
func TestRunPhaseOrder(t *testing.T) {
	names := make([]string, 0, len(phases()))
	for _, p := range phases() {
		names = append(names, p.Name)
	}
	want := []string{
		"extra-hosts",
		"ssh-agent-bridge",
		"claude-staging",
		"codex-staging",
		"gemini-staging",
		"copilot-staging",
		"init-files",
		"clipboard",
		"git-config",
		"docker-setup",
		"named-volume-chown",
		"populate-workspace-volume",
		"workspace-mcp-json",
		"pre-run-hook",
		"exec-dispatch",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("phase order:\n%v\nwant:\n%v", names, want)
	}
}

// TestRunFatalPhaseStopsPipeline asserts a fatal phase error carries its
// exit code out of Run and later phases (including the exec) never run.
func TestRunFatalPhaseStopsPipeline(t *testing.T) {
	ts := newTestSys(t, 0, true)
	// Docker mutex violation: fatal in an early-middle phase.
	ctx, stderr := newTestContext(ts, Config{DockerDIND: "1", DockerGID: "9", Home: "/root"})
	code := Run(ctx)
	if code != 1 {
		t.Fatalf("Run() = %d, want 1", code)
	}
	if len(ts.execs) != 0 {
		t.Error("exec ran after a fatal phase")
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// TestRunPreRunExitCodePassthrough asserts the pre_run hook's literal exit
// code becomes the process exit code (EXEC-06 through the whole pipeline).
func TestRunPreRunExitCodePassthrough(t *testing.T) {
	ts := newTestSys(t, 0, true)
	ts.runHook = func(c Cmd) (int, error) {
		if c.Argv[0] == "gosu" {
			return 42, nil
		}
		return 0, nil
	}
	ctx, _ := newTestContext(ts, Config{PreRun: "exit 42", Home: "/root"})
	if code := Run(ctx); code != 42 {
		t.Errorf("Run() = %d, want the hook's literal 42", code)
	}
	if len(ts.execs) != 0 {
		t.Error("user command exec'd after a failed pre_run hook")
	}
}

func TestExecDispatchNonRootDirect(t *testing.T) {
	// EXEC-11: non-root execs argv directly — no gosu anywhere.
	ts := newTestSys(t, 1000, true)
	ctx, _ := newTestContext(ts, Config{Home: "/tmp/h"})
	ctx.Argv = []string{"id", "-u"}
	if err := execDispatchPhase(ctx); err != errHandoffComplete {
		t.Fatalf("err = %v, want handoff", err)
	}
	if got := strings.Join(ts.execs[0].argv, " "); got != "id -u" {
		t.Errorf("argv = %q", got)
	}
}

func TestExecDispatchRootGosuDrop(t *testing.T) {
	// EXEC-12: root+moatuser prepends the gosu drop.
	ts := newTestSys(t, 0, true)
	ctx, _ := newTestContext(ts, Config{Home: "/root"})
	ctx.Argv = []string{"claude", "--continue"}
	if err := execDispatchPhase(ctx); err != errHandoffComplete {
		t.Fatalf("err = %v, want handoff", err)
	}
	if got := strings.Join(ts.execs[0].argv, " "); got != "gosu moatuser claude --continue" {
		t.Errorf("argv = %q", got)
	}
}

func TestExecDispatchRootNoMoatuserFatal(t *testing.T) {
	// EXEC-13: root without moatuser — exact multi-line fatal, no exec.
	ts := newTestSys(t, 0, false)
	ctx, stderr := newTestContext(ts, Config{Home: "/root"})
	err := execDispatchPhase(ctx)
	exit, ok := err.(exitError)
	if !ok || exit.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}
	if len(ts.execs) != 0 {
		t.Error("user command ran despite the security fatal")
	}
	want := "Error: Container started as root but moatuser does not exist.\n" +
		"This is a security issue - running as root defeats container isolation.\n" +
		"\n" +
		"If you're using a custom image, ensure it creates a 'moatuser' account:\n" +
		"  RUN useradd -m -u 5000 -s /bin/bash moatuser\n" +
		"\n" +
		"Or run the container with a non-root user:\n" +
		"  docker run --user 1000:1000 ...\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestExecDispatchEnvScrub(t *testing.T) {
	// INIT-10 / B-P0: MOAT_INIT_FILES is removed from the exec env on BOTH
	// handoff paths; everything else passes through (parity: only that one
	// variable is scrubbed).
	for _, euid := range []int{0, 1000} {
		ts := newTestSys(t, euid, true)
		ts.env["MOAT_INIT_FILES"] = "secret\tYWJj"
		ts.env["HOME"] = "/home/moatuser"
		ts.env["MOAT_CLAUDE_INIT"] = "/mnt/claude"
		ctx, _ := newTestContext(ts, Config{Home: "/root"})
		if err := execDispatchPhase(ctx); err != errHandoffComplete {
			t.Fatalf("euid=%d: %v", euid, err)
		}
		env := strings.Join(ts.execs[0].env, "\n")
		if strings.Contains(env, "MOAT_INIT_FILES") {
			t.Errorf("euid=%d: MOAT_INIT_FILES leaked into the exec env", euid)
		}
		for _, want := range []string{"HOME=/home/moatuser", "MOAT_CLAUDE_INIT=/mnt/claude"} {
			if !strings.Contains(env, want) {
				t.Errorf("euid=%d: exec env missing %s", euid, want)
			}
		}
	}
}

func TestExecDispatchExecFailureCodes(t *testing.T) {
	// Shell parity for a failed exec: 127 when the command is not found.
	ts := newTestSys(t, 1000, false)
	ts.execErr = os.ErrNotExist
	ctx, stderr := newTestContext(ts, Config{Home: "/tmp/h"})
	ctx.Argv = []string{"no-such-cmd"}
	err := execDispatchPhase(ctx)
	exit, ok := err.(exitError)
	if !ok || exit.code != 127 {
		t.Fatalf("err = %v, want exitError{127}", err)
	}
	if !strings.Contains(stderr.String(), "no-such-cmd") {
		t.Errorf("stderr = %q", stderr.String())
	}

	// Companion: other start failures report 126.
	ts2 := newTestSys(t, 1000, false)
	ts2.execErr = os.ErrPermission
	ctx2, _ := newTestContext(ts2, Config{Home: "/tmp/h"})
	if exit, ok := execDispatchPhase(ctx2).(exitError); !ok || exit.code != 126 {
		t.Fatal("permission failure should map to 126")
	}
}

// TestExecDispatchConsistentDetection pins EXEC-14: the hook dispatch and
// the exec dispatch use the same (euid, moatuser) predicate — for a fixed
// tuple both choose the same branch class.
func TestExecDispatchConsistentDetection(t *testing.T) {
	cases := []struct {
		euid     int
		moatuser bool
		gosu     bool // both hook and exec should use gosu
	}{
		{0, true, true},
		{1000, true, false},
		{1000, false, false},
	}
	for _, tc := range cases {
		ts := newTestSys(t, tc.euid, tc.moatuser)
		ctx, _ := newTestContext(ts, Config{PreRun: "true", Home: "/h"})
		if err := preRunHookPhase(ctx); err != nil {
			t.Fatal(err)
		}
		if err := execDispatchPhase(ctx); err != errHandoffComplete {
			t.Fatal(err)
		}
		hookGosu := len(ts.runs) > 0 && ts.runs[0].Argv[0] == "gosu"
		execGosu := ts.execs[0].argv[0] == "gosu"
		if hookGosu != tc.gosu || execGosu != tc.gosu {
			t.Errorf("euid=%d moatuser=%v: hookGosu=%v execGosu=%v, want %v",
				tc.euid, tc.moatuser, hookGosu, execGosu, tc.gosu)
		}
	}
}
