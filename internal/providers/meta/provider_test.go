package meta

import (
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

type fakeProxy struct {
	credentials map[string][2]string // host -> {header, value}
	grants      map[string]string    // host -> grant
}

func newFakeProxy() *fakeProxy {
	return &fakeProxy{
		credentials: make(map[string][2]string),
		grants:      make(map[string]string),
	}
}

func (f *fakeProxy) SetCredential(host, value string) {
	f.credentials[host] = [2]string{"Authorization", value}
}

func (f *fakeProxy) SetCredentialHeader(host, headerName, headerValue string) {
	f.credentials[host] = [2]string{headerName, headerValue}
}

func (f *fakeProxy) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	f.credentials[host] = [2]string{headerName, headerValue}
	f.grants[host] = grant
}

func (f *fakeProxy) AddExtraHeader(host, headerName, headerValue string)                          {}
func (f *fakeProxy) AddResponseTransformer(host string, transformer provider.ResponseTransformer) {}
func (f *fakeProxy) RemoveRequestHeader(host, headerName string)                                  {}
func (f *fakeProxy) SetTokenSubstitution(host, placeholder, realToken string)                     {}

func TestConfigureProxy(t *testing.T) {
	p := &Provider{}
	proxy := newFakeProxy()
	cred := &provider.Credential{Token: "test-token-123"}

	p.ConfigureProxy(proxy, cred)

	for _, host := range []string{"graph.facebook.com", "graph.instagram.com"} {
		got, ok := proxy.credentials[host]
		if !ok {
			t.Fatalf("no credential set for %s", host)
		}
		if got[0] != "Authorization" {
			t.Errorf("host %s: header = %q, want Authorization", host, got[0])
		}
		if got[1] != "Bearer test-token-123" {
			t.Errorf("host %s: value = %q, want Bearer test-token-123", host, got[1])
		}
		if proxy.grants[host] != "meta" {
			t.Errorf("host %s: grant = %q, want meta", host, proxy.grants[host])
		}
	}
}

func TestName(t *testing.T) {
	p := &Provider{}
	if got := p.Name(); got != "meta" {
		t.Errorf("Name() = %q, want meta", got)
	}
}

func TestContainerEnv(t *testing.T) {
	p := &Provider{}
	env := p.ContainerEnv(&provider.Credential{})
	if len(env) != 0 {
		t.Errorf("ContainerEnv() = %v, want empty", env)
	}
}

func TestImpliedDependencies(t *testing.T) {
	p := &Provider{}
	deps := p.ImpliedDependencies()
	if len(deps) != 0 {
		t.Errorf("ImpliedDependencies() = %v, want empty", deps)
	}
}

func TestCanRefresh(t *testing.T) {
	p := &Provider{}

	tests := []struct {
		name     string
		metadata map[string]string
		want     bool
	}{
		{"nil metadata", nil, false},
		{"empty metadata", map[string]string{}, false},
		{"app_id only", map[string]string{MetaKeyAppID: "123"}, false},
		{"app_secret only", map[string]string{MetaKeyAppSecret: "secret"}, false},
		{"both present", map[string]string{MetaKeyAppID: "123", MetaKeyAppSecret: "secret"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cred := &provider.Credential{Metadata: tt.metadata}
			if got := p.CanRefresh(cred); got != tt.want {
				t.Errorf("CanRefresh() = %v, want %v", got, tt.want)
			}
		})
	}
}
