package codex

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestJoinCommand(t *testing.T) {
	p := &Provider{}

	// Interactive: choose an independent server when the CLI supports it.
	got, err := p.JoinCommand(provider.JoinOpts{})
	if err != nil {
		t.Fatalf("JoinCommand: %v", err)
	}
	if len(got) != 3 || got[0] != "sh" || got[1] != "-c" {
		t.Fatalf("interactive join = %v, want shell launcher", got)
	}

	// Companion: headless uses codex exec.
	got, err = p.JoinCommand(provider.JoinOpts{Prompt: "summarize the diff"})
	if err != nil {
		t.Fatalf("JoinCommand: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"codex", "exec", "summarize the diff"}) {
		t.Errorf("headless join = %v, want [codex exec summarize the diff]", got)
	}
}

func TestInteractiveJoinServerSelection(t *testing.T) {
	for _, tc := range []struct {
		name string
		help string
		want string
	}{
		{"supports embedded server", "Usage: codex [OPTIONS]\n  --no-daemon  Run without the background server", "--no-daemon\n"},
		{"older CLI", "Usage: codex [OPTIONS]\n  --help  Print help", "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// A failed launch must propagate its status rather than retrying
			// against the primary's daemon. No real Codex or credentials needed.
			stub := "#!/bin/sh\nif [ \"$1\" = --help ]; then\n  printf '%s\\n' \"$JOIN_HELP\"\n  exit 0\nfi\nprintf '%s\\n' \"$*\"\nexit 23\n"
			if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			argv, err := (&Provider{}).JoinCommand(provider.JoinOpts{})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "JOIN_HELP="+tc.help)
			out, err := cmd.CombinedOutput()
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
				t.Fatalf("launch error = %v, want exit 23; output: %s", err, out)
			}
			if string(out) != tc.want {
				t.Errorf("launch arguments = %q, want %q", out, tc.want)
			}
		})
	}
}

func TestJoinCommandRejectsUnsupportedSessionFlags(t *testing.T) {
	p := &Provider{}

	if _, err := p.JoinCommand(provider.JoinOpts{Continue: true}); err == nil {
		t.Error("--continue should error rather than being silently dropped")
	} else if !strings.Contains(err.Error(), "--continue") {
		t.Errorf("error should name the flag; got %q", err)
	}

	// Companion: --resume errors the same way.
	if _, err := p.JoinCommand(provider.JoinOpts{Resume: "abc123"}); err == nil {
		t.Error("--resume should error rather than being silently dropped")
	} else if !strings.Contains(err.Error(), "--resume") {
		t.Errorf("error should name the flag; got %q", err)
	}
}

func TestIdentifiesAs(t *testing.T) {
	p := &Provider{}
	if !p.IdentifiesAs("codex") {
		t.Error("IdentifiesAs(codex) should be true")
	}
	// Companion: it must not claim other agents' runs.
	if p.IdentifiesAs("claude") {
		t.Error("IdentifiesAs(claude) should be false")
	}
}
