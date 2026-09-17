package daemon

import (
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

func TestCodexNeedsRefresh(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want bool
	}{
		{"comfortably valid", time.Now().Add(time.Hour), false},
		{"inside the skew", time.Now().Add(codexRefreshSkew / 2), true},
		{"already expired", time.Now().Add(-time.Minute), true},
		// Only credentials written before the provider recorded a fallback TTL
		// land here. Treating them as due is what heals them: one refresh
		// records an expiry and they never take this branch again.
		{"no recorded expiry", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexNeedsRefresh(&provider.Credential{ExpiresAt: tt.in}); got != tt.want {
				t.Fatalf("codexNeedsRefresh = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateCredentialRefs(t *testing.T) {
	tests := []struct {
		name    string
		refs    []string
		grants  []string
		wantErr bool
	}{
		{"declared and supported", []string{"codex"}, []string{"codex", "github"}, false},
		{"no refs at all", nil, []string{"github"}, false},
		// The daemon reads a secret out of the store on the caller's behalf, so
		// a ref for a grant the run never declared must be refused.
		{"undeclared grant", []string{"codex"}, []string{"github"}, true},
		// Every other credential travels as a value the caller already held;
		// resolving one by name would let a caller obtain it without having it.
		{"grant that does not use refs", []string{"github"}, []string{"github"}, true},
		{"unknown provider", []string{"not-a-provider"}, []string{"not-a-provider"}, true},
		{"one good ref does not excuse a bad one", []string{"codex", "claude"}, []string{"codex", "claude"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCredentialRefs(tt.refs, tt.grants)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateCredentialRefs(%v, %v) = %v, wantErr %v", tt.refs, tt.grants, err, tt.wantErr)
			}
		})
	}
}

// Bundles hold real secrets and must never reach the persisted run registry or
// a registration payload, so they are the one thing the daemon re-resolves from
// the store itself. CredentialBundleGrants is how a caller says which.
func TestCredentialBundleGrants(t *testing.T) {
	rc := NewRunContext("run-1")
	if got := rc.CredentialBundleGrants(); len(got) != 0 {
		t.Fatalf("CredentialBundleGrants on a fresh context = %v, want none", got)
	}

	rc.SetCredentialBundle("chatgpt.com", bundleWithGrant("codex-subscription-v1", "codex"))
	rc.SetCredentialBundle("chatgpt.com", bundleWithGrant("codex-subscription-v1", "codex"))
	if got := rc.CredentialBundleGrants(); len(got) != 1 || got[0] != "codex" {
		t.Fatalf("CredentialBundleGrants = %v, want [codex] with no duplicate", got)
	}

	// An upsert of the same bundle id replaces rather than appends, so a
	// refresh must not grow the list.
	rc.SetCredentialBundle("other.example", bundleWithGrant("other-v1", "other"))
	if got := rc.CredentialBundleGrants(); len(got) != 2 || got[0] != "codex" || got[1] != "other" {
		t.Fatalf("CredentialBundleGrants = %v, want a stable sorted [codex other]", got)
	}
}

func bundleWithGrant(id, grant string) credential.Bundle {
	return credential.Bundle{
		ID: id, Grant: grant,
		Replacements: []credential.HeaderReplacement{{Name: "Authorization", Placeholder: "p", Value: "v"}},
	}
}
