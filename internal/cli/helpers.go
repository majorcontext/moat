package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/majorcontext/moat/internal/config"
)

// validEnvKey matches valid environment variable names.
// Must start with letter or underscore, followed by letters, digits, or underscores.
var validEnvKey = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ResolveWorkspacePath resolves and validates a workspace path argument.
// Returns the absolute, symlink-resolved path.
func ResolveWorkspacePath(workspace string) (string, error) {
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

// ParseEnvFlags validates and parses environment variable flags into the config.
func ParseEnvFlags(envFlags []string, cfg *config.Config) error {
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

// HasDependency checks if a dependency prefix exists in the list.
// Matches exact name (e.g., "node") or name with version (e.g., "node@20").
func HasDependency(deps []string, prefix string) bool {
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
