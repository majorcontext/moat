package codex

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/majorcontext/moat/internal/provider"
)

const maxWorkspaceDiscoveryBody = 1 << 20

// workspaceDiscoveryTransformer keeps the account identity returned by discovery
// consistent with the synthetic auth.json. Codex 0.160+ requires a matching
// account before starting the TUI. Routing constraints remain server-authoritative:
// never fabricate an account, backend origin, or routing override.
func workspaceDiscoveryTransformer(accountID string) provider.ResponseTransformer {
	return func(reqValue, respValue any) (any, bool) {
		req, ok := reqValue.(*http.Request)
		if !ok || req.URL == nil || req.Method != http.MethodGet ||
			req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Hostname(), subscriptionHost) ||
			(req.URL.Port() != "" && req.URL.Port() != "443") ||
			req.URL.Path != workspaceDiscoveryPath || req.Header.Get("ChatGPT-Account-ID") != accountID {
			return respValue, false
		}
		resp, ok := respValue.(*http.Response)
		if !ok || resp.StatusCode != http.StatusOK || resp.Body == nil || resp.ContentLength > maxWorkspaceDiscoveryBody {
			return respValue, false
		}
		encoding := resp.Header.Get("Content-Encoding")
		if encoding != "" && encoding != "identity" && encoding != "gzip" {
			return respValue, false
		}

		// Retain the original stream on oversized or failed reads, including Close.
		original := resp.Body
		body, err := io.ReadAll(io.LimitReader(original, maxWorkspaceDiscoveryBody+1))
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), original), original}
		if err != nil || len(body) > maxWorkspaceDiscoveryBody {
			return resp, false
		}
		if encoding == "gzip" {
			reader, gzipErr := gzip.NewReader(bytes.NewReader(body))
			if gzipErr != nil {
				return resp, false
			}
			body, err = io.ReadAll(io.LimitReader(reader, maxWorkspaceDiscoveryBody+1))
			_ = reader.Close()
			if err != nil || len(body) > maxWorkspaceDiscoveryBody {
				return resp, false
			}
		}

		var payload map[string]json.RawMessage
		if json.Unmarshal(body, &payload) != nil {
			return resp, false
		}
		var accounts []map[string]json.RawMessage
		if json.Unmarshal(payload["accounts"], &accounts) != nil {
			return resp, false
		}
		changed := false
		placeholder, _ := json.Marshal(syntheticAccountID)
		for _, account := range accounts {
			var id string
			if json.Unmarshal(account["id"], &id) == nil && id == accountID {
				account["id"] = placeholder
				changed = true
			}
		}
		if !changed {
			return resp, false
		}
		payload["accounts"], _ = json.Marshal(accounts)
		var defaultID string
		if json.Unmarshal(payload["default_account_id"], &defaultID) == nil && defaultID == accountID {
			payload["default_account_id"] = placeholder
		}
		var ordering []string
		if json.Unmarshal(payload["account_ordering"], &ordering) == nil && ordering != nil {
			for i, id := range ordering {
				if id == accountID {
					ordering[i] = syntheticAccountID
				}
			}
			payload["account_ordering"], _ = json.Marshal(ordering)
		}
		body, err = json.Marshal(payload)
		if err != nil {
			return resp, false
		}
		_ = original.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("ETag")
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
		return resp, true
	}
}
