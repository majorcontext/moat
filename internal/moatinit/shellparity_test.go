//go:build linux

package moatinit

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// This file is a differential pressure harness: it runs the REAL
// internal/deps/scripts/moat-init.sh and the REAL compiled Go entrypoint
// side by side against crafted environments and compares observable
// behavior — exit codes, contract stderr, the child-visible environment,
// and the resulting file trees (paths, types, modes, content).
//
// It covers the non-root branch only (the test process is not root; the
// root/gosu legs are the e2e parity harness's job) and avoids every phase
// that touches shared absolute paths (/etc/hosts writes, /run/moat/ssh,
// /workspace/.mcp.json, populate) except where the phase's own guard makes
// the case side-effect-free (root guards, malformed-entry skips,
// resolve-before-write failures).

var (
	buildOnce sync.Once
	goBinPath string
	buildErr  error
)

func builtGoEntrypoint(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "moat-init-parity")
		if err != nil {
			buildErr = err
			return
		}
		goBinPath = filepath.Join(dir, "moat-init")
		cmd := exec.Command("go", "build", "-o", goBinPath, "github.com/majorcontext/moat/cmd/moat-init")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("building entrypoint: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return goBinPath
}

func scriptPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../deps/scripts/moat-init.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("moat-init.sh not found: %v", err)
	}
	return p
}

// legResult is one implementation's observable outcome.
type legResult struct {
	exit   int
	stdout string
	stderr string
	tree   string // normalized listing of the leg's HOME
}

// runLeg executes one implementation with an isolated HOME and the given
// MOAT_* env, returning the observable outcome. baseEnv entries may contain
// the placeholder @HOME@ which is substituted with the leg's home dir.
func runLeg(t *testing.T, argv []string, home string, env map[string]string, cmdArgs []string) legResult {
	t.Helper()
	full := append(append([]string{}, argv...), cmdArgs...)
	cmd := exec.Command(full[0], full[1:]...)
	cmd.Dir = home // deterministic cwd for both legs
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+strings.ReplaceAll(v, "@HOME@", home))
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if ok := errAs(err, &exitErr); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("running %v: %v", full, err)
		}
	}
	return legResult{exit: code, stdout: stdout.String(), stderr: stderr.String(), tree: treeListing(t, home)}
}

func errAs(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

// treeListing renders a home dir as "relpath type mode sha256[:12]" lines,
// mtime-free and root-relative so two legs' trees compare byte-for-byte.
func treeListing(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			dest, _ := os.Readlink(p)
			lines = append(lines, fmt.Sprintf("%s symlink -> %s", rel, dest))
		case d.IsDir():
			lines = append(lines, fmt.Sprintf("%s dir %o", rel, info.Mode().Perm()))
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			lines = append(lines, fmt.Sprintf("%s file %o %s", rel, info.Mode().Perm(), hex.EncodeToString(sum[:])[:12]))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// normalizeChildEnv filters an `env` dump down to comparable lines: the
// shell leg adds PWD/SHLVL/OLDPWD/_ that the exec'd-direct Go leg does not,
// and HOME differs per leg.
func normalizeChildEnv(out, home string) string {
	keep := make([]string, 0, 16)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "PWD="), strings.HasPrefix(line, "OLDPWD="),
			strings.HasPrefix(line, "SHLVL="), strings.HasPrefix(line, "_="):
			continue
		}
		keep = append(keep, strings.ReplaceAll(line, home, "@HOME@"))
	}
	sort.Strings(keep)
	return strings.Join(keep, "\n")
}

// stage writes a staging file with an explicit mode.
func stageParity(t *testing.T, dir, name string, mode os.FileMode, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

// parityCase is one differential scenario.
type parityCase struct {
	name string
	// setup stages shared fixtures and returns the MOAT_* env (values may
	// use @HOME@) plus the user command. stderrExact compares stderr
	// byte-for-byte; envCompare compares the normalized child `env` output
	// (the command must then be []string{"env"}).
	setup       func(t *testing.T, shared string) (env map[string]string, cmd []string)
	stderrExact bool
	// stderrFramed compares stderr only from the framed "moat:" block on:
	// a signal-killed hook makes the SHELL itself print a job-status line
	// ("Terminated") before the framed message — incidental shell output,
	// not part of the scripted contract.
	stderrFramed bool
	envCompare   bool
	wantExit     int
}

func TestShellGoDifferentialParity(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	goBin := builtGoEntrypoint(t)
	script := scriptPath(t)
	xvfbPresent := func() bool { _, err := exec.LookPath("Xvfb"); return err == nil }()

	cases := []parityCase{
		{
			name: "baseline env passthrough",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				return map[string]string{"SOME_USER_VAR": "kept"}, []string{"env"}
			},
			envCompare: true,
		},
		{
			name: "claude staging full allowlist",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				staging := filepath.Join(shared, "claude-init")
				stageParity(t, staging, "settings.json", 0o640, `{"s":1}`)
				stageParity(t, staging, ".credentials.json", 0o644, `{"token":"x"}`)
				stageParity(t, staging, "remote-settings.json", 0o644, `{"r":1}`)
				stageParity(t, staging, "stats-cache.json", 0o644, `{}`)
				stageParity(t, staging, "CLAUDE.md", 0o644, "ctx")
				stageParity(t, staging, ".claude.json", 0o644, `{"ok":true}`)
				stageParity(t, filepath.Join(staging, "statsig"), "cache.db", 0o600, "st")
				stageParity(t, staging, "stray.txt", 0o644, "must not copy")
				stageParity(t, staging, "mcp.json", 0o644, "not on claude allowlist")
				return map[string]string{"MOAT_CLAUDE_INIT": staging}, []string{"true"}
			},
			stderrExact: true,
		},
		{
			name: "codex and gemini staging modes",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				codex := filepath.Join(shared, "codex-init")
				stageParity(t, codex, "config.toml", 0o644, "cfg")
				stageParity(t, codex, "auth.json", 0o644, `{"k":"v"}`)
				stageParity(t, codex, "AGENTS.md", 0o644, "agents")
				gemini := filepath.Join(shared, "gemini-init")
				stageParity(t, gemini, "settings.json", 0o640, `{}`)
				stageParity(t, gemini, "oauth_creds.json", 0o644, `{}`)
				return map[string]string{"MOAT_CODEX_INIT": codex, "MOAT_GEMINI_INIT": gemini}, []string{"true"}
			},
			stderrExact: true,
		},
		{
			name: "copilot staging",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				st := filepath.Join(shared, "copilot-init")
				stageParity(t, st, "config.json", 0o644, `{}`)
				stageParity(t, st, "settings.json", 0o600, `{}`)
				stageParity(t, st, "permissions-config.json", 0o644, `{}`)
				return map[string]string{"MOAT_COPILOT_INIT": st}, []string{"true"}
			},
			stderrExact: true,
		},
		{
			name: "init files multi-record deep chain + env scrub",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				rec := func(path, content string) string {
					return path + "\t" + base64.StdEncoding.EncodeToString([]byte(content))
				}
				records := rec("@HOME@/.config/deep/nested/tool/config.toml", "secret-one") + "\n" +
					rec("@HOME@/.toolrc", "secret-two") + "\n"
				return map[string]string{"MOAT_INIT_FILES": records}, []string{"env"}
			},
			envCompare: true,
		},
		{
			name: "clipboard exports DISPLAY",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				if xvfbPresent {
					t.Skip("Xvfb installed; skipping to avoid spawning a real X server")
				}
				return map[string]string{"MOAT_CLIPBOARD": "1"}, []string{"env"}
			},
			envCompare: true,
		},
		{
			name: "git system config identical",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				if _, err := exec.LookPath("git"); err != nil {
					t.Skip("git not installed")
				}
				return map[string]string{
					"GIT_CONFIG_SYSTEM":   "@HOME@/system-gitconfig",
					"MOAT_GIT_USER_NAME":  `Ada "quoted" Lovelace`,
					"MOAT_GIT_USER_EMAIL": "ada@example.com",
					"MOAT_GIT_SSH_GITHUB": "1",
				}, []string{"true"}
			},
			stderrExact: true,
		},
		{
			name: "git insteadOf opt-out",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				if _, err := exec.LookPath("git"); err != nil {
					t.Skip("git not installed")
				}
				return map[string]string{
					"GIT_CONFIG_SYSTEM":   "@HOME@/system-gitconfig",
					"MOAT_GIT_SSH_GITHUB": "0",
				}, []string{"true"}
			},
			stderrExact: true,
		},
		{
			name: "docker mutex fatal",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				return map[string]string{"MOAT_DOCKER_DIND": "1", "MOAT_DOCKER_GID": "999"}, []string{"true"}
			},
			stderrExact: true,
			wantExit:    1,
		},
		{
			name: "dind silently skipped as non-root",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				return map[string]string{"MOAT_DOCKER_DIND": "1"}, []string{"true"}
			},
			stderrExact: true,
		},
		{
			name: "populate root guard fatal",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				return map[string]string{"MOAT_WORKSPACE_VOLUME": "1"}, []string{"true"}
			},
			stderrExact: true,
			wantExit:    1,
		},
		{
			name: "volume chown skipped as non-root",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				return map[string]string{"MOAT_VOLUME_CHOWN": "/nonexistent/vol"}, []string{"true"}
			},
			stderrExact: true,
		},
		{
			name: "pre_run hook success writes marker",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				return map[string]string{"MOAT_PRE_RUN": "echo hooked > \"$HOME/.hook-marker\""}, []string{"true"}
			},
			stderrExact: true,
		},
		{
			name: "pre_run hook failure passes literal 42",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				return map[string]string{"MOAT_PRE_RUN": "echo doing-setup; exit 42"}, []string{"sh", "-c", "echo SHOULD-NOT-RUN"}
			},
			stderrExact: true,
			wantExit:    42,
		},
		{
			name: "pre_run hook signal-killed reports 143",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				return map[string]string{"MOAT_PRE_RUN": "kill -TERM $$"}, []string{"true"}
			},
			stderrFramed: true,
			wantExit:     143,
		},
		{
			name: "pre_run whitespace-only hook runs",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				return map[string]string{"MOAT_PRE_RUN": " "}, []string{"true"}
			},
			stderrExact: true,
		},
		{
			name: "extra hosts malformed entries all skipped",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				return map[string]string{"MOAT_EXTRA_HOSTS": "moat-proxy: :1.2.3.4 foo x:x"}, []string{"true"}
			},
			stderrExact: true,
		},
		{
			name: "exec exit code passthrough",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				return nil, []string{"sh", "-c", "exit 9"}
			},
			stderrExact: true,
			wantExit:    9,
		},
		{
			name: "exec command not found is 127",
			setup: func(t *testing.T, shared string) (map[string]string, []string) {
				return nil, []string{"definitely-not-a-real-command-xyz"}
			},
			wantExit: 127, // stderr wording is tool-generated and differs; codes must match
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			shared := t.TempDir()
			env, cmdArgs := tc.setup(t, shared)

			homeSh, homeGo := t.TempDir(), t.TempDir()
			sh := runLeg(t, []string{"sh", script}, homeSh, env, cmdArgs)
			goL := runLeg(t, []string{goBin}, homeGo, env, cmdArgs)

			if sh.exit != goL.exit {
				t.Errorf("exit codes diverge: sh=%d go=%d\nsh stderr:\n%s\ngo stderr:\n%s",
					sh.exit, goL.exit, sh.stderr, goL.stderr)
			}
			if sh.exit != tc.wantExit {
				t.Errorf("sh exit = %d, want %d (stderr:\n%s)", sh.exit, tc.wantExit, sh.stderr)
			}
			if tc.stderrExact {
				shErr := strings.ReplaceAll(sh.stderr, homeSh, "@HOME@")
				goErr := strings.ReplaceAll(goL.stderr, homeGo, "@HOME@")
				if shErr != goErr {
					t.Errorf("stderr diverges:\n--- sh ---\n%q\n--- go ---\n%q", shErr, goErr)
				}
			}
			if tc.stderrFramed {
				frame := func(s string) string {
					idx := strings.Index(s, "moat: ")
					if idx < 0 {
						return s
					}
					return s[idx:]
				}
				if frame(sh.stderr) != frame(goL.stderr) {
					t.Errorf("framed stderr diverges:\n--- sh ---\n%q\n--- go ---\n%q",
						frame(sh.stderr), frame(goL.stderr))
				}
			}
			if tc.envCompare {
				shEnv := normalizeChildEnv(sh.stdout, homeSh)
				goEnv := normalizeChildEnv(goL.stdout, homeGo)
				if shEnv != goEnv {
					t.Errorf("child env diverges:\n--- sh ---\n%s\n--- go ---\n%s", shEnv, goEnv)
				}
			}
			if sh.tree != goL.tree {
				t.Errorf("home trees diverge:\n--- sh ---\n%s\n--- go ---\n%s", sh.tree, goL.tree)
			}
		})
	}
}

// TestShellGoDifferentialResolveFailure is split out (≈10s of real retry
// budget across both legs) and skipped under -short: an unresolvable
// '@'-target must fail closed with the identical three-line error in both
// implementations.
func TestShellGoDifferentialResolveFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("10s retry budget; skipped with -short")
	}
	if _, err := exec.LookPath("getent"); err != nil {
		t.Skip("getent not installed (script leg needs it)")
	}
	goBin := builtGoEntrypoint(t)
	script := scriptPath(t)
	env := map[string]string{"MOAT_EXTRA_HOSTS": "moat-proxy:@nope.invalid"}

	sh := runLeg(t, []string{"sh", script}, t.TempDir(), env, []string{"true"})
	goL := runLeg(t, []string{goBin}, t.TempDir(), env, []string{"true"})

	if sh.exit != 1 || goL.exit != 1 {
		t.Fatalf("exits: sh=%d go=%d, want 1/1", sh.exit, goL.exit)
	}
	if sh.stderr != goL.stderr {
		t.Errorf("stderr diverges:\n--- sh ---\n%q\n--- go ---\n%q", sh.stderr, goL.stderr)
	}
}

// TestShellGoInitFilesInvalidBase64 pins the one sanctioned residue
// divergence on the invalid-payload fatal: both implementations abort
// non-zero before exec, the shell leaves a truncated/empty file behind
// (redirect-then-decode), the Go port decodes to a buffer first and leaves
// nothing.
func TestShellGoInitFilesInvalidBase64(t *testing.T) {
	goBin := builtGoEntrypoint(t)
	script := scriptPath(t)
	env := map[string]string{"MOAT_INIT_FILES": "@HOME@/.sec/cfg\t!!!not-base64!!!"}

	homeSh, homeGo := t.TempDir(), t.TempDir()
	sh := runLeg(t, []string{"sh", script}, homeSh, env, []string{"sh", "-c", "echo SHOULD-NOT-RUN"})
	goL := runLeg(t, []string{goBin}, homeGo, env, []string{"sh", "-c", "echo SHOULD-NOT-RUN"})

	if sh.exit == 0 || goL.exit == 0 {
		t.Fatalf("invalid base64 must be fatal: sh=%d go=%d", sh.exit, goL.exit)
	}
	for name, r := range map[string]legResult{"sh": sh, "go": goL} {
		if strings.Contains(r.stdout, "SHOULD-NOT-RUN") {
			t.Errorf("%s leg exec'd the command after a fatal init-files record", name)
		}
	}
	// Shell residue: the redirect truncates the file before base64 fails.
	if _, err := os.Stat(filepath.Join(homeSh, ".sec/cfg")); err != nil {
		t.Errorf("expected the shell leg's partial file (documents the baseline): %v", err)
	}
	// Go: decode-to-buffer leaves nothing (sanctioned hardening, plan B-P1).
	if _, err := os.Stat(filepath.Join(homeGo, ".sec/cfg")); !os.IsNotExist(err) {
		t.Error("go leg left a partial secret file behind")
	}
}

// TestSplitInitRecordMatchesLiveShell differentially checks the record
// splitter against the script's actual `IFS=<tab> read -r` loop for a
// corpus of adversarial record shapes.
func TestSplitInitRecordMatchesLiveShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	corpus := []string{
		"a\tXX", "\tXX", "\t\tXX", "a", "a\tb\tc", "a\t\tb", "a\tb\t",
		"a\tb\t\t", "a\t", "", "path with space\tQUJD", "\t", "\t\t",
		"a\tb c d", "üñï\tßase64", "a\t b", " a\tb", "a \tb",
	}
	for _, line := range corpus {
		gotPath, gotContent := splitInitRecord(line)
		// \036 (record separator, octal — portable in POSIX printf, unlike
		// \xHH) delimits the two captured fields.
		out, err := exec.Command("sh", "-c",
			`printf '%s\n' "$1" | while IFS="$(printf '\t')" read -r filepath content; do printf '%s\036%s' "$filepath" "$content"; done`,
			"sh", line).Output()
		if err != nil {
			t.Fatalf("shell probe for %q: %v", line, err)
		}
		wantPath, wantContent := "", ""
		if parts := strings.SplitN(string(out), "\x1e", 2); len(parts) == 2 {
			wantPath, wantContent = parts[0], parts[1]
		}
		if gotPath != wantPath || gotContent != wantContent {
			t.Errorf("splitInitRecord(%q) = (%q, %q); live sh read gives (%q, %q)",
				line, gotPath, gotContent, wantPath, wantContent)
		}
	}
}

// TestDecodeInitContentMatchesCoreutils differentially checks base64
// accept/reject parity against `base64 -d` for a corpus of payload shapes.
func TestDecodeInitContentMatchesCoreutils(t *testing.T) {
	if _, err := exec.LookPath("base64"); err != nil {
		t.Skip("base64 not installed")
	}
	corpus := []string{
		"", "YWJj", "YW\r\nJj", "YWJjZA==", "YWJjZA=", "!!!", "YWJj ", " YWJj",
		"Y W J j", "====", "AA==", strings.Repeat("QUJDREVGRw==", 1),
	}
	for _, payload := range corpus {
		goBytes, goErr := decodeInitContent(payload)
		cmd := exec.Command("base64", "-d")
		cmd.Stdin = strings.NewReader(payload)
		shBytes, shErr := cmd.Output()
		if (goErr == nil) != (shErr == nil) {
			t.Errorf("decode divergence for %q: go err=%v, base64 -d err=%v", payload, goErr, shErr)
			continue
		}
		if goErr == nil && string(goBytes) != string(shBytes) {
			t.Errorf("decoded bytes diverge for %q: go=%q sh=%q", payload, goBytes, shBytes)
		}
	}
}
