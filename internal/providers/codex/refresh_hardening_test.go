package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

func subscriptionCred(t *testing.T) *provider.Credential {
	t.Helper()
	encoded, err := json.Marshal(SubscriptionAuth{
		AccessToken: "old-access", RefreshToken: "old-refresh", AccountID: "real-account",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &provider.Credential{
		Provider: string(credential.ProviderCodexSubscription), Token: string(encoded), CreatedAt: time.Now(),
	}
}

// Go replays a POST body on a 307/308. Following a redirect from the token
// endpoint would hand the refresh token to whatever host the redirect names.
func TestRefreshRefusesRedirects(t *testing.T) {
	var leaked bool
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = true
		w.WriteHeader(http.StatusOK)
	}))
	defer sink.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	original := codexHTTPClient
	codexHTTPClient = &http.Client{
		Transport:     rewriteTransport{target: redirector.URL},
		CheckRedirect: original.CheckRedirect,
	}
	t.Cleanup(func() { codexHTTPClient = original })

	if _, err := (&Provider{}).Refresh(context.Background(), nil, subscriptionCred(t)); err == nil {
		t.Fatal("a redirected refresh must fail, not silently follow")
	}
	if leaked {
		t.Fatal("refresh token was replayed to the redirect target")
	}
}

// A revoked or otherwise terminally rejected refresh must be distinguishable so
// callers can tell the user to re-grant instead of retrying forever.
func TestRefreshMapsRejectionToRevoked(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		original := codexHTTPClient
		codexHTTPClient = &http.Client{Transport: rewriteTransport{target: server.URL}}
		_, err := (&Provider{}).Refresh(context.Background(), nil, subscriptionCred(t))
		codexHTTPClient = original
		server.Close()
		if !errors.Is(err, provider.ErrTokenRevoked) {
			t.Errorf("status %d: err = %v, want ErrTokenRevoked", status, err)
		}
	}
}

// Companion: a transient upstream failure must NOT look revoked, or a blip
// would tell users to redo an interactive login.
func TestRefreshServerErrorIsNotRevoked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	original := codexHTTPClient
	codexHTTPClient = &http.Client{Transport: rewriteTransport{target: server.URL}}
	t.Cleanup(func() { codexHTTPClient = original })

	_, err := (&Provider{}).Refresh(context.Background(), nil, subscriptionCred(t))
	if err == nil {
		t.Fatal("a 503 must still be an error")
	}
	if errors.Is(err, provider.ErrTokenRevoked) {
		t.Fatal("a transient 503 must not be reported as a revoked credential")
	}
}

// A refreshed credential must always carry an expiry. Without one the daemon
// treats it as due and re-exchanges the single-use refresh token on every
// registration and every tick.
func TestRefreshAlwaysRecordsAnExpiry(t *testing.T) {
	tests := []struct {
		name        string
		accessToken string
		wantExact   bool
	}{
		{"parseable jwt uses its own exp", credential.GenerateAccessTokenPlaceholder("real-account"), true},
		{"opaque token falls back to a default ttl", "not-a-jwt", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]string{
					"access_token": tt.accessToken, "refresh_token": "new-refresh",
				})
			}))
			defer server.Close()
			original := codexHTTPClient
			codexHTTPClient = &http.Client{Transport: rewriteTransport{target: server.URL}}
			t.Cleanup(func() { codexHTTPClient = original })

			updated, err := (&Provider{}).Refresh(context.Background(), nil, subscriptionCred(t))
			if err != nil {
				t.Fatal(err)
			}
			if updated.ExpiresAt.IsZero() {
				t.Fatal("refreshed credential has no expiry; it would refresh on every tick")
			}
			if tt.wantExact && !updated.ExpiresAt.Equal(jwtExpiry(tt.accessToken)) {
				t.Errorf("ExpiresAt = %v, want the token's own exp %v", updated.ExpiresAt, jwtExpiry(tt.accessToken))
			}
			if !tt.wantExact && updated.ExpiresAt.After(time.Now().Add(defaultAccessTokenTTL+time.Minute)) {
				t.Errorf("ExpiresAt = %v, want a conservative fallback", updated.ExpiresAt)
			}
		})
	}
}

func TestAccessTokenExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	jwt := credential.GenerateAccessTokenPlaceholder("acct")
	if got := accessTokenExpiry(jwt, now); !got.Equal(jwtExpiry(jwt)) {
		t.Errorf("accessTokenExpiry(jwt) = %v, want the embedded exp", got)
	}
	if got := accessTokenExpiry("opaque", now); !got.Equal(now.Add(defaultAccessTokenTTL)) {
		t.Errorf("accessTokenExpiry(opaque) = %v, want now+%v", got, defaultAccessTokenTTL)
	}
}

func TestParseCodexVersionOutput(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"codex-cli 0.146.0\n", "0.146.0"},
		{"codex-cli 0.154.9", "0.154.9"},
		// A warning line carrying an unrelated version must not be mistaken for
		// Codex's own, whichever side of it that line falls on.
		{"WARNING: helper 1.2.3 unavailable\ncodex-cli 0.150.0\n", "0.150.0"},
		{"codex-cli 0.150.0\nnote: 9.9.9 available\n", "0.150.0"},
		// A bare version line is still accepted, in case the prefix changes.
		{"0.152.0\n", "0.152.0"},
		// Nothing version-shaped, and nothing unambiguous, reads as unknown.
		{"codex-cli\n", ""},
		{"WARNING: helper 1.2.3 unavailable\n", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := parseCodexVersionOutput(tt.in); got != tt.want {
			t.Errorf("parseCodexVersionOutput(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A stored credential that cannot be decoded is a broken credential, not a
// provider that lacks refresh support — the distinction decides whether the
// user is told to re-grant.
func TestRefreshOnCorruptCredentialAsksForRegrant(t *testing.T) {
	cred := &provider.Credential{Provider: string(credential.ProviderCodexSubscription), Token: "{not json"}
	_, err := (&Provider{}).Refresh(context.Background(), nil, cred)
	if errors.Is(err, provider.ErrRefreshNotSupported) {
		t.Fatal("a corrupt credential must not be reported as unsupported refresh")
	}
	if !errors.Is(err, provider.ErrTokenRevoked) {
		t.Fatalf("err = %v, want ErrTokenRevoked so the user is told to re-grant", err)
	}
}
