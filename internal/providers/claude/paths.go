package claude

import (
	"path/filepath"
	"strings"
)

// WorkspaceToClaudeDir converts an absolute workspace path to Claude's project directory format.
// Example: /home/alice/projects/myapp -> -home-alice-projects-myapp
func WorkspaceToClaudeDir(absPath string) string {
	// Normalize to forward slashes for cross-platform consistency
	normalized := filepath.ToSlash(absPath)
	cleaned := strings.TrimPrefix(normalized, "/")
	return "-" + strings.ReplaceAll(cleaned, "/", "-")
}
