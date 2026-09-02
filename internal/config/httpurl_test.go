package config

import "testing"

func TestValidateHTTPURL(t *testing.T) {
	valid := []struct {
		raw            string
		wantNormalized string
	}{
		{raw: "https://gw.lunaroute.com", wantNormalized: "https://gw.lunaroute.com"},
		{raw: "http://localhost:8787", wantNormalized: "http://localhost:8787"},
		{raw: "https://gw.example.com:8443/anthropic", wantNormalized: "https://gw.example.com:8443/anthropic"},
		{raw: "http://127.0.0.1:8080", wantNormalized: "http://127.0.0.1:8080"},
		{raw: "http://[::1]:8080", wantNormalized: "http://[::1]:8080"},
		// Trailing slashes are stripped: callers join a path onto the result,
		// so "https://gw/" would produce "https://gw//v1/messages". Every
		// entry point normalizes here, so moat.yaml and --base-url cannot
		// disagree about the same endpoint.
		{raw: "https://gw.lunaroute.com/", wantNormalized: "https://gw.lunaroute.com"},
		{raw: "https://gw.lunaroute.com///", wantNormalized: "https://gw.lunaroute.com"},
		{raw: "https://gw.example.com/anthropic/", wantNormalized: "https://gw.example.com/anthropic"},
		{raw: "http://localhost:8787/", wantNormalized: "http://localhost:8787"},
	}
	for _, tt := range valid {
		u, normalized, err := ValidateHTTPURL(tt.raw)
		if err != nil {
			t.Errorf("ValidateHTTPURL(%q): unexpected error: %v", tt.raw, err)
			continue
		}
		if u == nil {
			t.Errorf("ValidateHTTPURL(%q): nil URL with nil error", tt.raw)
		}
		if normalized != tt.wantNormalized {
			t.Errorf("ValidateHTTPURL(%q) normalized = %q, want %q", tt.raw, normalized, tt.wantNormalized)
		}
	}

	// Companion cases: everything a container could not connect to. This is the
	// single gate for moat.yaml claude.base_url, `moat grant anthropic
	// --base-url`, and a credential's recorded endpoint, so each rule matters
	// in three places.
	invalid := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "no scheme", raw: "gw.lunaroute.com"},
		{name: "wrong scheme", raw: "ftp://gw.example.com"},
		{name: "file scheme", raw: "file:///etc/passwd"},
		{name: "no host", raw: "http://"},
		// Non-empty Host (":8080") but no hostname at all.
		{name: "port with no host", raw: "http://:8080"},
		{name: "control character", raw: "https://gw\x7f.com"},
		{name: "scheme only with slashes", raw: "://localhost:8080"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			if u, normalized, err := ValidateHTTPURL(tt.raw); err == nil {
				t.Errorf("ValidateHTTPURL(%q) = (%v, %q), want an error", tt.raw, u, normalized)
			}
		})
	}
}
