package credential

import (
	"testing"
)

func TestGitHubDeviceAuth_Scopes(t *testing.T) {
	auth := &GitHubDeviceAuth{
		ClientID: "test-client-id",
		Scopes:   []string{"repo", "read:user"},
	}

	if auth.ClientID != "test-client-id" {
		t.Errorf("ClientID = %q, want %q", auth.ClientID, "test-client-id")
	}
	if len(auth.Scopes) != 2 {
		t.Errorf("Scopes length = %d, want 2", len(auth.Scopes))
	}
}
