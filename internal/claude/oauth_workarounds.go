package claude

import (
	"bytes"
	"io"
	"net/http"

	"github.com/majorcontext/moat/internal/log"
)

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

// CreateOAuthEndpointTransformer creates a response transformer that handles
// 403 errors on OAuth endpoints by returning empty success responses.
//
// The transformer:
// 1. Only acts on 403 status codes
// 2. Checks if the request path matches one of OAuthEndpointWorkarounds
// 3. Returns an empty but valid JSON response for that endpoint
// 4. Adds X-Moat-Transformed header for observability
//
// We don't check the response body because:
// - These are explicitly listed OAuth endpoints (not wildcards)
// - Any 403 on these endpoints is almost certainly a scope issue
// - Body checking requires handling gzip/compression which adds complexity
// - Transforming a non-scope 403 is harmless (returns empty data, no crash)
//
// Original 403 responses are still logged for debugging, but the client
// receives a success response to prevent crashes.
func CreateOAuthEndpointTransformer() func(req, resp interface{}) (interface{}, bool) {
	return func(reqInterface, respInterface interface{}) (interface{}, bool) {
		req, ok := reqInterface.(*http.Request)
		if !ok {
			return respInterface, false
		}

		resp, ok := respInterface.(*http.Response)
		if !ok {
			return respInterface, false
		}

		log.Debug("OAuth transformer invoked", "path", req.URL.Path, "status", resp.StatusCode)

		// Only transform 403 responses
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
		log.Debug("transforming OAuth endpoint 403 to empty success",
			"endpoint", matchedEndpoint,
			"reason", "OAuth endpoints require user:profile scope not available in long-lived tokens")

		// Return empty success response for this endpoint
		return createEmptyOAuthResponse(matchedEndpoint), true
	}
}

// createEmptyOAuthResponse creates an empty but valid JSON response for an OAuth endpoint.
func createEmptyOAuthResponse(path string) *http.Response {
	var body []byte

	switch path {
	case "/api/oauth/profile":
		// Empty profile - Claude Code will handle missing data gracefully
		body = []byte(`{"id":"","email":"","name":""}`)
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
