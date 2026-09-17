package run

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/daemon"
)

func codexBundle() credential.Bundle {
	return credential.Bundle{
		ID:    string(credential.ProviderCodexSubscription),
		Grant: "codex",
		Replacements: []credential.HeaderReplacement{
			{Name: "Authorization", Placeholder: "fake", Value: "sentinel-real-access-token"},
			{Name: "ChatGPT-Account-ID", Placeholder: "fake", Value: "sentinel-real-account-id"},
		},
	}
}

func TestBuildRegisterRequest_CodexUsesSecretFreeCredentialRef(t *testing.T) {
	rc := daemon.NewRunContext("run-codex")
	rc.SetCredentialBundle("chatgpt.com", codexBundle())

	req := buildRegisterRequest(rc, []string{"codex"})
	if len(req.CredentialRefs) != 1 || req.CredentialRefs[0] != "codex" {
		t.Fatalf("CredentialRefs = %v, want [codex]", req.CredentialRefs)
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"sentinel-real-access-token", "sentinel-real-account-id"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("registration payload retained secret %q: %s", secret, data)
		}
	}
}

// Companion to the case above. A codex grant that fell back to an OpenAI API
// key installs no bundle, so it must not ask the daemon to resolve one: the ref
// is what forces the new daemon capabilities, and requiring them for an
// API-key-only user would make them restart their daemon for nothing.
func TestBuildRegisterRequest_CodexAPIKeyFallbackSendsNoCredentialRef(t *testing.T) {
	rc := daemon.NewRunContext("run-codex-apikey")
	rc.SetCredentialWithGrant("api.openai.com", "Authorization", "Bearer sk-real", "openai")

	req := buildRegisterRequest(rc, []string{"codex"})
	if len(req.CredentialRefs) != 0 {
		t.Fatalf("CredentialRefs = %v, want none for an API-key fallback run", req.CredentialRefs)
	}
	if len(req.Credentials) != 1 {
		t.Fatalf("Credentials = %v, want the API key to still register normally", req.Credentials)
	}
}
