package codex

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkspaceDiscoveryTransformer(t *testing.T) {
	const body = `{"accounts":[{"id":"real-account","workspace_backend_origin":"https://chatgpt.com","account_routing_override":"NO_CONSTRAINT","plan_type":"pro","future":1234567890123456789},{"id":"other-account"}],"account_ordering":["other-account","real-account"],"default_account_id":"real-account","future":true}`
	for _, compressed := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "gzip"}[compressed], func(t *testing.T) {
			wireBody := []byte(body)
			header := http.Header{"ETag": {"original"}}
			if compressed {
				var buf bytes.Buffer
				w := gzip.NewWriter(&buf)
				_, _ = w.Write(wireBody)
				_ = w.Close()
				wireBody = buf.Bytes()
				header.Set("Content-Encoding", "gzip")
			}
			// CONNECT forwarding carries the explicit TLS port in the URL.
			req := httptest.NewRequest(http.MethodGet, subscriptionOrigin+":443"+workspaceDiscoveryPath, nil)
			req.Header.Set("ChatGPT-Account-ID", "real-account")
			resp := &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(wireBody)), ContentLength: int64(len(wireBody))}
			_, changed := workspaceDiscoveryTransformer("real-account")(req, resp)
			defer resp.Body.Close()
			if !changed {
				t.Fatal("discovery identity was not mapped")
			}
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			var actual, expected any
			_ = json.Unmarshal(got, &actual)
			_ = json.Unmarshal([]byte(strings.ReplaceAll(body, "real-account", syntheticAccountID)), &expected)
			// Unknown numeric fields must not be rounded through float64.
			if strings.Contains(string(got), "real-account") || !strings.Contains(string(got), "1234567890123456789") {
				t.Fatalf("identity or unknown fields changed incorrectly: %s", got)
			}
			want, _ := json.Marshal(expected)
			gotJSON, _ := json.Marshal(actual)
			if !bytes.Equal(gotJSON, want) {
				t.Fatalf("got %s, want %s", gotJSON, want)
			}
			if resp.ContentLength != int64(len(got)) || resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("ETag") != "" {
				t.Fatalf("invalid transformed response metadata: %v", resp.Header)
			}
		})
	}
}

func TestWorkspaceDiscoveryTransformerPassThrough(t *testing.T) {
	const valid = `{"accounts":[{"id":"real-account","workspace_backend_origin":"https://chatgpt.com","account_routing_override":"us"}]}`
	for _, tc := range []struct {
		name, url, method, account, body, encoding string
		status                                     int
	}{
		{name: "unauthorized", status: 401},
		{name: "upstream failure", status: 503},
		{name: "other endpoint", url: subscriptionOrigin + "/backend-api/codex/responses"},
		{name: "other origin", url: "https://example.com" + workspaceDiscoveryPath},
		{name: "other port", url: "https://chatgpt.com:8443" + workspaceDiscoveryPath},
		{name: "plaintext", url: "http://chatgpt.com" + workspaceDiscoveryPath},
		{name: "other method", method: http.MethodPost},
		{name: "other account header", account: "other-account"},
		{name: "malformed", body: "not json"},
		{name: "missing account", body: `{"accounts":[{"id":"other-account"}]}`},
		{name: "legacy map", body: `{"accounts":{"real-account":{"account":{"account_id":"real-account"}}}}`},
		{name: "unsupported compression", encoding: "br"},
		{name: "broken gzip", encoding: "gzip"},
		{name: "oversized unknown length", body: strings.Repeat("x", maxWorkspaceDiscoveryBody+10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.url == "" {
				tc.url = subscriptionOrigin + workspaceDiscoveryPath
			}
			if tc.method == "" {
				tc.method = http.MethodGet
			}
			if tc.account == "" {
				tc.account = "real-account"
			}
			if tc.body == "" {
				tc.body = valid
			}
			if tc.status == 0 {
				tc.status = http.StatusOK
			}
			req := httptest.NewRequest(tc.method, tc.url, nil)
			req.Header.Set("ChatGPT-Account-ID", tc.account)
			resp := &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), ContentLength: -1}
			resp.Header.Set("Content-Encoding", tc.encoding)
			_, changed := workspaceDiscoveryTransformer("real-account")(req, resp)
			defer resp.Body.Close()
			if changed {
				t.Fatal("unrelated or invalid response was changed")
			}
			got, err := io.ReadAll(resp.Body)
			if err != nil || string(got) != tc.body {
				t.Fatalf("passthrough body was damaged: %v", err)
			}
		})
	}
}
