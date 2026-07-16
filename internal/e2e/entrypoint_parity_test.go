//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
)

// stateDumperScript is the fixed user command both parity legs run. It
// emits a machine-parseable manifest between markers covering what a naive
// file+id snapshot would miss (plan §7): identity with the supplementary
// set order-normalized, the exec'd environment (proxy auth tokens redacted
// to a fixed placeholder, per-run values dropped), /etc/hosts, the system
// git config, file trees with modes+ownership (mtimes masked by omission;
// the statsig dir compared as a content hash so a copy corruption cannot
// hide behind a mask), a census of the expected long-lived children, and a
// MOAT_INIT_FILES leak scan.
const stateDumperScript = `
echo MOAT-MANIFEST-BEGIN
echo "[identity]"
id -u
id -g
id -G | tr " " "\n" | sort -n | paste -sd " " -
echo "[env]"
env | sort \
  | grep -v "^HOSTNAME=" \
  | grep -v "^SHLVL=" \
  | grep -v "^_=" \
  | sed -E "s#^(HTTPS?_PROXY|https?_proxy)=http://moat:[^@]*@#\1=http://moat:REDACTED@#" \
  | sed -E "s#^(MOAT_SSH_TCP_ADDR)=.*#\1=REDACTED#" \
  | sed -E "s#^(SSH_AUTH_SOCK)=.*#\1=REDACTED#"
echo "[hosts]"
cat /etc/hosts 2>/dev/null || echo none
echo "[gitconfig]"
{ git config --system --list 2>/dev/null || echo none; } | sort
echo "[tree]"
for d in "$HOME/.claude" "$HOME/.codex" "$HOME/.gemini" "$HOME/.copilot" /workspace; do
  if [ -e "$d" ]; then
    echo "-- $d"
    find "$d" -path "*/statsig" -prune -o -printf "%y %M %u %g %P\n" 2>/dev/null | sort
  fi
done
if [ -f "$HOME/.claude.json" ]; then stat -c "%A %U %G .claude.json" "$HOME/.claude.json"; else echo "no .claude.json"; fi
echo "[statsig]"
if [ -d "$HOME/.claude/statsig" ]; then
  (cd "$HOME/.claude/statsig" && find . -type f | sort | xargs cat 2>/dev/null | sha256sum)
else
  echo none
fi
echo "[children]"
for want in socat Xvfb dockerd; do
  found=absent
  for c in /proc/[0-9]*/comm; do
    if [ "$(cat "$c" 2>/dev/null)" = "$want" ]; then found=running; break; fi
  done
  echo "$want $found"
done
echo "[init-files-leak]"
if env | grep -q "^MOAT_INIT_FILES="; then echo LEAKED; else echo clean; fi
echo MOAT-MANIFEST-END
`

// runEntrypointLeg starts one parity leg with the given entrypoint
// implementation selected via the operator-only host env channel, waits for
// completion, and returns the extracted manifest plus the full log text.
func runEntrypointLeg(t *testing.T, impl, name string, opts run.Options) (manifest, allLogs string, waitErr error) {
	t.Helper()
	t.Setenv("MOAT_INIT_IMPL", impl)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	opts.Name = name + "-" + impl
	r, err := mgr.Create(ctx, opts)
	if err != nil {
		t.Fatalf("Create(%s): %v", impl, err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start(%s): %v", impl, err)
	}
	waitErr = mgr.Wait(ctx, r.ID)
	time.Sleep(200 * time.Millisecond)

	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore(%s): %v", impl, err)
	}
	logs, err := store.ReadLogs(0, 5000)
	if err != nil {
		t.Fatalf("ReadLogs(%s): %v", impl, err)
	}
	var b strings.Builder
	for _, entry := range logs {
		b.WriteString(entry.Line)
		b.WriteString("\n")
	}
	allLogs = b.String()

	begin := strings.Index(allLogs, "MOAT-MANIFEST-BEGIN")
	end := strings.Index(allLogs, "MOAT-MANIFEST-END")
	if begin >= 0 && end > begin {
		manifest = allLogs[begin:end]
	}
	return manifest, allLogs, waitErr
}

// diffManifests reports the first differing line for readable failures.
func diffManifests(t *testing.T, name, sh, goM string) {
	t.Helper()
	if sh == goM {
		return
	}
	shLines, goLines := strings.Split(sh, "\n"), strings.Split(goM, "\n")
	for i := 0; i < len(shLines) || i < len(goLines); i++ {
		var a, b string
		if i < len(shLines) {
			a = shLines[i]
		}
		if i < len(goLines) {
			b = goLines[i]
		}
		if a != b {
			t.Errorf("%s: manifests diverge at line %d:\n  sh: %q\n  go: %q", name, i+1, a, b)
			break
		}
	}
	t.Errorf("%s: full manifests differ\n===== sh =====\n%s\n===== go =====\n%s", name, sh, goM)
}

// parityScenario is one config the harness diffs across implementations.
type parityScenario struct {
	name string
	opts func(t *testing.T) run.Options
}

func parityScenarios() []parityScenario {
	dumperCmd := []string{"sh", "-c", stateDumperScript}
	return []parityScenario{
		{
			// Plain run: privilege drop, baseline env, no features.
			name: "baseline",
			opts: func(t *testing.T) run.Options {
				return run.Options{Workspace: createTestWorkspace(t), Cmd: dumperCmd}
			},
		},
		{
			// Gap fixture: pre_run hook (success path) leaves its marker in
			// /workspace as moatuser before the dumper runs.
			name: "pre-run-hook",
			opts: func(t *testing.T) run.Options {
				return run.Options{
					Workspace: createTestWorkspace(t),
					Cmd:       dumperCmd,
					Config: &config.Config{
						Hooks: config.HooksConfig{PreRun: "date +%s > /workspace/.pre-run.timestamp && echo marker > /workspace/.pre-run-marker"},
					},
				}
			},
		},
		{
			// Gap fixture: MOAT_WORKSPACE_VOLUME full populate — tar copy,
			// ownership hand-off, and the .mcp.json ordering all covered by
			// the [tree] section.
			name: "workspace-volume",
			opts: func(t *testing.T) run.Options {
				return run.Options{
					Workspace:     createTestWorkspace(t),
					Cmd:           dumperCmd,
					WorkspaceMode: config.WorkspaceModeVolume,
				}
			},
		},
		{
			// Gap fixture: clipboard — Xvfb child census + DISPLAY in env.
			name: "clipboard",
			opts: func(t *testing.T) run.Options {
				return run.Options{Workspace: createTestWorkspace(t), Cmd: dumperCmd, Clipboard: true}
			},
		},
		{
			// Gap fixture: multi-record MOAT_INIT_FILES with a deep parent
			// chain — 0600 files, 0755 parents, ancestor chown, and the
			// INIT-10 scrub all visible in [tree] + [init-files-leak].
			name: "init-files-multi",
			opts: func(t *testing.T) run.Options {
				rec := func(path, content string) string {
					return path + "\t" + base64.StdEncoding.EncodeToString([]byte(content))
				}
				records := rec("/home/moatuser/.config/deep/nested/tool/config.toml", "secret-one") + "\n" +
					rec("/home/moatuser/.parityrc", "secret-two")
				return run.Options{
					Workspace: createTestWorkspace(t),
					Cmd:       dumperCmd,
					Env:       []string{"MOAT_INIT_FILES=" + records},
				}
			},
		},
	}
}

// TestEntrypointParityBaseline diffs the full manifest between the sh and
// go entrypoints on every available runtime (Docker and Apple reparent
// children differently — the census must match per-runtime).
func TestEntrypointParityBaseline(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		sc := parityScenarios()[0]
		shM, shLogs, _ := runEntrypointLeg(t, "sh", "parity-base", sc.opts(t))
		goM, goLogs, _ := runEntrypointLeg(t, "go", "parity-base", sc.opts(t))
		if shM == "" {
			t.Fatalf("sh leg produced no manifest; logs:\n%s", shLogs)
		}
		if goM == "" {
			t.Fatalf("go leg produced no manifest; logs:\n%s", goLogs)
		}
		diffManifests(t, sc.name, shM, goM)
	})
}

// TestEntrypointParityScenarios diffs the remaining scenarios on Docker
// (the full-matrix leg per the plan's runtime matrix).
func TestEntrypointParityScenarios(t *testing.T) {
	requireDocker(t)
	for _, sc := range parityScenarios()[1:] {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			shM, shLogs, _ := runEntrypointLeg(t, "sh", "parity-"+sc.name, sc.opts(t))
			goM, goLogs, _ := runEntrypointLeg(t, "go", "parity-"+sc.name, sc.opts(t))
			if shM == "" {
				t.Fatalf("sh leg produced no manifest; logs:\n%s", shLogs)
			}
			if goM == "" {
				t.Fatalf("go leg produced no manifest; logs:\n%s", goLogs)
			}
			diffManifests(t, sc.name, shM, goM)
			// The scrub is load-bearing enough to assert directly, not just
			// via cross-leg equality (both legs leaking would still match).
			for impl, m := range map[string]string{"sh": shM, "go": goM} {
				if strings.Contains(m, "LEAKED") {
					t.Errorf("%s leg leaked MOAT_INIT_FILES into the exec env", impl)
				}
			}
		})
	}
}

// TestEntrypointParityPreRunFailure is the negative probe: a failing
// pre_run hook must produce the framed #372 diagnostic and the hook's
// literal exit code on BOTH implementations, and the user command must not
// run.
func TestEntrypointParityPreRunFailure(t *testing.T) {
	requireDocker(t)
	for _, impl := range []string{"sh", "go"} {
		impl := impl
		t.Run(impl, func(t *testing.T) {
			opts := run.Options{
				Workspace: createTestWorkspace(t),
				Cmd:       []string{"sh", "-c", "echo SHOULD-NOT-RUN"},
				Config: &config.Config{
					Hooks: config.HooksConfig{PreRun: "echo doing-setup; exit 7"},
				},
			}
			manifest, logs, waitErr := runEntrypointLeg(t, impl, "parity-prerun-fail", opts)
			if manifest != "" {
				t.Error("manifest produced despite failing hook")
			}
			for _, want := range []string{
				"moat: pre_run hook failed (exit code 7)",
				"moat:   command: echo doing-setup; exit 7",
			} {
				if !strings.Contains(logs, want) {
					t.Errorf("logs missing %q:\n%s", want, logs)
				}
			}
			if strings.Contains(logs, "SHOULD-NOT-RUN") {
				t.Error("user command ran after a failing pre_run hook")
			}
			if waitErr == nil || !strings.Contains(waitErr.Error(), "7") {
				t.Errorf("wait error = %v, want the hook's literal exit code 7", waitErr)
			}
		})
	}
}

// TestEntrypointDispatcherClosedEnum is the dispatcher negative probe: an
// unknown MOAT_INIT_IMPL value must fail loudly, never fall back to an
// unintended entrypoint.
func TestEntrypointDispatcherClosedEnum(t *testing.T) {
	requireDocker(t)
	opts := run.Options{
		Workspace: createTestWorkspace(t),
		Cmd:       []string{"sh", "-c", "echo SHOULD-NOT-RUN"},
	}
	_, logs, waitErr := runEntrypointLeg(t, "bogus", "parity-bad-impl", opts)
	if !strings.Contains(logs, "Error: invalid MOAT_INIT_IMPL 'bogus'") {
		t.Errorf("logs missing the closed-enum error:\n%s", logs)
	}
	if strings.Contains(logs, "SHOULD-NOT-RUN") {
		t.Error("user command ran under an invalid dispatcher value")
	}
	if waitErr == nil {
		t.Error("run succeeded despite an invalid MOAT_INIT_IMPL")
	}
}

// TestEntrypointGoLongLivedChildren verifies the Go leg's detached children
// survive the exec handoff: the SSH bridge/Xvfb census in the manifest runs
// AFTER the entrypoint has been replaced by the user command, so a
// "running" entry proves the child outlived the exec.
func TestEntrypointGoLongLivedChildren(t *testing.T) {
	requireDocker(t)
	opts := run.Options{
		Workspace: createTestWorkspace(t),
		Cmd:       []string{"sh", "-c", stateDumperScript},
		Clipboard: true,
	}
	manifest, logs, _ := runEntrypointLeg(t, "go", "parity-children", opts)
	if manifest == "" {
		t.Fatalf("no manifest; logs:\n%s", logs)
	}
	if !strings.Contains(manifest, "Xvfb running") {
		t.Errorf("Xvfb did not survive the exec handoff:\n%s", manifest)
	}
}
