package credential

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func inheritedStores(t *testing.T) (*FileStore, *FileStore, *FileStore) {
	t.Helper()
	t.Setenv("MOAT_HOME", t.TempDir())
	if err := config.CreateProfile("base", config.ProfileConfig{InheritDefault: true}); err != nil {
		t.Fatal(err)
	}
	if err := config.CreateProfile("child", config.ProfileConfig{Inherit: "base"}); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	open := func(name string) *FileStore {
		s, err := NewProfileStore(name, key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	return open(""), open("base"), open("child")
}

func saveProfileToken(t *testing.T, s *FileStore, p Provider, token string) {
	t.Helper()
	if err := s.Save(Credential{Provider: p, Token: token}); err != nil {
		t.Fatal(err)
	}
}

func assertProfileToken(t *testing.T, s *FileStore, p Provider, token string) {
	t.Helper()
	c, err := s.Get(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != token {
		t.Fatalf("%s token = %q, want %q", p, c.Token, token)
	}
}

func TestProfileStoreLiveOverridesAndSources(t *testing.T) {
	def, base, child := inheritedStores(t)
	saveProfileToken(t, def, "github", "default")
	saveProfileToken(t, base, "oauth:sentry", "base")
	assertProfileToken(t, child, "github", "default")
	assertProfileToken(t, child, "oauth:sentry", "base")
	saveProfileToken(t, child, "oauth:sentry", "child")
	assertProfileToken(t, base, "oauth:sentry", "base")
	saveProfileToken(t, def, "github", "rotated")
	assertProfileToken(t, child, "github", "rotated")
	for p, want := range map[Provider]string{"github": "", "oauth:sentry": "child"} {
		if got, err := child.Source(p); err != nil || got != want {
			t.Fatalf("source = %q, %v", got, err)
		}
	}
	creds, err := child.List()
	if err != nil || len(creds) != 2 {
		t.Fatalf("list = %v, %v", creds, err)
	}
	isolated, err := NewProfileStore("isolated", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolated.Get("github"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("isolated get = %v", err)
	}
}

func TestProfileRevokePreventsFallback(t *testing.T) {
	def, base, child := inheritedStores(t)
	saveProfileToken(t, def, "github", "default")
	saveProfileToken(t, base, "github", "base")
	if err := child.Delete("github"); err != nil {
		t.Fatal(err)
	}
	assertProfileToken(t, base, "github", "base")
	if _, err := child.Get("github"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if creds, err := child.List(); err != nil || len(creds) != 0 {
		t.Fatalf("revoked list = %v, %v", creds, err)
	}
	saveProfileToken(t, child, "github", "local")
	assertProfileToken(t, child, "github", "local")
	if err := child.Delete("github"); err != nil {
		t.Fatal(err)
	}
	if _, err := child.Get("github"); !errors.Is(err, ErrNotFound) {
		t.Fatal("local delete fell back", err)
	}
	if err := base.Delete("github"); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewProfileStore("child", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Get("github"); !errors.Is(err, ErrNotFound) {
		t.Fatal("reopen lost exclusion", err)
	}
}

func TestProfileCorruptOverrideNeverFallsBack(t *testing.T) {
	_, base, child := inheritedStores(t)
	saveProfileToken(t, base, "github", "base")
	if err := os.WriteFile(child.path("github"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := child.Get("github"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if creds, err := child.List(); err != nil || len(creds) != 0 {
		t.Fatalf("list exposed parent: %v, %v", creds, err)
	}
}

func TestIntermediateProfileRevocationBlocksDescendant(t *testing.T) {
	root, middle, leaf := inheritedStores(t)
	saveProfileToken(t, root, "github", "root")
	assertProfileToken(t, leaf, "github", "root")
	if err := middle.Delete("github"); err != nil {
		t.Fatal(err)
	}
	if excluded, err := leaf.IsExcluded("github"); err != nil || excluded {
		t.Fatalf("leaf must not have its own exclusion: %v, %v", excluded, err)
	}
	if _, err := leaf.Get("github"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("descendant bypassed intermediate exclusion: %v", err)
	}
	if _, err := leaf.Source("github"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source bypassed intermediate exclusion: %v", err)
	}
	if creds, err := leaf.List(); err != nil || len(creds) != 0 {
		t.Fatalf("list bypassed intermediate exclusion: %v, %v", creds, err)
	}
	assertProfileToken(t, root, "github", "root")
	// Re-granting at the intermediate level restores inheritance below it.
	saveProfileToken(t, middle, "github", "middle")
	assertProfileToken(t, leaf, "github", "middle")
	assertProfileToken(t, root, "github", "root")
}

func TestRevocationExcludesFutureParentGrants(t *testing.T) {
	root, middle, leaf := inheritedStores(t)
	saveProfileToken(t, middle, "oauth:linear", "local")
	if err := middle.Delete("oauth:linear"); err != nil {
		t.Fatal(err)
	}
	// The parent did not have this grant at revoke time. Adding it later must
	// not re-enable a grant explicitly revoked in the intermediate profile.
	saveProfileToken(t, root, "oauth:linear", "new-parent")
	if _, err := leaf.Get("oauth:linear"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("future parent grant bypassed exclusion: %v", err)
	}
	assertProfileToken(t, root, "oauth:linear", "new-parent")
	saveProfileToken(t, leaf, "oauth:linear", "leaf-override")
	assertProfileToken(t, leaf, "oauth:linear", "leaf-override")
}

func TestRefreshStorePinsOwner(t *testing.T) {
	_, base, child := inheritedStores(t)
	saveProfileToken(t, base, "oauth:linear", "base")
	owner, unlock, err := LockRefreshStore(context.Background(), child, "oauth:linear")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	// A local grant arriving during refresh must not be overwritten.
	saveProfileToken(t, child, "oauth:linear", "child")
	if err := owner.Save(Credential{Provider: "oauth:linear", Token: "refreshed"}); err != nil {
		t.Fatal(err)
	}
	assertProfileToken(t, base, "oauth:linear", "refreshed")
	assertProfileToken(t, child, "oauth:linear", "child")
}

func TestSSHProfileInheritance(t *testing.T) {
	def, base, child := inheritedStores(t)
	add := func(s *FileStore, host, fp string) {
		t.Helper()
		if err := s.AddSSHMapping(SSHMapping{Host: host, KeyFingerprint: fp}); err != nil {
			t.Fatal(err)
		}
	}
	add(def, "github.com", "default")
	add(base, "work.example", "base")
	add(child, "github.com", "child")
	mappings, err := child.GetSSHMappings()
	if err != nil || len(mappings) != 2 {
		t.Fatalf("mappings = %v, %v", mappings, err)
	}
	if source, err := child.SSHSource("work.example"); err != nil || source != "base" {
		t.Fatalf("source = %s, %v", source, err)
	}
	local, err := child.localSSHMappings()
	if err != nil || len(local) != 1 {
		t.Fatalf("copied parent mappings: %v, %v", local, err)
	}
	if err := child.RemoveSSHMapping("work.example"); err != nil {
		t.Fatal(err)
	}
	mappings, err = child.GetSSHMappings()
	if err != nil || len(mappings) != 1 {
		t.Fatalf("revoked mappings = %v, %v", mappings, err)
	}
	if _, err := base.SSHSource("work.example"); err != nil {
		t.Fatal("modified parent", err)
	}
	add(child, "work.example", "custom")
	if source, err := child.SSHSource("work.example"); err != nil || source != "child" {
		t.Fatalf("source = %s, %v", source, err)
	}
}
