// Package mcpoauth implements OAuth authorization code flow for MCP servers.
// It runs a local HTTP server to receive the OAuth callback, opens a browser
// for authorization, and exchanges the auth code for tokens.
package mcpoauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/ui"
)

// Config holds the OAuth configuration for an MCP server.
type Config struct {
	AuthURL  string // Authorization endpoint
	TokenURL string // Token endpoint
	ClientID string // OAuth client ID
	Scopes   string // Space-separated scopes
}

// TokenResponse holds the tokens returned from an OAuth token exchange.
type TokenResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in,omitempty"`
	ExpiresAt    time.Time `json:"-"`
}

// callbackTimeout is how long to wait for the OAuth callback.
const callbackTimeout = 5 * time.Minute

// maxTokenResponseBytes limits the size of OAuth token endpoint responses
// to prevent unbounded reads from a malicious or misconfigured server.
const maxTokenResponseBytes = 1 << 20 // 1 MB

// tokenHTTPClient is a dedicated client for OAuth token requests. Using a
// private transport avoids routing through the credential-injecting proxy
// if http.DefaultTransport is patched elsewhere.
var tokenHTTPClient = &http.Client{
	Transport: &http.Transport{},
	Timeout:   30 * time.Second,
}

// Authorize runs the OAuth authorization code flow:
// 1. Starts a local HTTP server for the callback
// 2. Returns the authorization URL for the user to open
// 3. Waits for the callback with the auth code
// 4. Exchanges the auth code for tokens
func Authorize(ctx context.Context, cfg Config) (*TokenResponse, error) {
	// Generate cryptographic state parameter
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("generating state: %w", err)
	}
	state := hex.EncodeToString(stateBytes)

	// Start local callback server on a random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting callback server: %w", err)
	}
	defer listener.Close()

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return nil, fmt.Errorf("unexpected listener address type: %T", listener.Addr())
	}
	port := tcpAddr.Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	// Channel to receive the auth code
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		// Validate state
		if r.URL.Query().Get("state") != state {
			errCh <- fmt.Errorf("invalid state parameter (possible CSRF attack)")
			http.Error(w, "Invalid state parameter", http.StatusBadRequest)
			return
		}

		// Check for errors from the OAuth provider
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			desc := r.URL.Query().Get("error_description")
			if desc != "" {
				errMsg = errMsg + ": " + desc
			}
			errCh <- fmt.Errorf("OAuth error: %s", errMsg)
			_, _ = fmt.Fprintf(w, "<html><body><h1>Authorization Failed</h1><p>%s</p><p>You can close this tab.</p></body></html>", html.EscapeString(errMsg))
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no authorization code in callback")
			http.Error(w, "No authorization code", http.StatusBadRequest)
			return
		}

		codeCh <- code
		_, _ = fmt.Fprint(w, "<html><body><h1>Authorization Successful</h1><p>You can close this tab and return to the terminal.</p></body></html>")
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- fmt.Errorf("callback server error: %w", serveErr)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx) //nolint:errcheck
	}()

	// Build authorization URL
	authURL, err := buildAuthURL(cfg, redirectURI, state)
	if err != nil {
		return nil, err
	}

	ui.Infof("\nOpen this URL in your browser to authorize:\n\n  %s\n", authURL)
	ui.Info("Waiting for authorization...")

	// Wait for callback or timeout
	var code string
	timeoutCtx, cancel := context.WithTimeout(ctx, callbackTimeout)
	defer cancel()

	select {
	case code = <-codeCh:
		// Got the auth code
	case err := <-errCh:
		return nil, err
	case <-timeoutCtx.Done():
		return nil, fmt.Errorf("authorization timed out after %s", callbackTimeout)
	}

	ui.Info("Authorization received, exchanging for tokens...")

	// Exchange auth code for tokens
	return exchangeCode(ctx, cfg, code, redirectURI)
}

// RefreshAccessToken uses a refresh token to obtain a new access token.
func RefreshAccessToken(ctx context.Context, tokenURL, clientID, refreshToken string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tokenHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing refresh response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access_token in refresh response")
	}

	if tokenResp.ExpiresIn > 0 {
		tokenResp.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	return &tokenResp, nil
}

// buildAuthURL constructs the OAuth authorization URL.
func buildAuthURL(cfg Config, redirectURI, state string) (string, error) {
	u, err := url.Parse(cfg.AuthURL)
	if err != nil {
		return "", fmt.Errorf("invalid auth URL: %w", err)
	}

	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	if cfg.Scopes != "" {
		q.Set("scope", cfg.Scopes)
	}
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// exchangeCode exchanges an authorization code for tokens.
func exchangeCode(ctx context.Context, cfg Config, code, redirectURI string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
		"client_id":    {cfg.ClientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tokenHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access_token in token response")
	}

	if tokenResp.ExpiresIn > 0 {
		tokenResp.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	return &tokenResp, nil
}
