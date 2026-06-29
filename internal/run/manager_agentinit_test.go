package run

import (
	"context"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestSetupCodexStaging_UnknownGrant(t *testing.T) {
	m := &Manager{}
	cfg := &config.Config{}
	cfg.Codex.MCP = map[string]config.MCPServerSpec{"srv": {Grant: "bogus"}}
	// The provider is nil here: grant validation errors return before
	// PrepareContainer is ever called.
	_, err := m.setupCodexStaging(context.Background(), nil, Options{Config: cfg}, false, "", "", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown grant") {
		t.Fatalf("expected unknown-grant error, got %v", err)
	}
}

func TestSetupCodexStaging_GrantNotDeclared(t *testing.T) {
	m := &Manager{}
	cfg := &config.Config{}
	// "github" is a known grant but is not in the top-level grants list.
	cfg.Codex.MCP = map[string]config.MCPServerSpec{"srv": {Grant: "github"}}
	_, err := m.setupCodexStaging(context.Background(), nil, Options{Config: cfg}, false, "", "", nil)
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("expected grant-not-declared error, got %v", err)
	}
}
