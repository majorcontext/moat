package graphite

import (
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

// mockProxyConfigurer implements provider.ProxyConfigurer for testing.
type mockProxyConfigurer struct {
	credentials map[string]string
}

func newMockProxy() *mockProxyConfigurer {
	return &mockProxyConfigurer{credentials: make(map[string]string)}
}

func (m *mockProxyConfigurer) SetCredential(host, value string) {
	m.credentials[host] = value
}

func (m *mockProxyConfigurer) SetCredentialHeader(host, headerName, headerValue string) {
	m.credentials[host] = headerName + ": " + headerValue
}

func (m *mockProxyConfigurer) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	m.credentials[host] = headerName + ": " + headerValue
}

func (m *mockProxyConfigurer) AddExtraHeader(host, headerName, headerValue string) {}

func (m *mockProxyConfigurer) AddResponseTransformer(host string, transformer provider.ResponseTransformer) {
}

func (m *mockProxyConfigurer) RemoveRequestHeader(host, header string) {}

func (m *mockProxyConfigurer) SetTokenSubstitution(host, placeholder, realToken string) {}

func TestProvider_ConfigureProxy(t *testing.T) {
	p := &Provider{}
	proxy := newMockProxy()
	cred := &provider.Credential{Token: "test-token"}

	p.ConfigureProxy(proxy, cred)

	want := "Authorization: token test-token"
	if proxy.credentials["api.graphite.com"] != want {
		t.Errorf("api.graphite.com credential = %q, want %q", proxy.credentials["api.graphite.com"], want)
	}
}

func TestProvider_ContainerInitFiles(t *testing.T) {
	p := &Provider{}
	cred := &provider.Credential{Token: "test-token"}

	files := p.ContainerInitFiles(cred, "/home/user")
	if len(files) != 1 {
		t.Fatalf("ContainerInitFiles() returned %d files, want 1", len(files))
	}

	wantPath := "/home/user/.config/graphite/user_config"
	content, ok := files[wantPath]
	if !ok {
		t.Fatalf("ContainerInitFiles() missing key %q", wantPath)
	}

	wantContent := `{"authToken":"moat-proxy-injected"}`
	if content != wantContent {
		t.Errorf("content = %q, want %q", content, wantContent)
	}
}

func TestProvider_ImpliedDependencies(t *testing.T) {
	p := &Provider{}
	deps := p.ImpliedDependencies()
	want := []string{"graphite-cli", "node", "git"}
	if len(deps) != len(want) {
		t.Fatalf("ImpliedDependencies() returned %d deps, want %d", len(deps), len(want))
	}
	for i, w := range want {
		if deps[i] != w {
			t.Errorf("ImpliedDependencies()[%d] = %q, want %q", i, deps[i], w)
		}
	}
}

func TestProvider_Grant_EnvVar(t *testing.T) {
	t.Setenv("GRAPHITE_TOKEN", "test-graphite-token")

	p := &Provider{}
	_, err := p.Grant(t.Context())
	if err == nil {
		t.Skip("unexpected success - may be hitting real API")
	}
	// Error is expected since we can't reach api.graphite.com in tests
}
