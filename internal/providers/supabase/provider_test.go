package supabase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/zalando/go-keyring"
)

type (
	proxyCall struct{ host, header, value, grant string }
	mockProxy struct{ calls []proxyCall }
)

func (m *mockProxy) SetCredential(string, string)                                  {}
func (m *mockProxy) SetCredentialHeader(string, string, string)                    {}
func (m *mockProxy) AddExtraHeader(string, string, string)                         {}
func (m *mockProxy) AddResponseTransformer(string, credential.ResponseTransformer) {}
func (m *mockProxy) RemoveRequestHeader(string, string)                            {}
func (m *mockProxy) SetTokenSubstitution(string, string, string)                   {}
func (m *mockProxy) SetCredentialWithGrant(host, header, value, grant string) {
	m.calls = append(m.calls, proxyCall{host, header, value, grant})
}

func TestProviderInjection(t *testing.T) {
	p := New()
	if deps := p.ImpliedDependencies(); !slices.Equal(deps, []string{"supabase-cli"}) {
		t.Fatalf("ImpliedDependencies() = %v, want [supabase-cli]", deps)
	}
	if env := p.ContainerEnv(nil); env != nil {
		t.Fatalf("ContainerEnv(nil) = %v, want nil", env)
	}
	cred := &provider.Credential{Token: "sbp_real"}
	env := p.ContainerEnv(cred)
	if len(env) != 1 || !regexp.MustCompile(`^SUPABASE_ACCESS_TOKEN=sbp_[a-f0-9]{40}$`).MatchString(env[0]) {
		t.Fatalf("ContainerEnv = %v", env)
	}
	if other := p.ContainerEnv(cred); other[0] == env[0] {
		t.Fatal("the same credential received the same placeholder for two runs")
	}
	proxy := &mockProxy{}
	p.ConfigureProxy(proxy, cred)
	if len(proxy.calls) != 1 || proxy.calls[0] != (proxyCall{"api.supabase.com", "Authorization", "Bearer sbp_real", "supabase"}) {
		t.Fatalf("proxy calls = %v", proxy.calls)
	}
}

func TestGrantFromEnvironment(t *testing.T) {
	t.Setenv("SUPABASE_ACCESS_TOKEN", "sbp_test_token")
	cred, err := New().Grant(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cred.Token != "sbp_test_token" || cred.Metadata[provider.MetaKeyTokenSource] != "env" {
		t.Fatalf("credential = %+v", cred)
	}
}

func TestGrantAfterChoice(t *testing.T) {
	for _, tt := range []struct {
		name      string
		confirmed bool
		loadToken string
		loadErr   error
		wantToken string
		wantPaste bool
	}{
		{name: "declined import", wantToken: "pasted", wantPaste: true},
		{name: "imported", confirmed: true, loadToken: "from-host", wantToken: "from-host"},
		{name: "missing host token", confirmed: true, wantToken: "pasted", wantPaste: true},
		{name: "host read failed", confirmed: true, loadErr: errors.New("keyring denied"), wantToken: "pasted", wantPaste: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pasted := false
			cred, err := grantAfterChoice(context.Background(), tt.confirmed,
				func() (string, error) { return tt.loadToken, tt.loadErr },
				func(context.Context) (*provider.Credential, error) {
					pasted = true
					return &provider.Credential{Token: "pasted"}, nil
				})
			if err != nil || cred.Token != tt.wantToken || pasted != tt.wantPaste {
				t.Fatalf("credential = %+v, pasted = %v, err = %v", cred, pasted, err)
			}
		})
	}
}

func TestReadHostToken(t *testing.T) {
	t.Setenv("SUPABASE_NO_KEYRING", "0")
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "profile"), []byte("work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var accounts []string
	get := func(service, account string) (string, error) {
		if service != keyringService {
			t.Errorf("service = %q", service)
		}
		accounts = append(accounts, account)
		if account == "work" {
			return "sbp_host", nil
		}
		return "", keyring.ErrNotFound
	}
	token, err := readHostToken(home, get)
	if err != nil || token != "sbp_host" || len(accounts) != 1 || accounts[0] != "work" {
		t.Fatalf("token = %q, accounts = %v, err = %v", token, accounts, err)
	}

	get = func(_, account string) (string, error) {
		if account == legacyAccount {
			return "sbp_legacy", nil
		}
		return "", keyring.ErrNotFound
	}
	token, err = readHostToken(home, get)
	if err != nil || token != "sbp_legacy" {
		t.Fatalf("legacy token = %q, err = %v", token, err)
	}

	if err := os.WriteFile(filepath.Join(home, legacyAccount), []byte("sbp_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUPABASE_NO_KEYRING", "1")
	token, err = readHostToken(home, func(_, _ string) (string, error) {
		t.Fatal("keyring should be skipped")
		return "", nil
	})
	if err != nil || token != "sbp_file" {
		t.Fatalf("file token = %q, err = %v", token, err)
	}
	if err := os.WriteFile(filepath.Join(home, legacyAccount), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err = readHostToken(home, func(_, _ string) (string, error) {
		t.Fatal("keyring should be skipped")
		return "", nil
	})
	if err != nil || token != "" {
		t.Fatalf("empty file token = %q, err = %v", token, err)
	}
	if err := os.WriteFile(filepath.Join(home, legacyAccount), []byte("sbp_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SUPABASE_NO_KEYRING", "0")
	keyringFailure := errors.New("keyring denied")
	token, err = readHostToken(home, func(_, _ string) (string, error) {
		return "", keyringFailure
	})
	if err != nil || token != "sbp_file" {
		t.Fatalf("file fallback after keyring error = %q, err = %v", token, err)
	}

	emptyHome := t.TempDir()
	token, err = readHostToken(emptyHome, func(_, _ string) (string, error) {
		return "", keyring.ErrNotFound
	})
	if err != nil || token != "" {
		t.Fatalf("missing login = %q, err = %v", token, err)
	}
	token, err = readHostToken(emptyHome, func(_, _ string) (string, error) {
		return "", keyringFailure
	})
	if token != "" || !errors.Is(err, keyringFailure) {
		t.Fatalf("keyring error = %q, err = %v", token, err)
	}
}
