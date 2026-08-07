package claude

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestRenderAnthropicShellEnv_exportsPlaceholderKey(t *testing.T) {
	got := RenderAnthropicShellEnv()

	if !strings.Contains(got, "export ANTHROPIC_API_KEY=") {
		t.Error("expected the file to export ANTHROPIC_API_KEY")
	}
	if !strings.Contains(got, ProxyInjectedPlaceholder) {
		t.Errorf("expected the placeholder %q in the exported value", ProxyInjectedPlaceholder)
	}
}

// The guard must unset BASH_ENV as well as the key. A shell-script launcher
// starts a fresh non-interactive bash, which re-sources BASH_ENV and would
// re-export the key — silently undoing the unset.
func TestRenderAnthropicShellEnv_guardUnsetsBashEnv(t *testing.T) {
	got := RenderAnthropicShellEnv()

	if !strings.Contains(got, "claude()") {
		t.Fatal("expected a claude() guard function")
	}
	if !strings.Contains(got, "unset ANTHROPIC_API_KEY BASH_ENV") {
		t.Error("guard must unset BASH_ENV alongside ANTHROPIC_API_KEY, or a script launcher re-exports the key")
	}
}

func TestAnthropicShellEnvVars_pointsAtStagedFile(t *testing.T) {
	vars := AnthropicShellEnvVars()

	want := "BASH_ENV=" + ClaudeInitMountPath + "/" + AnthropicShellEnvFileName
	if !slices.Contains(vars, want) {
		t.Errorf("AnthropicShellEnvVars() = %v, want it to contain %q", vars, want)
	}
}

func TestPrepareContainer_stagesAnthropicShellEnvWhenScoped(t *testing.T) {
	p := &OAuthProvider{}

	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		ScopeAnthropicKeyToShell: true,
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	data, err := os.ReadFile(filepath.Join(cfg.StagingDir, AnthropicShellEnvFileName))
	if err != nil {
		t.Fatalf("reading %s: %v", AnthropicShellEnvFileName, err)
	}
	if !strings.Contains(string(data), "export ANTHROPIC_API_KEY=") {
		t.Error("staged file does not export ANTHROPIC_API_KEY")
	}

	want := "BASH_ENV=" + AnthropicShellEnvPath
	if !slices.Contains(cfg.Env, want) {
		t.Errorf("cfg.Env = %v, want it to contain %q", cfg.Env, want)
	}

	// The whole point: the key must not also be in the container environment,
	// where Claude Code would pick it up over its OAuth login.
	for _, e := range cfg.Env {
		if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			t.Errorf("ANTHROPIC_API_KEY leaked into the container env: %q", e)
		}
	}
}

// Companion case: without the flag, nothing is staged and BASH_ENV is untouched.
func TestPrepareContainer_noAnthropicShellEnvWhenUnscoped(t *testing.T) {
	p := &OAuthProvider{}

	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	if _, err := os.Stat(filepath.Join(cfg.StagingDir, AnthropicShellEnvFileName)); !os.IsNotExist(err) {
		t.Errorf("expected no %s to be staged, stat err = %v", AnthropicShellEnvFileName, err)
	}
	for _, e := range cfg.Env {
		if strings.HasPrefix(e, "BASH_ENV=") {
			t.Errorf("unexpected BASH_ENV in cfg.Env: %q", e)
		}
	}
}
