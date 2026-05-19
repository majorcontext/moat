package kiro

import (
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

type mockProxyConfigurer struct {
	headers map[string]map[string]string
	grants  map[string]string
}

func newMockProxy() *mockProxyConfigurer {
	return &mockProxyConfigurer{headers: make(map[string]map[string]string), grants: make(map[string]string)}
}

func (m *mockProxyConfigurer) SetCredential(host, value string) {}
func (m *mockProxyConfigurer) SetCredentialHeader(host, h, v string) {
	if m.headers[host] == nil {
		m.headers[host] = map[string]string{}
	}
	m.headers[host][h] = v
}
func (m *mockProxyConfigurer) SetCredentialWithGrant(host, h, v, g string) {
	if m.headers[host] == nil {
		m.headers[host] = map[string]string{}
	}
	m.headers[host][h] = v
	m.grants[host] = g
}
func (m *mockProxyConfigurer) AddExtraHeader(host, h, v string)                                   {}
func (m *mockProxyConfigurer) AddResponseTransformer(host string, _ provider.ResponseTransformer) {}
func (m *mockProxyConfigurer) RemoveRequestHeader(host, h string)                                 {}
func (m *mockProxyConfigurer) SetTokenSubstitution(host, p, r string)                             {}

func TestProviderName(t *testing.T) {
	if (&Provider{}).Name() != "kiro" {
		t.Errorf("Name() = %q, want kiro", (&Provider{}).Name())
	}
}

func TestConfigureProxyInjectsBearerOnKiroHosts(t *testing.T) {
	m := newMockProxy()
	(&Provider{}).ConfigureProxy(m, &provider.Credential{Token: "real-token"})
	for _, host := range kiroAPIHosts {
		got := m.headers[host]["Authorization"]
		if got != "Bearer real-token" {
			t.Errorf("host %s Authorization = %q, want %q", host, got, "Bearer real-token")
		}
		if m.grants[host] != "kiro" {
			t.Errorf("host %s grant = %q, want %q", host, m.grants[host], "kiro")
		}
	}
	for _, host := range kiroPassthroughHosts {
		if _, ok := m.headers[host]; ok {
			t.Errorf("passthrough host %s should not receive credential injection", host)
		}
	}
}

func TestContainerEnvSetsPlaceholder(t *testing.T) {
	env := (&Provider{}).ContainerEnv(&provider.Credential{Token: "real"})
	want := "KIRO_API_KEY=" + KiroAPIKeyPlaceholder
	if len(env) != 1 || env[0] != want {
		t.Errorf("ContainerEnv() = %v, want [%q]", env, want)
	}
}

func TestInterfaceCompliance(t *testing.T) {
	var _ provider.CredentialProvider = (*Provider)(nil)
	var _ provider.AgentProvider = (*Provider)(nil)
}
