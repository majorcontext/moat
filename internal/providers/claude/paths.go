package claude

import (
	"path/filepath"
	"strings"
)

// WorkspaceToClaudeDir converts an absolute workspace path to Claude's project
// directory format under ~/.claude/projects.
//
// Example: /home/alice/projects/myapp -> -home-alice-projects-myapp
//
// This must match Claude Code's own slug rule exactly, otherwise moat-mounted
// container sessions land in a different projects dir than host sessions and the
// project's history/memory silently forks. Claude Code replaces every
// non-alphanumeric character with "-" (verified against the claude binary
// v2.1.156): letters and digits are kept as-is, everything else (including ".",
// "_", spaces and "/") becomes a single "-", and runs are not collapsed. The
// leading "/" of an absolute path therefore yields the leading "-".
//
// Note: Claude Code applies the rule over UTF-16 code units, so a non-ASCII
// path character (e.g. an astral-plane emoji) would map to two dashes there but
// one here. This only affects non-ASCII workspace paths, which do not occur in
// practice; ASCII paths are byte-for-byte identical.
func WorkspaceToClaudeDir(absPath string) string {
	// Normalize to forward slashes for cross-platform consistency.
	normalized := filepath.ToSlash(absPath)

	var b strings.Builder
	b.Grow(len(normalized))
	for _, r := range normalized {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
