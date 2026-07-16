package moatinit

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestInitFilesPhaseWritesRecords(t *testing.T) {
	ts := newTestSys(t, 0, true)
	ts.env["MOAT_INIT_FILES"] = "sentinel" // proves the phase unsets it
	records := "/home/moatuser/.config/graphite/user_config\t" + b64("token = \"gt_x\"") + "\n" +
		"/etc/other/cfg\t" + b64("data") + "\n"
	ctx, _ := newTestContext(ts, Config{InitFiles: records, Home: "/root"})
	if err := initFilesPhase(ctx); err != nil {
		t.Fatal(err)
	}

	// Content decoded exactly, no trailing newline added.
	if got := fileContent(t, ts, "/home/moatuser/.config/graphite/user_config"); got != "token = \"gt_x\"" {
		t.Errorf("decoded content = %q", got)
	}
	// INIT-07: every record file is 0600.
	for _, p := range []string{"/home/moatuser/.config/graphite/user_config", "/etc/other/cfg"} {
		if got := statMode(t, ts, p); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", p, got)
		}
	}
	// INIT-05: the immediate parent is 0755.
	if got := statMode(t, ts, "/home/moatuser/.config/graphite"); got != 0o755 {
		t.Errorf("parent mode = %o, want 755", got)
	}

	// INIT-08: file chown + ancestor walk up to but excluding INIT_HOME.
	for _, p := range []string{
		"/home/moatuser/.config/graphite/user_config",
		"/home/moatuser/.config/graphite",
		"/home/moatuser/.config",
	} {
		if !ts.chowned(p) {
			t.Errorf("missing chown for %s", p)
		}
	}
	if ts.chowned("/home/moatuser") {
		t.Error("walk chowned INIT_HOME itself")
	}
	// The out-of-home record's walk climbs to / (parity — B-P2 documented).
	if !ts.chowned("/etc/other") || !ts.chowned("/etc") {
		t.Error("out-of-home ancestor walk missing /etc/other or /etc")
	}
	if ts.chowned("/") {
		t.Error("walk chowned / itself")
	}

	// INIT-10: the variable is gone from the process env.
	if _, present := ts.env["MOAT_INIT_FILES"]; present {
		t.Error("MOAT_INIT_FILES still in process env after the phase")
	}
}

func TestInitFilesPhaseNonRootNoChown(t *testing.T) {
	// INIT-09 (companion to INIT-08): non-root writes files 0600 but
	// attempts no chown of any kind.
	ts := newTestSys(t, 1000, true)
	records := "/tmp/h/.config/app/cfg\t" + b64("x")
	ctx, _ := newTestContext(ts, Config{InitFiles: records, Home: "/tmp/h"})
	if err := initFilesPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if got := statMode(t, ts, "/tmp/h/.config/app/cfg"); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}
	if len(ts.chowns) != 0 {
		t.Errorf("non-root recorded chowns: %v", ts.chowns)
	}
}

func TestInitFilesPhaseEmptyAndSkips(t *testing.T) {
	// INIT-01: empty is a complete no-op.
	ts := newTestSys(t, 0, true)
	ctx, _ := newTestContext(ts, Config{InitFiles: "", Home: "/root"})
	if err := initFilesPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ts.chowns) != 0 {
		t.Error("empty MOAT_INIT_FILES did work")
	}

	// INIT-04: empty lines are skipped, surrounding records still written.
	ts2 := newTestSys(t, 0, true)
	records := "\n/a/b\t" + b64("v") + "\n\n"
	ctx2, _ := newTestContext(ts2, Config{InitFiles: records, Home: "/root"})
	if err := initFilesPhase(ctx2); err != nil {
		t.Fatal(err)
	}
	if got := fileContent(t, ts2, "/a/b"); got != "v" {
		t.Errorf("record around empty lines = %q", got)
	}
}

func TestInitFilesPhaseInvalidBase64FailsClosed(t *testing.T) {
	// B-P1: invalid base64 aborts non-zero BEFORE any file is written.
	ts := newTestSys(t, 0, true)
	records := "/sec/first\t" + b64("ok") + "\n/sec/second\t!!!bad!!!"
	ctx, stderr := newTestContext(ts, Config{InitFiles: records, Home: "/root"})
	err := initFilesPhase(ctx)
	exit, ok := err.(exitError)
	if !ok || exit.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}
	if stderr.Len() == 0 {
		t.Error("no diagnostic for invalid base64")
	}
	// Decode-to-buffer: the failing record left nothing on disk (not even
	// its parent dir), and the entrypoint aborts before exec.
	if exists(ts, "/sec/second") || exists(ts, "/sec/second/") {
		t.Error("partial secret written for invalid record")
	}
	// Records before the bad one were already written (parity with the
	// shell's sequential loop).
	if !exists(ts, "/sec/first") {
		t.Error("prior valid record missing")
	}
}

func TestInitFilesPhaseWidensPreexistingParent(t *testing.T) {
	// INIT-05 / B-P2: a pre-existing stricter parent is widened to exactly
	// 0755 (documented parity).
	ts := newTestSys(t, 0, true)
	strict := filepath.Join(ts.Root, "priv")
	if err := os.MkdirAll(strict, 0o700); err != nil {
		t.Fatal(err)
	}
	records := "/priv/cfg\t" + b64("x")
	ctx, _ := newTestContext(ts, Config{InitFiles: records, Home: "/root"})
	if err := initFilesPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if got := statMode(t, ts, "/priv"); got != 0o755 {
		t.Errorf("pre-existing parent mode = %o, want 755", got)
	}
}
