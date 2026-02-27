package daemon

import (
	"bytes"
	"io"
	"net/http"

	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/providers/claude"
)

// maxScrubBodySize is the maximum response body size to scrub for tokens.
// Larger responses are passed through unscrubbed to avoid memory issues.
const maxScrubBodySize = 512 * 1024

// newOAuthEndpointTransformer creates a response transformer that handles 403 errors
// on OAuth endpoints by returning empty success responses. This prevents Claude Code
// from crashing when using long-lived tokens that lack the user:profile scope.
//
// Delegates to providers/claude.CreateOAuthEndpointTransformer to avoid duplicating
// the endpoint list and response logic.
func newOAuthEndpointTransformer() func(req, resp interface{}) (interface{}, bool) {
	return claude.CreateOAuthEndpointTransformer()
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
