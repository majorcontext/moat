package daemon

import (
	"testing"

	"github.com/majorcontext/moat/internal/credential"
)

func TestRunContext_ImplementsProxyConfigurer(t *testing.T) {
	var _ credential.ProxyConfigurer = (*RunContext)(nil)
}

func TestRunContext_SetCredential(t *testing.T) {
	rc := NewRunContext("run_1")
	rc.SetCredential("api.github.com", "token ghp_abc")

	cred, ok := rc.GetCredential("api.github.com")
	if !ok {
		t.Fatal("expected credential for api.github.com")
	}
	if cred.Name != "Authorization" {
		t.Errorf("expected header Authorization, got %s", cred.Name)
	}
	if cred.Value != "token ghp_abc" {
		t.Errorf("expected value 'token ghp_abc', got %s", cred.Value)
	}
}

func TestRunContext_SetCredentialHeader(t *testing.T) {
	rc := NewRunContext("run_1")
	rc.SetCredentialHeader("api.anthropic.com", "x-api-key", "sk-ant-123")

	cred, ok := rc.GetCredential("api.anthropic.com")
	if !ok {
		t.Fatal("expected credential")
	}
	if cred.Name != "x-api-key" || cred.Value != "sk-ant-123" {
		t.Errorf("unexpected credential: %+v", cred)
	}
}

func TestRunContext_AddExtraHeader(t *testing.T) {
	rc := NewRunContext("run_1")
	rc.AddExtraHeader("api.anthropic.com", "anthropic-beta", "flag1")
	rc.AddExtraHeader("api.anthropic.com", "anthropic-version", "2023-06-01")

	headers := rc.GetExtraHeaders("api.anthropic.com")
	if len(headers) != 2 {
		t.Fatalf("expected 2 extra headers, got %d", len(headers))
	}
}

func TestRunContext_GetCredentialWithPort(t *testing.T) {
	rc := NewRunContext("run_1")
	rc.SetCredential("api.github.com", "token abc")

	// Should match "api.github.com:443" -> "api.github.com"
	cred, ok := rc.GetCredential("api.github.com:443")
	if !ok {
		t.Fatal("expected credential for host:port lookup")
	}
	if cred.Value != "token abc" {
		t.Errorf("unexpected value: %s", cred.Value)
	}
}
