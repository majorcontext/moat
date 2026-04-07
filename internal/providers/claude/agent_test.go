package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestPrepareContainer_writesContextFile(t *testing.T) {
	p := &OAuthProvider{}

	ctx := context.Background()
	runtimeContext := "# Moat Environment\n\nYou are running inside a sandbox.\n"

	cfg, err := p.PrepareContainer(ctx, provider.PrepareOpts{
		RuntimeContext: runtimeContext,
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	// Verify CLAUDE.md was written to the staging directory
	data, err := os.ReadFile(filepath.Join(cfg.StagingDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	if string(data) != runtimeContext {
		t.Errorf("CLAUDE.md content = %q, want %q", string(data), runtimeContext)
	}
}

func TestPrepareContainer_copiesRemoteSettings(t *testing.T) {
	// Set up a fake home with remote-settings.json
	fakeHome := t.TempDir()
	claudeDir := filepath.Join(fakeHome, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	settingsContent := `{"version":1,"settings":{"hooks":true}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "remote-settings.json"), []byte(settingsContent), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", fakeHome)

	p := &OAuthProvider{}
	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	// Verify remote-settings.json was copied to the staging directory
	data, err := os.ReadFile(filepath.Join(cfg.StagingDir, "remote-settings.json"))
	if err != nil {
		t.Fatalf("reading remote-settings.json: %v", err)
	}
	if string(data) != settingsContent {
		t.Errorf("remote-settings.json content = %q, want %q", string(data), settingsContent)
	}

	// Verify permissions are 0600
	info, err := os.Stat(filepath.Join(cfg.StagingDir, "remote-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("remote-settings.json permissions = %o, want 0600", perm)
	}
}

func TestPrepareContainer_skipsRemoteSettingsWhenMissing(t *testing.T) {
	// Set up a fake home without remote-settings.json
	fakeHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fakeHome, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", fakeHome)

	p := &OAuthProvider{}
	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	// Verify remote-settings.json was NOT created
	if _, err := os.Stat(filepath.Join(cfg.StagingDir, "remote-settings.json")); err == nil {
		t.Error("remote-settings.json should not exist when host file is missing")
	}
}

func TestPrepareContainer_skipsContextFileWhenEmpty(t *testing.T) {
	p := &OAuthProvider{}

	ctx := context.Background()

	cfg, err := p.PrepareContainer(ctx, provider.PrepareOpts{
		RuntimeContext: "",
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	// Verify CLAUDE.md was NOT written
	path := filepath.Join(cfg.StagingDir, "CLAUDE.md")
	if _, err := os.Stat(path); err == nil {
		t.Error("CLAUDE.md should not exist when RuntimeContext is empty")
	}
}
