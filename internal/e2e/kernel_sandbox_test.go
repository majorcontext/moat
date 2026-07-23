//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"runtime"
	"strings"
	"testing"

	llsys "github.com/landlock-lsm/go-landlock/landlock/syscall"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/run"
)

// sandboxProbeScript exercises the kernel sandbox boundary from inside the
// container. /opt/probe is created writable-by-moatuser at image build time,
// so a write failure there can only come from Landlock, not from DAC
// permissions. /opt/extra proves isolation.sandbox.allow_write works.
const sandboxProbeScript = `
echo "WS_WRITE=$(touch /workspace/probe 2>/dev/null && echo ok || echo denied)"
echo "HOME_WRITE=$(touch "$HOME/probe" 2>/dev/null && echo ok || echo denied)"
echo "TMP_WRITE=$(touch /tmp/probe 2>/dev/null && echo ok || echo denied)"
echo "PROBE_WRITE=$(touch /opt/probe/f 2>/dev/null && echo ok || echo denied)"
echo "EXTRA_WRITE=$(touch /opt/extra/f 2>/dev/null && echo ok || echo denied)"
echo "ETC_READ=$(cat /etc/os-release >/dev/null 2>&1 && echo ok || echo denied)"
`

const sandboxProbeHook = "mkdir -p /opt/probe /opt/extra && chown moatuser:moatuser /opt/probe /opt/extra"

// requireLandlock skips unless the host kernel supports Landlock. Containers
// share the host kernel on Linux, so a host probe is authoritative; on other
// hosts the container kernel cannot be probed from the test process.
func requireLandlock(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Landlock e2e requires a Linux host (container kernel == host kernel)")
	}
	if abi, err := llsys.LandlockGetABIVersion(); err != nil || abi < 1 {
		t.Skip("host kernel does not support Landlock")
	}
}

// runSandboxProbe creates, starts, and waits for a run executing
// sandboxProbeScript with the given config, returning all captured log lines.
func runSandboxProbe(t *testing.T, name string, cfg *config.Config) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	r, err := mgr.Create(ctx, run.Options{
		Name:      name,
		Workspace: createTestWorkspace(t),
		Cmd:       []string{"sh", "-c", sandboxProbeScript},
		Config:    cfg,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	logs, err := r.Store.ReadLogs(0, 200)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	lines := make([]string, 0, len(logs))
	for _, entry := range logs {
		lines = append(lines, entry.Line)
	}
	return lines
}

func assertLogLine(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, line := range lines {
		if strings.Contains(line, want) {
			return
		}
	}
	t.Errorf("logs missing %q:\n%s", want, strings.Join(lines, "\n"))
}

// TestKernelSandboxEnforced runs a real container with
// isolation.kernel_sandbox and asserts the Landlock write-allowlist holds:
// workspace/home/tmp/allow_write writable, a moatuser-owned path outside the
// allowlist denied, reads everywhere intact.
func TestKernelSandboxEnforced(t *testing.T) {
	requireDocker(t)
	requireLandlock(t)

	cfg := &config.Config{
		Isolation: config.IsolationConfig{
			KernelSandbox: true,
			Sandbox: config.SandboxPathsConfig{
				AllowWrite: []string{"/opt/extra"},
			},
		},
		Hooks: config.HooksConfig{PostBuildRoot: sandboxProbeHook},
	}
	lines := runSandboxProbe(t, "test-kernel-sandbox-on", cfg)

	assertLogLine(t, lines, "kernel sandbox active (Landlock ABI v")
	assertLogLine(t, lines, "WS_WRITE=ok")
	assertLogLine(t, lines, "HOME_WRITE=ok")
	assertLogLine(t, lines, "TMP_WRITE=ok")
	assertLogLine(t, lines, "PROBE_WRITE=denied")
	assertLogLine(t, lines, "EXTRA_WRITE=ok")
	assertLogLine(t, lines, "ETC_READ=ok")
}

// TestKernelSandboxDisabled is the companion: the identical image and probe
// without isolation.kernel_sandbox must not restrict anything and must not
// announce a sandbox — proving the denial above comes from Landlock, not
// from image permissions.
func TestKernelSandboxDisabled(t *testing.T) {
	requireDocker(t)
	requireLandlock(t)

	cfg := &config.Config{
		Hooks: config.HooksConfig{PostBuildRoot: sandboxProbeHook},
	}
	lines := runSandboxProbe(t, "test-kernel-sandbox-off", cfg)

	assertLogLine(t, lines, "PROBE_WRITE=ok")
	assertLogLine(t, lines, "WS_WRITE=ok")
	for _, line := range lines {
		if strings.Contains(line, "kernel sandbox active") {
			t.Errorf("unsandboxed run announced a kernel sandbox: %q", line)
		}
	}
}
