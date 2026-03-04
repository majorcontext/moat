package gemini

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestPrepareContainer_writesContextFile(t *testing.T) {
	p := &Provider{}

	ctx := context.Background()
	runtimeContext := "# Moat Environment\n\nYou are running inside a sandbox.\n"

	cfg, err := p.PrepareContainer(ctx, provider.PrepareOpts{
		RuntimeContext: runtimeContext,
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	// Verify GEMINI.md was written to the staging directory
	data, err := os.ReadFile(filepath.Join(cfg.StagingDir, "GEMINI.md"))
	if err != nil {
		t.Fatalf("reading GEMINI.md: %v", err)
	}
	if string(data) != runtimeContext {
		t.Errorf("GEMINI.md content = %q, want %q", string(data), runtimeContext)
	}
}

func TestPrepareContainer_skipsContextFileWhenEmpty(t *testing.T) {
	p := &Provider{}

	ctx := context.Background()

	cfg, err := p.PrepareContainer(ctx, provider.PrepareOpts{
		RuntimeContext: "",
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	// Verify GEMINI.md was NOT written
	path := filepath.Join(cfg.StagingDir, "GEMINI.md")
	if _, err := os.Stat(path); err == nil {
		t.Error("GEMINI.md should not exist when RuntimeContext is empty")
	}
}
