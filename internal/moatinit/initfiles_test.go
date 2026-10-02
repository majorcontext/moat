package moatinit

import (
	"bytes"
	"encoding/base64"
	"testing"
)

// TestSplitInitRecord pins the exact POSIX `IFS=<tab> read -r filepath
// content` splitting semantics. Because tab is IFS *whitespace*, the rules
// differ from a naive "split on first tab"; every case below was verified
// against a live /bin/sh running the script's actual loop.
func TestSplitInitRecord(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantPath    string
		wantContent string
	}{
		{"simple record", "a\tXX", "a", "XX"},
		// Leading tabs are IFS whitespace: stripped, so the payload becomes
		// the PATH — not an empty path.
		{"leading tab", "\tXX", "XX", ""},
		{"multiple leading tabs", "\t\tXX", "XX", ""},
		{"no tab", "a", "a", ""},
		// Extra tabs land in content (interior runs preserved)...
		{"extra fields", "a\tb\tc", "a", "b\tc"},
		// ...but the delimiter run after the path collapses entirely...
		{"adjacent delimiter tabs", "a\t\tb", "a", "b"},
		// ...and trailing tabs are trimmed from the last field.
		{"trailing tab", "a\tb\t", "a", "b"},
		{"trailing tab run", "a\tb\t\t", "a", "b"},
		{"only trailing tab", "a\t", "a", ""},
		// An empty line yields an empty path — the skip rule (INIT-04) that
		// makes MOAT_INIT_FILES' trailing newline harmless.
		{"empty line", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, content := splitInitRecord(tt.line)
			if path != tt.wantPath || content != tt.wantContent {
				t.Errorf("splitInitRecord(%q) = (%q, %q), want (%q, %q)",
					tt.line, path, content, tt.wantPath, tt.wantContent)
			}
		})
	}
}

func TestParseInitFiles(t *testing.T) {
	in := "/home/moatuser/.config/a\tYWJj\n/etc/b\tZGVm\n"
	recs := parseInitFiles(in)
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3 (two payloads + empty trailing line)", len(recs))
	}
	if recs[0].path != "/home/moatuser/.config/a" || recs[0].content != "YWJj" {
		t.Errorf("record 0 = %+v", recs[0])
	}
	if recs[1].path != "/etc/b" || recs[1].content != "ZGVm" {
		t.Errorf("record 1 = %+v", recs[1])
	}
	// The trailing empty line parses to an empty path (skipped by the phase).
	if recs[2].path != "" {
		t.Errorf("record 2 path = %q, want empty", recs[2].path)
	}
}

func TestDecodeInitContent(t *testing.T) {
	// Round-trip, including binary content and no added trailing newline
	// (INIT-06: printf '%s' | base64 -d writes exactly the decoded bytes).
	secret := []byte("token = \"abc123\"\x00\xff")
	got, err := decodeInitContent(base64.StdEncoding.EncodeToString(secret))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Errorf("decoded %q, want %q", got, secret)
	}

	// Empty content decodes to an empty file, not an error.
	if got, err := decodeInitContent(""); err != nil || len(got) != 0 {
		t.Errorf("decodeInitContent(\"\") = (%q, %v), want empty, nil", got, err)
	}

	// Newline-wrapped payloads decode like coreutils base64 -d...
	if got, err := decodeInitContent("YW\nJj"); err != nil || string(got) != "abc" {
		t.Errorf("newline-wrapped payload = (%q, %v), want (abc, nil)", got, err)
	}
	// ...but carriage returns are invalid input, exactly as base64 -d
	// rejects them (a CRLF-joined record must fail closed, not silently
	// decode where the shell would abort).
	if _, err := decodeInitContent("YW\r\nJj"); err == nil {
		t.Error("CR-tainted payload decoded; base64 -d rejects it")
	}

	// Companion: invalid base64 fails closed (the phase aborts before any
	// file is written and before exec).
	if _, err := decodeInitContent("!!!not-base64!!!"); err == nil {
		t.Error("decodeInitContent(invalid) = nil error, want failure")
	}
}
