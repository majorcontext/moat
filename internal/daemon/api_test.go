package daemon

import (
	"encoding/json"
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestRegisterRequest_JSON(t *testing.T) {
	req := RegisterRequest{
		RunID: "run_abc123",
		Credentials: []CredentialSpec{
			{Host: "api.github.com", Header: "Authorization", Value: "token ghp_abc", Grant: "github"},
			{Host: "api.anthropic.com", Header: "x-api-key", Value: "sk-ant-123"},
		},
		ExtraHeaders: []ExtraHeaderSpec{
			{Host: "api.anthropic.com", HeaderName: "anthropic-version", Value: "2023-06-01"},
		},
		RemoveHeaders: []RemoveHeaderSpec{
			{Host: "api.github.com", HeaderName: "x-forwarded-for"},
		},
		TokenSubstitutions: []TokenSubstitutionSpec{
			{Host: "api.example.com", Placeholder: "PLACEHOLDER", RealToken: "real-secret"},
		},
		MCPServers: []config.MCPServerConfig{
			{Name: "mcp-github", URL: "https://mcp.github.com"},
		},
		NetworkPolicy: "allowlist",
		NetworkAllow:  []string{"api.github.com", "api.anthropic.com"},
		Grants:        []string{"github", "anthropic"},
		AWSConfig: &AWSConfig{
			RoleARN: "arn:aws:iam::123456789012:role/test",
			Region:  "us-east-1",
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded RegisterRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify fields survived round-trip.
	if decoded.RunID != req.RunID {
		t.Errorf("RunID: got %q, want %q", decoded.RunID, req.RunID)
	}
	if len(decoded.Credentials) != 2 {
		t.Fatalf("Credentials: got %d, want 2", len(decoded.Credentials))
	}
	if decoded.Credentials[0].Host != "api.github.com" {
		t.Errorf("Credentials[0].Host: got %q, want %q", decoded.Credentials[0].Host, "api.github.com")
	}
	if decoded.Credentials[0].Grant != "github" {
		t.Errorf("Credentials[0].Grant: got %q, want %q", decoded.Credentials[0].Grant, "github")
	}
	if decoded.Credentials[1].Grant != "" {
		t.Errorf("Credentials[1].Grant: got %q, want empty", decoded.Credentials[1].Grant)
	}
	if len(decoded.ExtraHeaders) != 1 {
		t.Fatalf("ExtraHeaders: got %d, want 1", len(decoded.ExtraHeaders))
	}
	if decoded.ExtraHeaders[0].HeaderName != "anthropic-version" {
		t.Errorf("ExtraHeaders[0].HeaderName: got %q, want %q", decoded.ExtraHeaders[0].HeaderName, "anthropic-version")
	}
	if len(decoded.RemoveHeaders) != 1 {
		t.Fatalf("RemoveHeaders: got %d, want 1", len(decoded.RemoveHeaders))
	}
	if decoded.RemoveHeaders[0].HeaderName != "x-forwarded-for" {
		t.Errorf("RemoveHeaders[0].HeaderName: got %q, want %q", decoded.RemoveHeaders[0].HeaderName, "x-forwarded-for")
	}
	if len(decoded.TokenSubstitutions) != 1 {
		t.Fatalf("TokenSubstitutions: got %d, want 1", len(decoded.TokenSubstitutions))
	}
	if decoded.TokenSubstitutions[0].RealToken != "real-secret" {
		t.Errorf("TokenSubstitutions[0].RealToken: got %q, want %q", decoded.TokenSubstitutions[0].RealToken, "real-secret")
	}
	if len(decoded.MCPServers) != 1 {
		t.Fatalf("MCPServers: got %d, want 1", len(decoded.MCPServers))
	}
	if decoded.MCPServers[0].Name != "mcp-github" {
		t.Errorf("MCPServers[0].Name: got %q, want %q", decoded.MCPServers[0].Name, "mcp-github")
	}
	if decoded.NetworkPolicy != "allowlist" {
		t.Errorf("NetworkPolicy: got %q, want %q", decoded.NetworkPolicy, "allowlist")
	}
	if len(decoded.NetworkAllow) != 2 {
		t.Fatalf("NetworkAllow: got %d, want 2", len(decoded.NetworkAllow))
	}
	if len(decoded.Grants) != 2 {
		t.Fatalf("Grants: got %d, want 2", len(decoded.Grants))
	}
	if decoded.AWSConfig == nil {
		t.Fatal("AWSConfig: got nil, want non-nil")
	}
	if decoded.AWSConfig.RoleARN != "arn:aws:iam::123456789012:role/test" {
		t.Errorf("AWSConfig.RoleARN: got %q, want %q", decoded.AWSConfig.RoleARN, "arn:aws:iam::123456789012:role/test")
	}
}

func TestRegisterRequest_JSON_OmitsEmpty(t *testing.T) {
	req := RegisterRequest{
		RunID: "run_minimal",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Verify omitempty fields are not present.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	if _, ok := raw["run_id"]; !ok {
		t.Error("expected run_id in JSON")
	}
	for _, field := range []string{"credentials", "extra_headers", "remove_headers", "token_substitutions", "mcp_servers", "network_policy", "network_allow", "grants", "aws_config"} {
		if _, ok := raw[field]; ok {
			t.Errorf("expected field %q to be omitted when empty", field)
		}
	}
}

func TestRegisterResponse_JSON(t *testing.T) {
	resp := RegisterResponse{
		AuthToken: "tok_abc123def456",
		ProxyPort: 8443,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded RegisterResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.AuthToken != resp.AuthToken {
		t.Errorf("AuthToken: got %q, want %q", decoded.AuthToken, resp.AuthToken)
	}
	if decoded.ProxyPort != resp.ProxyPort {
		t.Errorf("ProxyPort: got %d, want %d", decoded.ProxyPort, resp.ProxyPort)
	}
}

func TestToRunContext(t *testing.T) {
	req := RegisterRequest{
		RunID: "run_test_42",
		Credentials: []CredentialSpec{
			{Host: "api.github.com", Header: "Authorization", Value: "token ghp_xyz", Grant: "github"},
			{Host: "api.anthropic.com", Header: "x-api-key", Value: "sk-ant-456"},
		},
		ExtraHeaders: []ExtraHeaderSpec{
			{Host: "api.anthropic.com", HeaderName: "anthropic-version", Value: "2023-06-01"},
			{Host: "api.anthropic.com", HeaderName: "anthropic-beta", Value: "messages-2024-01"},
		},
		RemoveHeaders: []RemoveHeaderSpec{
			{Host: "api.github.com", HeaderName: "x-forwarded-for"},
		},
		TokenSubstitutions: []TokenSubstitutionSpec{
			{Host: "api.example.com", Placeholder: "PLACEHOLDER_TOKEN", RealToken: "real-secret-token"},
		},
		MCPServers: []config.MCPServerConfig{
			{Name: "mcp-test", URL: "https://mcp.example.com"},
		},
		NetworkPolicy: "allowlist",
		NetworkAllow:  []string{"api.github.com"},
		AWSConfig: &AWSConfig{
			RoleARN: "arn:aws:iam::123456789012:role/test",
			Region:  "us-west-2",
		},
	}

	rc := req.ToRunContext()

	// Verify RunID.
	if rc.RunID != "run_test_42" {
		t.Errorf("RunID: got %q, want %q", rc.RunID, "run_test_42")
	}

	// Verify credentials.
	cred, ok := rc.GetCredential("api.github.com")
	if !ok {
		t.Fatal("expected credential for api.github.com")
	}
	if cred.Name != "Authorization" || cred.Value != "token ghp_xyz" || cred.Grant != "github" {
		t.Errorf("unexpected github credential: %+v", cred)
	}

	cred, ok = rc.GetCredential("api.anthropic.com")
	if !ok {
		t.Fatal("expected credential for api.anthropic.com")
	}
	if cred.Name != "x-api-key" || cred.Value != "sk-ant-456" {
		t.Errorf("unexpected anthropic credential: %+v", cred)
	}
	if cred.Grant != "" {
		t.Errorf("expected empty grant for anthropic, got %q", cred.Grant)
	}

	// Verify extra headers.
	headers := rc.GetExtraHeaders("api.anthropic.com")
	if len(headers) != 2 {
		t.Fatalf("expected 2 extra headers for anthropic, got %d", len(headers))
	}
	if headers[0].Name != "anthropic-version" || headers[0].Value != "2023-06-01" {
		t.Errorf("unexpected extra header[0]: %+v", headers[0])
	}
	if headers[1].Name != "anthropic-beta" || headers[1].Value != "messages-2024-01" {
		t.Errorf("unexpected extra header[1]: %+v", headers[1])
	}

	// Verify remove headers.
	removeHeaders := rc.GetRemoveHeaders("api.github.com")
	if len(removeHeaders) != 1 || removeHeaders[0] != "x-forwarded-for" {
		t.Errorf("unexpected remove headers: %v", removeHeaders)
	}

	// Verify token substitutions.
	sub, ok := rc.GetTokenSubstitution("api.example.com")
	if !ok {
		t.Fatal("expected token substitution for api.example.com")
	}
	if sub.Placeholder != "PLACEHOLDER_TOKEN" || sub.RealToken != "real-secret-token" {
		t.Errorf("unexpected token substitution: %+v", sub)
	}

	// Verify MCP servers.
	if len(rc.MCPServers) != 1 || rc.MCPServers[0].Name != "mcp-test" {
		t.Errorf("unexpected MCP servers: %+v", rc.MCPServers)
	}

	// Verify network policy.
	if rc.NetworkPolicy != "allowlist" {
		t.Errorf("NetworkPolicy: got %q, want %q", rc.NetworkPolicy, "allowlist")
	}
	if len(rc.NetworkAllow) != 1 || rc.NetworkAllow[0] != "api.github.com" {
		t.Errorf("unexpected NetworkAllow: %v", rc.NetworkAllow)
	}

	// Verify AWS config.
	if rc.AWSConfig == nil {
		t.Fatal("expected non-nil AWSConfig")
	}
	if rc.AWSConfig.RoleARN != "arn:aws:iam::123456789012:role/test" {
		t.Errorf("AWSConfig.RoleARN: got %q, want %q", rc.AWSConfig.RoleARN, "arn:aws:iam::123456789012:role/test")
	}
	if rc.AWSConfig.Region != "us-west-2" {
		t.Errorf("AWSConfig.Region: got %q, want %q", rc.AWSConfig.Region, "us-west-2")
	}
}
