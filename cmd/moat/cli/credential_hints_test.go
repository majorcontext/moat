package cli

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/storage"
)

func TestCredentialRejectionHints(t *testing.T) {
	gh401 := []storage.NetworkRequest{
		{URL: "https://github.com/octocat/Hello-World.git/info/refs?service=git-upload-pack", StatusCode: 401},
	}
	api403 := []storage.NetworkRequest{
		{URL: "https://api.github.com/user", StatusCode: 403},
	}
	ok := []storage.NetworkRequest{
		{URL: "https://github.com/octocat/Hello-World.git/info/refs", StatusCode: 200},
	}

	tests := []struct {
		name      string
		reqs      []storage.NetworkRequest
		grants    []string
		wantHint  bool
		wantGrant string // substring expected in the hint
	}{
		{name: "github 401 with github grant", reqs: gh401, grants: []string{"github"}, wantHint: true, wantGrant: "moat grant github"},
		{name: "api.github.com 403 with github grant", reqs: api403, grants: []string{"github"}, wantHint: true, wantGrant: "moat grant github"},
		{name: "github 401 but github not granted", reqs: gh401, grants: []string{"anthropic"}, wantHint: false},
		{name: "github 200 success", reqs: ok, grants: []string{"github"}, wantHint: false},
		{name: "no requests", reqs: nil, grants: []string{"github"}, wantHint: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := credentialRejectionHints(tt.reqs, tt.grants)
			if tt.wantHint {
				if len(got) != 1 {
					t.Fatalf("got %d hints, want 1: %v", len(got), got)
				}
				if !strings.Contains(got[0], tt.wantGrant) {
					t.Fatalf("hint %q does not mention %q", got[0], tt.wantGrant)
				}
			} else if len(got) != 0 {
				t.Fatalf("got %d hints, want 0: %v", len(got), got)
			}
		})
	}
}

// A look-alike host must not trigger a hint (suffix-match guards against
// substring false positives like "notgithub.com").
func TestCredentialRejectionHints_LookalikeHost(t *testing.T) {
	reqs := []storage.NetworkRequest{{URL: "https://notgithub.com/x", StatusCode: 401}}
	if got := credentialRejectionHints(reqs, []string{"github"}); len(got) != 0 {
		t.Fatalf("look-alike host should not match: %v", got)
	}
}
