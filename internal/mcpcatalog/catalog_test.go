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
		{"context7", Entry{URL: "https://mcp.context7.com/mcp", Grant: "mcp-context7", Header: "CONTEXT7_API_KEY", OAuth: false}, true},
		// Langfuse regional entries — object form, shared grant, Authorization header.
		{"langfuse-eu", Entry{URL: "https://cloud.langfuse.com/api/public/mcp", Grant: "mcp-langfuse", Header: "Authorization", OAuth: false}, true},
		{"langfuse-us", Entry{URL: "https://us.cloud.langfuse.com/api/public/mcp", Grant: "mcp-langfuse", Header: "Authorization", OAuth: false}, true},
		{"langfuse-jp", Entry{URL: "https://jp.cloud.langfuse.com/api/public/mcp", Grant: "mcp-langfuse", Header: "Authorization", OAuth: false}, true},
		{"langfuse-hipaa", Entry{URL: "https://hipaa.cloud.langfuse.com/api/public/mcp", Grant: "mcp-langfuse", Header: "Authorization", OAuth: false}, true},
		// No bare langfuse alias.
		{"langfuse", Entry{}, false},
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

func TestLangfuseRegions(t *testing.T) {
	regions := []string{"langfuse-eu", "langfuse-us", "langfuse-jp", "langfuse-hipaa"}
	urls := make(map[string]bool)
	for _, name := range regions {
		e, ok := Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) not found", name)
		}
		if e.Grant != "mcp-langfuse" {
			t.Errorf("Lookup(%q).Grant = %q, want %q", name, e.Grant, "mcp-langfuse")
		}
		if e.Header != "Authorization" {
			t.Errorf("Lookup(%q).Header = %q, want %q", name, e.Header, "Authorization")
		}
		if e.OAuth {
			t.Errorf("Lookup(%q).OAuth = true, want false", name)
		}
		if urls[e.URL] {
			t.Errorf("Lookup(%q).URL %q is a duplicate", name, e.URL)
		}
		urls[e.URL] = true
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
