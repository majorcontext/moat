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
		// String entry → OAuth defaults synthesized.
		{"linear", Entry{URL: "https://mcp.linear.app/mcp", Grant: "oauth:linear", Header: "Authorization"}, true},
		{"notion", Entry{URL: "https://mcp.notion.com/mcp", Grant: "oauth:notion", Header: "Authorization"}, true},
		// Object entry → explicit auth preserved, no defaulting.
		{"context7", Entry{URL: "https://mcp.context7.com/mcp", Grant: "mcp-context7", Header: "CONTEXT7_API_KEY"}, true},
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
