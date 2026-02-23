// Package oauthrelay implements an OAuth callback relay for routing authorization
// codes to the correct application container. Google OAuth requires registering
// specific host:port redirect URIs. Since multiple Moat runs share a single
// host:port via subdomain routing, this relay gives Google one registered callback
// (oauthrelay.localhost:port/callback) and routes auth codes to the right app.
package oauthrelay

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

// validAppName matches safe app names: starts with alphanumeric, may contain alphanumeric, dots, hyphens, underscores.
var validAppName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// Config holds the OAuth relay configuration.
type Config struct {
	// ClientID is the Google OAuth client ID.
	ClientID string
	// ClientSecret is the Google OAuth client secret.
	ClientSecret string
	// ProxyPort is the routing proxy port (e.g. 8080).
	ProxyPort int
}

// pendingFlow tracks an in-progress OAuth flow.
type pendingFlow struct {
	// App is the agent/app name that initiated the flow (maps to routing).
	App string
	// CallbackPath is the app's callback path (e.g. "/__auth/callback").
	CallbackPath string
	// CreatedAt is when the flow was initiated.
	CreatedAt time.Time
}

// Relay handles OAuth relay requests.
type Relay struct {
	cfg      Config
	flows    map[string]pendingFlow // state -> pendingFlow
	mu       sync.Mutex
	hostname string // e.g. "oauthrelay.localhost:8080"
}

// New creates a new OAuth relay handler.
func New(cfg Config) *Relay {
	return &Relay{
		cfg:      cfg,
		flows:    make(map[string]pendingFlow),
		hostname: fmt.Sprintf("oauthrelay.localhost:%d", cfg.ProxyPort),
	}
}

// Hostname returns the hostname this relay handles (e.g. "oauthrelay.localhost:8080").
func (r *Relay) Hostname() string {
	return r.hostname
}

// ServeHTTP routes requests to the appropriate handler.
func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/start":
		r.handleStart(w, req)
	case "/callback":
		r.handleCallback(w, req)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "not found",
		})
	}
}

// handleStart initiates an OAuth flow. The app redirects the browser here
// with ?app=<name>&callback_path=<path>. Moat generates a state parameter,
// records which app initiated the flow, and redirects to Google's OAuth endpoint.
func (r *Relay) handleStart(w http.ResponseWriter, req *http.Request) {
	app := req.URL.Query().Get("app")
	if app == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "missing required parameter: app",
		})
		return
	}
	if !validAppName.MatchString(app) {
		http.Error(w, "invalid app parameter", http.StatusBadRequest)
		return
	}

	callbackPath := req.URL.Query().Get("callback_path")
	if callbackPath == "" {
		callbackPath = "/__auth/callback"
	}
	if !strings.HasPrefix(callbackPath, "/") || strings.Contains(callbackPath, "..") || strings.ContainsAny(callbackPath, "?#") {
		http.Error(w, "invalid callback_path parameter", http.StatusBadRequest)
		return
	}

	// Generate cryptographic state parameter to track this flow
	state, err := generateState()
	if err != nil {
		log.Debug("oauthrelay: failed to generate state", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "internal error",
		})
		return
	}

	// Record the pending flow
	r.mu.Lock()
	r.flows[state] = pendingFlow{
		App:          app,
		CallbackPath: callbackPath,
		CreatedAt:    time.Now(),
	}
	r.mu.Unlock()

	log.Debug("oauthrelay: starting OAuth flow",
		"app", app,
		"callback_path", callbackPath,
		"state", truncateState(state))

	// Build Google OAuth authorization URL
	redirectURI := fmt.Sprintf("http://%s/callback", r.hostname)
	// TODO: allow apps to request custom scopes via query parameter
	googleURL := fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s&access_type=offline",
		url.QueryEscape(r.cfg.ClientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape("openid email profile"),
		url.QueryEscape(state),
	)

	http.Redirect(w, req, googleURL, http.StatusFound)
}

// handleCallback receives the OAuth callback from Google. It looks up which app
// initiated the flow via the state parameter and redirects the browser to the
// app's callback URL with the authorization code.
func (r *Relay) handleCallback(w http.ResponseWriter, req *http.Request) {
	// Check for OAuth error
	if errParam := req.URL.Query().Get("error"); errParam != "" {
		errDesc := req.URL.Query().Get("error_description")
		log.Debug("oauthrelay: Google returned error", "error", errParam, "description", errDesc)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":       "oauth_error",
			"detail":      errParam,
			"description": errDesc,
		})
		return
	}

	code := req.URL.Query().Get("code")
	state := req.URL.Query().Get("state")

	if code == "" || state == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "missing required parameters: code, state",
		})
		return
	}

	if len(state) < 8 {
		http.Error(w, "invalid state parameter", http.StatusBadRequest)
		return
	}

	// Look up the pending flow
	r.mu.Lock()
	flow, ok := r.flows[state]
	if ok {
		delete(r.flows, state)
	}
	r.mu.Unlock()

	if !ok {
		log.Debug("oauthrelay: unknown state parameter", "state", truncateState(state))
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "unknown or expired state parameter",
		})
		return
	}

	// Check for expired flows (10 minute timeout)
	if time.Since(flow.CreatedAt) > 10*time.Minute {
		log.Debug("oauthrelay: flow expired", "app", flow.App, "age", time.Since(flow.CreatedAt))
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "OAuth flow expired. Please try again.",
		})
		return
	}

	log.Debug("oauthrelay: routing auth code to app",
		"app", flow.App,
		"callback_path", flow.CallbackPath)

	// Build the app's callback URL via the routing proxy.
	// Pattern: web.<app>.localhost:<port>/<callback_path>?code=<code>
	appCallbackURL := fmt.Sprintf("http://web.%s.localhost:%d%s?code=%s",
		flow.App,
		r.cfg.ProxyPort,
		flow.CallbackPath,
		url.QueryEscape(code),
	)

	http.Redirect(w, req, appCallbackURL, http.StatusFound)
}

// CleanExpired removes pending flows older than the given duration.
func (r *Relay) CleanExpired(maxAge time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	removed := 0
	for state, flow := range r.flows {
		if time.Since(flow.CreatedAt) > maxAge {
			delete(r.flows, state)
			removed++
		}
	}
	return removed
}

// PendingFlows returns the number of pending OAuth flows.
func (r *Relay) PendingFlows() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.flows)
}

func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// truncateState returns a truncated state string safe for logging.
func truncateState(state string) string {
	if len(state) < 8 {
		return state + "..."
	}
	return state[:8] + "..."
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
