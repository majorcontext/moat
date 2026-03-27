package keep

import (
	"fmt"
	"time"

	keeplib "github.com/majorcontext/keep"
)

// NormalizeMCPCall creates a keeplib.Call for an MCP tools/call invocation.
func NormalizeMCPCall(toolName string, params map[string]any, scope string) keeplib.Call {
	return keeplib.Call{
		Operation: toolName,
		Params:    params,
		Context: keeplib.CallContext{
			Scope:     scope,
			Timestamp: time.Now(),
		},
	}
}

// NormalizeHTTPCall creates a keeplib.Call for an HTTP request.
func NormalizeHTTPCall(method, host, path string) keeplib.Call {
	return keeplib.Call{
		Operation: method + " " + host + path,
		Params: map[string]any{
			"method": method,
			"host":   host,
			"path":   path,
		},
		Context: keeplib.CallContext{
			Scope:     "http-" + host,
			Timestamp: time.Now(),
		},
	}
}

// SafeEvaluate wraps Engine.Evaluate with panic recovery so the proxy never
// crashes due to a Keep evaluation bug.
func SafeEvaluate(eng *keeplib.Engine, call keeplib.Call, scope string) (result keeplib.EvalResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("keep: panic during evaluation: %v", r)
			result = keeplib.EvalResult{Decision: keeplib.Deny}
		}
	}()
	return eng.Evaluate(call, scope)
}
