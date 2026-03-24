package oauth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config with all fields",
			config: Config{
				AuthURL:      "https://example.com/authorize",
				TokenURL:     "https://example.com/token",
				ClientID:     "my-client-id",
				ClientSecret: "my-secret",
				Scopes:       "read write",
			},
			wantErr: false,
		},
		{
			name: "valid config without optional fields",
			config: Config{
				AuthURL:  "https://example.com/authorize",
				TokenURL: "https://example.com/token",
				ClientID: "my-client-id",
			},
			wantErr: false,
		},
		{
			name: "missing auth_url",
			config: Config{
				TokenURL: "https://example.com/token",
				ClientID: "my-client-id",
			},
			wantErr: true,
			errMsg:  "auth_url",
		},
		{
			name: "missing token_url",
			config: Config{
				AuthURL:  "https://example.com/authorize",
				ClientID: "my-client-id",
			},
			wantErr: true,
			errMsg:  "token_url",
		},
		{
			name: "missing client_id",
			config: Config{
				AuthURL:  "https://example.com/authorize",
				TokenURL: "https://example.com/token",
			},
			wantErr: true,
			errMsg:  "client_id",
		},
		{
			name:    "all fields missing",
			config:  Config{},
			wantErr: true,
			errMsg:  "auth_url",
		},
		{
			name: "http auth_url rejected",
			config: Config{
				AuthURL:  "http://example.com/authorize",
				TokenURL: "https://example.com/token",
				ClientID: "my-client-id",
			},
			wantErr: true,
			errMsg:  "HTTPS",
		},
		{
			name: "http token_url rejected",
			config: Config{
				AuthURL:  "https://example.com/authorize",
				TokenURL: "http://example.com/token",
				ClientID: "my-client-id",
			},
			wantErr: true,
			errMsg:  "HTTPS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" {
					if got := err.Error(); !strings.Contains(got, tt.errMsg) {
						t.Errorf("error %q should mention %q", got, tt.errMsg)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("valid yaml", func(t *testing.T) {
		dir := t.TempDir()
		content := `auth_url: https://example.com/authorize
token_url: https://example.com/token
client_id: my-client-id
client_secret: my-secret
scopes: read write
`
		if err := os.WriteFile(filepath.Join(dir, "example.yaml"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}

		cfg, err := LoadConfig(dir, "example")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.AuthURL != "https://example.com/authorize" {
			t.Errorf("AuthURL = %q, want %q", cfg.AuthURL, "https://example.com/authorize")
		}
		if cfg.TokenURL != "https://example.com/token" {
			t.Errorf("TokenURL = %q, want %q", cfg.TokenURL, "https://example.com/token")
		}
		if cfg.ClientID != "my-client-id" {
			t.Errorf("ClientID = %q, want %q", cfg.ClientID, "my-client-id")
		}
		if cfg.ClientSecret != "my-secret" {
			t.Errorf("ClientSecret = %q, want %q", cfg.ClientSecret, "my-secret")
		}
		if cfg.Scopes != "read write" {
			t.Errorf("Scopes = %q, want %q", cfg.Scopes, "read write")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		_, err := LoadConfig(dir, "nonexistent")
		if err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
	})

	t.Run("invalid yaml", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(":::not yaml:::"), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadConfig(dir, "bad")
		if err == nil {
			t.Fatal("expected error for invalid yaml, got nil")
		}
	})

	t.Run("missing required fields", func(t *testing.T) {
		dir := t.TempDir()
		content := `client_id: my-client-id
`
		if err := os.WriteFile(filepath.Join(dir, "partial.yaml"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadConfig(dir, "partial")
		if err == nil {
			t.Fatal("expected validation error, got nil")
		}
	})
}

func TestSaveConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		AuthURL:  "https://example.com/authorize",
		TokenURL: "https://example.com/token",
		ClientID: "my-client-id",
	}

	if err := SaveConfig(dir, "test", cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify we can load it back.
	loaded, err := LoadConfig(dir, "test")
	if err != nil {
		t.Fatalf("unexpected error loading saved config: %v", err)
	}
	if loaded.AuthURL != cfg.AuthURL {
		t.Errorf("AuthURL = %q, want %q", loaded.AuthURL, cfg.AuthURL)
	}
	if loaded.TokenURL != cfg.TokenURL {
		t.Errorf("TokenURL = %q, want %q", loaded.TokenURL, cfg.TokenURL)
	}
	if loaded.ClientID != cfg.ClientID {
		t.Errorf("ClientID = %q, want %q", loaded.ClientID, cfg.ClientID)
	}
}

func TestDefaultConfigDir(t *testing.T) {
	dir := DefaultConfigDir()
	if dir == "" {
		t.Fatal("DefaultConfigDir returned empty string")
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("DefaultConfigDir returned relative path: %s", dir)
	}
	if filepath.Base(dir) != "oauth" {
		t.Errorf("DefaultConfigDir should end with 'oauth', got: %s", dir)
	}
}
