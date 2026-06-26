package cli

import "testing"

func TestBuildOpenURL(t *testing.T) {
	tests := []struct {
		name     string
		agent    string
		endpoint string
		port     int
		want     string
	}{
		{"global index", "", "", 8080, "https://localhost:8080/"},
		{"agent only", "demo", "", 8080, "https://demo.localhost:8080/"},
		{"agent and endpoint", "demo", "web", 8080, "https://web.demo.localhost:8080/"},
		{"fallback port", "demo", "web", 49213, "https://web.demo.localhost:49213/"},
		{"endpoint ignored without agent", "", "web", 8080, "https://localhost:8080/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildOpenURL(tt.agent, tt.endpoint, tt.port); got != tt.want {
				t.Errorf("buildOpenURL(%q,%q,%d) = %q, want %q", tt.agent, tt.endpoint, tt.port, got, tt.want)
			}
		})
	}
}

func TestDefaultOpenAgent(t *testing.T) {
	// Exactly one running agent: pick it (no cwd moat.yaml in the test dir).
	one := map[string]map[string]string{"solo": {"web": "127.0.0.1:3000"}}
	if got := defaultOpenAgent(one); got != "solo" {
		t.Errorf("single agent: got %q, want solo", got)
	}

	// Multiple agents with no cwd match: no default (caller opens global index).
	many := map[string]map[string]string{
		"a": {"web": "127.0.0.1:3000"},
		"b": {"web": "127.0.0.1:3001"},
	}
	if got := defaultOpenAgent(many); got != "" {
		t.Errorf("multiple agents: got %q, want empty", got)
	}

	// No agents: no default.
	if got := defaultOpenAgent(map[string]map[string]string{}); got != "" {
		t.Errorf("no agents: got %q, want empty", got)
	}
}

func TestJoinSortedKeys(t *testing.T) {
	got := joinSortedKeys(map[string]string{"web": "x", "api": "y", "admin": "z"})
	if got != "admin, api, web" {
		t.Errorf("joinSortedKeys = %q, want sorted 'admin, api, web'", got)
	}
	if got := joinSortedKeys(map[string]string{}); got != "none" {
		t.Errorf("empty joinSortedKeys = %q, want none", got)
	}
}
