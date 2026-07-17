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
	"github.com/majorcontext/moat/internal/initbin"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
)

// The moat-init Go entrypoint is the sole container entrypoint. This harness
// runs it in a real container across the feature scenarios and asserts the
// observable post-start state directly (identity, staged files + modes, the
// exec'd environment, long-lived children). It replaced the sh-vs-go
// differential harness when the shell entrypoint was removed; the pure-logic
// parity checks now live in internal/moatinit (unit + live-tool differential
// tests).

// stateDumperScript emits a machine-parseable manifest between markers: the
// exec'd process identity, its environment (proxy auth tokens redacted,
// per-run values dropped), the system git config, staged file trees with
// modes+ownership, a census of the expected long-lived children, and a
// MOAT_INIT_FILES leak scan.
const stateDumperScript = `
echo MOAT-MANIFEST-BEGIN
echo "[identity]"
echo "user=$(id -un)"
echo "uid=$(id -u)"
echo "gid=$(id -g)"
echo "[env]"
env | sort \
  | grep -v "^HOSTNAME=" \
  | grep -v "^SHLVL=" \
  | grep -v "^_=" \
  | sed -E "s#^(HTTPS?_PROXY|https?_proxy)=http://moat:[^@]*@#\1=http://moat:REDACTED@#" \
  | sed -E "s#^(MOAT_SSH_TCP_ADDR)=.*#\1=REDACTED#" \
  | sed -E "s#^(SSH_AUTH_SOCK)=.*#\1=REDACTED#"
echo "[gitconfig]"
{ git config --system --list 2>/dev/null || echo none; } | sort
echo "[tree]"
for d in "$HOME/.claude" "$HOME/.codex" "$HOME/.gemini" "$HOME/.copilot" /workspace; do
  if [ -e "$d" ]; then
    echo "-- $d"
    find "$d" -printf "%y %M %u %g %P\n" 2>/dev/null | sort
  fi
done
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

var dumperCmd = []string{"sh", "-c", stateDumperScript}

// runEntrypoint starts a run, waits for it, and returns the extracted
// manifest plus the full log text and the wait error.
func runEntrypoint(t *testing.T, name string, opts run.Options) (manifest, allLogs string, waitErr error) {
	t.Helper()
	if initbin.IsStub(initbin.Binary()) {
		// The test binary embeds the same initbin blob writeEntrypoint ships:
		// a stub would exec the fail-closed placeholder as PID 1 and fail
		// confusingly. `make test-e2e` regenerates the real binary first.
		t.Skip("embedded moat-init binary is the committed stub — run 'make generate-init' (or 'make test-e2e', which does) first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	opts.Name = name
	r, err := mgr.Create(ctx, opts)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitErr = mgr.Wait(ctx, r.ID)
	time.Sleep(200 * time.Millisecond)

	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	logs, err := store.ReadLogs(0, 5000)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
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

// section returns the lines of a "[name]" manifest section (up to the next
// "[" header or the end marker).
func section(manifest, name string) []string {
	start := strings.Index(manifest, "["+name+"]")
	if start < 0 {
		return nil
	}
	rest := manifest[start+len("["+name+"]"):]
	var out []string
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") || line == "MOAT-MANIFEST-END" {
			break
		}
		out = append(out, line)
	}
	return out
}

func manifestHas(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// assertBaseline checks the invariants every run must satisfy: the command
// runs as moatuser (uid 5000, privilege drop happened) and no secret env
// leaked.
func assertBaseline(t *testing.T, manifest string) {
	t.Helper()
	id := section(manifest, "identity")
	if !manifestHas(id, "user=moatuser") {
		t.Errorf("command did not run as moatuser:\n%v", id)
	}
	if !manifestHas(id, "uid=5000") {
		t.Errorf("privilege drop did not reach uid 5000:\n%v", id)
	}
	if leak := section(manifest, "init-files-leak"); !manifestHas(leak, "clean") {
		t.Errorf("MOAT_INIT_FILES leaked into the exec env:\n%v", leak)
	}
}

// TestEntrypointBaseline runs a plain command on every available runtime and
// asserts the privilege drop and env scrub.
func TestEntrypointBaseline(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		m, logs, _ := runEntrypoint(t, "acc-baseline", run.Options{
			Workspace: createTestWorkspace(t),
			Cmd:       dumperCmd,
		})
		if m == "" {
			t.Fatalf("no manifest; logs:\n%s", logs)
		}
		assertBaseline(t, m)
	})
}

// TestEntrypointFeatureScenarios exercises the feature phases on Docker and
// asserts each phase's observable result.
func TestEntrypointFeatureScenarios(t *testing.T) {
	requireDocker(t)

	t.Run("pre-run-hook", func(t *testing.T) {
		m, logs, _ := runEntrypoint(t, "acc-prerun", run.Options{
			Workspace: createTestWorkspace(t),
			Cmd:       dumperCmd,
			Config: &config.Config{
				Hooks: config.HooksConfig{PreRun: "echo marker > /workspace/.pre-run-marker"},
			},
		})
		if m == "" {
			t.Fatalf("no manifest; logs:\n%s", logs)
		}
		assertBaseline(t, m)
		// The hook ran as moatuser in /workspace and left an owned marker.
		if ws := section(m, "tree"); !hasFileOwned(ws, ".pre-run-marker", "moatuser") {
			t.Errorf("pre_run marker missing or not owned by moatuser:\n%v", ws)
		}
	})

	t.Run("workspace-volume", func(t *testing.T) {
		m, logs, _ := runEntrypoint(t, "acc-wsvol", run.Options{
			Workspace:     createTestWorkspace(t),
			Cmd:           dumperCmd,
			WorkspaceMode: config.WorkspaceModeVolume,
		})
		if m == "" {
			t.Fatalf("no manifest; logs:\n%s", logs)
		}
		assertBaseline(t, m)
		// The populated /workspace is owned by moatuser (the recursive chown
		// after the tar copy).
		if ws := section(m, "tree"); !hasAnyOwned(ws, "moatuser") {
			t.Errorf("/workspace not owned by moatuser after populate:\n%v", ws)
		}
	})

	t.Run("clipboard", func(t *testing.T) {
		m, logs, _ := runEntrypoint(t, "acc-clip", run.Options{
			Workspace: createTestWorkspace(t),
			Cmd:       dumperCmd,
			Clipboard: true,
		})
		if m == "" {
			t.Fatalf("no manifest; logs:\n%s", logs)
		}
		assertBaseline(t, m)
		if env := section(m, "env"); !manifestHas(env, "DISPLAY=:99") {
			t.Errorf("DISPLAY not exported for clipboard:\n%v", env)
		}
		// Xvfb is a long-lived child that must survive the exec handoff.
		if kids := section(m, "children"); !manifestHas(kids, "Xvfb running") {
			t.Errorf("Xvfb did not survive the exec handoff:\n%v", kids)
		}
	})

	t.Run("init-files-multi", func(t *testing.T) {
		rec := func(path, content string) string {
			return path + "\t" + base64.StdEncoding.EncodeToString([]byte(content))
		}
		records := rec("/home/moatuser/.config/deep/nested/tool/config.toml", "secret-one") + "\n" +
			rec("/home/moatuser/.acc-initrc", "secret-two")
		m, logs, _ := runEntrypoint(t, "acc-initfiles", run.Options{
			Workspace: createTestWorkspace(t),
			Cmd:       dumperCmd,
			Env:       []string{"MOAT_INIT_FILES=" + records},
		})
		if m == "" {
			t.Fatalf("no manifest; logs:\n%s", logs)
		}
		// The scrub is the load-bearing assertion here.
		assertBaseline(t, m)
		// Both secret files exist at 0600, owned by moatuser (visible in the
		// home tree).
		tree := section(m, "tree")
		if !hasFileMode(tree, ".acc-initrc", "-rw-------") {
			t.Errorf("init file .acc-initrc not at 0600:\n%v", tree)
		}
		if !hasFileMode(tree, "config.toml", "-rw-------") {
			t.Errorf("nested init file not at 0600:\n%v", tree)
		}
	})
}

// TestEntrypointPreRunFailure is the negative probe: a failing pre_run hook
// produces the framed #372 diagnostic and the hook's literal exit code, and
// the user command does not run.
func TestEntrypointPreRunFailure(t *testing.T) {
	requireDocker(t)
	_, logs, waitErr := runEntrypoint(t, "acc-prerun-fail", run.Options{
		Workspace: createTestWorkspace(t),
		Cmd:       []string{"sh", "-c", "echo SHOULD-NOT-RUN"},
		Config: &config.Config{
			Hooks: config.HooksConfig{PreRun: "echo doing-setup; exit 7"},
		},
	})
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
	// "exit code 7" (not a bare "7", which 17/27/127 would satisfy).
	if waitErr == nil || !strings.Contains(waitErr.Error(), "exit code 7") {
		t.Errorf("wait error = %v, want the hook's literal exit code 7", waitErr)
	}
}

// hasFileOwned reports whether the tree section has a regular-file line for
// base name `name` owned by `owner`. Tree lines are "%y %M %u %g %P".
func hasFileOwned(tree []string, name, owner string) bool {
	for _, l := range tree {
		f := strings.Fields(l)
		if len(f) >= 5 && f[0] == "f" && f[2] == owner && baseName(f[4]) == name {
			return true
		}
	}
	return false
}

func hasFileMode(tree []string, name, mode string) bool {
	for _, l := range tree {
		f := strings.Fields(l)
		if len(f) >= 5 && f[0] == "f" && f[1] == mode && baseName(f[4]) == name {
			return true
		}
	}
	return false
}

func hasAnyOwned(tree []string, owner string) bool {
	for _, l := range tree {
		f := strings.Fields(l)
		if len(f) >= 3 && f[2] == owner {
			return true
		}
	}
	return false
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
