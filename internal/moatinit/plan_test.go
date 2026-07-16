package moatinit

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run: go test ./internal/moatinit -run %s -update): %v", path, t.Name(), err)
	}
	if got != string(want) {
		t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// TestPlanGoldenFull pins the --plan output for a fully-loaded root
// environment: every feature active, every decision visible.
func TestPlanGoldenFull(t *testing.T) {
	ts := newTestSys(t, 0, true)
	for _, dir := range []string{"mnt/claude-init", "mnt/codex-init"} {
		if err := os.MkdirAll(filepath.Join(ts.Root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stageFile(t, ts, "mnt/codex-init", "mcp.json", 0o644, "{}")

	ctx, _ := newTestContext(ts, Config{
		ExtraHosts:        "moat-proxy:192.0.2.5 moat-host:@host.docker.internal bad:",
		SSHTCPAddr:        "192.168.65.2:5522",
		ClaudeInit:        "/mnt/claude-init",
		CodexInit:         "/mnt/codex-init",
		GeminiInit:        "/mnt/missing",
		InitFiles:         "/home/moatuser/.config/g/cfg\tYWJj\n",
		Clipboard:         "1",
		GitUserName:       "Ada",
		GitSSHGitHub:      "1",
		DockerDIND:        "1",
		WorkspaceVolume:   "1",
		WorkspaceExcludes: "./node_modules\n./dist",
		VolumeChown:       "/workspace/.cache",
		PreRun:            "npm install",
		Home:              "/root",
	})
	ctx.Argv = []string{"claude", "--continue"}
	checkGolden(t, "plan_full.golden", strings.Join(Plan(ctx), "\n")+"\n")
}

// TestPlanGoldenMinimal pins the all-skip plan for an empty non-root env.
func TestPlanGoldenMinimal(t *testing.T) {
	ts := newTestSys(t, 1000, false)
	ctx, _ := newTestContext(ts, Config{Home: "/tmp/h"})
	ctx.Argv = []string{"bash"}
	checkGolden(t, "plan_minimal.golden", strings.Join(Plan(ctx), "\n")+"\n")
}

// TestPlanIsSideEffectFree asserts the dry-run performs no writes, spawns,
// or environment mutations.
func TestPlanIsSideEffectFree(t *testing.T) {
	ts := newTestSys(t, 0, true)
	ts.env["MOAT_INIT_FILES"] = "sentinel"
	ctx, _ := newTestContext(ts, Config{
		ExtraHosts: "moat-proxy:192.0.2.5",
		SSHTCPAddr: "1.2.3.4:5",
		InitFiles:  "/a/b\tYWJj",
		Clipboard:  "1",
		DockerDIND: "1",
		PreRun:     "npm install",
		Home:       "/root",
	})
	_ = Plan(ctx)
	if len(ts.detached)+len(ts.runs)+len(ts.pipes)+len(ts.chowns)+len(ts.execs) != 0 {
		t.Error("Plan performed side effects")
	}
	if ts.env["MOAT_INIT_FILES"] != "sentinel" {
		t.Error("Plan mutated the environment")
	}
	if exists(ts, "/a/b") || exists(ts, "/run/moat/ssh") {
		t.Error("Plan touched the filesystem")
	}
}

// TestPlanFunctionalGateLines pins the two lines the release gate greps
// for: the privilege-drop decision and the MOAT_INIT_FILES scrub. A
// regenerated-but-defective binary that lost those phases fails the gate
// regardless of its checksum (plan §5).
func TestPlanFunctionalGateLines(t *testing.T) {
	for _, tc := range []struct {
		euid     int
		moatuser bool
		want     string
	}{
		{0, true, "privilege drop: exec gosu moatuser"},
		{1000, false, "privilege drop: exec"},
		{0, false, "privilege drop: FATAL"},
	} {
		ts := newTestSys(t, tc.euid, tc.moatuser)
		ctx, _ := newTestContext(ts, Config{InitFiles: "/a/b\tYWJj", Home: "/h"})
		out := strings.Join(Plan(ctx), "\n")
		if !strings.Contains(out, tc.want) {
			t.Errorf("euid=%d moatuser=%v: plan missing %q:\n%s", tc.euid, tc.moatuser, tc.want, out)
		}
		if !strings.Contains(out, "scrub MOAT_INIT_FILES") {
			t.Errorf("plan missing the scrub line:\n%s", out)
		}
	}
}

// TestFatalErrorContractGolden collects every scripted fatal stderr block
// across the phases into one golden file — the exact-wording contract the
// port preserves from moat-init.sh.
func TestFatalErrorContractGolden(t *testing.T) {
	var b strings.Builder
	scenario := func(name string, cfg Config, prep func(*testSys)) {
		ts := newTestSys(t, 1000, false)
		if prep != nil {
			prep(ts)
		}
		ctx, stderr := newTestContext(ts, cfg)
		code := Run(ctx)
		b.WriteString("== " + name + " (exit " + strconv.Itoa(code) + ") ==\n")
		b.WriteString(stderr.String())
	}

	scenario("unresolvable extra host", Config{ExtraHosts: "moat-proxy:@nope.invalid", Home: "/h"}, func(ts *testSys) {
		writeHosts(t, ts, "")
	})
	scenario("unwritable /etc/hosts", Config{ExtraHosts: "moat-proxy:192.0.2.5", Home: "/h"}, nil)
	scenario("docker mutex", Config{DockerDIND: "1", DockerGID: "999", Home: "/h"}, nil)
	scenario("populate as non-root", Config{WorkspaceVolume: "1", Home: "/h"}, func(ts *testSys) {
		writeHosts(t, ts, "")
	})
	scenario("pre_run hook failure", Config{PreRun: "echo doing-setup; exit 42", Home: "/h"}, func(ts *testSys) {
		ts.runHook = func(Cmd) (int, error) { return 42, nil }
	})

	// Root-without-moatuser needs euid 0.
	tsRoot := newTestSys(t, 0, false)
	ctxRoot, stderrRoot := newTestContext(tsRoot, Config{Home: "/root"})
	code := Run(ctxRoot)
	b.WriteString("== root without moatuser (exit " + strconv.Itoa(code) + ") ==\n")
	b.WriteString(stderrRoot.String())

	// Populate rc failure needs root + moatuser + a staging tree.
	tsPop := newTestSys(t, 0, true)
	setupStagingTree(t, tsPop)
	tsPop.pipeHook = func(src, dst Cmd) (int, int, error) { return 2, 0, nil }
	ctxPop, stderrPop := newTestContext(tsPop, Config{WorkspaceVolume: "1", Home: "/root"})
	code = Run(ctxPop)
	b.WriteString("== populate pipe failure (exit " + strconv.Itoa(code) + ") ==\n")
	b.WriteString(stderrPop.String())

	checkGolden(t, "fatal_errors.golden", b.String())
}
