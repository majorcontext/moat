package lunaroute

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

// mockProxy records every credential registration by host, so tests can
// assert both what is registered and what is not.
type mockProxy struct {
	creds  map[string]string
	grants map[string]string
}

func newMockProxy() *mockProxy {
	return &mockProxy{creds: map[string]string{}, grants: map[string]string{}}
}

func (m *mockProxy) SetCredential(host, value string) { m.creds[host] = value }
func (m *mockProxy) SetCredentialHeader(host, headerName, headerValue string) {
	m.creds[host] = headerName + ": " + headerValue
}

func (m *mockProxy) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	m.creds[host] = headerName + ": " + headerValue
	m.grants[host] = grant
}
func (m *mockProxy) AddExtraHeader(host, headerName, headerValue string)                          {}
func (m *mockProxy) AddResponseTransformer(host string, transformer provider.ResponseTransformer) {}
func (m *mockProxy) RemoveRequestHeader(host, header string)                                      {}
func (m *mockProxy) SetTokenSubstitution(host, placeholder, realToken string)                     {}

func TestConfigureProxy(t *testing.T) {
	proxy := newMockProxy()
	(&Provider{}).ConfigureProxy(proxy, &provider.Credential{Token: "lr_secret"})

	want := map[string]string{
		GatewayHost: "Authorization: Bearer lr_secret",
		MCPHost:     "LUNAROUTE-API-KEY: lr_secret",
	}
	for host, v := range want {
		if proxy.creds[host] != v {
			t.Errorf("%s credential = %q, want %q", host, proxy.creds[host], v)
		}
		if proxy.grants[host] != "lunaroute" {
			t.Errorf("%s grant = %q, want lunaroute", host, proxy.grants[host])
		}
	}
	if len(proxy.creds) != len(want) {
		t.Errorf("registered %d hosts, want exactly %d: %v", len(proxy.creds), len(want), proxy.creds)
	}
	// The key belongs to LunaRoute's inference and MCP hosts only.
	for _, host := range []string{"api.anthropic.com", "api.openai.com", "api.lunaroute.com"} {
		if _, ok := proxy.creds[host]; ok {
			t.Errorf("key registered for %s; it must only reach %s and %s", host, GatewayHost, MCPHost)
		}
	}
}

func TestContainerEnvEmpty(t *testing.T) {
	if env := (&Provider{}).ContainerEnv(&provider.Credential{Token: "lr_secret"}); len(env) != 0 {
		t.Errorf("ContainerEnv() = %v, want none", env)
	}
}

func TestContainerInitFiles(t *testing.T) {
	files := (&Provider{}).ContainerInitFiles(&provider.Credential{Token: "lr_secret"}, "/home/moatuser")
	if len(files) != 1 {
		t.Fatalf("got %d init files, want 1: %v", len(files), files)
	}
	content, ok := files["/home/moatuser/.pi/agent/auth.json"]
	if !ok {
		t.Fatalf("missing ~/.pi/agent/auth.json; got %v", files)
	}
	if strings.Contains(content, "lr_secret") {
		t.Fatal("init file contains the real key")
	}

	var auth map[string]struct {
		Type    string `json:"type"`
		Access  string `json:"access"`
		Refresh string `json:"refresh"`
		Expires int64  `json:"expires"`
	}
	if err := json.Unmarshal([]byte(content), &auth); err != nil {
		t.Fatalf("auth.json is not valid JSON: %v\n%s", err, content)
	}
	entry, ok := auth["lunaroute"]
	if !ok {
		t.Fatalf("auth.json has no lunaroute entry: %s", content)
	}
	// Pi only resolves an oauth entry for the extension's provider.
	if entry.Type != "oauth" || entry.Access != PlaceholderKey {
		t.Errorf("lunaroute entry = %+v, want oauth with access %s", entry, PlaceholderKey)
	}
	// An expired login would make Pi try to refresh with an empty token.
	if entry.Expires < 4102444800000 {
		t.Errorf("expires = %d, want far in the future", entry.Expires)
	}
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr string // "" = success
	}{
		{"ok", http.StatusOK, ""},
		{"unauthorized", http.StatusUnauthorized, "rejected"},
		{"forbidden", http.StatusForbidden, "rejected"},
		{"server error", http.StatusInternalServerError, "unexpected status 500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				gotPath = r.URL.Path
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			err := validateKey(context.Background(), srv.URL+"/v1/models", "lr_abc")
			if gotAuth != "Bearer lr_abc" || gotPath != "/v1/models" {
				t.Errorf("request auth=%q path=%q, want Bearer lr_abc on /v1/models", gotAuth, gotPath)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateKey() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateKey() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateKeyNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL + "/v1/models"
	srv.Close() // nothing listening now

	err := validateKey(context.Background(), url, "lr_abc")
	if err == nil {
		t.Fatal("validateKey() = nil, want network error")
	}
	// A network failure must not be reported as a bad key.
	if strings.Contains(err.Error(), "rejected") {
		t.Errorf("network error reported as a rejected key: %v", err)
	}
}

func TestCreateCredentialRejectsNonLRKeyWithoutRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	prev := modelsURL
	modelsURL = srv.URL + "/v1/models"
	defer func() { modelsURL = prev }()

	_, err := (&Provider{}).createCredential(context.Background(), "sk-ant-nope", SourceManual)
	if err == nil {
		t.Fatal("createCredential() accepted a key without the lr_ prefix")
	}
	if called {
		t.Error("a malformed key was sent to the gateway")
	}
}

func TestCreateCredentialAcceptsValidKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	prev := modelsURL
	modelsURL = srv.URL + "/v1/models"
	defer func() { modelsURL = prev }()

	cred, err := (&Provider{}).createCredential(context.Background(), "  lr_good\n", SourceEnv)
	if err != nil {
		t.Fatalf("createCredential() = %v", err)
	}
	if cred.Provider != "lunaroute" || cred.Token != "lr_good" {
		t.Errorf("credential = %+v, want provider lunaroute, trimmed token", cred)
	}
	if cred.Metadata[provider.MetaKeyTokenSource] != SourceEnv {
		t.Errorf("token source = %q, want %q", cred.Metadata[provider.MetaKeyTokenSource], SourceEnv)
	}
}
