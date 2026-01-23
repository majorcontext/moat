package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andybons/moat/internal/config"
)

func TestResolveWorkspacePath(t *testing.T) {
	// Create a temp directory for testing
	tempDir := t.TempDir()

	tests := []struct {
		name      string
		workspace string
		wantErr   bool
	}{
		{
			name:      "current directory",
			workspace: ".",
			wantErr:   false,
		},
		{
			name:      "temp directory",
			workspace: tempDir,
			wantErr:   false,
		},
		{
			name:      "non-existent directory",
			workspace: "/nonexistent/path/that/does/not/exist",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := resolveWorkspacePath(tt.workspace)
			if tt.wantErr {
				if err == nil {
					t.Error("resolveWorkspacePath() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("resolveWorkspacePath() error = %v", err)
				}
				if result == "" {
					t.Error("resolveWorkspacePath() returned empty string")
				}
				// Result should be absolute path
				if !filepath.IsAbs(result) {
					t.Errorf("resolveWorkspacePath() = %q, want absolute path", result)
				}
			}
		})
	}
}

func TestResolveWorkspacePath_File(t *testing.T) {
	// Create a temp file (not directory)
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "testfile.txt")
	if err := os.WriteFile(tempFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	_, err := resolveWorkspacePath(tempFile)
	if err == nil {
		t.Error("resolveWorkspacePath() expected error for file, got nil")
	}
}

func TestParseEnvFlags(t *testing.T) {
	tests := []struct {
		name    string
		flags   []string
		wantErr bool
		wantEnv map[string]string
	}{
		{
			name:    "empty flags",
			flags:   nil,
			wantErr: false,
			wantEnv: nil,
		},
		{
			name:    "valid key=value",
			flags:   []string{"FOO=bar"},
			wantErr: false,
			wantEnv: map[string]string{"FOO": "bar"},
		},
		{
			name:    "multiple flags",
			flags:   []string{"FOO=bar", "BAZ=qux"},
			wantErr: false,
			wantEnv: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name:    "value with equals sign",
			flags:   []string{"FOO=bar=baz"},
			wantErr: false,
			wantEnv: map[string]string{"FOO": "bar=baz"},
		},
		{
			name:    "empty value",
			flags:   []string{"FOO="},
			wantErr: false,
			wantEnv: map[string]string{"FOO": ""},
		},
		{
			name:    "underscore in key",
			flags:   []string{"FOO_BAR=value"},
			wantErr: false,
			wantEnv: map[string]string{"FOO_BAR": "value"},
		},
		{
			name:    "missing equals",
			flags:   []string{"INVALID"},
			wantErr: true,
		},
		{
			name:    "invalid key (starts with number)",
			flags:   []string{"1INVALID=value"},
			wantErr: true,
		},
		{
			name:    "invalid key (contains hyphen)",
			flags:   []string{"INVALID-KEY=value"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{}
			err := parseEnvFlags(tt.flags, cfg)

			if tt.wantErr {
				if err == nil {
					t.Error("parseEnvFlags() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("parseEnvFlags() error = %v", err)
				return
			}

			if tt.wantEnv == nil {
				return
			}

			for k, v := range tt.wantEnv {
				if cfg.Env[k] != v {
					t.Errorf("cfg.Env[%q] = %q, want %q", k, cfg.Env[k], v)
				}
			}
		})
	}
}

func TestHasDependency(t *testing.T) {
	tests := []struct {
		name   string
		deps   []string
		prefix string
		want   bool
	}{
		{
			name:   "exact match",
			deps:   []string{"node", "git"},
			prefix: "node",
			want:   true,
		},
		{
			name:   "with version",
			deps:   []string{"node@20", "git"},
			prefix: "node",
			want:   true,
		},
		{
			name:   "not found",
			deps:   []string{"git", "python"},
			prefix: "node",
			want:   false,
		},
		{
			name:   "empty list",
			deps:   nil,
			prefix: "node",
			want:   false,
		},
		{
			name:   "partial match should not match",
			deps:   []string{"nodejs"},
			prefix: "node",
			want:   false,
		},
		{
			name:   "version only (no name)",
			deps:   []string{"node@"},
			prefix: "node",
			want:   false, // node@ has empty version
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasDependency(tt.deps, tt.prefix)
			if got != tt.want {
				t.Errorf("hasDependency(%v, %q) = %v, want %v", tt.deps, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestValidateHost(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		// Valid hosts
		{"simple domain", "example.com", false},
		{"subdomain", "api.example.com", false},
		{"deep subdomain", "a.b.c.example.com", false},
		{"ipv4 address", "192.168.1.1", false},
		{"localhost", "localhost", false},
		{"wildcard", "*.example.com", false},
		{"single label", "myhost", false},

		// Invalid hosts
		{"empty string", "", true},
		{"whitespace only", "   ", true},
		{"contains space", "example .com", true},
		{"contains slash", "example.com/path", true},
		{"contains port", "example.com:8080", true},
		{"contains @", "user@example.com", true},
		{"contains #", "example.com#anchor", true},
		{"contains ?", "example.com?query", true},
		{"too long", string(make([]byte, 300)), true},
		{"label too long", "a" + string(make([]byte, 70)) + ".com", true},
		{"empty label", "example..com", true},
		{"starts with hyphen", "-example.com", true},
		{"ends with hyphen", "example-.com", true},
		{"wildcard only", "*.", true},
		{"invalid characters", "example!.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHost(tt.host)
			if tt.wantErr {
				if err == nil {
					t.Errorf("validateHost(%q) expected error, got nil", tt.host)
				}
			} else {
				if err != nil {
					t.Errorf("validateHost(%q) unexpected error: %v", tt.host, err)
				}
			}
		})
	}
}

func TestShortenPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "path in home",
			path: filepath.Join(home, "projects", "myapp"),
			want: "~/projects/myapp",
		},
		{
			name: "path outside home",
			path: "/var/log/test",
			want: "/var/log/test",
		},
		{
			name: "empty path",
			path: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortenPath(tt.path)
			if got != tt.want {
				t.Errorf("shortenPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestFormatTimeAgo(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{"just now", now.Add(-30 * time.Second), "just now"},
		{"1 minute ago", now.Add(-1 * time.Minute), "1 minute ago"},
		{"5 minutes ago", now.Add(-5 * time.Minute), "5 minutes ago"},
		{"1 hour ago", now.Add(-1 * time.Hour), "1 hour ago"},
		{"3 hours ago", now.Add(-3 * time.Hour), "3 hours ago"},
		{"1 day ago", now.Add(-24 * time.Hour), "1 day ago"},
		{"3 days ago", now.Add(-72 * time.Hour), "3 days ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimeAgo(tt.time)
			if got != tt.want {
				t.Errorf("formatTimeAgo() = %q, want %q", got, tt.want)
			}
		})
	}
}
