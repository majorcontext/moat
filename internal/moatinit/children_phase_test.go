package moatinit

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSSHAgentBridgePhaseGate(t *testing.T) {
	// SSH-01: unset/empty — no dir, no socat, no warning.
	ts := newTestSys(t, 0, true)
	ctx, stderr := newTestContext(ts, Config{})
	if err := sshAgentBridgePhase(ctx); err != nil {
		t.Fatal(err)
	}
	if exists(ts, "/run/moat/ssh") || len(ts.detached) != 0 || stderr.Len() != 0 {
		t.Error("empty MOAT_SSH_TCP_ADDR did work")
	}
}

func TestSSHAgentBridgePhaseSuccess(t *testing.T) {
	ts := newTestSys(t, 0, true)
	// Simulate socat creating the socket: a real unix listener at the
	// rerooted path (created by the detach hook, like socat would).
	ts.detachHook = func(c Cmd) (int, error) {
		sockPath := strings.TrimPrefix(strings.SplitN(c.Argv[1], ",", 2)[0], "UNIX-LISTEN:")
		l, err := net.Listen("unix", sockPath)
		if err != nil {
			t.Fatalf("test listener: %v", err)
		}
		t.Cleanup(func() { l.Close() })
		return 4242, nil
	}
	ctx, stderr := newTestContext(ts, Config{SSHTCPAddr: "192.168.65.2:5522"})
	if err := sshAgentBridgePhase(ctx); err != nil {
		t.Fatal(err)
	}

	// Directory prepared: 0755 + chowned to moatuser.
	if got := statMode(t, ts, "/run/moat/ssh"); got != 0o755 {
		t.Errorf("socket dir mode = %o, want 755", got)
	}
	if !ts.chowned("/run/moat/ssh") {
		t.Error("socket dir not chowned")
	}
	// socat argv: forking unix listener at mode 0660 bridged to the addr.
	argv := ts.detached[0].Argv
	if argv[0] != "socat" || !strings.Contains(argv[1], ",fork,mode=0660") || argv[2] != "TCP:192.168.65.2:5522" {
		t.Errorf("socat argv = %v", argv)
	}
	// Success path: socket chowned, no warnings.
	if !ts.chowned("/run/moat/ssh/agent.sock") {
		t.Error("socket not chowned on success path")
	}
	if stderr.Len() != 0 {
		t.Errorf("success path warned: %q", stderr.String())
	}
}

func TestSSHAgentBridgePhaseSocatDied(t *testing.T) {
	// SSH-08: socat no longer running after the wait — exact warning, and
	// the phase still succeeds (warning only).
	ts := newTestSys(t, 0, true)
	ts.alive[4242] = false
	ctx, stderr := newTestContext(ts, Config{SSHTCPAddr: "1.2.3.4:5"})
	if err := sshAgentBridgePhase(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "Warning: SSH agent bridge (socat) failed to start") {
		t.Errorf("stderr = %q", stderr.String())
	}
	// Full 2s budget consumed (socket never appeared).
	if ts.sleeps != sshSocketWaitIters {
		t.Errorf("sleeps = %d, want %d", ts.sleeps, sshSocketWaitIters)
	}
}

func TestSSHAgentBridgePhaseSocketNeverAppeared(t *testing.T) {
	// SSH-09: socat alive but no socket — exact warning, still non-fatal.
	ts := newTestSys(t, 0, true)
	ctx, stderr := newTestContext(ts, Config{SSHTCPAddr: "1.2.3.4:5"})
	if err := sshAgentBridgePhase(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "Warning: SSH agent socket was not created after 2s") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestClipboardPhase(t *testing.T) {
	// X-CLIPBOARD-XVFB: exact "1" starts Xvfb :99 and exports DISPLAY.
	ts := newTestSys(t, 0, true)
	ctx, _ := newTestContext(ts, Config{Clipboard: "1"})
	if err := clipboardPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ts.detached[0].Argv, " "); got != "Xvfb :99 -screen 0 1x1x8" {
		t.Errorf("Xvfb argv = %q", got)
	}
	if ts.env["DISPLAY"] != ":99" {
		t.Error("DISPLAY not exported")
	}

	// GIT-CLIP-02: DISPLAY exported even when the spawn fails.
	ts2 := newTestSys(t, 0, true)
	ts2.detachHook = func(Cmd) (int, error) { return 0, os.ErrNotExist }
	ctx2, _ := newTestContext(ts2, Config{Clipboard: "1"})
	if err := clipboardPhase(ctx2); err != nil {
		t.Fatal(err)
	}
	if ts2.env["DISPLAY"] != ":99" {
		t.Error("DISPLAY not exported after failed Xvfb spawn")
	}

	// Companions: any non-"1" value is a no-op.
	for _, v := range []string{"", "0", "true"} {
		ts3 := newTestSys(t, 0, true)
		ctx3, _ := newTestContext(ts3, Config{Clipboard: v})
		if err := clipboardPhase(ctx3); err != nil {
			t.Fatal(err)
		}
		if len(ts3.detached) != 0 || ts3.env["DISPLAY"] != "" {
			t.Errorf("Clipboard=%q did work", v)
		}
	}
}

func TestDockerSetupPhaseMutex(t *testing.T) {
	// DOCKER-01: both set — exit 1 with the exact three lines, before
	// either mode body runs.
	ts := newTestSys(t, 0, true)
	ctx, stderr := newTestContext(ts, Config{DockerDIND: "1", DockerGID: "999"})
	err := dockerSetupPhase(ctx)
	if exit, ok := err.(exitError); !ok || exit.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}
	want := "Error: MOAT_DOCKER_DIND and MOAT_DOCKER_GID are mutually exclusive\n" +
		"Use MOAT_DOCKER_GID when mounting host's docker socket\n" +
		"Use MOAT_DOCKER_DIND when running Docker-in-Docker\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if len(ts.detached) != 0 || len(ts.runs) != 0 {
		t.Error("mode body ran despite mutex violation")
	}

	// Companion: one set — no mutex error.
	ts2 := newTestSys(t, 1000, true) // non-root: dind body also skipped
	ctx2, stderr2 := newTestContext(ts2, Config{DockerDIND: "1"})
	if err := dockerSetupPhase(ctx2); err != nil {
		t.Fatal(err)
	}
	if stderr2.Len() != 0 {
		t.Errorf("single-var run warned: %q", stderr2.String())
	}
}

func TestDindSetupReadyPath(t *testing.T) {
	ts := newTestSys(t, 0, true)
	// dockerd "creates" the socket when spawned.
	ts.detachHook = func(c Cmd) (int, error) {
		if c.Argv[0] != "dockerd" {
			t.Fatalf("unexpected detached child: %v", c.Argv)
		}
		dir := filepath.Join(ts.Root, "var/run")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		l, err := net.Listen("unix", filepath.Join(dir, "docker.sock"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		return 777, nil
	}
	ctx, stderr := newTestContext(ts, Config{DockerDIND: "1"})
	if err := dockerSetupPhase(ctx); err != nil {
		t.Fatal(err)
	}
	out := stderr.String()
	for _, want := range []string{
		"Starting Docker daemon (dind mode)...",
		"Waiting for Docker daemon to be ready...",
		"Docker daemon is ready (took 0s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr missing %q:\n%s", want, out)
		}
	}
	// dockerd argv + log redirection.
	if got := strings.Join(ts.detached[0].Argv, " "); got != "dockerd --storage-driver=vfs --log-level=warn" {
		t.Errorf("dockerd argv = %q", got)
	}
	if ts.detached[0].LogFile != "/var/log/dockerd.log" {
		t.Errorf("dockerd log = %q", ts.detached[0].LogFile)
	}
	// Group setup: docker group missing -> groupadd, then usermod.
	cmds := make([]string, 0, len(ts.runs))
	for _, c := range ts.runs {
		cmds = append(cmds, strings.Join(c.Argv, " "))
	}
	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, "groupadd docker") || !strings.Contains(joined, "usermod -aG docker moatuser") {
		t.Errorf("group setup commands = %v", cmds)
	}
}

func TestDindSetupDaemonDiedFatal(t *testing.T) {
	// DOCKER-06: dockerd dies during the wait — fatal with the log tail.
	ts := newTestSys(t, 0, true)
	if err := os.MkdirAll(filepath.Join(ts.Root, "var/log"), 0o755); err != nil {
		t.Fatal(err)
	}
	ts.detachHook = func(c Cmd) (int, error) {
		if err := ts.WriteFile("/var/log/dockerd.log", []byte("line1\nfailed to start daemon: boom\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return 777, nil
	}
	ts.alive[777] = false
	ts.runHook = func(Cmd) (int, error) { return 1, nil } // docker info never succeeds
	ctx, stderr := newTestContext(ts, Config{DockerDIND: "1"})
	err := dockerSetupPhase(ctx)
	if exit, ok := err.(exitError); !ok || exit.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}
	out := stderr.String()
	for _, want := range []string{
		"Error: Docker daemon failed to start",
		"Check /var/log/dockerd.log for details:",
		"failed to start daemon: boom",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr missing %q:\n%s", want, out)
		}
	}
}

func TestDindSetupTimeoutFatal(t *testing.T) {
	// DOCKER-07: alive but never ready — timeout error with socket state.
	ts := newTestSys(t, 0, true)
	ts.runHook = func(Cmd) (int, error) { return 1, nil }
	ctx, stderr := newTestContext(ts, Config{DockerDIND: "1"})
	err := dockerSetupPhase(ctx)
	if exit, ok := err.(exitError); !ok || exit.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "Error: Docker daemon did not become ready within 30 seconds") {
		t.Errorf("missing timeout error:\n%s", out)
	}
	if !strings.Contains(out, "Socket exists: no") {
		t.Errorf("missing socket state:\n%s", out)
	}
	if ts.sleeps != dindTimeoutSeconds {
		t.Errorf("sleeps = %d, want %d", ts.sleeps, dindTimeoutSeconds)
	}
}

func TestHostSocketSetup(t *testing.T) {
	// DOCKER-10/12/13: GID detected from an in-container stat; group
	// created when the GID is unowned; moatuser joins the owning group.
	newSockSys := func(t *testing.T) *testSys {
		ts := newTestSys(t, 0, true)
		dir := filepath.Join(ts.Root, "var/run")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		l, err := net.Listen("unix", filepath.Join(dir, "docker.sock"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		return ts
	}
	gid := strconv.Itoa(os.Getgid()) // the test socket's actual gid

	// Case A: a group already owns the GID — reused, no groupadd.
	ts := newSockSys(t)
	ts.groupsByGID[gid] = "staff"
	ctx, stderr := newTestContext(ts, Config{DockerGID: "999"})
	if err := dockerSetupPhase(ctx); err != nil {
		t.Fatal(err)
	}
	cmds := make([]string, 0, len(ts.runs))
	for _, c := range ts.runs {
		cmds = append(cmds, strings.Join(c.Argv, " "))
	}
	if strings.Contains(strings.Join(cmds, "\n"), "groupadd") {
		t.Errorf("groupadd ran for an owned GID: %v", cmds)
	}
	if !strings.Contains(strings.Join(cmds, "\n"), "usermod -aG staff moatuser") {
		t.Errorf("usermod missing: %v", cmds)
	}
	if stderr.Len() != 0 {
		t.Errorf("warned on the happy path: %q", stderr.String())
	}

	// Case B: unowned GID — groupadd -g <gid> moat-docker; the group map
	// updates when groupadd runs (as the real getent re-resolution would).
	ts2 := newSockSys(t)
	ts2.runHook = func(c Cmd) (int, error) {
		if c.Argv[0] == "groupadd" {
			ts2.groupsByGID[c.Argv[2]] = "moat-docker"
		}
		return 0, nil
	}
	ctx2, _ := newTestContext(ts2, Config{DockerGID: "999"})
	if err := dockerSetupPhase(ctx2); err != nil {
		t.Fatal(err)
	}
	cmds2 := make([]string, 0, len(ts2.runs))
	for _, c := range ts2.runs {
		cmds2 = append(cmds2, strings.Join(c.Argv, " "))
	}
	joined := strings.Join(cmds2, "\n")
	if !strings.Contains(joined, "groupadd -g "+gid+" moat-docker") {
		t.Errorf("groupadd missing/wrong: %v", cmds2)
	}
	if !strings.Contains(joined, "usermod -aG moat-docker moatuser") {
		t.Errorf("usermod missing: %v", cmds2)
	}

	// Case C: socket absent — host mode skipped entirely (DOCKER-09).
	ts3 := newTestSys(t, 0, true)
	ctx3, stderr3 := newTestContext(ts3, Config{DockerGID: "999"})
	if err := dockerSetupPhase(ctx3); err != nil {
		t.Fatal(err)
	}
	if len(ts3.runs) != 0 || stderr3.Len() != 0 {
		t.Error("host mode ran without a socket")
	}
}

func TestPreRunHookPhase(t *testing.T) {
	// EXEC-01: empty/unset — no-op (a whitespace command DOES run).
	ts := newTestSys(t, 1000, true)
	ctx, _ := newTestContext(ts, Config{})
	if err := preRunHookPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ts.runs) != 0 {
		t.Error("empty hook ran")
	}

	// EXEC-02: non-root runs sh -c in /workspace (child-confined cwd).
	ts2 := newTestSys(t, 1000, true)
	ctx2, _ := newTestContext(ts2, Config{PreRun: "npm install"})
	if err := preRunHookPhase(ctx2); err != nil {
		t.Fatal(err)
	}
	c := ts2.runs[0]
	if got := strings.Join(c.Argv, " "); got != "sh -c npm install" {
		t.Errorf("non-root hook argv = %v", c.Argv)
	}
	if c.Dir != ts2.RealPath("/workspace") {
		t.Errorf("hook dir = %q", c.Dir)
	}

	// EXEC-03: root+moatuser goes through gosu with the exact command string.
	ts3 := newTestSys(t, 0, true)
	ctx3, _ := newTestContext(ts3, Config{PreRun: "npm install"})
	if err := preRunHookPhase(ctx3); err != nil {
		t.Fatal(err)
	}
	want := []string{"gosu", "moatuser", "sh", "-c", "cd /workspace && npm install"}
	if strings.Join(ts3.runs[0].Argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("root hook argv = %v, want %v", ts3.runs[0].Argv, want)
	}

	// EXEC-04: root without moatuser — hook silently skipped, no error.
	ts4 := newTestSys(t, 0, false)
	ctx4, stderr4 := newTestContext(ts4, Config{PreRun: "touch /should-not-exist"})
	if err := preRunHookPhase(ctx4); err != nil {
		t.Fatal(err)
	}
	if len(ts4.runs) != 0 || stderr4.Len() != 0 {
		t.Error("root-no-moatuser hook ran or warned")
	}
}

func TestPreRunHookPhaseFailureFramedAndLiteralExit(t *testing.T) {
	// EXEC-06: framed diagnostic + the hook's LITERAL exit code.
	ts := newTestSys(t, 0, true)
	ts.runHook = func(Cmd) (int, error) { return 42, nil }
	ctx, stderr := newTestContext(ts, Config{PreRun: "echo doing-setup; exit 42"})
	err := preRunHookPhase(ctx)
	exit, ok := err.(exitError)
	if !ok {
		t.Fatalf("err = %v, want exitError", err)
	}
	if exit.code != 42 {
		t.Errorf("exit code = %d, want the hook's literal 42", exit.code)
	}
	want := "\n" +
		"moat: pre_run hook failed (exit code 42)\n" +
		"moat:   command: echo doing-setup; exit 42\n" +
		"moat:   the pre_run hook runs as moatuser in /workspace before your command.\n" +
		"moat:   fix the command above, or remove hooks.pre_run from moat.yaml.\n"
	if stderr.String() != want {
		t.Errorf("framed message = %q, want %q", stderr.String(), want)
	}
}
