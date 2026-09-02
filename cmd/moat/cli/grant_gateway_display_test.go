package cli

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
)

// TestCredTypeGateway covers the grant-list display: a gateway key is not
// interchangeable with an Anthropic key, so two rows that behave very
// differently must not look identical.
func TestCredTypeGateway(t *testing.T) {
	gateway := credential.Credential{
		Provider: credential.ProviderAnthropic,
		Token:    "lr_key",
		Metadata: map[string]string{credential.MetaKeyBaseURL: "https://gw.lunaroute.com"},
	}
	if got := credType(gateway); got != "api-key (gateway)" {
		t.Errorf("credType(gateway key) = %q, want %q", got, "api-key (gateway)")
	}

	// Companion cases: a plain Anthropic key, and one whose metadata exists but
	// carries no endpoint, both stay "api-key".
	plain := credential.Credential{Provider: credential.ProviderAnthropic, Token: "sk-ant-api03-key"}
	if got := credType(plain); got != "api-key" {
		t.Errorf("credType(plain key) = %q, want %q", got, "api-key")
	}

	emptyMeta := credential.Credential{
		Provider: credential.ProviderAnthropic,
		Token:    "sk-ant-api03-key",
		Metadata: map[string]string{credential.MetaKeyBaseURL: ""},
	}
	if got := credType(emptyMeta); got != "api-key" {
		t.Errorf("credType(empty base_url) = %q, want %q", got, "api-key")
	}
}

// TestShowProviderMetadataGatewayEndpoint covers the grant-show display: the
// endpoint is the whole difference between this credential and a plain
// Anthropic key, so it has to be visible.
func TestShowProviderMetadataGatewayEndpoint(t *testing.T) {
	out := captureStdout(t, func() {
		showProviderMetadata(&credential.Credential{
			Provider: credential.ProviderAnthropic,
			Token:    "lr_key",
			Metadata: map[string]string{credential.MetaKeyBaseURL: "https://gw.lunaroute.com"},
		})
	})
	if !strings.Contains(out, "https://gw.lunaroute.com") {
		t.Errorf("output %q does not show the endpoint", out)
	}
	if !strings.Contains(out, "Endpoint:") {
		t.Errorf("output %q does not label the endpoint", out)
	}

	// Companion case: a plain Anthropic key has no endpoint to show, and must
	// not print an empty label.
	plainOut := captureStdout(t, func() {
		showProviderMetadata(&credential.Credential{
			Provider: credential.ProviderAnthropic,
			Token:    "sk-ant-api03-key",
		})
	})
	if strings.Contains(plainOut, "Endpoint:") {
		t.Errorf("output %q shows an Endpoint label for a plain key", plainOut)
	}
}
