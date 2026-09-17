package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

func TestIsolatedCodexEnvDropsAmbientCredentials(t *testing.T) {
	got := isolatedCodexEnv([]string{
		"PATH=/bin", "CODEX_HOME=/real", "OPENAI_API_KEY=secret", "OPENAI_BASE_URL=https://evil.example",
		"CHATGPT_MANAGED_CONFIG=ambient", "CODEX_FUTURE_AUTH_FLAG=ambient",
	}, "/private/login")
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "secret") || strings.Contains(joined, "evil.example") || strings.Contains(joined, "ambient") || strings.Contains(joined, "CODEX_HOME=/real") {
		t.Fatalf("ambient Codex auth leaked into isolated environment: %v", got)
	}
	if !strings.Contains(joined, "CODEX_HOME=/private/login") {
		t.Fatalf("private CODEX_HOME missing: %v", got)
	}
}

func TestValidateVersion(t *testing.T) {
	for _, version := range []string{"0.146.0", "0.154.9"} {
		if err := ValidateVersion(version); err != nil {
			t.Errorf("ValidateVersion(%q): %v", version, err)
		}
	}
	for _, version := range []string{"0.145.0", "0.155.0", "1.0.0", "latest"} {
		if err := ValidateVersion(version); err == nil {
			t.Errorf("ValidateVersion(%q) succeeded, want rejection", version)
		}
	}
}

func TestPrepareContainerScopesOpenAIKeyAwayFromCodex(t *testing.T) {
	cfg, err := (&Provider{}).PrepareContainer(context.Background(), provider.PrepareOpts{ScopeOpenAIKeyToShell: true})
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Cleanup()
	for _, item := range cfg.Env {
		if strings.HasPrefix(item, "OPENAI_API_KEY=") {
			t.Fatalf("Codex environment contains API-key override: %v", cfg.Env)
		}
	}
	data, err := os.ReadFile(filepath.Join(cfg.StagingDir, OpenAIShellEnvFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "unset OPENAI_API_KEY BASH_ENV") {
		t.Fatalf("nested Codex guard missing: %s", data)
	}
}

func TestRefreshRotatesCredentialWithoutConfiguringProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["refresh_token"] != "old-refresh" || body["client_id"] != codexClientID {
			t.Errorf("unexpected refresh request: %v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  credential.GenerateAccessTokenPlaceholder("real-account"),
			"id_token":      credential.GenerateIDTokenPlaceholder("real-account"),
			"refresh_token": "new-refresh",
		})
	}))
	defer server.Close()
	originalClient := codexHTTPClient
	// The endpoint is a constant in production. Redirect its transport in the
	// test so the request URL and payload still exercise the real code path.
	codexHTTPClient = &http.Client{Transport: rewriteTransport{target: server.URL}}
	t.Cleanup(func() { codexHTTPClient = originalClient })

	auth := SubscriptionAuth{
		AccessToken: "old-access", RefreshToken: "old-refresh", AccountID: "real-account",
	}
	encoded, _ := json.Marshal(auth)
	cred := &provider.Credential{Provider: string(credential.ProviderCodexSubscription), Token: string(encoded), CreatedAt: time.Now()}
	updated, err := (&Provider{}).Refresh(context.Background(), nil, cred)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSubscriptionAuth(updated)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.RefreshToken != "new-refresh" || decoded.AccessToken == "old-access" {
		t.Fatalf("refresh did not rotate tokens: %+v", decoded)
	}
	if cred.Token == updated.Token {
		t.Fatal("Refresh mutated no state")
	}
}

type rewriteTransport struct{ target string }

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL, _ = clone.URL.Parse(r.target)
	return http.DefaultTransport.RoundTrip(clone)
}

// The grant must always use the device-code flow. Ordinary `codex login`
// finishes through a browser redirect to a localhost callback on the machine
// running Codex, so it succeeds on a laptop and fails over SSH or on a
// headless host — and no flag should make the user predict which they are.
func TestGrantAlwaysUsesDeviceCodeLogin(t *testing.T) {
	body, err := os.ReadFile("grant.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)

	if !strings.Contains(source, `exec.CommandContext(ctx, codexPath, "login", "--device-auth")`) {
		t.Error("the grant does not invoke `codex login --device-auth` unconditionally")
	}
	// No opt-out: a browser-login path would reintroduce the case that only
	// works on some machines, and Codex's own advice to reach it names a
	// command that writes to the user's real ~/.codex.
	if strings.Contains(source, "grantOptions") || strings.Contains(source, "deviceAuth") {
		t.Error("device-code login must not be conditional on a grant option")
	}
	// The private CODEX_HOME is the guarantee worth stating out loud, since
	// Codex's own output gives the user no reason to expect it.
	if !strings.Contains(source, "private, temporary CODEX_HOME") {
		t.Error("the grant should tell the user its login is isolated from ~/.codex")
	}
}
