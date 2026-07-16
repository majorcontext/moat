package moatinit

import (
	"encoding/base64"
	"strings"
)

// initFileRecord is one MOAT_INIT_FILES record: an absolute path and its
// base64-encoded content.
type initFileRecord struct {
	path    string
	content string
}

// parseInitFiles mirrors the script's record loop:
//
//	printf '%s\n' "$MOAT_INIT_FILES" | while IFS="$(printf '\t')" read -r filepath content
//
// Records are newline-separated. Field splitting follows POSIX `read` with
// IFS set to a single tab — and because tab is IFS *whitespace*, the
// semantics are subtler than "split on first tab" (verified against a live
// /bin/sh; see TestSplitInitRecord):
//
//   - leading tabs are stripped, so a leading-tab record yields the payload
//     as the PATH (not an empty path)
//   - the delimiter run after the first field is consumed entirely
//   - interior tabs inside the remainder are preserved (extra fields land in
//     content), but trailing tabs are trimmed from it
//
// An empty line yields an empty path, which the phase skips (INIT-04) — that
// is what makes a trailing newline harmless. In practice the producer
// (internal/run) only emits <abs-path>\t<base64> records; these edge rules
// exist for byte-parity with the shell on malformed input.
func parseInitFiles(v string) []initFileRecord {
	lines := strings.Split(v, "\n")
	recs := make([]initFileRecord, 0, len(lines))
	for _, line := range lines {
		path, content := splitInitRecord(line)
		recs = append(recs, initFileRecord{path: path, content: content})
	}
	return recs
}

// splitInitRecord applies the IFS=<tab> read -r splitting rules above to a
// single record.
func splitInitRecord(line string) (path, content string) {
	line = strings.TrimLeft(line, "\t")
	idx := strings.IndexByte(line, '\t')
	if idx < 0 {
		return line, ""
	}
	path = line[:idx]
	content = strings.TrimLeft(line[idx:], "\t")
	content = strings.TrimRight(content, "\t")
	return path, content
}

// decodeInitContent decodes a record's base64 payload (INIT-06). Go's
// StdEncoding decoder already ignores \r and \n like coreutils `base64 -d`
// (embedded newlines cannot occur here anyway — a newline would split the
// record), and rejects other non-alphabet bytes exactly as `base64 -d`
// rejects "invalid input". Decoding happens to a buffer BEFORE any file is
// touched, so an invalid payload aborts fail-closed without leaving a
// partial secret on disk (plan Appendix B P1; the shell's `base64 -d >
// "$filepath"` could leave a truncated file behind before aborting — the
// buffer-first port is the sanctioned hardening of that same fatal path).
func decodeInitContent(content string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(content)
}
