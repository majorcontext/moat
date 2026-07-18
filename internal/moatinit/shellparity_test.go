//go:build linux

package moatinit

import (
	"os/exec"
	"strings"
	"testing"
)

// These tests pin the two pure-logic parsers that replaced coreutils in the
// Go entrypoint against the real tools they emulate — the last remnants of
// the shell-vs-Go differential harness after the shell entrypoint was
// removed. They do not need the (deleted) entrypoint script: each diffs a
// helper directly against a live `sh`/`base64 -d`.

// TestSplitInitRecordMatchesLiveShell differentially checks the record
// splitter against a live `IFS=<tab> read -r` loop for a corpus of
// adversarial record shapes.
func TestSplitInitRecordMatchesLiveShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	corpus := []string{
		"a\tXX", "\tXX", "\t\tXX", "a", "a\tb\tc", "a\t\tb", "a\tb\t",
		"a\tb\t\t", "a\t", "", "path with space\tQUJD", "\t", "\t\t",
		"a\tb c d", "üñï\tßase64", "a\t b", " a\tb", "a \tb",
	}
	for _, line := range corpus {
		gotPath, gotContent := splitInitRecord(line)
		// \036 (record separator, octal — portable in POSIX printf, unlike
		// \xHH) delimits the two captured fields.
		out, err := exec.Command("sh", "-c",
			`printf '%s\n' "$1" | while IFS="$(printf '\t')" read -r filepath content; do printf '%s\036%s' "$filepath" "$content"; done`,
			"sh", line).Output()
		if err != nil {
			t.Fatalf("shell probe for %q: %v", line, err)
		}
		wantPath, wantContent := "", ""
		if parts := strings.SplitN(string(out), "\x1e", 2); len(parts) == 2 {
			wantPath, wantContent = parts[0], parts[1]
		}
		if gotPath != wantPath || gotContent != wantContent {
			t.Errorf("splitInitRecord(%q) = (%q, %q); live sh read gives (%q, %q)",
				line, gotPath, gotContent, wantPath, wantContent)
		}
	}
}

// TestDecodeInitContentMatchesCoreutils differentially checks base64
// accept/reject parity against `base64 -d` for a corpus of payload shapes.
func TestDecodeInitContentMatchesCoreutils(t *testing.T) {
	if _, err := exec.LookPath("base64"); err != nil {
		t.Skip("base64 not installed")
	}
	corpus := []string{
		"", "YWJj", "YW\r\nJj", "YWJjZA==", "YWJjZA=", "!!!", "YWJj ", " YWJj",
		"Y W J j", "====", "AA==", strings.Repeat("QUJDREVGRw==", 1),
	}
	for _, payload := range corpus {
		goBytes, goErr := decodeInitContent(payload)
		cmd := exec.Command("base64", "-d")
		cmd.Stdin = strings.NewReader(payload)
		shBytes, shErr := cmd.Output()
		if (goErr == nil) != (shErr == nil) {
			t.Errorf("decode divergence for %q: go err=%v, base64 -d err=%v", payload, goErr, shErr)
			continue
		}
		if goErr == nil && string(goBytes) != string(shBytes) {
			t.Errorf("decoded bytes diverge for %q: go=%q sh=%q", payload, goBytes, shBytes)
		}
	}
}
