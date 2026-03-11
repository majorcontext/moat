package netrules

import "testing"

func TestMatchPath(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"/user", "/user", true},
		{"/user", "/user/foo", false},
		{"/repos/*", "/repos/foo", true},
		{"/repos/*", "/repos/foo/bar", false},
		{"/repos/*/pulls", "/repos/foo/pulls", true},
		{"/repos/*/pulls", "/repos/foo/bar/pulls", false},
		{"/*", "/anything", true},
		{"/*", "/", false},
		{"/repos/**", "/repos/foo", true},
		{"/repos/**", "/repos/foo/bar/baz", true},
		{"/repos/**", "/repos", false},
		{"/admin/**", "/admin/users/123", true},
		{"/**", "/anything/at/all", true},
		{"/**", "/", true},
		{"/repos/*/issues/**", "/repos/foo/issues/123/comments", true},
		{"/repos/*/issues/**", "/repos/foo/bar/issues/123", false},
		{"/repos/**/issues", "/repos/foo/issues", true},
		{"/repos/**/issues", "/repos/issues", false}, // ** in middle requires 1+ segment
		{"/repos/*", "/repos/foo/", true},
		{"/repos/*", "//repos//foo", true},
		{"/", "/", true},
		{"/", "/foo", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_vs_"+tt.path, func(t *testing.T) {
			got := MatchPath(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("MatchPath(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}
