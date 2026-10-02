package moatinit

import (
	"encoding/base64"
	"path/filepath"
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

// initFilesPhase writes the MOAT_INIT_FILES records to disk (INIT region):
// per record, create the parent chain (0755 on the immediate parent, even a
// pre-existing stricter one — parity), decode the payload to a buffer,
// write the file, force 0600, and on the root+moatuser path chown the file
// plus every ancestor directory up to but excluding INIT_HOME (the walk
// deliberately climbs to '/' for out-of-home paths — parity).
//
// Decode/mkdir/write/chmod failures are fatal and abort before exec: a
// partial secret or a world-readable credential file must never start the
// user command. Chown failures are best-effort and silent.
//
// After the loop the variable is removed from the process environment (the
// script's `unset MOAT_INIT_FILES`) so no later child — the pre_run hook,
// gosu, the exec'd command — inherits the base64 secret payload (INIT-10).
func initFilesPhase(ctx *Context) error {
	cfg, sys := ctx.Cfg, ctx.Sys
	if cfg.InitFiles == "" {
		return nil // INIT-01: no work, no ownership resolution
	}
	chown, initHome := initFilesOwnership(sys.Geteuid(), moatuserExists(sys), cfg.Home)
	var owner User
	if chown {
		u, ok := sys.LookupUser("moatuser")
		if !ok {
			chown = false
		}
		owner = u
	}

	for _, rec := range parseInitFiles(cfg.InitFiles) {
		if rec.path == "" {
			continue // INIT-04: harmless trailing-newline record
		}
		// Decode first (buffer, not stream): an invalid payload aborts
		// before any directory or file is touched (Appendix B P1 — the
		// shell could leave a truncated file; failing earlier is the
		// sanctioned fail-closed ordering of the same fatal).
		data, err := decodeInitContent(rec.content)
		if err != nil {
			return fatalPhaseError(ctx, "decoding init file "+rec.path, err)
		}
		dir := filepath.Dir(rec.path)
		if err := sys.MkdirAll(dir, 0o755); err != nil {
			return fatalPhaseError(ctx, "creating "+dir, err)
		}
		// chmod 755 applies to the immediate parent only, including a
		// pre-existing one at a stricter mode (INIT-05, Appendix B P2 —
		// documented parity, not an accident).
		if err := sys.Chmod(dir, 0o755); err != nil {
			return fatalPhaseError(ctx, "setting mode on "+dir, err)
		}
		if err := sys.WriteFile(rec.path, data, 0o600); err != nil {
			return fatalPhaseError(ctx, "writing "+rec.path, err)
		}
		// Force 0600 even when the file pre-existed at a wider mode
		// (WriteFile only applies the mode at creation — INIT-07).
		if err := sys.Chmod(rec.path, 0o600); err != nil {
			return fatalPhaseError(ctx, "restricting "+rec.path, err)
		}

		if chown {
			_ = sys.Chown(rec.path, owner.UID, owner.GID)
			for d := dir; d != "/" && d != "." && d != initHome; d = filepath.Dir(d) {
				_ = sys.Chown(d, owner.UID, owner.GID)
			}
		}
	}

	sys.Unsetenv("MOAT_INIT_FILES")
	return nil
}

// decodeInitContent decodes a record's base64 payload (INIT-06) with
// coreutils `base64 -d` acceptance semantics: newlines are tolerated
// anywhere (they cannot occur inside a record anyway — a newline splits the
// record), but carriage returns are INVALID INPUT — Go's decoder would
// silently strip \r where the shell fails closed (a CRLF-joined
// MOAT_INIT_FILES leaves a trailing \r on each payload), and the
// differential shell-parity harness pins that divergence. All other
// non-alphabet bytes are rejected exactly like `base64 -d`.
//
// Decoding happens to a buffer BEFORE any file is touched, so an invalid
// payload aborts fail-closed without leaving a partial secret on disk (plan
// Appendix B P1; the shell's `base64 -d > "$filepath"` can leave a
// truncated file behind before aborting — the buffer-first port is the
// sanctioned hardening of that same fatal path).
func decodeInitContent(content string) ([]byte, error) {
	if strings.ContainsRune(content, '\r') {
		return nil, base64.CorruptInputError(strings.IndexByte(content, '\r'))
	}
	return base64.StdEncoding.DecodeString(content)
}
