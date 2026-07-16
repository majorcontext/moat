package moatinit

import "strings"

// workspaceVolumeEnabled mirrors WS-01: populate_workspace_volume runs only
// when MOAT_WORKSPACE_VOLUME is exactly the string "1" — not "true", "01",
// " 1", or any other value.
func workspaceVolumeEnabled(v string) bool {
	return v == "1"
}

// stagingDir mirrors WS-03: `staging="${MOAT_WORKSPACE_STAGING:-/mnt/host-workspace}"`
// — the `:-` expansion defaults on unset AND on empty.
func stagingDir(staging string) string {
	if staging == "" {
		return "/mnt/host-workspace"
	}
	return staging
}

// excludeFileContent mirrors WS-04: the exclude file is created empty and,
// when MOAT_WORKSPACE_EXCLUDES is non-empty, receives the value verbatim
// (printf '%s' — no trailing newline appended). Patterns are
// newline-delimited "./"-prefixed paths produced by run.workspaceExcludes;
// an empty exclude file excludes nothing (WS-06).
func excludeFileContent(excludes string) string {
	return excludes
}

// volumeChownPaths mirrors the named-volume chown loop's word-splitting:
// `set -f` disables glob expansion (a target containing [ ] * ? is treated
// literally, never expanded against the filesystem), while word-splitting on
// default IFS stays on — the paths are space-separated.
func volumeChownPaths(v string) []string {
	return strings.Fields(v)
}
