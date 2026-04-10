package gcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/majorcontext/moat/internal/log"
)

// DefaultEmail is the fallback email used when the authenticated identity is unknown.
const DefaultEmail = "default@moat.local"

// EndpointHandler serves GCE metadata server emulation routes.
// It implements http.Handler and responds to the subset of metadata
// endpoints that gcloud CLI and Google client libraries require.
type EndpointHandler struct {
	getToken  func(ctx context.Context) (*oauth2.Token, error)
	projectID string
	scopes    []string
	email     string
}

// NewEndpointHandler creates a metadata emulation handler backed by a
// CredentialProvider.
func NewEndpointHandler(cp *CredentialProvider) *EndpointHandler {
	email := cp.Email()
	if email == "" {
		email = DefaultEmail
	}
	return &EndpointHandler{
		getToken:  cp.GetToken,
		projectID: cp.ProjectID(),
		scopes:    cp.Scopes(),
		email:     email,
	}
}

// NewEndpointHandlerFromTokenFunc creates a handler with a custom token
// function. This is intended for testing.
func NewEndpointHandlerFromTokenFunc(
	getToken func(ctx context.Context) (*oauth2.Token, error),
	projectID string,
	scopes []string,
	email string,
) *EndpointHandler {
	if email == "" {
		email = DefaultEmail
	}
	return &EndpointHandler{
		getToken:  getToken,
		projectID: projectID,
		scopes:    scopes,
		email:     email,
	}
}

// ServeHTTP implements http.Handler. All requests must include the
// Metadata-Flavor: Google header or receive a 403 response.
func (h *EndpointHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Require Metadata-Flavor header on all requests.
	if r.Header.Get("Metadata-Flavor") != "Google" {
		http.Error(w, "Missing required header: Metadata-Flavor: Google", http.StatusForbidden)
		return
	}

	// All responses include the Metadata-Flavor header.
	w.Header().Set("Metadata-Flavor", "Google")

	switch r.URL.Path {
	// Liveness probes.
	case "/", "/computeMetadata/", "/computeMetadata/v1/":
		w.WriteHeader(http.StatusOK)

	// Token endpoint.
	case "/computeMetadata/v1/instance/service-accounts/default/token":
		h.serveToken(w, r)

	// Email endpoint.
	case "/computeMetadata/v1/instance/service-accounts/default/email":
		fmt.Fprint(w, h.email)

	// Scopes endpoint.
	case "/computeMetadata/v1/instance/service-accounts/default/scopes":
		fmt.Fprint(w, strings.Join(h.scopes, "\n"))

	// Aliases endpoint.
	case "/computeMetadata/v1/instance/service-accounts/default/aliases":
		fmt.Fprint(w, "default")

	// Service account directory listing.
	case "/computeMetadata/v1/instance/service-accounts/default/",
		"/computeMetadata/v1/instance/service-accounts/default":
		fmt.Fprint(w, "aliases\nemail\nidentity\nscopes\ntoken\n")

	// Service accounts listing.
	case "/computeMetadata/v1/instance/service-accounts/",
		"/computeMetadata/v1/instance/service-accounts":
		fmt.Fprintf(w, "default/\n%s/\n", h.email)

	// Project ID.
	case "/computeMetadata/v1/project/project-id":
		fmt.Fprint(w, h.projectID)

	// Numeric project ID (not available; return 0).
	case "/computeMetadata/v1/project/numeric-project-id":
		fmt.Fprint(w, "0")

	// Identity token (not implemented).
	case "/computeMetadata/v1/instance/service-accounts/default/identity":
		http.Error(w, "identity tokens not implemented", http.StatusNotFound)

	default:
		http.NotFound(w, r)
	}
}

// serveToken returns an access token in GCE metadata format.
func (h *EndpointHandler) serveToken(w http.ResponseWriter, r *http.Request) {
	tok, err := h.getToken(r.Context())
	if err != nil {
		msg := classifyError(err)
		log.Error("gcloud token fetch error", "error", err)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	expiresIn := 3600 // default for tokens without expiry
	if !tok.Expiry.IsZero() {
		expiresIn = int(time.Until(tok.Expiry).Seconds())
		if expiresIn < 0 {
			expiresIn = 0
		}
	}

	resp := map[string]any{
		"access_token": tok.AccessToken,
		"expires_in":   expiresIn,
		"token_type":   "Bearer",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Error("failed to encode token response", "error", err)
	}
}

// classifyError returns a user-friendly error message for common token errors.
// Note: matches against error message strings from golang.org/x/oauth2 and google SDK.
// These are not part of a stable API — audit when bumping those dependencies.
func classifyError(err error) string {
	msg := err.Error()

	switch {
	case strings.Contains(msg, "could not find default credentials"):
		return "gcloud credential error: no Application Default Credentials found on host.\n\n" +
			"Run 'gcloud auth application-default login' on your host."

	case strings.Contains(msg, "oauth2: cannot fetch token"):
		return "gcloud credential error: failed to refresh token.\n\n" +
			"Your host credentials may have expired. Run 'gcloud auth application-default login'."

	case strings.Contains(msg, "context canceled") || strings.Contains(msg, "context deadline exceeded"):
		return "gcloud credential error: request canceled or timed out."

	default:
		return "gcloud credential error: unexpected error fetching token.\n\n" +
			"Check the daemon log for details: ~/.moat/debug/daemon.log"
	}
}
