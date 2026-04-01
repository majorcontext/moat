package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/credential"
)

func TestRedactToken(t *testing.T) {
	// Save and restore global flag
	orig := showToken
	defer func() { showToken = orig }()

	tests := []struct {
		name   string
		token  string
		reveal bool
		want   string
	}{
		{"empty token", "", false, "(empty)"},
		{"short token", "abc", false, "****"},
		{"exactly 4 chars", "abcd", false, "****"},
		{"normal token", "ghp_abc123XY", false, "****************23XY"},
		{"reveal token", "ghp_abc123XY", true, "ghp_abc123XY"},
		{"reveal empty", "", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			showToken = tt.reveal
			got := redactToken(tt.token)
			if got != tt.want {
				t.Errorf("redactToken(%q) = %q, want %q", tt.token, got, tt.want)
			}
		})
	}
}

func TestFilterMetadata(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]string
		want int // expected number of keys in result
	}{
		{"nil map", nil, 0},
		{"empty map", map[string]string{}, 0},
		{"only secrets", map[string]string{
			"refresh_token": "rt",
			"client_secret": "cs",
			"token_source":  "cli",
		}, 0},
		{"oauth secrets filtered", map[string]string{
			"client_id":     "id-123",
			"client_secret": "secret",
			"token_url":     "https://example.com/token",
			"refresh_token": "rt",
			"token_source":  "oauth",
		}, 0},
		{"meta secrets filtered", map[string]string{
			"meta_app_id":     "app-123",
			"meta_app_secret": "app-secret",
			"token_source":    "env",
		}, 0},
		{"mixed", map[string]string{
			"token_source":  "cli",
			"refresh_token": "rt",
			"region":        "us-east-1",
			"auth_type":     "oauth",
		}, 2}, // region + auth_type
		{"safe keys preserved", map[string]string{
			"region":           "us-west-2",
			"session_duration": "1h",
			"profile":          "prod",
			"auth_type":        "oauth",
			"client_secret":    "should-be-hidden",
		}, 4}, // region + session_duration + profile + auth_type
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterMetadata(tt.in)
			if tt.want == 0 {
				if got != nil {
					t.Errorf("filterMetadata() = %v, want nil", got)
				}
				return
			}
			if len(got) != tt.want {
				t.Errorf("filterMetadata() returned %d keys, want %d: %v", len(got), tt.want, got)
			}
			// Secrets must not appear
			for _, secret := range []string{
				"refresh_token", "client_secret", "token_source",
				"client_id", "token_url", "meta_app_id", "meta_app_secret",
			} {
				if _, ok := got[secret]; ok {
					t.Errorf("filterMetadata() should not contain %q", secret)
				}
			}
		})
	}
}

func TestShowSSHCredential(t *testing.T) {
	t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		t.Fatalf("getting encryption key: %v", err)
	}

	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}

	// Add an SSH mapping
	err = store.AddSSHMapping(credential.SSHMapping{
		Host:           "github.com",
		KeyFingerprint: "SHA256:abcdef1234567890abcdef1234567890",
		KeyPath:        "/home/user/.ssh/id_ed25519",
		CreatedAt:      time.Now(),
	})
	if err != nil {
		t.Fatalf("adding SSH mapping: %v", err)
	}

	// Test found
	err = showSSHCredential(store, "github.com")
	if err != nil {
		t.Errorf("showSSHCredential(github.com) = %v, want nil", err)
	}

	// Test not found
	err = showSSHCredential(store, "gitlab.com")
	if err == nil {
		t.Error("showSSHCredential(gitlab.com) = nil, want error")
	}
}

func TestGrantShowEmptySSHHost(t *testing.T) {
	t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := rootCmd
	cmd.SetArgs([]string{"grant", "show", "ssh:"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for empty SSH host, got nil")
	}
	if !strings.Contains(err.Error(), "SSH host cannot be empty") {
		t.Errorf("expected 'SSH host cannot be empty' error, got: %v", err)
	}
}

func TestGrantShowNotFound(t *testing.T) {
	t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := rootCmd
	cmd.SetArgs([]string{"grant", "show", "nonexistent"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent provider, got nil")
	}
	if !strings.Contains(err.Error(), "no credential found") {
		t.Errorf("expected 'no credential found' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "moat grant nonexistent") {
		t.Errorf("expected actionable hint in error, got: %v", err)
	}
}

func TestGrantShowIntegration(t *testing.T) {
	t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Store a credential
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		t.Fatalf("getting encryption key: %v", err)
	}

	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}

	cred := credential.Credential{
		Provider:  credential.ProviderGitHub,
		Token:     "ghp_testtoken1234567890abcdef",
		Scopes:    []string{"repo", "read:org"},
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			"token_source": "cli",
		},
	}
	if err := store.Save(cred); err != nil {
		t.Fatalf("saving credential: %v", err)
	}

	// Run show command — should not error
	cmd := rootCmd
	cmd.SetArgs([]string{"grant", "show", "github"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("grant show github failed: %v", err)
	}
}
