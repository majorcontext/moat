package proxy

import (
	"reflect"
	"testing"
)

func TestParseHostPattern(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected hostPattern
	}{
		{
			name:  "simple host",
			input: "api.example.com",
			expected: hostPattern{
				pattern:    "api.example.com",
				host:       "api.example.com",
				port:       0,
				isWildcard: false,
			},
		},
		{
			name:  "host with port",
			input: "api.example.com:8080",
			expected: hostPattern{
				pattern:    "api.example.com:8080",
				host:       "api.example.com",
				port:       8080,
				isWildcard: false,
			},
		},
		{
			name:  "wildcard host",
			input: "*.example.com",
			expected: hostPattern{
				pattern:    "*.example.com",
				host:       "example.com",
				port:       0,
				isWildcard: true,
			},
		},
		{
			name:  "wildcard host with port",
			input: "*.example.com:443",
			expected: hostPattern{
				pattern:    "*.example.com:443",
				host:       "example.com",
				port:       443,
				isWildcard: true,
			},
		},
		{
			name:  "default https port",
			input: "api.example.com:443",
			expected: hostPattern{
				pattern:    "api.example.com:443",
				host:       "api.example.com",
				port:       443,
				isWildcard: false,
			},
		},
		{
			name:  "default http port",
			input: "api.example.com:80",
			expected: hostPattern{
				pattern:    "api.example.com:80",
				host:       "api.example.com",
				port:       80,
				isWildcard: false,
			},
		},
		{
			name:  "invalid port - not a number",
			input: "api.example.com:abc",
			expected: hostPattern{
				pattern:    "api.example.com:abc",
				host:       "api.example.com",
				port:       0,
				isWildcard: false,
			},
		},
		{
			name:  "port out of range high",
			input: "api.example.com:99999",
			expected: hostPattern{
				pattern:    "api.example.com:99999",
				host:       "api.example.com",
				port:       0,
				isWildcard: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseHostPattern(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("parseHostPattern(%q) = %+v, want %+v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestMatchHost(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		host     string
		port     int
		expected bool
	}{
		// Exact matching
		{
			name:     "exact match default port 443",
			patterns: []string{"api.example.com"},
			host:     "api.example.com",
			port:     443,
			expected: true,
		},
		{
			name:     "exact match default port 80",
			patterns: []string{"api.example.com"},
			host:     "api.example.com",
			port:     80,
			expected: true,
		},
		{
			name:     "exact match no match different host",
			patterns: []string{"api.example.com"},
			host:     "foo.example.com",
			port:     443,
			expected: false,
		},
		{
			name:     "exact match no match non-default port",
			patterns: []string{"api.example.com"},
			host:     "api.example.com",
			port:     8080,
			expected: false,
		},

		// Wildcard matching
		{
			name:     "wildcard match single level",
			patterns: []string{"*.example.com"},
			host:     "api.example.com",
			port:     443,
			expected: true,
		},
		{
			name:     "wildcard match multiple levels",
			patterns: []string{"*.example.com"},
			host:     "foo.bar.example.com",
			port:     443,
			expected: true,
		},
		{
			name:     "wildcard no match base domain",
			patterns: []string{"*.example.com"},
			host:     "example.com",
			port:     443,
			expected: false,
		},
		{
			name:     "wildcard no match different domain",
			patterns: []string{"*.example.com"},
			host:     "api.different.com",
			port:     443,
			expected: false,
		},
		{
			name:     "wildcard no match suffix mismatch",
			patterns: []string{"*.example.com"},
			host:     "notexample.com",
			port:     443,
			expected: false,
		},

		// Port-specific matching
		{
			name:     "specific port match",
			patterns: []string{"api.example.com:8080"},
			host:     "api.example.com",
			port:     8080,
			expected: true,
		},
		{
			name:     "specific port no match wrong port",
			patterns: []string{"api.example.com:8080"},
			host:     "api.example.com",
			port:     443,
			expected: false,
		},
		{
			name:     "specific port 443 matches",
			patterns: []string{"api.example.com:443"},
			host:     "api.example.com",
			port:     443,
			expected: true,
		},
		{
			name:     "specific port 443 no match port 80",
			patterns: []string{"api.example.com:443"},
			host:     "api.example.com",
			port:     80,
			expected: false,
		},

		// Wildcard with port
		{
			name:     "wildcard with specific port match",
			patterns: []string{"*.example.com:8080"},
			host:     "api.example.com",
			port:     8080,
			expected: true,
		},
		{
			name:     "wildcard with specific port no match wrong port",
			patterns: []string{"*.example.com:8080"},
			host:     "api.example.com",
			port:     443,
			expected: false,
		},

		// Multiple patterns
		{
			name: "multiple patterns first matches",
			patterns: []string{
				"api.example.com",
				"foo.example.com",
			},
			host:     "api.example.com",
			port:     443,
			expected: true,
		},
		{
			name: "multiple patterns second matches",
			patterns: []string{
				"api.example.com",
				"foo.example.com",
			},
			host:     "foo.example.com",
			port:     443,
			expected: true,
		},
		{
			name: "multiple patterns none match",
			patterns: []string{
				"api.example.com",
				"foo.example.com",
			},
			host:     "bar.example.com",
			port:     443,
			expected: false,
		},
		{
			name: "multiple patterns with wildcard",
			patterns: []string{
				"api.example.com",
				"*.github.com",
			},
			host:     "raw.githubusercontent.com",
			port:     443,
			expected: false,
		},

		// Real-world GitHub examples
		{
			name: "github grant patterns - github.com",
			patterns: []string{
				"github.com",
				"api.github.com",
				"*.githubusercontent.com",
				"*.github.com",
			},
			host:     "github.com",
			port:     443,
			expected: true,
		},
		{
			name: "github grant patterns - api.github.com",
			patterns: []string{
				"github.com",
				"api.github.com",
				"*.githubusercontent.com",
				"*.github.com",
			},
			host:     "api.github.com",
			port:     443,
			expected: true,
		},
		{
			name: "github grant patterns - raw.githubusercontent.com",
			patterns: []string{
				"github.com",
				"api.github.com",
				"*.githubusercontent.com",
				"*.github.com",
			},
			host:     "raw.githubusercontent.com",
			port:     443,
			expected: true,
		},
		{
			name: "github grant patterns - gist.github.com",
			patterns: []string{
				"github.com",
				"api.github.com",
				"*.githubusercontent.com",
				"*.github.com",
			},
			host:     "gist.github.com",
			port:     443,
			expected: true,
		},
		{
			name: "github grant patterns - unrelated host",
			patterns: []string{
				"github.com",
				"api.github.com",
				"*.githubusercontent.com",
				"*.github.com",
			},
			host:     "evil.com",
			port:     443,
			expected: false,
		},

		// Edge cases
		{
			name:     "empty patterns no match",
			patterns: []string{},
			host:     "api.example.com",
			port:     443,
			expected: false,
		},
		{
			name:     "port 0 for pattern means match 80 and 443 only",
			patterns: []string{"api.example.com"},
			host:     "api.example.com",
			port:     9999,
			expected: false,
		},
		{
			name:     "case insensitive match",
			patterns: []string{"api.github.com"},
			host:     "API.GITHUB.COM",
			port:     443,
			expected: true,
		},
		{
			name:     "wildcard case insensitive",
			patterns: []string{"*.github.com"},
			host:     "API.GITHUB.COM",
			port:     443,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse patterns
			parsed := make([]hostPattern, len(tt.patterns))
			for i, p := range tt.patterns {
				parsed[i] = parseHostPattern(p)
			}

			result := matchHost(parsed, tt.host, tt.port)
			if result != tt.expected {
				t.Errorf("matchHost(%v, %q, %d) = %v, want %v",
					tt.patterns, tt.host, tt.port, result, tt.expected)
			}
		})
	}
}

func TestGetHostsForGrant(t *testing.T) {
	tests := []struct {
		name     string
		grant    string
		expected []string
	}{
		{
			name:  "github grant",
			grant: "github",
			expected: []string{
				"github.com",
				"api.github.com",
				"*.githubusercontent.com",
				"*.github.com",
			},
		},
		{
			name:  "github grant with repo scope",
			grant: "github:repo",
			expected: []string{
				"github.com",
				"api.github.com",
				"*.githubusercontent.com",
				"*.github.com",
			},
		},
		{
			name:  "github grant with multiple scopes",
			grant: "github:repo,user",
			expected: []string{
				"github.com",
				"api.github.com",
				"*.githubusercontent.com",
				"*.github.com",
			},
		},
		{
			name:     "unknown grant",
			grant:    "unknown",
			expected: []string{},
		},
		{
			name:     "unknown grant with scope",
			grant:    "unknown:scope",
			expected: []string{},
		},
		{
			name:     "empty grant",
			grant:    "",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetHostsForGrant(tt.grant)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("GetHostsForGrant(%q) = %v, want %v", tt.grant, result, tt.expected)
			}
		})
	}
}

func TestMatchesPattern(t *testing.T) {
	tests := []struct {
		name     string
		pattern  hostPattern
		host     string
		port     int
		expected bool
	}{
		{
			name: "exact match with default port",
			pattern: hostPattern{
				pattern:    "api.example.com",
				host:       "api.example.com",
				port:       0,
				isWildcard: false,
			},
			host:     "api.example.com",
			port:     443,
			expected: true,
		},
		{
			name: "exact match with specific port",
			pattern: hostPattern{
				pattern:    "api.example.com:8080",
				host:       "api.example.com",
				port:       8080,
				isWildcard: false,
			},
			host:     "api.example.com",
			port:     8080,
			expected: true,
		},
		{
			name: "wildcard match",
			pattern: hostPattern{
				pattern:    "*.example.com",
				host:       "example.com",
				port:       0,
				isWildcard: true,
			},
			host:     "api.example.com",
			port:     443,
			expected: true,
		},
		{
			name: "wildcard no match base domain",
			pattern: hostPattern{
				pattern:    "*.example.com",
				host:       "example.com",
				port:       0,
				isWildcard: true,
			},
			host:     "example.com",
			port:     443,
			expected: false,
		},
		{
			name: "port mismatch specific port",
			pattern: hostPattern{
				pattern:    "api.example.com:8080",
				host:       "api.example.com",
				port:       8080,
				isWildcard: false,
			},
			host:     "api.example.com",
			port:     443,
			expected: false,
		},
		{
			name: "port mismatch default port pattern with non-default port",
			pattern: hostPattern{
				pattern:    "api.example.com",
				host:       "api.example.com",
				port:       0,
				isWildcard: false,
			},
			host:     "api.example.com",
			port:     8080,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesPattern(tt.pattern, tt.host, tt.port)
			if result != tt.expected {
				t.Errorf("matchesPattern(%+v, %q, %d) = %v, want %v",
					tt.pattern, tt.host, tt.port, result, tt.expected)
			}
		})
	}
}
