package run

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/daemon"
)

func TestBuildRegisterRequest_CodexUsesSecretFreeCredentialRef(t *testing.T) {
	rc := daemon.NewRunContext("run-codex")
	rc.SetCredentialBundle("chatgpt.com", credential.CredentialBundle{
		ID: "codex-subscription-v1",
		Replacements: []credential.HeaderReplacement{
			{Name: "Authorization", Placeholder: "fake", Value: "sentinel-real-access-token"},
			{Name: "ChatGPT-Account-ID", Placeholder: "fake", Value: "sentinel-real-account-id"},
		},
	})

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
