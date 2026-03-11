package netrules

import "testing"

func TestEvaluateRules(t *testing.T) {
	rules := []Rule{
		{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
		{Action: "allow", Method: "POST", PathPattern: "/repos/*/pulls"},
		{Action: "deny", Method: "DELETE", PathPattern: "/**"},
	}

	tests := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{"GET repos matches", "GET", "/repos/foo", "allow"},
		{"POST pulls matches", "POST", "/repos/foo/pulls", "allow"},
		{"DELETE blocked", "DELETE", "/repos/foo", "deny"},
		{"PUT no match", "PUT", "/repos/foo", ""},
		{"GET other path no match", "GET", "/user", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateRules(rules, tt.method, tt.path)
			if got != tt.want {
				t.Errorf("EvaluateRules(%q, %q) = %q, want %q", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestEvaluateRulesWildcardMethod(t *testing.T) {
	rules := []Rule{
		{Action: "deny", Method: "*", PathPattern: "/admin/**"},
		{Action: "allow", Method: "*", PathPattern: "/**"},
	}

	tests := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{"admin blocked", "GET", "/admin/users", "deny"},
		{"admin POST blocked", "POST", "/admin/settings", "deny"},
		{"other allowed", "GET", "/api", "allow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateRules(rules, tt.method, tt.path)
			if got != tt.want {
				t.Errorf("EvaluateRules(%q, %q) = %q, want %q", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestEvaluateRulesFirstMatchWins(t *testing.T) {
	rules := []Rule{
		{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
		{Action: "deny", Method: "GET", PathPattern: "/**"},
	}

	got := EvaluateRules(rules, "GET", "/repos/foo")
	if got != "allow" {
		t.Errorf("expected first-match-wins allow, got %q", got)
	}
}

func TestEvaluateRulesQueryStringStripped(t *testing.T) {
	rules := []Rule{
		{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
	}

	got := EvaluateRules(rules, "GET", "/repos/foo?page=1")
	if got != "allow" {
		t.Errorf("expected query string to be stripped, got %q", got)
	}
}

// exactHostMatch is a simple host matcher for testing.
func exactHostMatch(pattern, host string, port int) bool {
	return pattern == host && (port == 80 || port == 443)
}

func TestCheckStrict(t *testing.T) {
	hostRules := []HostRules{
		{
			Host: "api.github.com",
			Rules: []Rule{
				{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
				{Action: "deny", Method: "DELETE", PathPattern: "/**"},
			},
		},
		{Host: "registry.npmjs.org"},
	}

	tests := []struct {
		name    string
		host    string
		port    int
		method  string
		path    string
		allowed bool
	}{
		{"allowed by rule", "api.github.com", 443, "GET", "/repos/foo", true},
		{"denied by rule", "api.github.com", 443, "DELETE", "/repos/foo", false},
		{"no rule match falls to strict deny", "api.github.com", 443, "PUT", "/user", false},
		{"host-only entry allows all", "registry.npmjs.org", 443, "DELETE", "/anything", true},
		{"unlisted host denied", "evil.com", 443, "GET", "/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Check("strict", hostRules, tt.host, tt.port, tt.method, tt.path, exactHostMatch)
			if got != tt.allowed {
				t.Errorf("Check() = %v, want %v", got, tt.allowed)
			}
		})
	}
}

func TestCheckPermissive(t *testing.T) {
	hostRules := []HostRules{
		{
			Host: "api.github.com",
			Rules: []Rule{
				{Action: "deny", Method: "DELETE", PathPattern: "/**"},
			},
		},
	}

	tests := []struct {
		name    string
		host    string
		port    int
		method  string
		path    string
		allowed bool
	}{
		{"deny rule blocks", "api.github.com", 443, "DELETE", "/repos/foo", false},
		{"no match falls to permissive allow", "api.github.com", 443, "GET", "/repos/foo", true},
		{"unlisted host allowed", "other.com", 443, "GET", "/", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Check("permissive", hostRules, tt.host, tt.port, tt.method, tt.path, exactHostMatch)
			if got != tt.allowed {
				t.Errorf("Check() = %v, want %v", got, tt.allowed)
			}
		})
	}
}
