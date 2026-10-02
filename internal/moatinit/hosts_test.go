package moatinit

import (
	"reflect"
	"testing"
)

func TestSplitExtraHosts(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"spaces only", "   ", nil},
		{"single", "a:1", []string{"a:1"}},
		// HOSTS-02: double space yields exactly two tokens, no empty third.
		{"double space", "a:1  b:2", []string{"a:1", "b:2"}},
		// Default IFS also splits on tabs and newlines.
		{"tab and newline separators", "a:1\tb:2\nc:3", []string{"a:1", "b:2", "c:3"}},
		{"leading/trailing whitespace", " a:1 ", []string{"a:1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitExtraHosts(tt.in)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitExtraHosts(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseHostEntry(t *testing.T) {
	tests := []struct {
		tok        string
		wantName   string
		wantTarget string
	}{
		// HOSTS-03: split on the FIRST colon.
		{"x:a:b", "x", "a:b"},
		{"x:", "x", ""},
		{":y", "", "y"},
		// Colon-less: both parameter expansions leave the token unchanged.
		{"z", "z", "z"},
		// First colon at position 0 for a bare IPv6-ish token.
		{"::1", "", ":1"},
		{"moat-proxy:192.0.2.5", "moat-proxy", "192.0.2.5"},
		{"moat-host:@host.docker.internal", "moat-host", "@host.docker.internal"},
	}
	for _, tt := range tests {
		t.Run(tt.tok, func(t *testing.T) {
			e := parseHostEntry(tt.tok)
			if e.name != tt.wantName || e.target != tt.wantTarget {
				t.Errorf("parseHostEntry(%q) = {name:%q target:%q}, want {name:%q target:%q}",
					tt.tok, e.name, e.target, tt.wantName, tt.wantTarget)
			}
		})
	}
}

func TestHostEntrySkip(t *testing.T) {
	tests := []struct {
		tok  string
		want bool
	}{
		// HOSTS-04: malformed entries are skipped...
		{"moat-proxy:", true},
		{":1.2.3.4", true},
		{"foo", true},
		{"x:x", true},
		// ...but 'moat-proxy:@' is NOT skipped here (target "@" is
		// non-empty); it advances to the resolve branch and fails there.
		{"moat-proxy:@", false},
		{"a:1", false},
	}
	for _, tt := range tests {
		t.Run(tt.tok, func(t *testing.T) {
			if got := parseHostEntry(tt.tok).skip(); got != tt.want {
				t.Errorf("skip(%q) = %v, want %v", tt.tok, got, tt.want)
			}
		})
	}
}

func TestResolveTarget(t *testing.T) {
	tests := []struct {
		target       string
		wantHostname string
		wantResolve  bool
	}{
		// HOSTS-05: '@'-prefix means resolve; anything else is literal.
		{"@foo", "foo", true},
		{"@host.docker.internal", "host.docker.internal", true},
		{"192.168.64.1", "", false},
		// 'a@b' is a literal target, not a resolve form.
		{"a@b", "", false},
		// '::1' is written verbatim as a literal.
		{"::1", "", false},
		// Bare '@' resolves the empty hostname (which then fails the
		// resolve loop and exits 1 — parity with the script).
		{"@", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			host, resolve := hostEntry{name: "n", target: tt.target}.resolveTarget()
			if host != tt.wantHostname || resolve != tt.wantResolve {
				t.Errorf("resolveTarget(%q) = (%q, %v), want (%q, %v)",
					tt.target, host, resolve, tt.wantHostname, tt.wantResolve)
			}
		})
	}
}
