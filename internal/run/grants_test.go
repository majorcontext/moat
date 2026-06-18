package run

import (
	"crypto/rand"
	stderrors "errors"
	"os"
	"path/filepath"
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
		t.Fatalf("missing case: detector=%v validators=%v — they must agree", detected, rejected)
	}

	// Symmetric case: with every grant present, both must report nothing. A bug
	// that flagged a spurious missing grant on an otherwise-valid store would
	// only surface here, not in the all-missing direction above.
	full := newGrantsTestStore(t)
	for _, p := range []string{"github", "mcp:render"} {
		if err := full.Save(credential.Credential{Provider: credential.Provider(p), Token: "tok"}); err != nil {
			t.Fatalf("Save %s: %v", p, err)
		}
	}
	detected = len(DetectMissingGrants(grants, cfg, full)) > 0
	rejected = validateGrants(grants, full) != nil || validateMCPGrants(cfg, full) != nil
	if detected || rejected {
		t.Fatalf("present case: detector=%v validators=%v — both must report none", detected, rejected)
	}
}

func TestClassifyMissingReason(t *testing.T) {
	cases := []struct {
		msg  string
		want MissingReason
	}{
		{"decrypting credential for github: cipher: message authentication failed", ReasonDecryptFailed},
		{"credential not found: github", ReasonNotConfigured},
		{"reading credential file: open /x: permission denied", ReasonReadFailed},
		{"invalid credential file", ReasonReadFailed},
	}
	for _, c := range cases {
		if got := classifyMissingReason(stderrors.New(c.msg)); got != c.want {
			t.Errorf("classifyMissingReason(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

// A credential file that exists but can't be read cleanly (here, truncated so
// it's shorter than the cipher nonce → "invalid credential file") must be
// reported as a non-promptable read failure with the raw error surfaced, not as
// a promptable "not configured" grant.
func TestDetectMissingGrantsReadFailed(t *testing.T) {
	dir := t.TempDir()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	// Save a valid credential, then truncate the file so the next read fails
	// with a store error other than not-found or decrypt.
	if err := store.Save(credential.Credential{Provider: "github", Token: "tok"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "github.enc"), []byte("x"), 0o600); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	got := DetectMissingGrants([]string{"github"}, nil, store)
	if len(got) != 1 {
		t.Fatalf("got %d missing, want 1: %+v", len(got), got)
	}
	m := got[0]
	if m.Reason != ReasonReadFailed {
		t.Errorf("Reason = %v, want ReasonReadFailed", m.Reason)
	}
	if m.Promptable {
		t.Error("read failure must not be promptable")
	}
	if m.Detail == "" {
		t.Error("read failure must carry the raw store error in Detail")
	}
}

// A credential stored under one key but read with another decrypts-fails. Both
// the generic and MCP detection paths must report ReasonDecryptFailed (not
// ReasonNotConfigured) so the user is told the key changed, not that the
// credential is missing.
func TestDetectMissingGrantsDecryptFailed(t *testing.T) {
	dir := t.TempDir()
	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	if _, err := rand.Read(keyA); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	if _, err := rand.Read(keyB); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	storeA, err := credential.NewFileStore(dir, keyA)
	if err != nil {
		t.Fatalf("NewFileStore A: %v", err)
	}
	for _, p := range []string{"github", "mcp:render"} {
		if err := storeA.Save(credential.Credential{Provider: credential.Provider(p), Token: "tok"}); err != nil {
			t.Fatalf("Save %s: %v", p, err)
		}
	}

	storeB, err := credential.NewFileStore(dir, keyB)
	if err != nil {
		t.Fatalf("NewFileStore B: %v", err)
	}

	cfg := &config.Config{MCP: []config.MCPServerConfig{
		{Name: "render", Auth: &config.MCPAuthConfig{Grant: "mcp:render"}},
	}}
	got := DetectMissingGrants(AppendMCPGrants([]string{"github"}, cfg), cfg, storeB)
	by := map[string]MissingGrant{}
	for _, m := range got {
		by[m.Grant] = m
	}
	for _, g := range []string{"github", "mcp:render"} {
		if m, ok := by[g]; !ok || m.Reason != ReasonDecryptFailed {
			t.Errorf("%s: want ReasonDecryptFailed, got %+v (ok=%v)", g, m, ok)
		}
	}
}
