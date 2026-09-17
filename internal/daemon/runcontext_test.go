package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
)

func TestRunContext_ToProxyContextData_HostGateway(t *testing.T) {
	rc := NewRunContext("run_host_test")
	rc.HostGateway = "host.docker.internal"
	rc.AllowedHostPorts = []int{8288, 5432}

	d := rc.ToProxyContextData()

	if d.HostGateway != "host.docker.internal" {
		t.Errorf("HostGateway = %q, want %q", d.HostGateway, "host.docker.internal")
	}
	if len(d.AllowedHostPorts) != 2 || d.AllowedHostPorts[0] != 8288 || d.AllowedHostPorts[1] != 5432 {
		t.Errorf("AllowedHostPorts = %v, want [8288 5432]", d.AllowedHostPorts)
	}
}

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

func TestRunContext_SetCredentialBundleConvertsAtomically(t *testing.T) {
	rc := NewRunContext("run-bundle")
	rc.SetCredentialBundle("chatgpt.com", credential.Bundle{
		ID: "codex-subscription-v1", Grant: "codex", RequireAll: true,
		Scope: credential.Scope{RequireTLS: true, Origins: []string{"https://chatgpt.com"}, Methods: []string{"POST"}, PathPrefixes: []string{"/backend-api/codex"}},
		Replacements: []credential.HeaderReplacement{
			{Name: "Authorization", Placeholder: "fake", Value: "real"},
			{Name: "ChatGPT-Account-ID", Placeholder: "fake-account", Value: "real-account"},
		},
	})

	data := rc.ToProxyContextData()
	bundles := data.CredentialBundles
	if len(bundles) != 1 || len(bundles[0].Replacements) != 2 || !bundles[0].RequireAll {
		t.Fatalf("unexpected proxy bundle: %+v", bundles)
	}
	serialized, err := json.Marshal(rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "real-account") || strings.Contains(string(serialized), `"credential_bundles"`) {
		t.Fatalf("RunContext serialization exposed credential bundle: %s", serialized)
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
