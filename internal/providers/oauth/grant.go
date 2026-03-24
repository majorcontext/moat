package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// tokenClient is the HTTP client used for token exchange requests.
var tokenClient = &http.Client{Timeout: 30 * time.Second}

// TokenResponse holds the JSON response from the token endpoint.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
}

// generatePKCE generates a PKCE code verifier and challenge.
// Verifier: 32 random bytes, base64url-encoded (no padding).
// Challenge: base64url(sha256(verifier)).
func generatePKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return verifier, challenge
}

// generateState generates a random state parameter (16 bytes, base64url-encoded).
func generateState() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// buildAuthURL constructs the authorization URL with all required query parameters.
func buildAuthURL(cfg *Config, state, challenge, redirectURI, resource string) string {
	u, _ := url.Parse(cfg.AuthURL)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	if cfg.Scopes != "" {
		q.Set("scope", cfg.Scopes)
	}
	if resource != "" {
		q.Set("resource", resource)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// startCallbackServer starts a local HTTP server on a random port to receive the OAuth callback.
func startCallbackServer(expectedState string, codeCh chan<- string, errCh chan<- error) (*http.Server, int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, fmt.Errorf("listen: %w", err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		ln.Close()
		return nil, 0, fmt.Errorf("unexpected address type %T", ln.Addr())
	}
	port := tcpAddr.Port

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		// Check for OAuth error parameter.
		if oauthErr := q.Get("error"); oauthErr != "" {
			desc := q.Get("error_description")
			msg := oauthErr
			if desc != "" {
				msg += ": " + desc
			}
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<html><body><h1>Authorization failed</h1><p>%s</p></body></html>", msg)
			errCh <- fmt.Errorf("oauth error: %s", msg)
			return
		}

		// Validate state.
		if q.Get("state") != expectedState {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "<html><body><h1>Invalid state parameter</h1></body></html>")
			errCh <- fmt.Errorf("state mismatch: expected %q, got %q", expectedState, q.Get("state"))
			return
		}

		// Extract code.
		code := q.Get("code")
		if code == "" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "<html><body><h1>Missing authorization code</h1></body></html>")
			errCh <- fmt.Errorf("missing authorization code in callback")
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h1>Authorization successful</h1><p>You can close this window.</p></body></html>")
		codeCh <- code
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()

	return srv, port, nil
}

// exchangeCode exchanges an authorization code for tokens.
func exchangeCode(ctx context.Context, cfg *Config, code, redirectURI, codeVerifier string) (*TokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {cfg.ClientID},
		"code_verifier": {codeVerifier},
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tokenClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	return &tok, nil
}

// openBrowser attempts to open a URL in the user's default browser.
func openBrowser(u string) error {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	default:
		cmd = "xdg-open"
	}
	return exec.Command(cmd, u).Start()
}

// RunGrant orchestrates the full OAuth authorization code flow with PKCE.
func RunGrant(ctx context.Context, name string, cfg *Config, resource string) (*provider.Credential, error) {
	verifier, challenge := generatePKCE()
	state := generateState()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv, port, err := startCallbackServer(state, codeCh, errCh)
	if err != nil {
		return nil, fmt.Errorf("starting callback server: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	// Perform DCR now that we know the actual redirect URI.
	if cfg.ClientID == "" && cfg.RegistrationEndpoint != "" {
		fmt.Println("Registering OAuth client...")
		reg, regErr := registerClient(ctx, cfg.RegistrationEndpoint, "moat", []string{redirectURI})
		if regErr != nil {
			return nil, fmt.Errorf("dynamic client registration: %w", regErr)
		}
		cfg.ClientID = reg.ClientID
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("no client_id: discovery did not find a registration endpoint and no client_id was configured")
	}

	authURL := buildAuthURL(cfg, state, challenge, redirectURI, resource)

	// Try to open browser; not fatal if it fails.
	_ = openBrowser(authURL)

	// Wait for callback or timeout.
	var code string
	select {
	case code = <-codeCh:
	case e := <-errCh:
		return nil, fmt.Errorf("authorization failed: %w", e)
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("timed out waiting for authorization callback")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	tok, err := exchangeCode(ctx, cfg, code, redirectURI, verifier)
	if err != nil {
		return nil, fmt.Errorf("exchanging code: %w", err)
	}

	now := time.Now()
	cred := &provider.Credential{
		Provider:  "oauth:" + name,
		Token:     tok.AccessToken,
		CreatedAt: now,
		Metadata: map[string]string{
			provider.MetaKeyTokenSource: "oauth",
			"token_url":                 cfg.TokenURL,
			"client_id":                 cfg.ClientID,
		},
	}

	if cfg.Scopes != "" {
		cred.Scopes = strings.Fields(cfg.Scopes)
	}

	if tok.ExpiresIn > 0 {
		cred.ExpiresAt = now.Add(time.Duration(tok.ExpiresIn) * time.Second)
	}

	if tok.RefreshToken != "" {
		cred.Metadata["refresh_token"] = tok.RefreshToken
	}
	if cfg.ClientSecret != "" {
		cred.Metadata["client_secret"] = cfg.ClientSecret
	}
	if resource != "" {
		cred.Metadata["resource"] = resource
	}

	return cred, nil
}
