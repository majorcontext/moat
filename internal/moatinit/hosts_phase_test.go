package moatinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readHosts(t *testing.T, ts *testSys) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ts.Root, "etc/hosts"))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

func writeHosts(t *testing.T, ts *testSys, content string) {
	t.Helper()
	dir := filepath.Join(ts.Root, "etc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hosts"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExtraHostsPhaseNoOp(t *testing.T) {
	// HOSTS-01: unset, empty, and whitespace-only leave /etc/hosts
	// byte-identical and succeed.
	for _, v := range []string{"", "   "} {
		ts := newTestSys(t, 0, true)
		writeHosts(t, ts, "127.0.0.1 localhost\n")
		ctx, _ := newTestContext(ts, Config{ExtraHosts: v})
		if err := extraHostsPhase(ctx); err != nil {
			t.Fatalf("ExtraHosts=%q: %v", v, err)
		}
		if got := readHosts(t, ts); got != "127.0.0.1 localhost\n" {
			t.Errorf("ExtraHosts=%q modified /etc/hosts: %q", v, got)
		}
	}
}

func TestExtraHostsPhaseLiteralAppend(t *testing.T) {
	ts := newTestSys(t, 0, true)
	writeHosts(t, ts, "127.0.0.1 localhost\n")
	ctx, _ := newTestContext(ts, Config{ExtraHosts: "moat-proxy:192.0.2.5 moat-host:192.0.2.5"})
	if err := extraHostsPhase(ctx); err != nil {
		t.Fatal(err)
	}
	// HOSTS-09: appended in order, "<ip> <name>\n", prior content intact.
	want := "127.0.0.1 localhost\n192.0.2.5 moat-proxy\n192.0.2.5 moat-host\n"
	if got := readHosts(t, ts); got != want {
		t.Errorf("hosts = %q, want %q", got, want)
	}
	// Literal targets never hit the resolver.
	if len(ts.resolveCalls) != 0 {
		t.Errorf("literal targets resolved DNS: %v", ts.resolveCalls)
	}
}

func TestExtraHostsPhaseSkipsMalformed(t *testing.T) {
	ts := newTestSys(t, 0, true)
	writeHosts(t, ts, "")
	// HOSTS-04 companions: all skipped, no error, file unchanged.
	ctx, _ := newTestContext(ts, Config{ExtraHosts: "moat-proxy: :1.2.3.4 foo x:x"})
	if err := extraHostsPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readHosts(t, ts); got != "" {
		t.Errorf("malformed entries wrote to hosts: %q", got)
	}
}

func TestExtraHostsPhaseResolve(t *testing.T) {
	ts := newTestSys(t, 0, true)
	writeHosts(t, ts, "")
	// HOSTS-06: IPv4 preferred when both records exist.
	ts.resolve4["host.docker.internal"] = "192.0.2.10"
	ts.resolveAny["host.docker.internal"] = "::1"
	ctx, _ := newTestContext(ts, Config{ExtraHosts: "moat-host:@host.docker.internal"})
	if err := extraHostsPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readHosts(t, ts); got != "192.0.2.10 moat-host\n" {
		t.Errorf("hosts = %q, want IPv4-preferred entry", got)
	}
}

func TestExtraHostsPhaseResolveFallbackAny(t *testing.T) {
	ts := newTestSys(t, 0, true)
	writeHosts(t, ts, "")
	// Only the getent-hosts fallback answers (IPv6-only name).
	ts.resolveAny["v6only"] = "fd00::5"
	ctx, stderr := newTestContext(ts, Config{ExtraHosts: "svc:@v6only"})
	if err := extraHostsPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readHosts(t, ts); got != "fd00::5 svc\n" {
		t.Errorf("hosts = %q, want fallback entry", got)
	}
	// Sanctioned P1 warning for the IPv6/loopback fallback.
	if !strings.Contains(stderr.String(), "Warning: /etc/hosts entry 'svc' resolved to 'fd00::5'") {
		t.Errorf("missing IPv6 fallback warning, stderr: %q", stderr.String())
	}
}

func TestExtraHostsPhaseResolveFailureFailsClosed(t *testing.T) {
	ts := newTestSys(t, 1000, false)
	writeHosts(t, ts, "")
	ctx, stderr := newTestContext(ts, Config{ExtraHosts: "moat-proxy:@nope.invalid"})
	err := extraHostsPhase(ctx)
	exit, ok := err.(exitError)
	if !ok || exit.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}
	// HOSTS-08: exact three-line error.
	want := "Error: moat-init.sh could not resolve 'nope.invalid' for /etc/hosts entry 'moat-proxy'.\n" +
		"The container's DNS should answer this name. On Docker Desktop, verify that\n" +
		"'getent hosts nope.invalid' works inside this container.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	// HOSTS-14: 25 attempts (both lookups each) with a sleep after each.
	if ts.sleeps != dnsWaitIters {
		t.Errorf("sleeps = %d, want %d", ts.sleeps, dnsWaitIters)
	}
	if got := len(ts.resolveCalls); got != 2*dnsWaitIters {
		t.Errorf("resolver calls = %d, want %d", got, 2*dnsWaitIters)
	}
	if got := readHosts(t, ts); got != "" {
		t.Errorf("failed resolve still wrote hosts: %q", got)
	}
}

func TestExtraHostsPhaseWriteFailureFailsClosed(t *testing.T) {
	ts := newTestSys(t, 1000, false)
	// No /etc directory at all — the append cannot create the file.
	ctx, stderr := newTestContext(ts, Config{ExtraHosts: "moat-proxy:192.0.2.5"})
	err := extraHostsPhase(ctx)
	exit, ok := err.(exitError)
	if !ok || exit.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}
	// HOSTS-10: exact three-line error, UID interpolated.
	want := "Error: moat-init.sh cannot write moat-proxy to /etc/hosts (required for moat proxy resolution).\n" +
		"The container user (UID 1000) lacks permission to modify /etc/hosts.\n" +
		"Rebuild the base image so moat-init.sh runs as root, or grant CAP_DAC_OVERRIDE.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestExtraHostsPhaseEntryIndependence(t *testing.T) {
	// HOSTS-13: per-entry state resets; b does not inherit a's IP, and a
	// resolve failure budget is fresh per entry.
	ts := newTestSys(t, 0, true)
	writeHosts(t, ts, "")
	ts.resolve4["hostA"] = "10.0.0.1"
	ts.resolve4["hostB"] = "10.0.0.2"
	ctx, _ := newTestContext(ts, Config{ExtraHosts: "a:@hostA b:@hostB"})
	if err := extraHostsPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readHosts(t, ts); got != "10.0.0.1 a\n10.0.0.2 b\n" {
		t.Errorf("hosts = %q", got)
	}
}
