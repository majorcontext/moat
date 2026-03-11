package netrules

import (
	"path"
	"strings"
)

// MatchPath checks if a request path matches a pattern.
// Patterns support:
//   - "*"  matches a single path segment
//   - "**" matches zero or more path segments
//
// Paths are normalized before matching (double slashes collapsed,
// trailing slashes removed, dot segments resolved).
// Query strings should be stripped before calling this function.
func MatchPath(pattern, reqPath string) bool {
	pattern = normalizePath(pattern)
	reqPath = normalizePath(reqPath)

	patternParts := splitPath(pattern)
	pathParts := splitPath(reqPath)

	return matchParts(patternParts, pathParts)
}

// normalizePath cleans a path: collapses double slashes, resolves dots,
// removes trailing slash (except root "/").
func normalizePath(p string) string {
	p = path.Clean(p)
	if p == "" || p == "." {
		return "/"
	}
	return p
}

// splitPath splits a cleaned path into segments.
// "/" returns an empty slice.
// "/foo/bar" returns ["foo", "bar"].
func splitPath(p string) []string {
	if p == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(p, "/"), "/")
}

// matchParts recursively matches pattern parts against path parts.
// consumed tracks whether any fixed or wildcard segment has been consumed
// before this call (used to determine if ** can match zero segments).
func matchParts(pattern, reqPath []string) bool {
	return matchPartsInner(pattern, reqPath, false)
}

func matchPartsInner(pattern, reqPath []string, consumed bool) bool {
	for len(pattern) > 0 {
		seg := pattern[0]
		pattern = pattern[1:]

		if seg == "**" {
			if len(pattern) == 0 {
				// "**" at end: requires at least one segment if there were
				// preceding segments; otherwise (pattern was just "/**") it
				// matches zero or more.
				if consumed {
					return len(reqPath) > 0
				}
				return true
			}
			// Try consuming 1..N segments for the ** glob.
			start := 0
			if consumed {
				start = 1
			}
			for i := start; i <= len(reqPath); i++ {
				if matchPartsInner(pattern, reqPath[i:], true) {
					return true
				}
			}
			return false
		}

		if len(reqPath) == 0 {
			return false
		}

		if seg == "*" {
			reqPath = reqPath[1:]
			consumed = true
			continue
		}

		if seg != reqPath[0] {
			return false
		}
		reqPath = reqPath[1:]
		consumed = true
	}

	return len(reqPath) == 0
}
