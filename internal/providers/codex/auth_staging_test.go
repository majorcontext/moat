package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

func TestResolveAuthKind(t *testing.T) {
	tests := []struct {
		name string
		cred *provider.Credential
		want AuthKind
	}{
		{"no credential", nil, AuthNone},
		{"subscription", &provider.Credential{Provider: string(credential.ProviderCodexSubscription)}, AuthSubscription},
		{"api key", &provider.Credential{Provider: string(credential.ProviderOpenAI)}, AuthAPIKey},
		// An unrecognized provider must not be guessed into a mode: claiming an
		// authentication Codex does not have is worse than leaving it logged out.
		{"unrelated provider", &provider.Credential{Provider: "github"}, AuthNone},
		{"empty provider", &provider.Credential{}, AuthNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveAuthKind(tt.cred); got != tt.want {
				t.Fatalf("ResolveAuthKind() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Regression: a run that staged Codex without a granted credential (codex.mcp,
// or codex.sync_logs) fell through to the subscription branch and wrote a
// synthetic ChatGPT login. Codex then reported itself authenticated and every
// request 401'd against a proxy that had no bundle installed for it.
func TestPopulateStagingDir_NoCredentialWritesNoAuthFile(t *testing.T) {
	dir := t.TempDir()
	if err := PopulateStagingDir(nil, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
		data, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
		t.Fatalf("staged an auth.json with no credential granted:\n%s", data)
	}
}

func TestPopulateStagingDir_APIKeyStagesPlaceholderOnly(t *testing.T) {
	dir := t.TempDir()
	cred := &provider.Credential{Provider: string(credential.ProviderOpenAI), Token: "sk-real-secret"}
	if err := PopulateStagingDir(cred, dir); err != nil {
		t.Fatal(err)
	}
	var auth map[string]any
	readAuthFile(t, dir, &auth)
	if auth["OPENAI_API_KEY"] != OpenAIAPIKeyPlaceholder {
		t.Errorf("OPENAI_API_KEY = %v, want the placeholder", auth["OPENAI_API_KEY"])
	}
	if _, ok := auth["auth_mode"]; ok {
		t.Errorf("API-key staging must not claim a ChatGPT auth mode: %v", auth)
	}
	assertNoSecret(t, dir, "sk-real-secret")
}

func TestPopulateStagingDir_SubscriptionStagesSyntheticTokens(t *testing.T) {
	dir := t.TempDir()
	cred := &provider.Credential{
		Provider: string(credential.ProviderCodexSubscription),
		Token:    `{"access_token":"real-access","refresh_token":"real-refresh","account_id":"real-account"}`,
	}
	if err := PopulateStagingDir(cred, dir); err != nil {
		t.Fatal(err)
	}
	var auth struct {
		AuthMode string            `json:"auth_mode"`
		Tokens   map[string]string `json:"tokens"`
	}
	readAuthFile(t, dir, &auth)
	if auth.AuthMode != "chatgptAuthTokens" {
		t.Errorf("auth_mode = %q, want chatgptAuthTokens", auth.AuthMode)
	}
	if auth.Tokens["account_id"] != syntheticAccountID {
		t.Errorf("account_id = %q, want the synthetic id", auth.Tokens["account_id"])
	}
	for _, secret := range []string{"real-access", "real-refresh", "real-account"} {
		assertNoSecret(t, dir, secret)
	}
}

// The proxy computes the expected Authorization placeholder independently of
// the staged file. If the two ever disagree the bundle silently stops matching
// and Codex sends a synthetic bearer upstream.
func TestStagedAccessTokenMatchesTheBundlePlaceholder(t *testing.T) {
	dir := t.TempDir()
	cred := &provider.Credential{
		Provider: string(credential.ProviderCodexSubscription),
		Token:    `{"access_token":"a","refresh_token":"b","account_id":"c"}`,
	}
	if err := PopulateStagingDir(cred, dir); err != nil {
		t.Fatal(err)
	}
	var auth struct {
		Tokens map[string]string `json:"tokens"`
	}
	readAuthFile(t, dir, &auth)

	rec := &bundleRecorder{}
	(&Provider{}).ConfigureProxy(rec, cred)
	if rec.bundle.ID == "" {
		t.Fatal("ConfigureProxy installed no bundle")
	}
	want := map[string]string{
		"Authorization":      "Bearer " + auth.Tokens["access_token"],
		"ChatGPT-Account-ID": auth.Tokens["account_id"],
	}
	for _, r := range rec.bundle.Replacements {
		if want[r.Name] != r.Placeholder {
			t.Errorf("%s placeholder = %q, but staging wrote %q", r.Name, r.Placeholder, want[r.Name])
		}
	}
}

// Only subscription auth pins the ChatGPT origin. An empty string is not the
// same as an absent key: it configures an empty origin rather than letting
// Codex fall back to its own default.
func TestPrepareContainerConfigOmitsChatGPTBaseURLWithoutSubscription(t *testing.T) {
	for _, tt := range []struct {
		name string
		cred *provider.Credential
		want bool
	}{
		{"subscription", &provider.Credential{Provider: string(credential.ProviderCodexSubscription), Token: `{"access_token":"a","refresh_token":"b","account_id":"c"}`}, true},
		{"api key", &provider.Credential{Provider: string(credential.ProviderOpenAI), Token: "sk-x"}, false},
		{"no credential", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := (&Provider{}).PrepareContainer(t.Context(), provider.PrepareOpts{Credential: tt.cred})
			if err != nil {
				t.Fatal(err)
			}
			defer cfg.Cleanup()
			toml, err := os.ReadFile(filepath.Join(cfg.StagingDir, "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Contains(string(toml), "chatgpt_base_url")
			if got != tt.want {
				t.Fatalf("chatgpt_base_url present = %v, want %v:\n%s", got, tt.want, toml)
			}
			if strings.Contains(string(toml), "chatgpt_base_url = ''") {
				t.Fatalf("emitted an empty chatgpt_base_url instead of omitting the key:\n%s", toml)
			}
		})
	}
}

// moat-init hard-fails a run whose Codex version is outside the verified range.
// That gate only makes sense for the synthetic auth file, so the marker that
// turns it on must be set for subscription auth and for nothing else.
func TestSubscriptionVersionMarkerIsSetOnlyForSubscriptionAuth(t *testing.T) {
	for _, tt := range []struct {
		name string
		cred *provider.Credential
		want bool
	}{
		{"subscription", &provider.Credential{Provider: string(credential.ProviderCodexSubscription), Token: `{"access_token":"a","refresh_token":"b","account_id":"c"}`}, true},
		{"api key", &provider.Credential{Provider: string(credential.ProviderOpenAI), Token: "sk-x"}, false},
		{"no credential", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := (&Provider{}).PrepareContainer(t.Context(), provider.PrepareOpts{Credential: tt.cred})
			if err != nil {
				t.Fatal(err)
			}
			defer cfg.Cleanup()
			got := false
			for _, item := range cfg.Env {
				if item == "MOAT_CODEX_SUBSCRIPTION_AUTH=1" {
					got = true
				}
			}
			if got != tt.want {
				t.Fatalf("MOAT_CODEX_SUBSCRIPTION_AUTH set = %v, want %v (env %v)", got, tt.want, cfg.Env)
			}
		})
	}
}

type bundleRecorder struct {
	provider.ProxyConfigurer
	bundle credential.Bundle
}

func (b *bundleRecorder) SetCredentialBundle(_ string, bundle credential.Bundle) { b.bundle = bundle }

func readAuthFile(t *testing.T, dir string, into any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatal(err)
	}
}

func assertNoSecret(t *testing.T, dir, secret string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), secret) {
			t.Fatalf("staged file %s leaked %q", entry.Name(), secret)
		}
	}
}
