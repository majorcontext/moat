package claude

import "strings"

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
// "_", spaces and path separators) becomes a single "-", and runs are not
// collapsed. The leading "/" of an absolute path therefore yields the leading
// "-". No separator normalization is needed first: "\" and ":" are
// non-alphanumeric too, so Windows paths map identically without filepath.ToSlash.
//
// Note: Claude Code applies the rule over UTF-16 code units. A BMP character
// (one UTF-16 unit) — including non-ASCII letters such as "é" or CJK — maps to a
// single "-" on both sides, matching this implementation. Only characters above
// U+FFFF (astral plane, encoded as a UTF-16 surrogate pair) diverge: Claude
// emits two dashes where this rune-based loop emits one. Such characters do not
// occur in real workspace paths.
func WorkspaceToClaudeDir(absPath string) string {
	var b strings.Builder
	b.Grow(len(absPath))
	for _, r := range absPath {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
