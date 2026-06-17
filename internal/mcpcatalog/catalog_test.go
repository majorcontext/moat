package mcpcatalog

import (
	"reflect"
	"testing"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		name   string
		want   Entry
		wantOK bool
	}{
		// String entry → OAuth defaults synthesized (OAuth=true).
		{"linear", Entry{URL: "https://mcp.linear.app/mcp", Grant: "oauth:linear", Header: "Authorization", OAuth: true}, true},
		{"notion", Entry{URL: "https://mcp.notion.com/mcp", Grant: "oauth:notion", Header: "Authorization", OAuth: true}, true},
		{"posthog", Entry{URL: "https://mcp.posthog.com/mcp", Grant: "oauth:posthog", Header: "Authorization", OAuth: true}, true},
		{"betterstack", Entry{URL: "https://mcp.betterstack.com", Grant: "oauth:betterstack", Header: "Authorization", OAuth: true}, true},
		{"sentry", Entry{URL: "https://mcp.sentry.dev/mcp", Grant: "oauth:sentry", Header: "Authorization", OAuth: true}, true},
		// Object entry → explicit API-key auth preserved, no defaulting (OAuth=false).
		{"context7", Entry{URL: "https://mcp.context7.com/mcp", Grant: "mcp:context7", Header: "CONTEXT7_API_KEY", OAuth: false}, true},
		// Unknown.
		{"nonexistent", Entry{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Lookup(tt.name)
			if ok != tt.wantOK {
				t.Fatalf("Lookup(%q) ok = %v, want %v", tt.name, ok, tt.wantOK)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Lookup(%q) = %+v, want %+v", tt.name, got, tt.want)
			}
		})
	}
}

func TestGrantName(t *testing.T) {
	tests := []struct {
		grant      string
		wantServer string
		wantOK     bool
	}{
		// Canonical and deprecated forms both strip to the same server name.
		{"mcp:context7", "context7", true},
		{"mcp-context7", "context7", true},
		{"mcp:render", "render", true},
		{"mcp-render", "render", true},
		// Non-MCP grants are not matched.
		{"oauth:notion", "", false},
		{"github", "", false},
		{"ssh:github.com", "", false},
		{"", "", false},
		// Empty server name after the prefix is rejected.
		{"mcp:", "", false},
		{"mcp-", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.grant, func(t *testing.T) {
			server, ok := GrantName(tt.grant)
			if ok != tt.wantOK {
				t.Fatalf("GrantName(%q) ok = %v, want %v", tt.grant, ok, tt.wantOK)
			}
			if server != tt.wantServer {
				t.Errorf("GrantName(%q) server = %q, want %q", tt.grant, server, tt.wantServer)
			}
			if IsGrant(tt.grant) != tt.wantOK {
				t.Errorf("IsGrant(%q) = %v, want %v", tt.grant, IsGrant(tt.grant), tt.wantOK)
			}
		})
	}
}

// TestGrantNameMatchesOAuthExclusion confirms that mcp:context7 keeps
// OAuth=false, so OAuth auto-discovery still excludes it.
func TestGrantNameMatchesOAuthExclusion(t *testing.T) {
	e, ok := Lookup("context7")
	if !ok {
		t.Fatal("Lookup(context7) not found")
	}
	if e.OAuth {
		t.Errorf("context7 OAuth = true, want false (mcp: grant must not be OAuth)")
	}
}

func TestNamesSortedAndNonEmpty(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("Names() is empty")
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("Names() not sorted: %q before %q", names[i-1], names[i])
		}
	}
}
