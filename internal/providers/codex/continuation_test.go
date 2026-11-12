package codex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/spf13/cobra"
)

func TestCodexContinuation(t *testing.T) {
	original := codexContinue
	t.Cleanup(func() { codexContinue = original })
	for _, flag := range []string{"--continue", "-c", "--continue=false"} {
		root := &cobra.Command{Use: "moat"}
		(&Provider{}).RegisterCLI(root)
		cmd, _, _ := root.Find([]string{"codex"})
		if err := cmd.ParseFlags([]string{flag}); err != nil {
			t.Fatal(err)
		}
		continuing := flag != "--continue=false"
		if codexContinue != continuing {
			t.Fatalf("%s parsed incorrectly", flag)
		}
		for _, mode := range []string{"interactive", "initial", "print"} {
			prompt, initial := "", ""
			want := []string{"codex"}
			if mode == "print" {
				prompt = "follow up"
				want = append(want, "exec")
			}
			if mode == "initial" {
				initial = "follow up"
			}
			if continuing {
				want = append(want, "resume", "--last")
			}
			if mode != "interactive" {
				want = append(want, "follow up")
			}
			if got := buildCodexCommand(prompt, initial, codexContinue); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s/%s: %q, want %q", flag, mode, got, want)
			}
		}
		for _, sync := range []*bool{nil, boolPtr(true), boolPtr(false)} {
			cfg := &config.Config{Codex: config.CodexConfig{SyncLogs: sync}}
			err := validateCodexContinuation(cfg)
			if (err != nil) != (continuing && sync != nil && !*sync) {
				t.Fatalf("sync %v: %v", sync, err)
			}
		}
		if err := validateCodexContinuation(nil); err != nil {
			t.Fatal(err)
		}
	}
}

func boolPtr(value bool) *bool { return &value }

func TestCodexSharedSessionsContinuation(t *testing.T) {
	original := codexContinue
	t.Cleanup(func() { codexContinue = original })
	for _, tc := range []struct {
		name         string
		continuation bool
	}{
		{"normal launch allows shared sessions", false},
		{"continuation rejects shared sessions", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codexContinue = tc.continuation
			cfg := &config.Config{Codex: config.CodexConfig{SharedSessions: true}}
			err := validateCodexContinuation(cfg)
			if !tc.continuation {
				if err != nil {
					t.Fatalf("normal launch rejected shared sessions: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("continuation accepted shared sessions")
			}
			for _, detail := range []string{"--continue", "codex.shared_sessions", "/workspace", "another project's conversation"} {
				if !strings.Contains(err.Error(), detail) {
					t.Errorf("error %q does not explain %q", err, detail)
				}
			}
		})
	}
}

// Exercise real CLI wiring, but replace the executor before it can create any
// runtime resources. A YAML command must not discard the continuation command.
func TestCodexContinuationOverridesYAMLCommand(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	t.Setenv("MOAT_KEYRING_BACKEND", "file")
	originalExec, originalDryRun, originalContinue := cli.ExecuteRun, cli.DryRun, codexContinue
	t.Cleanup(func() { cli.ExecuteRun, cli.DryRun, codexContinue = originalExec, originalDryRun, originalContinue })
	cli.DryRun = false
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "moat.yaml"), []byte("command: [echo, custom]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"", "follow up"} {
		called := false
		cli.ExecuteRun = func(_ context.Context, opts cli.ExecOptions) (*cli.ExecResult, error) {
			called = true
			want := buildCodexCommand(prompt, "", true)
			if !reflect.DeepEqual(opts.Command, want) {
				t.Fatalf("command = %q, want %q", opts.Command, want)
			}
			if !opts.Config.ShouldSyncCodexLogs() {
				t.Fatal("continuation did not enable persistence")
			}
			return &cli.ExecResult{}, nil
		}
		root := &cobra.Command{Use: "moat"}
		(&Provider{}).RegisterCLI(root)
		args := []string{"codex", workspace, "--continue"}
		if prompt != "" {
			args = append(args, "-p", prompt)
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("executor was not called")
		}
	}
}

func TestCodexContinuationRejectsDisabledPersistenceBeforeExecution(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	t.Setenv("MOAT_KEYRING_BACKEND", "file")
	originalExec, originalDryRun, originalContinue := cli.ExecuteRun, cli.DryRun, codexContinue
	t.Cleanup(func() { cli.ExecuteRun, cli.DryRun, codexContinue = originalExec, originalDryRun, originalContinue })
	cli.DryRun = false
	called := false
	cli.ExecuteRun = func(_ context.Context, _ cli.ExecOptions) (*cli.ExecResult, error) {
		called = true
		return &cli.ExecResult{}, nil
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "moat.yaml"), []byte("codex:\n  sync_logs: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "moat"}
	(&Provider{}).RegisterCLI(root)
	root.SetArgs([]string{"codex", workspace, "--continue"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "codex.sync_logs") {
		t.Fatalf("expected disabled persistence error, got %v", err)
	}
	if called {
		t.Fatal("executor called despite disabled persistence")
	}
}
