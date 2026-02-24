package credential

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileStore_SaveAndGet(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	cred := Credential{
		Provider:  ProviderGitHub,
		Token:     "ghp_test123",
		Scopes:    []string{"repo", "read:user"},
		CreatedAt: time.Now(),
	}

	if err := store.Save(cred); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ProviderGitHub)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Token != cred.Token {
		t.Errorf("Token = %q, want %q", got.Token, cred.Token)
	}
}

func TestFileStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	cred := Credential{
		Provider:  ProviderGitHub,
		Token:     "ghp_test123",
		CreatedAt: time.Now(),
	}

	store.Save(cred)
	if err := store.Delete(ProviderGitHub); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = store.Get(ProviderGitHub)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestFileStore_GetNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	_, err = store.Get(ProviderGitHub)
	if err == nil {
		t.Error("expected error for non-existent credential")
	}
}

func TestFileStore_List(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Initially empty
	creds, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(creds) != 0 {
		t.Errorf("List() = %d credentials, want 0", len(creds))
	}

	// Add two credentials
	store.Save(Credential{
		Provider:  ProviderGitHub,
		Token:     "ghp_test123",
		CreatedAt: time.Now(),
	})
	store.Save(Credential{
		Provider:  ProviderAWS,
		Token:     "aws_test456",
		CreatedAt: time.Now(),
	})

	creds, err = store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(creds) != 2 {
		t.Errorf("List() = %d credentials, want 2", len(creds))
	}
}

func TestNewFileStore_InvalidKeyLength(t *testing.T) {
	dir := t.TempDir()
	_, err := NewFileStore(dir, []byte("short-key"))
	if err == nil {
		t.Error("expected error for invalid key length")
	}
}

func TestDefaultStoreDir_NoProfile(t *testing.T) {
	orig := ActiveProfile
	ActiveProfile = ""
	defer func() { ActiveProfile = orig }()

	dir := DefaultStoreDir()
	if strings.Contains(dir, "profiles") {
		t.Errorf("DefaultStoreDir() without profile should not contain 'profiles', got %q", dir)
	}
	if !strings.HasSuffix(dir, filepath.Join(".moat", "credentials")) {
		t.Errorf("DefaultStoreDir() = %q, want suffix %q", dir, filepath.Join(".moat", "credentials"))
	}
}

func TestDefaultStoreDir_WithProfile(t *testing.T) {
	orig := ActiveProfile
	ActiveProfile = "myproject"
	defer func() { ActiveProfile = orig }()

	dir := DefaultStoreDir()
	want := filepath.Join(".moat", "credentials", "profiles", "myproject")
	if !strings.HasSuffix(dir, want) {
		t.Errorf("DefaultStoreDir() = %q, want suffix %q", dir, want)
	}
}

func TestValidateProfile(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		wantErr bool
	}{
		{"empty is valid", "", false},
		{"simple name", "myproject", false},
		{"with hyphens", "my-project", false},
		{"with underscores", "my_project", false},
		{"with digits", "project1", false},
		{"starts with digit", "1project", false},
		{"mixed", "My-Project_2", false},
		{"starts with hyphen", "-bad", true},
		{"starts with underscore", "_bad", true},
		{"contains spaces", "bad name", true},
		{"contains dots", "bad.name", true},
		{"contains slashes", "bad/name", true},
		{"contains special chars", "bad@name", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProfile(tt.profile)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateProfile(%q) error = %v, wantErr %v", tt.profile, err, tt.wantErr)
			}
		})
	}
}

func TestProfileIsolation(t *testing.T) {
	// Create a temp dir structure to simulate profile isolation
	baseDir := t.TempDir()
	key := []byte("test-encryption-key-32-bytes!!ab")

	// Store a credential in the default (no profile) store
	defaultStore, err := NewFileStore(baseDir, key)
	if err != nil {
		t.Fatalf("NewFileStore (default): %v", err)
	}
	defaultStore.Save(Credential{
		Provider:  ProviderGitHub,
		Token:     "default-token",
		CreatedAt: time.Now(),
	})

	// Store a credential in a profile store
	profileDir := filepath.Join(baseDir, "profiles", "myproject")
	profileStore, err := NewFileStore(profileDir, key)
	if err != nil {
		t.Fatalf("NewFileStore (profile): %v", err)
	}
	profileStore.Save(Credential{
		Provider:  ProviderGitHub,
		Token:     "profile-token",
		CreatedAt: time.Now(),
	})

	// Verify isolation: default store has default token
	got, err := defaultStore.Get(ProviderGitHub)
	if err != nil {
		t.Fatalf("Get from default: %v", err)
	}
	if got.Token != "default-token" {
		t.Errorf("default store token = %q, want %q", got.Token, "default-token")
	}

	// Verify isolation: profile store has profile token
	got, err = profileStore.Get(ProviderGitHub)
	if err != nil {
		t.Fatalf("Get from profile: %v", err)
	}
	if got.Token != "profile-token" {
		t.Errorf("profile store token = %q, want %q", got.Token, "profile-token")
	}
}

func TestListProfiles(t *testing.T) {
	// Create temp home with profile directories
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// No profiles dir yet
	profiles, err := ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles with no dir: %v", err)
	}
	if len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}

	// Create some profile directories
	profilesDir := filepath.Join(tmpHome, ".moat", "credentials", "profiles")
	os.MkdirAll(filepath.Join(profilesDir, "alpha"), 0700)
	os.MkdirAll(filepath.Join(profilesDir, "beta"), 0700)

	profiles, err = ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(profiles) != 2 {
		t.Errorf("expected 2 profiles, got %d", len(profiles))
	}
}

// --- Additional profile tests ---

func TestValidateProfile_PathTraversal(t *testing.T) {
	// Verify that path traversal attempts are rejected by ValidateProfile.
	// These are security-critical: a profile name like "../../../etc" must never
	// be accepted since it would escape the credentials directory.
	tests := []struct {
		name    string
		profile string
	}{
		{"dot-dot", ".."},
		{"single dot", "."},
		{"traversal up", "../.."},
		{"traversal with path", "../../etc"},
		{"deep traversal", "../../../etc/shadow"},
		{"backslash traversal", "..\\..\\etc"},
		{"null byte", "profile\x00evil"},
		{"tilde home", "~root"},
		{"absolute path unix", "/etc/passwd"},
		{"double dot embedded", "foo/../bar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateProfile(tt.profile); err == nil {
				t.Errorf("ValidateProfile(%q) should reject path traversal, got nil", tt.profile)
			}
		})
	}
}

func TestDefaultStoreDir_BackwardCompatibility(t *testing.T) {
	// When no profile is set, credentials stored in the default directory
	// must remain accessible. This verifies the zero-value of ActiveProfile
	// produces the same directory path that a pre-profiles installation uses.
	orig := ActiveProfile
	defer func() { ActiveProfile = orig }()

	// Simulate pre-profiles behavior: empty profile
	ActiveProfile = ""
	defaultDir := DefaultStoreDir()

	// The default dir must be exactly ~/.moat/credentials (no profiles subdirectory)
	expectedSuffix := filepath.Join(".moat", "credentials")
	if !strings.HasSuffix(defaultDir, expectedSuffix) {
		t.Errorf("DefaultStoreDir() = %q, want suffix %q", defaultDir, expectedSuffix)
	}
	// Must NOT contain "profiles" anywhere in the path
	if strings.Contains(defaultDir, "profiles") {
		t.Errorf("DefaultStoreDir() with empty profile should not contain 'profiles', got %q", defaultDir)
	}

	// Now verify that credentials saved before profiles exist are still accessible
	key := []byte("test-encryption-key-32-bytes!!ab")
	baseDir := t.TempDir()

	// Save a credential to the "old" default location
	oldStore, err := NewFileStore(baseDir, key)
	if err != nil {
		t.Fatalf("NewFileStore (old default): %v", err)
	}
	oldStore.Save(Credential{
		Provider:  ProviderGitHub,
		Token:     "pre-profiles-token",
		CreatedAt: time.Now(),
	})

	// Open the same directory again (simulating upgrade: same dir, new code)
	newStore, err := NewFileStore(baseDir, key)
	if err != nil {
		t.Fatalf("NewFileStore (new default): %v", err)
	}
	got, err := newStore.Get(ProviderGitHub)
	if err != nil {
		t.Fatalf("Get pre-profiles credential: %v", err)
	}
	if got.Token != "pre-profiles-token" {
		t.Errorf("token = %q, want %q", got.Token, "pre-profiles-token")
	}
}

func TestProfileIsolation_ListAndDelete(t *testing.T) {
	// Verify that List() in one profile doesn't return credentials from another,
	// and Delete() in one profile doesn't affect the other.
	baseDir := t.TempDir()
	key := []byte("test-encryption-key-32-bytes!!ab")

	// Create stores for default and two profiles
	defaultStore, err := NewFileStore(baseDir, key)
	if err != nil {
		t.Fatalf("NewFileStore (default): %v", err)
	}
	profileADir := filepath.Join(baseDir, "profiles", "alpha")
	storeA, err := NewFileStore(profileADir, key)
	if err != nil {
		t.Fatalf("NewFileStore (alpha): %v", err)
	}
	profileBDir := filepath.Join(baseDir, "profiles", "beta")
	storeB, err := NewFileStore(profileBDir, key)
	if err != nil {
		t.Fatalf("NewFileStore (beta): %v", err)
	}

	// Save credentials: GitHub in default, GitHub+AWS in alpha, Anthropic in beta
	defaultStore.Save(Credential{Provider: ProviderGitHub, Token: "default-gh", CreatedAt: time.Now()})
	storeA.Save(Credential{Provider: ProviderGitHub, Token: "alpha-gh", CreatedAt: time.Now()})
	storeA.Save(Credential{Provider: ProviderAWS, Token: "alpha-aws", CreatedAt: time.Now()})
	storeB.Save(Credential{Provider: ProviderAnthropic, Token: "beta-anthropic", CreatedAt: time.Now()})

	// Verify List() isolation
	defaultCreds, err := defaultStore.List()
	if err != nil {
		t.Fatalf("List (default): %v", err)
	}
	if len(defaultCreds) != 1 {
		t.Errorf("default List() = %d, want 1", len(defaultCreds))
	}

	alphaCreds, err := storeA.List()
	if err != nil {
		t.Fatalf("List (alpha): %v", err)
	}
	if len(alphaCreds) != 2 {
		t.Errorf("alpha List() = %d, want 2", len(alphaCreds))
	}

	betaCreds, err := storeB.List()
	if err != nil {
		t.Fatalf("List (beta): %v", err)
	}
	if len(betaCreds) != 1 {
		t.Errorf("beta List() = %d, want 1", len(betaCreds))
	}

	// Delete GitHub from alpha; default and beta should be unaffected
	if err := storeA.Delete(ProviderGitHub); err != nil {
		t.Fatalf("Delete alpha GitHub: %v", err)
	}

	// Alpha now has only AWS
	alphaCreds, err = storeA.List()
	if err != nil {
		t.Fatalf("List (alpha after delete): %v", err)
	}
	if len(alphaCreds) != 1 {
		t.Errorf("alpha List() after delete = %d, want 1", len(alphaCreds))
	}
	if alphaCreds[0].Provider != ProviderAWS {
		t.Errorf("remaining alpha cred = %q, want %q", alphaCreds[0].Provider, ProviderAWS)
	}

	// Default store still has its GitHub token
	got, err := defaultStore.Get(ProviderGitHub)
	if err != nil {
		t.Fatalf("Get default GitHub after alpha delete: %v", err)
	}
	if got.Token != "default-gh" {
		t.Errorf("default token = %q, want %q", got.Token, "default-gh")
	}

	// Beta still has Anthropic
	got, err = storeB.Get(ProviderAnthropic)
	if err != nil {
		t.Fatalf("Get beta Anthropic after alpha delete: %v", err)
	}
	if got.Token != "beta-anthropic" {
		t.Errorf("beta token = %q, want %q", got.Token, "beta-anthropic")
	}
}

func TestProfileIsolation_SameProviderDifferentTokens(t *testing.T) {
	// Same provider in multiple profiles stores distinct tokens.
	baseDir := t.TempDir()
	key := []byte("test-encryption-key-32-bytes!!ab")

	profiles := []string{"dev", "staging", "prod"}
	stores := make(map[string]*FileStore)
	for _, p := range profiles {
		dir := filepath.Join(baseDir, "profiles", p)
		s, err := NewFileStore(dir, key)
		if err != nil {
			t.Fatalf("NewFileStore (%s): %v", p, err)
		}
		stores[p] = s
		s.Save(Credential{
			Provider:  ProviderGitHub,
			Token:     "token-" + p,
			CreatedAt: time.Now(),
		})
	}

	// Each profile should return its own token
	for _, p := range profiles {
		got, err := stores[p].Get(ProviderGitHub)
		if err != nil {
			t.Fatalf("Get (%s): %v", p, err)
		}
		want := "token-" + p
		if got.Token != want {
			t.Errorf("profile %q token = %q, want %q", p, got.Token, want)
		}
	}
}

func TestListProfiles_IgnoresFiles(t *testing.T) {
	// ListProfiles should only return directories, not regular files.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	profilesDir := filepath.Join(tmpHome, ".moat", "credentials", "profiles")
	os.MkdirAll(filepath.Join(profilesDir, "real-profile"), 0700)
	// Create a regular file that should be ignored
	os.WriteFile(filepath.Join(profilesDir, "not-a-profile.txt"), []byte("junk"), 0600)

	profiles, err := ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Errorf("expected 1 profile, got %d: %v", len(profiles), profiles)
	}
	if len(profiles) > 0 && profiles[0] != "real-profile" {
		t.Errorf("profile = %q, want %q", profiles[0], "real-profile")
	}
}

func TestDefaultStoreDir_ProfileDoesNotAffectDefault(t *testing.T) {
	// Switching ActiveProfile and back should give the original default dir.
	orig := ActiveProfile
	defer func() { ActiveProfile = orig }()

	ActiveProfile = ""
	defaultDir := DefaultStoreDir()

	ActiveProfile = "someprofile"
	profileDir := DefaultStoreDir()

	ActiveProfile = ""
	afterDir := DefaultStoreDir()

	if defaultDir != afterDir {
		t.Errorf("DefaultStoreDir() changed after profile round-trip: %q vs %q", defaultDir, afterDir)
	}
	if defaultDir == profileDir {
		t.Errorf("DefaultStoreDir() should differ between default and profile, both = %q", defaultDir)
	}
	if !strings.Contains(profileDir, filepath.Join("profiles", "someprofile")) {
		t.Errorf("profile dir %q should contain profiles/someprofile", profileDir)
	}
}

func TestFileStore_WrongKeyCannotDecrypt(t *testing.T) {
	// Credentials encrypted with one key cannot be read with a different key.
	// This is important for profile security: different profiles using different
	// keys (or key changes) should fail gracefully.
	dir := t.TempDir()
	key1 := []byte("test-encryption-key-32-bytes!!ab")
	key2 := []byte("different-encrypt-key-32-bytes!!")

	store1, err := NewFileStore(dir, key1)
	if err != nil {
		t.Fatalf("NewFileStore (key1): %v", err)
	}
	store1.Save(Credential{
		Provider:  ProviderGitHub,
		Token:     "secret-token",
		CreatedAt: time.Now(),
	})

	store2, err := NewFileStore(dir, key2)
	if err != nil {
		t.Fatalf("NewFileStore (key2): %v", err)
	}
	_, err = store2.Get(ProviderGitHub)
	if err == nil {
		t.Error("expected decryption error with wrong key, got nil")
	}
	if !strings.Contains(err.Error(), "decrypting") {
		t.Errorf("error should mention decryption, got: %v", err)
	}
}

func TestValidateProfile_LongName(t *testing.T) {
	// Very long but valid profile names should be accepted.
	longName := strings.Repeat("a", 200)
	if err := ValidateProfile(longName); err != nil {
		t.Errorf("ValidateProfile(%q) should accept long name, got: %v", longName[:20]+"...", err)
	}
}

func TestValidateProfile_SingleChar(t *testing.T) {
	// Single-character alphanumeric names should be valid.
	for _, c := range []string{"a", "Z", "0", "9"} {
		if err := ValidateProfile(c); err != nil {
			t.Errorf("ValidateProfile(%q) should accept single char, got: %v", c, err)
		}
	}
}

func TestDefaultStoreDir_ProfileSubdirectoryStructure(t *testing.T) {
	// Verify the exact directory structure for profiles.
	orig := ActiveProfile
	defer func() { ActiveProfile = orig }()

	ActiveProfile = "myapp"
	dir := DefaultStoreDir()

	// Path must end with .moat/credentials/profiles/myapp
	parts := strings.Split(filepath.ToSlash(dir), "/")
	n := len(parts)
	if n < 4 {
		t.Fatalf("path too short: %q", dir)
	}
	tail := parts[n-4:]
	if tail[0] != ".moat" || tail[1] != "credentials" || tail[2] != "profiles" || tail[3] != "myapp" {
		t.Errorf("expected path ending in .moat/credentials/profiles/myapp, got %v", tail)
	}
}
