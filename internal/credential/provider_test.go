package credential

import (
	"testing"

	"github.com/andybons/moat/internal/container"
)

func TestGitHubSetup_ConfigureProxy(t *testing.T) {
	setup := &GitHubSetup{}
	if setup.Provider() != ProviderGitHub {
		t.Errorf("Provider() = %v, want %v", setup.Provider(), ProviderGitHub)
	}

	// Test that ConfigureProxy sets the correct headers
	mockProxy := &mockProxyConfigurer{credentials: make(map[string]string)}
	cred := &Credential{Token: "test-token"}

	setup.ConfigureProxy(mockProxy, cred)

	if mockProxy.credentials["api.github.com"] != "Bearer test-token" {
		t.Errorf("api.github.com credential = %q, want %q", mockProxy.credentials["api.github.com"], "Bearer test-token")
	}
	if mockProxy.credentials["github.com"] != "Bearer test-token" {
		t.Errorf("github.com credential = %q, want %q", mockProxy.credentials["github.com"], "Bearer test-token")
	}
}

func TestGitHubSetup_ContainerEnv(t *testing.T) {
	setup := &GitHubSetup{}
	cred := &Credential{Token: "test-token"}

	env := setup.ContainerEnv(cred)
	if len(env) != 0 {
		t.Errorf("ContainerEnv() returned %d vars, want 0", len(env))
	}
}

func TestGitHubSetup_ContainerMounts(t *testing.T) {
	setup := &GitHubSetup{}
	cred := &Credential{Token: "test-token"}

	mounts, cleanupPath, err := setup.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Errorf("ContainerMounts() error = %v", err)
	}
	if len(mounts) != 0 {
		t.Errorf("ContainerMounts() returned %d mounts, want 0", len(mounts))
	}
	if cleanupPath != "" {
		t.Errorf("ContainerMounts() cleanupPath = %q, want empty", cleanupPath)
	}
}

func TestGetProviderSetup(t *testing.T) {
	// Test built-in GitHub provider
	githubSetup := GetProviderSetup(ProviderGitHub)
	if githubSetup == nil {
		t.Error("GetProviderSetup(ProviderGitHub) returned nil")
	}
	if githubSetup.Provider() != ProviderGitHub {
		t.Errorf("Provider() = %v, want %v", githubSetup.Provider(), ProviderGitHub)
	}

	// Test unknown provider returns nil
	unknownSetup := GetProviderSetup(Provider("unknown"))
	if unknownSetup != nil {
		t.Error("GetProviderSetup(unknown) should return nil")
	}
}

func TestRegisterProviderSetup(t *testing.T) {
	// Register a custom provider
	customProvider := Provider("custom")
	customSetup := &mockProviderSetup{provider: customProvider}

	RegisterProviderSetup(customProvider, customSetup)

	// Verify it can be retrieved
	setup := GetProviderSetup(customProvider)
	if setup == nil {
		t.Error("GetProviderSetup(custom) returned nil after registration")
	}
	if setup.Provider() != customProvider {
		t.Errorf("Provider() = %v, want %v", setup.Provider(), customProvider)
	}

	// Clean up
	delete(providerSetups, customProvider)
}

func TestIsOAuthToken(t *testing.T) {
	tests := []struct {
		token string
		want  bool
	}{
		{"sk-ant-oat01-abc123", true},
		{"sk-ant-oat02-xyz789", true},
		{"sk-ant-api01-abc123", false},
		{"some-other-token", false},
		{"sk-ant-oa", false}, // Too short
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.token, func(t *testing.T) {
			if got := IsOAuthToken(tt.token); got != tt.want {
				t.Errorf("IsOAuthToken(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}
}

// mockProxyConfigurer implements ProxyConfigurer for testing.
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
	if m.extraHeaders == nil {
		m.extraHeaders = make(map[string]map[string]string)
	}
	if m.extraHeaders[host] == nil {
		m.extraHeaders[host] = make(map[string]string)
	}
	m.extraHeaders[host][headerName] = headerValue
}

// mockProviderSetup implements ProviderSetup for testing.
type mockProviderSetup struct {
	provider Provider
}

func (m *mockProviderSetup) Provider() Provider {
	return m.provider
}

func (m *mockProviderSetup) ConfigureProxy(p ProxyConfigurer, cred *Credential) {}

func (m *mockProviderSetup) ContainerEnv(cred *Credential) []string {
	return nil
}

func (m *mockProviderSetup) ContainerMounts(cred *Credential, containerHome string) ([]container.MountConfig, string, error) {
	return nil, "", nil
}

func (m *mockProviderSetup) Cleanup(cleanupPath string) {}
