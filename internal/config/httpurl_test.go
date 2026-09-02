package config

import "testing"

func TestValidateHTTPURL(t *testing.T) {
	valid := []string{
		"https://gw.lunaroute.com",
		"http://localhost:8787",
		"https://gw.example.com:8443/anthropic",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
	}
	for _, raw := range valid {
		u, err := ValidateHTTPURL(raw)
		if err != nil {
			t.Errorf("ValidateHTTPURL(%q): unexpected error: %v", raw, err)
			continue
		}
		if u == nil {
			t.Errorf("ValidateHTTPURL(%q): nil URL with nil error", raw)
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
			if u, err := ValidateHTTPURL(tt.raw); err == nil {
				t.Errorf("ValidateHTTPURL(%q) = %v, want an error", tt.raw, u)
			}
		})
	}
}
