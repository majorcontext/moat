package configprovider

import (
	"testing"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

func TestConfigProviderName(t *testing.T) {
	cp := NewConfigProvider(ProviderDef{Name: "test-provider"}, "builtin")
	if cp.Name() != "test-provider" {
		t.Errorf("Name() = %q, want %q", cp.Name(), "test-provider")
	}
}

func TestConfigProviderDescription(t *testing.T) {
	cp := NewConfigProvider(ProviderDef{
		Name:        "test",
		Description: "Test provider",
	}, "custom")
	if cp.Description() != "Test provider" {
		t.Errorf("Description() = %q, want %q", cp.Description(), "Test provider")
	}
}

func TestConfigProviderSource(t *testing.T) {
	tests := []struct {
		source string
	}{
		{"builtin"},
		{"custom"},
	}
	for _, tt := range tests {
		cp := NewConfigProvider(ProviderDef{Name: "test"}, tt.source)
		if cp.Source() != tt.source {
			t.Errorf("Source() = %q, want %q", cp.Source(), tt.source)
		}
	}
}

type mockProxy struct {
	calls []proxyCall
}

type proxyCall struct {
	host, headerName, headerValue, grant string
}

func (m *mockProxy) SetCredential(host, value string)                                     {}
func (m *mockProxy) SetCredentialHeader(host, headerName, headerValue string)             {}
func (m *mockProxy) AddExtraHeader(host, headerName, headerValue string)                  {}
func (m *mockProxy) AddResponseTransformer(host string, t credential.ResponseTransformer) {}
func (m *mockProxy) RemoveRequestHeader(host, headerName string)                          {}
func (m *mockProxy) SetTokenSubstitution(host, placeholder, realToken string)             {}

func (m *mockProxy) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	m.calls = append(m.calls, proxyCall{host, headerName, headerValue, grant})
}

func TestConfigureProxy(t *testing.T) {
	cp := NewConfigProvider(ProviderDef{
		Name: "gitlab",
		Hosts: []string{
			"gitlab.com",
			"*.gitlab.com",
		},
		Inject: InjectConfig{
			Header: "PRIVATE-TOKEN",
		},
	}, "builtin")

	mock := &mockProxy{}
	cred := &provider.Credential{Token: "test-token"}
	cp.ConfigureProxy(mock, cred)

	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 proxy calls, got %d", len(mock.calls))
	}

	// First host
	if mock.calls[0].host != "gitlab.com" {
		t.Errorf("call[0].host = %q, want %q", mock.calls[0].host, "gitlab.com")
	}
	if mock.calls[0].headerName != "PRIVATE-TOKEN" {
		t.Errorf("call[0].headerName = %q, want %q", mock.calls[0].headerName, "PRIVATE-TOKEN")
	}
	if mock.calls[0].headerValue != "test-token" {
		t.Errorf("call[0].headerValue = %q, want %q", mock.calls[0].headerValue, "test-token")
	}
	if mock.calls[0].grant != "gitlab" {
		t.Errorf("call[0].grant = %q, want %q", mock.calls[0].grant, "gitlab")
	}

	// Second host
	if mock.calls[1].host != "*.gitlab.com" {
		t.Errorf("call[1].host = %q, want %q", mock.calls[1].host, "*.gitlab.com")
	}
}

func TestConfigureProxyWithPrefix(t *testing.T) {
	cp := NewConfigProvider(ProviderDef{
		Name:  "vercel",
		Hosts: []string{"api.vercel.com"},
		Inject: InjectConfig{
			Header: "Authorization",
			Prefix: "Bearer ",
		},
	}, "builtin")

	mock := &mockProxy{}
	cred := &provider.Credential{Token: "my-token"}
	cp.ConfigureProxy(mock, cred)

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 proxy call, got %d", len(mock.calls))
	}
	if mock.calls[0].headerValue != "Bearer my-token" {
		t.Errorf("headerValue = %q, want %q", mock.calls[0].headerValue, "Bearer my-token")
	}
}

func TestContainerEnv(t *testing.T) {
	tests := []struct {
		name         string
		containerEnv string
		want         []string
	}{
		{
			name:         "with container_env",
			containerEnv: "GITLAB_TOKEN",
			want:         []string{"GITLAB_TOKEN=" + credential.ProxyInjectedPlaceholder},
		},
		{
			name:         "no container_env",
			containerEnv: "",
			want:         nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := NewConfigProvider(ProviderDef{
				Name:         "test",
				ContainerEnv: tt.containerEnv,
			}, "builtin")
			got := cp.ContainerEnv(&provider.Credential{Token: "tok"})
			if len(got) != len(tt.want) {
				t.Fatalf("ContainerEnv() returned %d items, want %d", len(got), len(tt.want))
			}
			for i, v := range got {
				if v != tt.want[i] {
					t.Errorf("ContainerEnv()[%d] = %q, want %q", i, v, tt.want[i])
				}
			}
		})
	}
}

func TestContainerMountsReturnsNil(t *testing.T) {
	cp := NewConfigProvider(ProviderDef{Name: "test"}, "builtin")
	mounts, cleanup, err := cp.ContainerMounts(&provider.Credential{}, "/home/user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mounts != nil {
		t.Error("expected nil mounts")
	}
	if cleanup != "" {
		t.Error("expected empty cleanup path")
	}
}

func TestImpliedDependenciesReturnsNil(t *testing.T) {
	cp := NewConfigProvider(ProviderDef{Name: "test"}, "builtin")
	if deps := cp.ImpliedDependencies(); deps != nil {
		t.Errorf("ImpliedDependencies() = %v, want nil", deps)
	}
}

func TestInterfaceCompliance(t *testing.T) {
	var _ provider.CredentialProvider = (*ConfigProvider)(nil)
	var _ provider.DescribableProvider = (*ConfigProvider)(nil)
}
