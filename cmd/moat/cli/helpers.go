package cli

import (
	"fmt"
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

// hasCredential checks if a credential is stored for the given provider.
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
	_, err = store.Get(provider)
	if err != nil {
		log.Debug("no credential: not found in store", "provider", provider, "error", err)
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
