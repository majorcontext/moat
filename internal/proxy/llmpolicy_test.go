package proxy

import (
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
