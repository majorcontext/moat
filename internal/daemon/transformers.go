package daemon

import (
	"bytes"
	"io"
	"net/http"

	"github.com/majorcontext/moat/internal/log"
)

// maxScrubBodySize is the maximum response body size to scrub for tokens.
// Larger responses are passed through unscrubbed to avoid memory issues.
const maxScrubBodySize = 512 * 1024

// oauthEndpointWorkarounds lists OAuth API endpoints that require response
// transformation to work around scope limitations in long-lived tokens.
var oauthEndpointWorkarounds = []string{
	"/api/oauth/profile",
	"/api/oauth/usage",
}

// newOAuthEndpointTransformer creates a response transformer that handles 403 errors
// on OAuth endpoints by returning empty success responses. This prevents Claude Code
// from crashing when using long-lived tokens that lack the user:profile scope.
//
// Mirrors the logic in providers/claude/oauth_workarounds.go.
func newOAuthEndpointTransformer() func(req, resp interface{}) (interface{}, bool) {
	return func(reqInterface, respInterface interface{}) (interface{}, bool) {
		req, ok := reqInterface.(*http.Request)
		if !ok {
			return respInterface, false
		}

		resp, ok := respInterface.(*http.Response)
		if !ok {
			return respInterface, false
		}

		if resp.StatusCode != http.StatusForbidden {
			return resp, false
		}

		var matchedEndpoint string
		for _, endpoint := range oauthEndpointWorkarounds {
			if req.URL.Path == endpoint {
				matchedEndpoint = endpoint
				break
			}
		}
		if matchedEndpoint == "" {
			return resp, false
		}

		resp.Body.Close()

		log.Debug("response transformed",
			"subsystem", "proxy",
			"action", "transform",
			"reason", "oauth-scope-workaround",
			"endpoint", matchedEndpoint,
			"original_status", http.StatusForbidden)

		var body []byte
		switch matchedEndpoint {
		case "/api/oauth/profile":
			body = []byte(`{"id":"","email":"","name":""}`)
		case "/api/oauth/usage":
			body = []byte(`{"usage":{}}`)
		default:
			body = []byte(`{}`)
		}

		//nolint:bodyclose // Response body will be closed by the HTTP handler
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
		}, true
	}
}

// newResponseScrubber creates a response transformer that replaces real tokens
// with placeholders in response bodies, preventing credential leakage.
//
// Mirrors the logic in providers/configprovider/provider.go.
func newResponseScrubber(realToken, placeholder string) func(req, resp interface{}) (interface{}, bool) {
	return func(_, respInterface interface{}) (interface{}, bool) {
		resp, ok := respInterface.(*http.Response)
		if !ok || resp.Body == nil {
			return respInterface, false
		}

		ct := resp.Header.Get("Content-Type")
		if ct != "" && !bytes.Contains([]byte(ct), []byte("json")) && !bytes.Contains([]byte(ct), []byte("text")) {
			return resp, false
		}

		if resp.ContentLength > maxScrubBodySize {
			return resp, false
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxScrubBodySize))
		resp.Body.Close()
		if err != nil {
			resp.Body = io.NopCloser(bytes.NewReader(body))
			return resp, false
		}

		tokenBytes := []byte(realToken)
		scrubbed := bytes.ReplaceAll(body, tokenBytes, []byte(placeholder))
		if !bytes.Equal(body, scrubbed) {
			log.Debug("scrubbed credential from response body",
				"subsystem", "daemon",
				"placeholder", placeholder,
				"bodyLen", len(body),
				"occurrences", bytes.Count(body, tokenBytes),
			)
			resp.Body = io.NopCloser(bytes.NewReader(scrubbed))
			resp.ContentLength = int64(len(scrubbed))
			return resp, true
		}

		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, false
	}
}
