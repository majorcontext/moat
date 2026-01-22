package credential

import (
	"testing"

	"github.com/andybons/moat/internal/container"
)

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

func TestParseGrantProvider(t *testing.T) {
	tests := []struct {
		grant string
		want  Provider
	}{
		{"github", ProviderGitHub},
		{"github:repo", ProviderGitHub},
		{"github:repo,user", ProviderGitHub},
		{"aws", ProviderAWS},
		{"aws:s3", ProviderAWS},
		{"anthropic", ProviderAnthropic},
		{"unknown", Provider("unknown")},
		{"", Provider("")},
	}

	for _, tt := range tests {
		t.Run(tt.grant, func(t *testing.T) {
			if got := ParseGrantProvider(tt.grant); got != tt.want {
				t.Errorf("ParseGrantProvider(%q) = %v, want %v", tt.grant, got, tt.want)
			}
		})
	}
}

func TestImpliedDependencies(t *testing.T) {
	tests := []struct {
		name   string
		grants []string
		want   []string
	}{
		{
			name:   "github grant implies gh and git",
			grants: []string{"github"},
			want:   []string{"gh", "git"},
		},
		{
			name:   "github with scope implies gh and git",
			grants: []string{"github:repo"},
			want:   []string{"gh", "git"},
		},
		{
			name:   "aws grant implies aws",
			grants: []string{"aws"},
			want:   []string{"aws"},
		},
		{
			name:   "anthropic grant implies nothing",
			grants: []string{"anthropic"},
			want:   nil,
		},
		{
			name:   "multiple grants",
			grants: []string{"github", "aws"},
			want:   []string{"gh", "git", "aws"},
		},
		{
			name:   "empty grants",
			grants: []string{},
			want:   nil,
		},
		{
			name:   "unknown grant implies nothing",
			grants: []string{"unknown"},
			want:   nil,
		},
		{
			name:   "no duplicates",
			grants: []string{"github", "github:repo"},
			want:   []string{"gh", "git"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ImpliedDependencies(tt.grants)
			if len(got) != len(tt.want) {
				t.Errorf("ImpliedDependencies() = %v, want %v", got, tt.want)
				return
			}
			for i, dep := range got {
				if dep != tt.want[i] {
					t.Errorf("ImpliedDependencies()[%d] = %q, want %q", i, dep, tt.want[i])
				}
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
