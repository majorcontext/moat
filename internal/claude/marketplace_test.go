package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsSSHURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		// SSH URLs
		{"git@github.com:org/repo.git", true},
		{"git@gitlab.com:org/repo.git", true},
		{"ssh://git@github.com/org/repo.git", true},
		{"ssh://git@gitlab.com:22/org/repo.git", true},

		// HTTPS URLs
		{"https://github.com/org/repo.git", false},
		{"https://gitlab.com/org/repo.git", false},
		{"http://github.com/org/repo.git", false},

		// Edge cases
		{"", false},
		{"github.com/org/repo", false},

		// Security edge cases - malformed URLs
		{"ssh://", true},                             // Empty ssh URL
		{"git@:foo", true},                           // Missing host
		{"git@@github.com:org/repo.git", true},       // Double @
		{"git@github.com:", true},                    // Empty path
		{"ssh://git@", true},                         // No host after @
		{"file://git@github.com:foo", false},         // Wrong scheme with @ and :
		{"ftp://git@github.com/org/repo.git", false}, // Wrong scheme
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := IsSSHURL(tt.url)
			if got != tt.want {
				t.Errorf("IsSSHURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestExtractHost(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		// SSH URLs
		{"git@github.com:org/repo.git", "github.com"},
		{"git@gitlab.com:org/repo.git", "gitlab.com"},
		{"ssh://git@github.com/org/repo.git", "github.com"},
		{"ssh://git@gitlab.com:22/org/repo.git", "gitlab.com"},

		// HTTPS URLs
		{"https://github.com/org/repo.git", "github.com"},
		{"https://gitlab.com/org/repo.git", "gitlab.com"},
		{"http://example.com/repo.git", "example.com"},

		// Edge cases
		{"", ""},

		// Security edge cases - malformed URLs that should be handled gracefully
		{"ssh://", ""},   // Empty ssh URL
		{"git@:foo", ""}, // Missing host
		{"git@@github.com:org/repo.git", "@github.com"},                   // Double @ returns malformed host
		{"git@github.com:", "github.com"},                                 // Empty path
		{"ssh://git@", ""},                                                // No host after @
		{"ssh://git@host:22/path", "host"},                                // Port and path
		{"ssh://git@host:22", "host"},                                     // Just port
		{"https://user:pass@github.com/org/repo", "user:pass@github.com"}, // Auth in HTTPS (unusual)
		{"git@[::1]:repo.git", "["},                                       // IPv6 literal (SCP format) - imperfect, returns first char before :
		{"ssh://git@[::1]:22/repo", "["},                                  // IPv6 in ssh:// - imperfect, stops at first : in IPv6
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := ExtractHost(tt.url)
			if got != tt.want {
				t.Errorf("ExtractHost(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestFilterSSHGrants(t *testing.T) {
	grants := []string{
		"github",
		"anthropic",
		"ssh:github.com",
		"ssh:gitlab.com",
		"ssh", // Generic SSH grant
	}

	hosts := FilterSSHGrants(grants)

	expected := []string{"github.com", "gitlab.com"}
	if len(hosts) != len(expected) {
		t.Fatalf("FilterSSHGrants returned %d hosts, want %d", len(hosts), len(expected))
	}

	for i, host := range hosts {
		if host != expected[i] {
			t.Errorf("hosts[%d] = %q, want %q", i, host, expected[i])
		}
	}
}

func TestHasSSHGrant(t *testing.T) {
	tests := []struct {
		name  string
		hosts []string
		host  string
		want  bool
	}{
		{
			name:  "exact match",
			hosts: []string{"github.com", "gitlab.com"},
			host:  "github.com",
			want:  true,
		},
		{
			name:  "no match",
			hosts: []string{"github.com"},
			host:  "gitlab.com",
			want:  false,
		},
		{
			name:  "wildcard match",
			hosts: []string{"*.github.com"},
			host:  "enterprise.github.com",
			want:  true,
		},
		{
			name:  "wildcard no match",
			hosts: []string{"*.github.com"},
			host:  "gitlab.com",
			want:  false,
		},
		{
			name:  "empty hosts",
			hosts: nil,
			host:  "github.com",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasSSHGrant(tt.hosts, tt.host)
			if got != tt.want {
				t.Errorf("hasSSHGrant(%v, %q) = %v, want %v", tt.hosts, tt.host, got, tt.want)
			}
		})
	}
}

func TestMarketplaceAccessError(t *testing.T) {
	err := &MarketplaceAccessError{
		Name:   "acme-internal",
		URL:    "git@github.com:acme/plugins.git",
		Host:   "github.com",
		Reason: "no SSH grant configured",
	}

	msg := err.Error()

	// Check that error message contains helpful information
	checks := []string{
		"acme-internal",
		"git@github.com:acme/plugins.git",
		"github.com",
		"moat grant ssh --host github.com",
		"ssh:github.com",
		"ssh-add -l",
	}

	for _, check := range checks {
		if !strings.Contains(msg, check) {
			t.Errorf("error message should contain %q, got:\n%s", check, msg)
		}
	}
}

func TestValidateSSHAccess(t *testing.T) {
	t.Run("no settings", func(t *testing.T) {
		err := ValidateSSHAccess(nil, nil)
		if err != nil {
			t.Errorf("ValidateSSHAccess(nil, nil) = %v, want nil", err)
		}
	})

	t.Run("https marketplace", func(t *testing.T) {
		settings := &Settings{
			ExtraKnownMarketplaces: map[string]MarketplaceEntry{
				"public": {
					Source: MarketplaceSource{
						Source: "git",
						URL:    "https://github.com/org/plugins.git",
					},
				},
			},
		}
		err := ValidateSSHAccess(settings, nil)
		if err != nil {
			t.Errorf("HTTPS marketplace should not require SSH grant: %v", err)
		}
	})

	t.Run("ssh marketplace without grant", func(t *testing.T) {
		settings := &Settings{
			ExtraKnownMarketplaces: map[string]MarketplaceEntry{
				"private": {
					Source: MarketplaceSource{
						Source: "git",
						URL:    "git@github.com:org/plugins.git",
					},
				},
			},
		}
		err := ValidateSSHAccess(settings, nil)
		if err == nil {
			t.Error("SSH marketplace without grant should error")
		}
		if _, ok := err.(*MarketplaceAccessError); !ok {
			t.Errorf("error should be MarketplaceAccessError, got %T", err)
		}
	})

	t.Run("ssh marketplace with grant", func(t *testing.T) {
		settings := &Settings{
			ExtraKnownMarketplaces: map[string]MarketplaceEntry{
				"private": {
					Source: MarketplaceSource{
						Source: "git",
						URL:    "git@github.com:org/plugins.git",
					},
				},
			},
		}
		err := ValidateSSHAccess(settings, []string{"github.com"})
		if err != nil {
			t.Errorf("SSH marketplace with grant should not error: %v", err)
		}
	})

	t.Run("directory marketplace", func(t *testing.T) {
		settings := &Settings{
			ExtraKnownMarketplaces: map[string]MarketplaceEntry{
				"local": {
					Source: MarketplaceSource{
						Source: "directory",
						Path:   "/opt/plugins",
					},
				},
			},
		}
		err := ValidateSSHAccess(settings, nil)
		if err != nil {
			t.Errorf("directory marketplace should not require SSH grant: %v", err)
		}
	})
}

func TestConvertToDirectorySource(t *testing.T) {
	t.Run("git source", func(t *testing.T) {
		entry := MarketplaceEntry{
			Source: MarketplaceSource{
				Source: "git",
				URL:    "git@github.com:org/plugins.git",
			},
		}

		result := ConvertToDirectorySource("acme", entry, "/moat/claude-plugins")

		if result.Source.Source != "directory" {
			t.Errorf("Source.Source = %q, want %q", result.Source.Source, "directory")
		}
		if result.Source.Path != "/moat/claude-plugins/marketplaces/acme" {
			t.Errorf("Source.Path = %q, want %q", result.Source.Path, "/moat/claude-plugins/marketplaces/acme")
		}
	})

	t.Run("directory source unchanged", func(t *testing.T) {
		entry := MarketplaceEntry{
			Source: MarketplaceSource{
				Source: "directory",
				Path:   "/opt/plugins",
			},
		}

		result := ConvertToDirectorySource("local", entry, "/moat/claude-plugins")

		if result.Source.Source != "directory" {
			t.Errorf("Source.Source = %q, want %q", result.Source.Source, "directory")
		}
		if result.Source.Path != "/opt/plugins" {
			t.Errorf("Source.Path = %q, want %q (unchanged)", result.Source.Path, "/opt/plugins")
		}
	})
}

func TestMarketplaceManager_MarketplacePath(t *testing.T) {
	m := NewMarketplaceManager("/home/user/.moat/claude/plugins")
	path := m.MarketplacePath("acme")
	expected := "/home/user/.moat/claude/plugins/marketplaces/acme"
	if path != expected {
		t.Errorf("MarketplacePath = %q, want %q", path, expected)
	}
}

func TestMarketplaceManager_MarketplacePath_PathTraversal(t *testing.T) {
	m := NewMarketplaceManager("/home/user/.moat/claude/plugins")

	tests := []struct {
		name     string
		input    string
		wantPath string // Empty means should be rejected
	}{
		{
			name:     "valid name",
			input:    "acme-plugins",
			wantPath: "/home/user/.moat/claude/plugins/marketplaces/acme-plugins",
		},
		{
			name:     "name with underscore",
			input:    "acme_plugins",
			wantPath: "/home/user/.moat/claude/plugins/marketplaces/acme_plugins",
		},
		{
			name:     "simple traversal",
			input:    "..",
			wantPath: "",
		},
		{
			name:     "traversal with name",
			input:    "../etc",
			wantPath: "",
		},
		{
			name:     "traversal in middle",
			input:    "foo/../bar",
			wantPath: "",
		},
		{
			name:     "forward slash",
			input:    "foo/bar",
			wantPath: "",
		},
		{
			name:     "backslash",
			input:    "foo\\bar",
			wantPath: "",
		},
		{
			name:     "double dots embedded (rejected due to .. check)",
			input:    "foo..bar",
			wantPath: "", // Note: overly restrictive but safe
		},
		{
			name:     "current dir",
			input:    ".",
			wantPath: "",
		},
		{
			name:     "hidden file",
			input:    ".hidden",
			wantPath: "/home/user/.moat/claude/plugins/marketplaces/.hidden",
		},
		{
			name:     "absolute path attempt",
			input:    "/etc/passwd",
			wantPath: "",
		},
		{
			name:     "empty name",
			input:    "",
			wantPath: "",
		},
		{
			name:     "space only (rejected)",
			input:    "   ",
			wantPath: "",
		},
		{
			name:     "newline attempt (rejected)",
			input:    "foo\nbar",
			wantPath: "",
		},
		{
			name:     "tab character (rejected)",
			input:    "foo\tbar",
			wantPath: "",
		},
		{
			name:     "carriage return (rejected)",
			input:    "foo\rbar",
			wantPath: "",
		},
		{
			name:     "null byte (rejected)",
			input:    "foo\x00bar",
			wantPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.MarketplacePath(tt.input)
			if got != tt.wantPath {
				t.Errorf("MarketplacePath(%q) = %q, want %q", tt.input, got, tt.wantPath)
			}
		})
	}
}

func TestMarketplaceManager_EnsureMarketplace_Directory(t *testing.T) {
	cacheDir := t.TempDir()
	m := NewMarketplaceManager(cacheDir)

	// Create a directory marketplace
	marketplaceDir := filepath.Join(t.TempDir(), "plugins")
	if err := os.MkdirAll(marketplaceDir, 0755); err != nil {
		t.Fatal(err)
	}

	entry := MarketplaceEntry{
		Source: MarketplaceSource{
			Source: "directory",
			Path:   marketplaceDir,
		},
	}

	err := m.EnsureMarketplace("local", entry, nil)
	if err != nil {
		t.Errorf("EnsureMarketplace should succeed for directory: %v", err)
	}
}

func TestMarketplaceManager_EnsureMarketplace_DirectoryNotFound(t *testing.T) {
	cacheDir := t.TempDir()
	m := NewMarketplaceManager(cacheDir)

	entry := MarketplaceEntry{
		Source: MarketplaceSource{
			Source: "directory",
			Path:   "/nonexistent/path",
		},
	}

	err := m.EnsureMarketplace("missing", entry, nil)
	if err == nil {
		t.Error("EnsureMarketplace should error for nonexistent directory")
	}
	if !strings.Contains(err.Error(), "directory not found") {
		t.Errorf("error should mention directory not found: %v", err)
	}
}

func TestMarketplaceManager_EnsureMarketplace_SSHWithoutGrant(t *testing.T) {
	cacheDir := t.TempDir()
	m := NewMarketplaceManager(cacheDir)

	entry := MarketplaceEntry{
		Source: MarketplaceSource{
			Source: "git",
			URL:    "git@github.com:org/private-plugins.git",
		},
	}

	err := m.EnsureMarketplace("private", entry, nil) // No SSH grants
	if err == nil {
		t.Error("EnsureMarketplace should error without SSH grant")
	}
	if _, ok := err.(*MarketplaceAccessError); !ok {
		t.Errorf("error should be MarketplaceAccessError, got %T", err)
	}
}
