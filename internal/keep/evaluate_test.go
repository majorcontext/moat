package keep

import (
	"testing"

	keeplib "github.com/majorcontext/keep"
)

func TestNormalizeMCPCall(t *testing.T) {
	params := map[string]any{"id": "123"}
	call := NormalizeMCPCall("delete_issue", params, "mcp-github")

	if call.Operation != "delete_issue" {
		t.Errorf("Operation = %q, want %q", call.Operation, "delete_issue")
	}
	if call.Params["id"] != "123" {
		t.Errorf("Params[id] = %v, want %q", call.Params["id"], "123")
	}
	if call.Context.Scope != "mcp-github" {
		t.Errorf("Scope = %q, want %q", call.Context.Scope, "mcp-github")
	}
	if call.Context.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestNormalizeMCPCallNilParams(t *testing.T) {
	call := NormalizeMCPCall("list_repos", nil, "mcp-github")

	if call.Operation != "list_repos" {
		t.Errorf("Operation = %q, want %q", call.Operation, "list_repos")
	}
	if call.Params != nil {
		t.Errorf("Params = %v, want nil", call.Params)
	}
}

func TestNormalizeHTTPCall(t *testing.T) {
	call := NormalizeHTTPCall("GET", "api.github.com", "/repos")

	if call.Operation != "GET api.github.com/repos" {
		t.Errorf("Operation = %q, want %q", call.Operation, "GET api.github.com/repos")
	}
	if call.Params["method"] != "GET" {
		t.Errorf("Params[method] = %v, want %q", call.Params["method"], "GET")
	}
	if call.Params["host"] != "api.github.com" {
		t.Errorf("Params[host] = %v, want %q", call.Params["host"], "api.github.com")
	}
	if call.Params["path"] != "/repos" {
		t.Errorf("Params[path] = %v, want %q", call.Params["path"], "/repos")
	}
	if call.Context.Scope != "http-api.github.com" {
		t.Errorf("Scope = %q, want %q", call.Context.Scope, "http-api.github.com")
	}
}

func TestSafeEvaluateWithEngine(t *testing.T) {
	yamlRules := `
scope: test
mode: enforce
rules:
  - name: deny-all
    match:
      operation: "*"
    action: deny
    message: blocked
`
	eng, err := keeplib.LoadFromBytes([]byte(yamlRules))
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	defer eng.Close()

	call := keeplib.Call{Operation: "anything"}
	result, err := SafeEvaluate(eng, call, "test")
	if err != nil {
		t.Fatalf("SafeEvaluate error: %v", err)
	}
	if result.Decision != keeplib.Deny {
		t.Errorf("Decision = %q, want %q", result.Decision, keeplib.Deny)
	}
}

func TestSafeEvaluateUnknownScope(t *testing.T) {
	yamlRules := `
scope: test
mode: enforce
rules:
  - name: deny-all
    match:
      operation: "*"
    action: deny
    message: blocked
`
	eng, err := keeplib.LoadFromBytes([]byte(yamlRules))
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	defer eng.Close()

	call := keeplib.Call{Operation: "anything"}
	_, err = SafeEvaluate(eng, call, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown scope")
	}
}

func TestSafeEvaluatePanicRecovery(t *testing.T) {
	// Pass a nil engine to trigger a panic inside SafeEvaluate.
	call := keeplib.Call{Operation: "anything"}
	result, err := SafeEvaluate(nil, call, "test")
	if err == nil {
		t.Fatal("expected error from panic recovery")
	}
	if result.Decision != keeplib.Deny {
		t.Errorf("Decision = %q, want %q (fail-closed on panic)", result.Decision, keeplib.Deny)
	}
}
