package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAppleCLI installs a `container` stand-in driven by files in its
// directory: <id>.code is the status `start --attach` exits with, <id>.hold
// keeps the guest running while it exists, and <id>.fail makes the start fail
// before the guest process runs.
func fakeAppleCLI(t *testing.T) (*AppleRuntime, string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
d="$(dirname "$0")"
case "$1" in
start)
	id="$3"
	if [ -f "$d/$id.fail" ]; then
		echo "Error: failed to find target executable /missing" >&2
		exit 1
	fi
	touch "$d/$id.started" "$d/$id.running"
	while [ -f "$d/$id.hold" ]; do sleep 0.02; done
	rm -f "$d/$id.running"
	exit "$(cat "$d/$id.code")"
	;;
inspect)
	id="$2"
	state=stopped
	[ -f "$d/$id.running" ] && state=running
	started=
	[ -f "$d/$id.started" ] && started='"startedDate":"2026-11-22T16:31:02Z",'
	echo "[{\"id\":\"$id\",\"status\":{$started\"state\":\"$state\"}}]"
	;;
rm)
	exit 0
	;;
esac
`
	path := filepath.Join(dir, "container")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	rt := &AppleRuntime{containerBin: path}
	t.Cleanup(func() { _ = rt.Close() })
	return rt, dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAppleWaitReportsGuestExitStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
		want int64
	}{
		{"failure", "17", 17},
		{"success", "0", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, dir := fakeAppleCLI(t)
			writeFile(t, dir, "ctr.code", tc.code)
			writeFile(t, dir, "ctr.hold", "")

			if err := rt.StartContainer(t.Context(), "ctr"); err != nil {
				t.Fatalf("StartContainer: %v", err)
			}
			if state, err := rt.ContainerState(t.Context(), "ctr"); err != nil || state != "running" {
				t.Fatalf("state after start = %q, %v; want running", state, err)
			}
			if err := os.Remove(filepath.Join(dir, "ctr.hold")); err != nil {
				t.Fatal(err)
			}

			code, err := rt.WaitContainer(t.Context(), "ctr")
			if err != nil || code != tc.want {
				t.Errorf("WaitContainer = %d, %v; want %d", code, err, tc.want)
			}
		})
	}
}

// A guest that exits nonzero before it is ever seen running has still
// started, unlike a start that fails with the same CLI status.
func TestAppleStartSeparatesFastExitFromStartFailure(t *testing.T) {
	t.Run("fast guest exit", func(t *testing.T) {
		rt, dir := fakeAppleCLI(t)
		writeFile(t, dir, "ctr.code", "1")

		if err := rt.StartContainer(t.Context(), "ctr"); err != nil {
			t.Fatalf("StartContainer: %v", err)
		}
		if code, err := rt.WaitContainer(t.Context(), "ctr"); err != nil || code != 1 {
			t.Errorf("WaitContainer = %d, %v; want 1", code, err)
		}
	})

	t.Run("start failure", func(t *testing.T) {
		rt, dir := fakeAppleCLI(t)
		writeFile(t, dir, "ctr.fail", "")

		err := rt.StartContainer(t.Context(), "ctr")
		if err == nil || !strings.Contains(err.Error(), "failed to find target executable") {
			t.Fatalf("StartContainer error = %v, want the CLI's start failure", err)
		}
		if rt.launchFor("ctr") != nil {
			t.Error("a failed start left a launch record")
		}
	})
}

// A container started by another process has no launch record here, so the
// wait falls back to inspect, which carries no status.
func TestAppleWaitWithoutLaunchPolls(t *testing.T) {
	rt, dir := fakeAppleCLI(t)
	writeFile(t, dir, "ctr.started", "")

	code, err := rt.WaitContainer(t.Context(), "ctr")
	if err != nil || code != 0 {
		t.Errorf("WaitContainer = %d, %v; want 0", code, err)
	}
}

func TestAppleWaitHonorsCancellation(t *testing.T) {
	rt, dir := fakeAppleCLI(t)
	writeFile(t, dir, "ctr.code", "0")
	writeFile(t, dir, "ctr.hold", "")
	if err := rt.StartContainer(t.Context(), "ctr"); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := rt.WaitContainer(ctx, "ctr"); err == nil {
		t.Error("WaitContainer returned without error while the guest was running")
	}
	// The guest keeps its launch record after an abandoned wait.
	if err := os.Remove(filepath.Join(dir, "ctr.hold")); err != nil {
		t.Fatal(err)
	}
	if code, err := rt.WaitContainer(t.Context(), "ctr"); err != nil || code != 0 {
		t.Errorf("second WaitContainer = %d, %v; want 0", code, err)
	}
}

func TestAppleAttachedExitIsNotAnAttachError(t *testing.T) {
	t.Run("guest exit", func(t *testing.T) {
		rt, dir := fakeAppleCLI(t)
		writeFile(t, dir, "ctr.code", "17")

		if err := rt.StartAttached(t.Context(), "ctr", AttachOptions{}); err != nil {
			t.Fatalf("StartAttached: %v", err)
		}
		if code, err := rt.WaitContainer(t.Context(), "ctr"); err != nil || code != 17 {
			t.Errorf("WaitContainer = %d, %v; want 17", code, err)
		}
	})

	t.Run("start failure", func(t *testing.T) {
		rt, dir := fakeAppleCLI(t)
		writeFile(t, dir, "ctr.fail", "")

		if err := rt.StartAttached(t.Context(), "ctr", AttachOptions{}); err == nil {
			t.Fatal("StartAttached succeeded although the guest never started")
		}
	})
}
