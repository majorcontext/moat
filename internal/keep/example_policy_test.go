package keep

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	keeplib "github.com/majorcontext/keep"
)

// TestExamplePolicyBodyEnforces loads the shipped examples/policy-body Keep
// policy exactly as the daemon would and asserts it actually denies a
// secret-bearing JSON body and allows a clean one. This is the end-to-end
// operation-match + body-evaluation path that earlier unit tests skipped — the
// gap that let a non-matching rule (uppercase/glob operation vs keep's
// lowercased path.Match) ship without failing any test.
func TestExamplePolicyBodyEnforces(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "policy-body", ".keep", "http-body-rules.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read example policy: %v", err)
	}

	eng, err := keeplib.LoadFromBytes(data)
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	defer eng.Close()

	if !eng.RequiresBody("http") {
		t.Fatal("example policy should require body inspection")
	}

	secret := keeplib.NewHTTPCallWithBody("POST", "httpbin.org", "/post",
		map[string]any{"token": "AKIAIOSFODNN7REALKEY"})
	clean := keeplib.NewHTTPCallWithBody("POST", "httpbin.org", "/post",
		map[string]any{"note": "nothing secret here"})

	secRes, err := keeplib.SafeEvaluate(context.Background(), eng, secret, "http")
	if err != nil {
		t.Fatalf("evaluate secret: %v", err)
	}
	if secRes.Decision != keeplib.Deny {
		t.Errorf("secret body: decision = %v, want Deny (rule %q)", secRes.Decision, secRes.Rule)
	}

	cleanRes, err := keeplib.SafeEvaluate(context.Background(), eng, clean, "http")
	if err != nil {
		t.Fatalf("evaluate clean: %v", err)
	}
	if cleanRes.Decision != keeplib.Allow {
		t.Errorf("clean body: decision = %v, want Allow", cleanRes.Decision)
	}

	// Companion case for the documented `params.body != null` guard: a bodyless
	// request (empty body passed as nil) must not be denied — the rule's
	// null-guard makes it not match, rather than erroring or fail-closing.
	bodyless := keeplib.NewHTTPCallWithBody("POST", "httpbin.org", "/post", nil)
	bodylessRes, err := keeplib.SafeEvaluate(context.Background(), eng, bodyless, "http")
	if err != nil {
		t.Fatalf("evaluate bodyless: %v", err)
	}
	if bodylessRes.Decision != keeplib.Allow {
		t.Errorf("bodyless request: decision = %v, want Allow", bodylessRes.Decision)
	}
}
