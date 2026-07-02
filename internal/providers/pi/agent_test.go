package pi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestPrepareContainerStagesContext(t *testing.T) {
	p := &Provider{}
	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		RuntimeContext: "# Moat Environment\n\nhello",
	})
	if err != nil {
		t.Fatalf("PrepareContainer: %v", err)
	}
	t.Cleanup(func() {
		if cfg.Cleanup != nil {
			cfg.Cleanup()
		}
	})

	// Context file written into the staging dir.
	ctxPath := filepath.Join(cfg.StagingDir, ContextFileName)
	data, readErr := os.ReadFile(ctxPath)
	if readErr != nil {
		t.Fatalf("reading staged context: %v", readErr)
	}
	if !strings.Contains(string(data), "hello") {
		t.Errorf("context file missing content: %q", data)
	}

	// Mount + env wired.
	foundMount := false
	for _, m := range cfg.Mounts {
		if m.Target == PiInitMountPath && m.Source == cfg.StagingDir && m.ReadOnly {
			foundMount = true
		}
	}
	if !foundMount {
		t.Errorf("expected read-only mount of staging dir at %s, got %+v", PiInitMountPath, cfg.Mounts)
	}
	assertEnv(t, cfg.Env, "PI_OFFLINE=1")
}

// Companion: an empty RuntimeContext writes no context file, but the mount and
// PI_OFFLINE env are still returned. Mirrors the equivalent tests in the
// claude/codex/gemini providers.
func TestPrepareContainerSkipsContextFileWhenEmpty(t *testing.T) {
	p := &Provider{}
	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		RuntimeContext: "",
	})
	if err != nil {
		t.Fatalf("PrepareContainer: %v", err)
	}
	t.Cleanup(func() {
		if cfg.Cleanup != nil {
			cfg.Cleanup()
		}
	})

	// No context file written.
	if _, statErr := os.Stat(filepath.Join(cfg.StagingDir, ContextFileName)); !os.IsNotExist(statErr) {
		t.Errorf("expected no %s when RuntimeContext is empty (stat err=%v)", ContextFileName, statErr)
	}

	// Mount + env still present.
	foundMount := false
	for _, m := range cfg.Mounts {
		if m.Target == PiInitMountPath && m.Source == cfg.StagingDir && m.ReadOnly {
			foundMount = true
		}
	}
	if !foundMount {
		t.Errorf("expected read-only mount at %s, got %+v", PiInitMountPath, cfg.Mounts)
	}
	assertEnv(t, cfg.Env, "PI_OFFLINE=1")
}

func assertEnv(t *testing.T, env []string, want string) {
	t.Helper()
	for _, e := range env {
		if e == want {
			return
		}
	}
	t.Errorf("env missing %q, got %v", want, env)
}
