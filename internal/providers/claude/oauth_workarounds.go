package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/majorcontext/moat/internal/log"
)

// oauthProfileResponse is the synthetic profile returned for 403'd OAuth
// profile requests. Subscription metadata is included when available so
// Claude Code can determine the account tier.
type oauthProfileResponse struct {
	ID               string `json:"id"`
	Email            string `json:"email"`
	Name             string `json:"name"`
	SubscriptionType string `json:"subscriptionType,omitempty"`
	RateLimitTier    string `json:"rateLimitTier,omitempty"`
}

// OAuthEndpointWorkarounds defines OAuth API endpoints that require response
// transformation to work around scope limitations in long-lived tokens.
//
// # Background
//
// Long-lived tokens created via `claude setup-token` do not include the
// `user:profile` scope, causing 403 "permission_error" responses on these
// endpoints. However, these endpoints are non-critical for core Claude Code
// functionality - they provide usage statistics and profile information for
// the UI status line and user display.
//
// # Why Transform Instead of Fail
//
// Rather than forcing users to re-authenticate with different scopes (which
// `claude setup-token` doesn't support anyway) or causing hard crashes in
// Claude Code's status line, we intercept 403 permission errors on these
// specific endpoints and return empty success responses. This allows Claude
// Code to degrade gracefully: no usage stats displayed, but no crashes either.
//
// # Security Consideration
//
// This transformation only applies to:
// 1. These explicitly listed endpoints
// 2. 403 status codes (all 403s on these endpoints, not just permission errors)
// 3. Requests using OAuth tokens (not API keys)
//
// Other errors (401, 500, etc.) pass through unchanged to preserve observability.
var OAuthEndpointWorkarounds = []string{
	"/api/oauth/profile", // User profile info (name, email) - for UI display
	"/api/oauth/usage",   // Usage statistics - for status line display
}

// metaKeyCachedBootstrap is the metadata key for the cached /api/bootstrap response.
// This is pre-fetched using the host's full-scope OAuth token at grant or run setup.
const metaKeyCachedBootstrap = "cachedBootstrap"

// CreateOAuthEndpointTransformer creates a response transformer that handles
// 403 errors on OAuth endpoints by returning empty success responses.
func CreateOAuthEndpointTransformer() func(req, resp interface{}) (interface{}, bool) {
	return CreateOAuthEndpointTransformerWithMeta(nil)
}

// CreateOAuthEndpointTransformerWithMeta creates a response transformer that:
// 1. Returns a cached /api/bootstrap response (if available in metadata)
// 2. Handles 403 errors on OAuth profile/usage endpoints with synthetic responses
//
// The cached bootstrap is needed because setup-tokens lack the scopes required
// for /api/bootstrap to return account info and feature flags. Without proper
// bootstrap data, Claude Code cannot detect subscription capabilities like
// 1M context window.
func CreateOAuthEndpointTransformerWithMeta(meta map[string]string) func(req, resp interface{}) (interface{}, bool) {
	return func(reqInterface, respInterface interface{}) (interface{}, bool) {
		req, ok := reqInterface.(*http.Request)
		if !ok {
			return respInterface, false
		}

		resp, ok := respInterface.(*http.Response)
		if !ok {
			return respInterface, false
		}

		// Handle /api/bootstrap: prefer the real response when it contains
		// account data (full-scope token), fall back to cached response when
		// the real response is degraded (setup-token returns account:null).
		if req.URL.Path == "/api/bootstrap" {
			if cached, hasCached := meta[metaKeyCachedBootstrap]; hasCached && cached != "" {
				if resp.StatusCode == http.StatusOK {
					realBody, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
					resp.Body.Close()
					if err == nil && bootstrapHasAccount(realBody) {
						log.Debug("bootstrap has account data, using real response",
							"subsystem", "proxy")
						//nolint:bodyclose // Response body will be closed by the HTTP handler
						return &http.Response{
							StatusCode:    resp.StatusCode,
							Header:        resp.Header,
							Body:          io.NopCloser(bytes.NewReader(realBody)),
							ContentLength: int64(len(realBody)),
							ProtoMajor:    1,
							ProtoMinor:    1,
						}, false
					}
				} else {
					resp.Body.Close()
				}

				// Real response is degraded (account:null or non-200) — use cache
				log.Debug("response transformed",
					"subsystem", "proxy",
					"action", "transform",
					"reason", "cached-bootstrap",
					"original_status", resp.StatusCode,
					"cached_len", len(cached))

				body := []byte(cached)
				//nolint:bodyclose // Response body will be closed by the HTTP handler
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type":       []string{"application/json"},
						"X-Moat-Transformed": []string{"cached-bootstrap"},
					},
					Body:          io.NopCloser(bytes.NewReader(body)),
					ContentLength: int64(len(body)),
					ProtoMajor:    1,
					ProtoMinor:    1,
				}, true
			}
		}

		// Only transform 403 responses on OAuth endpoints
		if resp.StatusCode != http.StatusForbidden {
			return resp, false
		}

		// Check if this is one of our workaround endpoints
		var matchedEndpoint string
		for _, endpoint := range OAuthEndpointWorkarounds {
			if req.URL.Path == endpoint {
				matchedEndpoint = endpoint
				break
			}
		}
		if matchedEndpoint == "" {
			return resp, false
		}

		// Close the original response body since we're replacing the entire response
		resp.Body.Close()

		// Log the transformation for observability
		log.Debug("response transformed",
			"subsystem", "proxy",
			"action", "transform",
			"grant", "anthropic",
			"reason", "oauth-scope-workaround",
			"endpoint", matchedEndpoint,
			"original_status", http.StatusForbidden)

		// Return success response for this endpoint, including subscription metadata
		//nolint:bodyclose // Response body will be closed by the HTTP handler
		return createOAuthResponseWithMeta(matchedEndpoint, meta), true
	}
}

// createOAuthResponseWithMeta creates a JSON response for an OAuth endpoint,
// including subscription metadata when available.
func createOAuthResponseWithMeta(path string, meta map[string]string) *http.Response {
	var body []byte

	switch path {
	case "/api/oauth/profile":
		// Include subscription metadata so Claude Code can determine account tier.
		profile := oauthProfileResponse{ID: "", Email: "", Name: ""}
		if meta != nil {
			profile.SubscriptionType = meta["subscriptionType"]
			profile.RateLimitTier = meta["rateLimitTier"]
		}
		body, _ = json.Marshal(profile)
	case "/api/oauth/usage":
		// Empty usage - status line will show no usage data
		body = []byte(`{"usage":{}}`)
	default:
		// Generic empty response
		body = []byte(`{}`)
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":       []string{"application/json"},
			"X-Moat-Transformed": []string{"oauth-scope-workaround"},
		},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		ProtoMajor:    1,
		ProtoMinor:    1,
	}
}

// bootstrapHasAccount checks whether a bootstrap response body contains
// non-null account data. Setup-tokens return {"account":null,...} which
// means Claude Code can't detect subscription capabilities.
func bootstrapHasAccount(body []byte) bool {
	var b struct {
		Account json.RawMessage `json:"account"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		return false
	}
	return len(b.Account) > 0 && string(b.Account) != "null"
}
