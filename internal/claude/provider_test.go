package claude

import (
	"testing"
	"time"

	"github.com/andybons/moat/internal/credential"
)

func TestAnthropicSetup_Provider(t *testing.T) {
	setup := &AnthropicSetup{}
	if setup.Provider() != credential.ProviderAnthropic {
		t.Errorf("Provider() = %v, want %v", setup.Provider(), credential.ProviderAnthropic)
	}
}

func TestAnthropicSetup_ConfigureProxy_OAuth(t *testing.T) {
	setup := &AnthropicSetup{}
	mockProxy := &mockProxyConfigurer{
		credentials:  make(map[string]string),
		extraHeaders: make(map[string]map[string]string),
	}
	cred := &credential.Credential{Token: "sk-ant-oat01-abc123"}

	setup.ConfigureProxy(mockProxy, cred)

	// OAuth tokens use Bearer auth
	if mockProxy.credentials["api.anthropic.com"] != "Bearer sk-ant-oat01-abc123" {
		t.Errorf("api.anthropic.com credential = %q, want %q", mockProxy.credentials["api.anthropic.com"], "Bearer sk-ant-oat01-abc123")
	}

	// Should also set anthropic-beta header
	if mockProxy.extraHeaders["api.anthropic.com"]["anthropic-beta"] != OAuthBetaHeader {
		t.Errorf("anthropic-beta header = %q, want %q", mockProxy.extraHeaders["api.anthropic.com"]["anthropic-beta"], OAuthBetaHeader)
	}
}

func TestAnthropicSetup_ConfigureProxy_APIKey(t *testing.T) {
	setup := &AnthropicSetup{}
	mockProxy := &mockProxyConfigurer{
		credentials:  make(map[string]string),
		extraHeaders: make(map[string]map[string]string),
	}
	cred := &credential.Credential{Token: "sk-ant-api01-abc123"}

	setup.ConfigureProxy(mockProxy, cred)

	// API keys use x-api-key header
	if mockProxy.credentials["api.anthropic.com"] != "x-api-key: sk-ant-api01-abc123" {
		t.Errorf("api.anthropic.com credential = %q, want %q", mockProxy.credentials["api.anthropic.com"], "x-api-key: sk-ant-api01-abc123")
	}

	// Should NOT set anthropic-beta header for API keys
	if len(mockProxy.extraHeaders["api.anthropic.com"]) != 0 {
		t.Errorf("extra headers should be empty for API keys, got %v", mockProxy.extraHeaders["api.anthropic.com"])
	}
}

func TestAnthropicSetup_ContainerEnv_OAuth(t *testing.T) {
	setup := &AnthropicSetup{}
	cred := &credential.Credential{Token: "sk-ant-oat01-abc123"}

	env := setup.ContainerEnv(cred)

	// OAuth should not set ANTHROPIC_API_KEY
	if len(env) != 0 {
		t.Errorf("ContainerEnv() for OAuth returned %d vars, want 0", len(env))
	}
}

func TestAnthropicSetup_ContainerEnv_APIKey(t *testing.T) {
	setup := &AnthropicSetup{}
	cred := &credential.Credential{Token: "sk-ant-api01-abc123"}

	env := setup.ContainerEnv(cred)

	// API key should set ANTHROPIC_API_KEY placeholder
	if len(env) != 1 {
		t.Errorf("ContainerEnv() for API key returned %d vars, want 1", len(env))
		return
	}
	if env[0] != "ANTHROPIC_API_KEY="+ProxyInjectedPlaceholder {
		t.Errorf("env[0] = %q, want %q", env[0], "ANTHROPIC_API_KEY="+ProxyInjectedPlaceholder)
	}
}

func TestAnthropicSetup_RegistrationViaInit(t *testing.T) {
	// The init() function should have registered AnthropicSetup
	setup := credential.GetProviderSetup(credential.ProviderAnthropic)
	if setup == nil {
		t.Fatal("GetProviderSetup(ProviderAnthropic) returned nil - init() may not have run")
	}
	if setup.Provider() != credential.ProviderAnthropic {
		t.Errorf("Provider() = %v, want %v", setup.Provider(), credential.ProviderAnthropic)
	}
}

func TestAnthropicSetup_ContainerMounts_APIKey(t *testing.T) {
	setup := &AnthropicSetup{}
	cred := &credential.Credential{Token: "sk-ant-api01-abc123"}

	mounts, cleanupPath, err := setup.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Errorf("ContainerMounts() error = %v", err)
	}
	if len(mounts) != 0 {
		t.Errorf("ContainerMounts() for API key returned %d mounts, want 0", len(mounts))
	}
	if cleanupPath != "" {
		t.Errorf("ContainerMounts() cleanupPath = %q, want empty", cleanupPath)
	}
}

func TestAnthropicSetup_ContainerMounts_OAuth(t *testing.T) {
	setup := &AnthropicSetup{}
	cred := &credential.Credential{
		Token:     "sk-ant-oat01-abc123",
		ExpiresAt: time.Now().Add(time.Hour),
		Scopes:    []string{"user:read"},
	}

	mounts, cleanupPath, err := setup.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Errorf("ContainerMounts() error = %v", err)
	}

	// Should have at least the credentials file mount
	if len(mounts) < 1 {
		t.Errorf("ContainerMounts() for OAuth returned %d mounts, want >= 1", len(mounts))
	}

	// Should have a cleanup path
	if cleanupPath == "" {
		t.Error("ContainerMounts() cleanupPath should not be empty for OAuth")
	}

	// Clean up
	if cleanupPath != "" {
		setup.Cleanup(cleanupPath)
	}
}

// mockProxyConfigurer implements credential.ProxyConfigurer for testing.
type mockProxyConfigurer struct {
	credentials  map[string]string
	extraHeaders map[string]map[string]string
}

func (m *mockProxyConfigurer) SetCredential(host, value string) {
	m.credentials[host] = value
}

func (m *mockProxyConfigurer) SetCredentialHeader(host, headerName, headerValue string) {
	m.credentials[host] = headerName + ": " + headerValue
}

func (m *mockProxyConfigurer) AddExtraHeader(host, headerName, headerValue string) {
	if m.extraHeaders[host] == nil {
		m.extraHeaders[host] = make(map[string]string)
	}
	m.extraHeaders[host][headerName] = headerValue
}
