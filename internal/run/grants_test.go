package run

import (
	"crypto/rand"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

func TestGrantToCommandExported(t *testing.T) {
	cases := map[string]string{
		"github":       "github",
		"oauth:notion": "oauth notion",
		"mcp:render":   "mcp render",
	}
	for grant, want := range cases {
		if got := GrantToCommand(grant); got != want {
			t.Errorf("GrantToCommand(%q) = %q, want %q", grant, got, want)
		}
	}
}

func TestAppendMCPGrantsExported(t *testing.T) {
	cfg := &config.Config{MCP: []config.MCPServerConfig{
		{Name: "render", Auth: &config.MCPAuthConfig{Grant: "mcp:render"}},
	}}
	got := AppendMCPGrants([]string{"github"}, cfg)
	want := []string{"github", "mcp:render"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("AppendMCPGrants = %v, want %v", got, want)
	}
}

func newGrantsTestStore(t *testing.T) *credential.FileStore {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	store, err := credential.NewFileStore(t.TempDir(), key)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return store
}

func TestDetectMissingGrants(t *testing.T) {
	store := newGrantsTestStore(t) // empty store: every grant is missing

	cfg := &config.Config{MCP: []config.MCPServerConfig{
		{Name: "render", Auth: &config.MCPAuthConfig{Grant: "mcp:render"}},
	}}
	grants := AppendMCPGrants([]string{"github", "aws", "bogusprov"}, cfg)

	got := DetectMissingGrants(grants, cfg, store)
	by := map[string]MissingGrant{}
	for _, m := range got {
		by[m.Grant] = m
	}

	if m, ok := by["github"]; !ok || !m.Promptable || m.Reason != ReasonNotConfigured || m.FixCommand != "moat grant github" {
		t.Errorf("github: %+v ok=%v", m, ok)
	}
	if m, ok := by["aws"]; !ok || m.Promptable {
		t.Errorf("aws should be non-promptable: %+v ok=%v", m, ok)
	}
	if m, ok := by["bogusprov"]; !ok || m.Promptable || m.Reason != ReasonUnknownProvider {
		t.Errorf("bogusprov should be unknown/non-promptable: %+v ok=%v", m, ok)
	}
	if m, ok := by["mcp:render"]; !ok || !m.Promptable || m.FixCommand != "moat grant mcp render" {
		t.Errorf("mcp:render: %+v ok=%v", m, ok)
	}
	if _, ok := by["ssh:github.com"]; ok {
		t.Error("ssh grants must not be detected (out of scope)")
	}
}

// Drift guard: DetectMissingGrants must flag exactly the grants the existing
// validators reject, so the pre-flight and Create's gate never diverge.
func TestDetectMissingGrantsMatchesValidators(t *testing.T) {
	store := newGrantsTestStore(t)
	cfg := &config.Config{MCP: []config.MCPServerConfig{
		{Name: "render", Auth: &config.MCPAuthConfig{Grant: "mcp:render"}},
	}}
	grants := AppendMCPGrants([]string{"github"}, cfg)

	detected := len(DetectMissingGrants(grants, cfg, store)) > 0
	rejected := validateGrants(grants, store) != nil || validateMCPGrants(cfg, store) != nil
	if detected != rejected {
		t.Fatalf("detector=%v validators=%v — they must agree", detected, rejected)
	}
}
