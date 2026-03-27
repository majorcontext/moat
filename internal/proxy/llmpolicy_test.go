package proxy

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"testing"

	keeplib "github.com/majorcontext/keep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateLLMResponse_AllowsReadTool(t *testing.T) {
	eng, err := keeplib.LoadFromBytes([]byte(`
scope: llm-gateway
mode: enforce
rules:
  - name: deny-edit
    match:
      operation: "llm.tool_use"
      when: "params.name == 'edit'"
    action: deny
    message: "Editing blocked."
`))
	require.NoError(t, err)
	defer eng.Close()

	body := []byte(`{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/foo"}}],"stop_reason":"tool_use"}`)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	result := evaluateLLMResponse(eng, body, resp)
	assert.False(t, result.Denied)
	assert.Empty(t, result.Rule)
	assert.Empty(t, result.Message)
}

func TestEvaluateLLMResponse_DeniesEditTool(t *testing.T) {
	eng, err := keeplib.LoadFromBytes([]byte(`
scope: llm-gateway
mode: enforce
rules:
  - name: deny-edit
    match:
      operation: "llm.tool_use"
      when: "params.name == 'edit'"
    action: deny
    message: "Editing blocked."
`))
	require.NoError(t, err)
	defer eng.Close()

	body := []byte(`{"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/foo"}}],"stop_reason":"tool_use"}`)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	result := evaluateLLMResponse(eng, body, resp)
	assert.True(t, result.Denied)
	assert.Equal(t, "deny-edit", result.Rule)
	assert.Contains(t, result.Message, "Editing blocked")
}

func TestEvaluateLLMResponse_NoToolsAllowed(t *testing.T) {
	eng, err := keeplib.LoadFromBytes([]byte(`
scope: llm-gateway
mode: enforce
rules:
  - name: deny-edit
    match:
      operation: "llm.tool_use"
      when: "params.name == 'edit'"
    action: deny
    message: "Editing blocked."
`))
	require.NoError(t, err)
	defer eng.Close()

	body := []byte(`{"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn"}`)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	result := evaluateLLMResponse(eng, body, resp)
	assert.False(t, result.Denied)
}

func TestBuildPolicyDeniedResponse(t *testing.T) {
	body := buildPolicyDeniedResponse("deny-edit", "Editing blocked.")
	assert.Contains(t, string(body), "policy_denied")
	assert.Contains(t, string(body), "deny-edit")
	assert.Contains(t, string(body), "Editing blocked")
}

func TestBuildPolicyDeniedResponse_EmptyMessage(t *testing.T) {
	body := buildPolicyDeniedResponse("deny-edit", "")
	assert.Contains(t, string(body), "deny-edit")
	assert.NotContains(t, string(body), ". .")
}

func gzipCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestEvaluateLLMResponse_GzipJSON(t *testing.T) {
	eng, err := keeplib.LoadFromBytes([]byte(`
scope: llm-gateway
mode: enforce
rules:
  - name: deny-edit
    match:
      operation: "llm.tool_use"
      when: "params.name == 'edit'"
    action: deny
    message: "Editing blocked."
`))
	require.NoError(t, err)
	defer eng.Close()

	plainBody := []byte(`{"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/foo"}}],"stop_reason":"tool_use"}`)
	compressed := gzipCompress(t, plainBody)

	resp := &http.Response{
		StatusCode: 200,
		Header: http.Header{
			"Content-Type":     []string{"application/json"},
			"Content-Encoding": []string{"gzip"},
		},
	}

	result := evaluateLLMResponse(eng, compressed, resp)
	assert.True(t, result.Denied)
	assert.Equal(t, "deny-edit", result.Rule)
}

func TestEvaluateLLMResponse_GzipSSE(t *testing.T) {
	eng, err := keeplib.LoadFromBytes([]byte(`
scope: llm-gateway
mode: enforce
rules:
  - name: deny-edit
    match:
      operation: "llm.tool_use"
      when: "params.name == 'edit'"
    action: deny
    message: "Editing blocked."
`))
	require.NoError(t, err)
	defer eng.Close()

	sseBody := "event: message_start\ndata: {\"type\":\"message\",\"content\":[]}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"t1\",\"name\":\"Edit\",\"input\":{}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"file_path\\\":\\\"/foo\\\"}\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	compressed := gzipCompress(t, []byte(sseBody))

	resp := &http.Response{
		StatusCode: 200,
		Header: http.Header{
			"Content-Type":     []string{"text/event-stream"},
			"Content-Encoding": []string{"gzip"},
		},
	}

	result := evaluateLLMResponse(eng, compressed, resp)
	assert.True(t, result.Denied)
	assert.Equal(t, "deny-edit", result.Rule)
}

func TestEvaluateLLMResponse_SSEAllowed(t *testing.T) {
	eng, err := keeplib.LoadFromBytes([]byte(`
scope: llm-gateway
mode: enforce
rules:
  - name: deny-edit
    match:
      operation: "llm.tool_use"
      when: "params.name == 'Edit'"
    action: deny
    message: "Editing blocked."
`))
	require.NoError(t, err)
	defer eng.Close()

	sseBody := "event: message_start\ndata: {\"type\":\"message\",\"content\":[]}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}

	result := evaluateLLMResponse(eng, []byte(sseBody), resp)
	assert.False(t, result.Denied)
	assert.NotNil(t, result.Events, "SSE allowed response should return events for re-serialization")
	assert.True(t, len(result.Events) > 0)
}

func TestEvaluateLLMResponse_InvalidGzip(t *testing.T) {
	eng, err := keeplib.LoadFromBytes([]byte(`
scope: llm-gateway
mode: enforce
rules:
  - name: deny-all
    match:
      operation: "*"
    action: deny
`))
	require.NoError(t, err)
	defer eng.Close()

	resp := &http.Response{
		StatusCode: 200,
		Header: http.Header{
			"Content-Type":     []string{"application/json"},
			"Content-Encoding": []string{"gzip"},
		},
	}

	// Not valid gzip — should fail-closed.
	result := evaluateLLMResponse(eng, []byte("not gzip data"), resp)
	assert.True(t, result.Denied)
	assert.Equal(t, "evaluation-error", result.Rule)
}

func TestEvaluateLLMResponse_LargeBodyExceedsMaxSize(t *testing.T) {
	// This tests the evaluateLLMResponse function directly — the size check
	// happens in the proxy handler, not here. But we verify that large bodies
	// still produce correct results when evaluated.
	eng, err := keeplib.LoadFromBytes([]byte(`
scope: llm-gateway
mode: enforce
rules:
  - name: deny-edit
    match:
      operation: "llm.tool_use"
      when: "params.name == 'Edit'"
    action: deny
`))
	require.NoError(t, err)
	defer eng.Close()

	// Build a large but valid JSON body with padding.
	padding := make([]byte, 1024)
	for i := range padding {
		padding[i] = ' '
	}
	body := fmt.Sprintf(`{"content":[{"type":"text","text":"%s"}],"stop_reason":"end_turn"}`, string(padding))

	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	result := evaluateLLMResponse(eng, []byte(body), resp)
	assert.False(t, result.Denied)
}
