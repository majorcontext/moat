package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	keeplib "github.com/majorcontext/keep"
	"github.com/majorcontext/keep/llm"
	"github.com/majorcontext/keep/llm/anthropic"
	"github.com/majorcontext/keep/sse"

	"github.com/majorcontext/moat/internal/log"
)

// maxLLMResponseSize is the maximum response body size (10MB) the proxy will
// buffer for LLM policy evaluation. Responses exceeding this are forwarded
// without evaluation (with a warning log).
const maxLLMResponseSize = 10 << 20

// llmCodec is the shared Anthropic codec instance. Codecs are stateless and
// safe for concurrent use.
var llmCodec = anthropic.NewCodec()

// llmPolicyResult holds the outcome of an LLM policy evaluation.
type llmPolicyResult struct {
	Denied  bool
	Rule    string
	Message string
	// For SSE responses: the (possibly redacted) events to forward.
	// nil for JSON responses or when denied.
	Events []sse.Event
}

// evaluateLLMResponse evaluates an Anthropic API response against a Keep
// engine. Handles both JSON and SSE (streaming) responses based on Content-Type.
// Fail-closed: evaluation errors cause denial.
func evaluateLLMResponse(eng *keeplib.Engine, body []byte, resp *http.Response) llmPolicyResult {
	ct := resp.Header.Get("Content-Type")
	isSSE := strings.Contains(ct, "text/event-stream")

	if isSSE {
		return evaluateLLMStream(eng, body)
	}
	return evaluateLLMJSON(eng, body)
}

// evaluateLLMJSON evaluates a non-streaming JSON response.
func evaluateLLMJSON(eng *keeplib.Engine, body []byte) llmPolicyResult {
	r, err := llm.EvaluateResponse(eng, llmCodec, body, "llm-gateway", llm.DecomposeConfig{})
	if err != nil {
		log.Warn("Keep LLM response evaluation error, denying (fail-closed)", "error", err)
		return llmPolicyResult{
			Denied:  true,
			Rule:    "evaluation-error",
			Message: fmt.Sprintf("LLM policy evaluation failed: %v", err),
		}
	}
	if r.Decision == keeplib.Deny {
		return llmPolicyResult{Denied: true, Rule: r.Rule, Message: r.Message}
	}
	return llmPolicyResult{}
}

// evaluateLLMStream parses SSE events from the buffered body, evaluates them,
// and returns the (possibly redacted) event list for forwarding.
func evaluateLLMStream(eng *keeplib.Engine, body []byte) llmPolicyResult {
	reader := sse.NewReader(bytes.NewReader(body))
	var events []sse.Event
	for {
		ev, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Warn("Keep LLM SSE parse error, denying (fail-closed)", "error", err)
			return llmPolicyResult{
				Denied:  true,
				Rule:    "evaluation-error",
				Message: fmt.Sprintf("Failed to parse SSE stream: %v", err),
			}
		}
		events = append(events, ev)
		if ev.Type == "message_stop" {
			break
		}
	}

	sr, err := llm.EvaluateStream(eng, llmCodec, events, "llm-gateway", llm.DecomposeConfig{})
	if err != nil {
		log.Warn("Keep LLM stream evaluation error, denying (fail-closed)", "error", err)
		return llmPolicyResult{
			Denied:  true,
			Rule:    "evaluation-error",
			Message: fmt.Sprintf("LLM policy evaluation failed: %v", err),
		}
	}
	if sr.Decision == keeplib.Deny {
		return llmPolicyResult{Denied: true, Rule: sr.Rule, Message: sr.Message}
	}
	return llmPolicyResult{Events: sr.Events}
}

// buildPolicyDeniedResponse returns a JSON error body matching the format
// the Keep LLM gateway uses, so Claude Code handles it consistently.
func buildPolicyDeniedResponse(rule, message string) []byte {
	msg := "Policy denied: " + rule
	if message != "" {
		msg += ". " + message
	}
	resp := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "policy_denied",
			"message": msg,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}
