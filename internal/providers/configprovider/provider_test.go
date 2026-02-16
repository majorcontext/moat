package configprovider

import (
	"io"
	"net/http"
	"strings"
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
	calls        []proxyCall
	tokenSubs    []tokenSubCall
	transformers int
}

type proxyCall struct {
	host, headerName, headerValue, grant string
}

type tokenSubCall struct {
	host, placeholder, realToken string
}

func (m *mockProxy) SetCredential(host, value string)                         {}
func (m *mockProxy) SetCredentialHeader(host, headerName, headerValue string) {}
func (m *mockProxy) AddExtraHeader(host, headerName, headerValue string)      {}
func (m *mockProxy) AddResponseTransformer(host string, t credential.ResponseTransformer) {
	m.transformers++
}
func (m *mockProxy) RemoveRequestHeader(host, headerName string) {}

func (m *mockProxy) SetTokenSubstitution(host, placeholder, realToken string) {
	m.tokenSubs = append(m.tokenSubs, tokenSubCall{host, placeholder, realToken})
}

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
			name:         "with container_env (header injection)",
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
				Inject:       InjectConfig{Header: "Authorization"}, // header injection mode
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

func TestContainerEnvTokenSubUsesHashedPlaceholder(t *testing.T) {
	cp := NewConfigProvider(ProviderDef{
		Name:         "telegram",
		ContainerEnv: "TELEGRAM_BOT_TOKEN",
		// No Inject.Header — token substitution mode
	}, "builtin")
	token := "123456:ABC-DEF"
	got := cp.ContainerEnv(&provider.Credential{Token: token})
	if len(got) != 1 {
		t.Fatalf("ContainerEnv() returned %d items, want 1", len(got))
	}
	// Should use hashed placeholder, not the static one or the real token
	env := got[0]
	if strings.Contains(env, token) {
		t.Error("ContainerEnv should not contain real token")
	}
	if strings.Contains(env, credential.ProxyInjectedPlaceholder) {
		t.Error("token substitution provider should use hashed placeholder, not static one")
	}
	if !strings.HasPrefix(env, "TELEGRAM_BOT_TOKEN=moat-") {
		t.Errorf("env = %q, want prefix %q", env, "TELEGRAM_BOT_TOKEN=moat-")
	}

	// Same token should produce same placeholder (deterministic)
	got2 := cp.ContainerEnv(&provider.Credential{Token: token})
	if got[0] != got2[0] {
		t.Errorf("same token produced different placeholders: %q vs %q", got[0], got2[0])
	}

	// Different token should produce different placeholder
	got3 := cp.ContainerEnv(&provider.Credential{Token: "999999:XYZ-123"})
	if got[0] == got3[0] {
		t.Error("different tokens should produce different placeholders")
	}
}

func TestConfigureProxyTokenSubstitution(t *testing.T) {
	cp := NewConfigProvider(ProviderDef{
		Name:         "telegram",
		Hosts:        []string{"api.telegram.org"},
		ContainerEnv: "TELEGRAM_BOT_TOKEN",
		// No Inject.Header — triggers token substitution mode
	}, "builtin")

	mock := &mockProxy{}
	token := "123456:ABC-DEF"
	cred := &provider.Credential{Token: token}
	cp.ConfigureProxy(mock, cred)

	// No header injection calls
	if len(mock.calls) != 0 {
		t.Errorf("expected 0 header injection calls, got %d", len(mock.calls))
	}

	// Token substitution should have been set up
	if len(mock.tokenSubs) != 1 {
		t.Fatalf("expected 1 token substitution, got %d", len(mock.tokenSubs))
	}
	sub := mock.tokenSubs[0]
	if sub.host != "api.telegram.org" {
		t.Errorf("tokenSub.host = %q, want %q", sub.host, "api.telegram.org")
	}
	// Placeholder should be a hashed value, not the static placeholder or the real token
	if sub.placeholder == credential.ProxyInjectedPlaceholder {
		t.Error("placeholder should be hashed, not the static ProxyInjectedPlaceholder")
	}
	if sub.placeholder == token {
		t.Error("placeholder should not be the real token")
	}
	if !strings.HasPrefix(sub.placeholder, "moat-") {
		t.Errorf("placeholder = %q, want moat- prefix", sub.placeholder)
	}
	if sub.realToken != token {
		t.Errorf("tokenSub.realToken = %q, want %q", sub.realToken, token)
	}

	// Placeholder should match what ContainerEnv produces
	envs := cp.ContainerEnv(cred)
	wantEnv := "TELEGRAM_BOT_TOKEN=" + sub.placeholder
	if len(envs) != 1 || envs[0] != wantEnv {
		t.Errorf("ContainerEnv = %v, want [%q] (must match proxy placeholder)", envs, wantEnv)
	}

	// Response scrubber should be registered for the host
	if mock.transformers != 1 {
		t.Errorf("expected 1 response transformer, got %d", mock.transformers)
	}
}

func TestResponseScrubber(t *testing.T) {
	realToken := "123456:ABC-DEF-secret"
	placeholder := "moat-abc123"
	scrubber := buildResponseScrubber(realToken, placeholder)

	t.Run("scrubs token from response body", func(t *testing.T) {
		body := `{"url":"https://api.telegram.org/bot123456:ABC-DEF-secret/webhook"}`
		resp := &http.Response{
			StatusCode:    200,
			Header:        http.Header{"Content-Type": []string{"application/json"}},
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
		}

		result, transformed := scrubber((*http.Request)(nil), resp)
		if !transformed {
			t.Fatal("expected response to be transformed")
		}
		newResp := result.(*http.Response)
		got, _ := io.ReadAll(newResp.Body)
		if strings.Contains(string(got), realToken) {
			t.Errorf("response body still contains real token: %s", got)
		}
		if !strings.Contains(string(got), placeholder) {
			t.Errorf("response body should contain placeholder: %s", got)
		}
	})

	t.Run("passes through when no token present", func(t *testing.T) {
		body := `{"ok":true,"result":{"id":123456,"is_bot":true}}`
		resp := &http.Response{
			StatusCode:    200,
			Header:        http.Header{"Content-Type": []string{"application/json"}},
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
		}

		_, transformed := scrubber((*http.Request)(nil), resp)
		if transformed {
			t.Error("should not transform response without token")
		}
	})

	t.Run("skips binary content", func(t *testing.T) {
		resp := &http.Response{
			StatusCode:    200,
			Header:        http.Header{"Content-Type": []string{"image/png"}},
			Body:          io.NopCloser(strings.NewReader(realToken)),
			ContentLength: int64(len(realToken)),
		}

		_, transformed := scrubber((*http.Request)(nil), resp)
		if transformed {
			t.Error("should not transform binary responses")
		}
	})
}

func TestInterfaceCompliance(t *testing.T) {
	var _ provider.CredentialProvider = (*ConfigProvider)(nil)
	var _ provider.DescribableProvider = (*ConfigProvider)(nil)
}
