package container

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeAppleCLI installs a `container` stand-in driven by files in its
// directory: <id>.code is the status `start --attach` exits with, <id>.hold
// keeps the guest running while it exists, and <id>.fail makes the start fail
// before the guest process runs. <id>.crash ends the CLI with that status
// while the guest keeps running, <id>.legacy selects the pre-1.0 inspect
// schema, <id>.noinspect makes inspect fail, and <id>.rmnotfound makes rm report
// a missing container.
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
	ps -o pgid= -p $$ | tr -d ' ' > "$d/$id.pgid"
	touch "$d/$id.started" "$d/$id.running"
	[ -f "$d/$id.crash" ] && exit "$(cat "$d/$id.crash")"
	while [ -f "$d/$id.hold" ]; do sleep 0.02; done
	rm -f "$d/$id.running"
	exit "$(cat "$d/$id.code")"
	;;
inspect)
	id="$2"
	if [ -f "$d/$id.noinspect" ]; then
		echo "Error: XPC connection interrupted" >&2
		exit 1
	fi
	state=stopped
	[ -f "$d/$id.running" ] && state=running
	started=
	[ -f "$d/$id.started" ] && started='"startedDate":"2026-11-22T16:31:02Z",'
	if [ -f "$d/$id.legacy" ]; then
		echo "[{\"id\":\"$id\",\"status\":\"$state\"}]"
	else
		echo "[{\"id\":\"$id\",\"status\":{$started\"state\":\"$state\"}}]"
	fi
	;;
rm)
	id="$3"
	if [ -f "$d/$id.rmnotfound" ]; then
		echo 'Error: notFound: "failed to delete one or more containers"' >&2
		exit 1
	fi
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

// The CLI's status is the guest's only once the guest has exited. A CLI that
// ends on its own must not end the run, and inspect lagging a real exit must
// not lose the status.
func TestAppleWaitConfirmsExitBeforeTrustingLaunchStatus(t *testing.T) {
	shortGrace := func(t *testing.T, d time.Duration) {
		t.Helper()
		old := launchExitGrace
		launchExitGrace = d
		t.Cleanup(func() { launchExitGrace = old })
	}
	type waitResult struct {
		code int64
		err  error
	}
	startCrashed := func(t *testing.T, status string) (*AppleRuntime, string, chan waitResult) {
		t.Helper()
		rt, dir := fakeAppleCLI(t)
		writeFile(t, dir, "ctr.crash", status)
		if err := rt.StartContainer(t.Context(), "ctr"); err != nil {
			t.Fatalf("StartContainer: %v", err)
		}
		<-rt.launchFor("ctr").done
		waited := make(chan waitResult, 1)
		go func() {
			code, err := rt.WaitContainer(t.Context(), "ctr")
			waited <- waitResult{code, err}
		}()
		return rt, dir, waited
	}

	t.Run("guest still running", func(t *testing.T) {
		shortGrace(t, 100*time.Millisecond)
		_, dir, waited := startCrashed(t, "1")
		select {
		case got := <-waited:
			t.Fatalf("WaitContainer = %d, %v while the guest is running", got.code, got.err)
		case <-time.After(500 * time.Millisecond):
		}
		if err := os.Remove(filepath.Join(dir, "ctr.running")); err != nil {
			t.Fatal(err)
		}
		// The CLI's status described the CLI, so the guest's is unknown.
		if got := <-waited; got.err != nil || got.code != 0 {
			t.Errorf("WaitContainer = %d, %v; want 0, nil", got.code, got.err)
		}
	})

	t.Run("inspect lags the exit", func(t *testing.T) {
		shortGrace(t, 5*time.Second)
		_, dir, waited := startCrashed(t, "17")
		time.Sleep(100 * time.Millisecond)
		if err := os.Remove(filepath.Join(dir, "ctr.running")); err != nil {
			t.Fatal(err)
		}
		if got := <-waited; got.err != nil || got.code != 17 {
			t.Errorf("WaitContainer = %d, %v; want 17, nil", got.code, got.err)
		}
	})
}

// Without a recorded start time there is no evidence the guest ran, so a
// failed CLI is a failed start and its message is kept. With one, the same
// status is the guest's.
func TestAppleStartFailureWithoutStartEvidence(t *testing.T) {
	for _, marker := range []string{"ctr.legacy", "ctr.noinspect"} {
		t.Run(marker, func(t *testing.T) {
			rt, dir := fakeAppleCLI(t)
			writeFile(t, dir, "ctr.fail", "")
			writeFile(t, dir, marker, "")
			err := rt.StartContainer(t.Context(), "ctr")
			if err == nil || !strings.Contains(err.Error(), "failed to find target executable") {
				t.Fatalf("StartContainer = %v, want the CLI's start error", err)
			}
			if rt.launchFor("ctr") != nil {
				t.Error("a failed start left a launch behind")
			}
		})
	}

	t.Run("start recorded", func(t *testing.T) {
		rt, dir := fakeAppleCLI(t)
		writeFile(t, dir, "ctr.code", "1")
		if err := rt.StartContainer(t.Context(), "ctr"); err != nil {
			t.Fatalf("StartContainer = %v, want a started guest that exited 1", err)
		}
		if code, err := rt.WaitContainer(t.Context(), "ctr"); err != nil || code != 1 {
			t.Errorf("WaitContainer = %d, %v; want 1, nil", code, err)
		}
	})
}

// The CLI forwards signals to the guest, so it must not share the terminal's
// foreground process group with moat.
func TestAppleLaunchRunsInOwnProcessGroup(t *testing.T) {
	rt, dir := fakeAppleCLI(t)
	writeFile(t, dir, "ctr.code", "0")
	writeFile(t, dir, "ctr.hold", "")
	if err := rt.StartContainer(t.Context(), "ctr"); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	defer os.Remove(filepath.Join(dir, "ctr.hold"))
	raw, err := os.ReadFile(filepath.Join(dir, "ctr.pgid"))
	if err != nil {
		t.Fatal(err)
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("pgid %q: %v", raw, err)
	}
	if pgid == syscall.Getpgrp() {
		t.Errorf("launch process shares moat's process group %d", pgid)
	}
}

// Starting a container again must end the earlier launch process, and the
// new launch supplies the status.
func TestAppleRestartReplacesLaunch(t *testing.T) {
	rt, dir := fakeAppleCLI(t)
	writeFile(t, dir, "ctr.code", "3")
	writeFile(t, dir, "ctr.hold", "")
	if err := rt.StartContainer(t.Context(), "ctr"); err != nil {
		t.Fatalf("first StartContainer: %v", err)
	}
	first := rt.launchFor("ctr")

	if err := rt.StartContainer(t.Context(), "ctr"); err != nil {
		t.Fatalf("second StartContainer: %v", err)
	}
	second := rt.launchFor("ctr")
	if second == first {
		t.Fatal("second start did not replace the launch")
	}
	select {
	case <-first.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the first launch process was left running")
	}
	if first.known {
		t.Errorf("first launch ended with status %d, want it killed", first.code)
	}

	if err := os.Remove(filepath.Join(dir, "ctr.hold")); err != nil {
		t.Fatal(err)
	}
	if code, err := rt.WaitContainer(t.Context(), "ctr"); err != nil || code != 3 {
		t.Errorf("WaitContainer = %d, %v; want 3, nil", code, err)
	}
}

// Removing a container ends and forgets its launch, whether or not the
// runtime still has the container.
func TestAppleRemoveDropsLaunch(t *testing.T) {
	for _, tc := range []struct{ name, marker string }{
		{"removed", ""},
		{"already gone", "ctr.rmnotfound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, dir := fakeAppleCLI(t)
			writeFile(t, dir, "ctr.code", "0")
			writeFile(t, dir, "ctr.hold", "")
			if tc.marker != "" {
				writeFile(t, dir, tc.marker, "")
			}
			if err := rt.StartContainer(t.Context(), "ctr"); err != nil {
				t.Fatalf("StartContainer: %v", err)
			}
			launch := rt.launchFor("ctr")

			if err := rt.RemoveContainer(t.Context(), "ctr"); err != nil {
				t.Fatalf("RemoveContainer: %v", err)
			}
			if rt.launchFor("ctr") != nil {
				t.Error("launch still tracked after removal")
			}
			select {
			case <-launch.done:
			case <-time.After(5 * time.Second):
				t.Fatal("launch process left running after removal")
			}
		})
	}
}
