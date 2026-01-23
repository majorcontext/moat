package cli

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/credential"
	"github.com/andybons/moat/internal/log"
)

// validEnvKey matches valid environment variable names.
// Must start with letter or underscore, followed by letters, digits, or underscores.
var validEnvKey = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// resolveWorkspacePath resolves and validates a workspace path argument.
// Returns the absolute, symlink-resolved path.
func resolveWorkspacePath(workspace string) (string, error) {
	absPath, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolving workspace path: %w", err)
	}

	// Resolve symlinks to get the real path
	absPath, err = filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("workspace path %q: %w", workspace, err)
	}

	// Verify path is a directory
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("workspace path %q: %w", absPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace path %q is not a directory", absPath)
	}

	return absPath, nil
}

// parseEnvFlags validates and parses environment variable flags into the config.
func parseEnvFlags(envFlags []string, cfg *config.Config) error {
	if len(envFlags) == 0 {
		return nil
	}

	if cfg.Env == nil {
		cfg.Env = make(map[string]string)
	}
	for _, e := range envFlags {
		key, value, ok := strings.Cut(e, "=")
		if !ok {
			return fmt.Errorf("invalid environment variable %q: expected KEY=VALUE format", e)
		}
		if !validEnvKey.MatchString(key) {
			return fmt.Errorf("invalid environment variable name %q: must start with letter or underscore, contain only letters, digits, and underscores", key)
		}
		cfg.Env[key] = value
	}
	return nil
}

// hasCredential checks if a valid (non-expired) credential is stored for the given provider.
func hasCredential(provider credential.Provider) bool {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		log.Debug("no credential: failed to get encryption key", "provider", provider, "error", err)
		return false
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		log.Debug("no credential: failed to open credential store", "provider", provider, "error", err)
		return false
	}
	cred, err := store.Get(provider)
	if err != nil {
		log.Debug("no credential: not found in store", "provider", provider, "error", err)
		return false
	}
	// Check if credential has expired
	if !cred.ExpiresAt.IsZero() && time.Now().After(cred.ExpiresAt) {
		log.Debug("credential expired", "provider", provider, "expiresAt", cred.ExpiresAt)
		return false
	}
	return true
}

// hasDependency checks if a dependency prefix exists in the list.
// Matches exact name (e.g., "node") or name with version (e.g., "node@20").
func hasDependency(deps []string, prefix string) bool {
	for _, d := range deps {
		if d == prefix {
			return true
		}
		// Check for prefix@version format, ensuring there's actually a version
		if strings.HasPrefix(d, prefix+"@") && len(d) > len(prefix)+1 {
			return true
		}
	}
	return false
}

// shortenPath shortens a path for display, using ~ for home directory.
func shortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}

	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}

	// If still too long, truncate from the start
	const maxLen = 40
	if len(path) > maxLen {
		return "..." + path[len(path)-maxLen+3:]
	}

	return path
}

// validateHost checks if a host string is valid for network allow rules.
// Accepts hostnames, IP addresses, or wildcard patterns (e.g., "*.example.com").
func validateHost(host string) error {
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}

	// Trim and validate length
	host = strings.TrimSpace(host)
	if len(host) > 253 {
		return fmt.Errorf("host too long (max 253 characters)")
	}

	// Check for obvious invalid characters
	if strings.ContainsAny(host, " \t\n\r/\\:@#?") {
		return fmt.Errorf("contains invalid characters")
	}

	// Handle wildcard patterns (e.g., "*.example.com")
	if strings.HasPrefix(host, "*.") {
		// Validate the rest as a domain
		domain := host[2:]
		if domain == "" {
			return fmt.Errorf("wildcard pattern missing domain")
		}
		return validateDomainPart(domain)
	}

	// Try parsing as IP address
	if ip := net.ParseIP(host); ip != nil {
		return nil // Valid IP address
	}

	// Validate as hostname/domain
	return validateDomainPart(host)
}

// validateDomainPart validates a domain name or hostname.
func validateDomainPart(domain string) error {
	if len(domain) == 0 {
		return fmt.Errorf("domain cannot be empty")
	}

	// Split into labels and validate each
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if len(label) == 0 {
			return fmt.Errorf("empty label in domain")
		}
		if len(label) > 63 {
			return fmt.Errorf("label %q too long (max 63 characters)", label)
		}
		// Labels must start with alphanumeric
		if !isAlphanumeric(label[0]) {
			return fmt.Errorf("label %q must start with letter or digit", label)
		}
		// Labels can contain alphanumeric and hyphens, but not start/end with hyphen
		for _, c := range label {
			if !isAlphanumeric(byte(c)) && c != '-' {
				return fmt.Errorf("label %q contains invalid character %q", label, c)
			}
		}
		if label[len(label)-1] == '-' {
			return fmt.Errorf("label %q cannot end with hyphen", label)
		}
	}

	return nil
}

// isAlphanumeric returns true if the byte is a letter or digit.
func isAlphanumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// formatTimeAgo formats a time as a human-readable "X ago" string.
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case d < 7*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}
