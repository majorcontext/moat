package run

import (
	"strings"
	"testing"
)

// TestPolicyRequiresBody covers the body-detection that gates the
// "keep-body-policy" daemon capability. Companion cases: a policy that
// references params.body must require body inspection; one that matches only on
// operation/path must not; and an empty map must not.
func TestPolicyRequiresBody(t *testing.T) {
	bodyPolicyHTTP := []byte(`scope: http
mode: enforce
rules:
  - name: deny-body
    match:
      when: "params.host == 'api.github.com' && params.body != null && params.body.action == 'delete'"
    action: deny
`)

	// Same body rule but declared under the mcp-github scope, so detection
	// resolves the compiled evaluator for the "mcp-github" map key and reports
	// true via genuine body-reference analysis — not the unknown-scope fail-safe.
	bodyPolicyMCP := []byte(`scope: mcp-github
mode: enforce
rules:
  - name: deny-body
    match:
      operation: "delete_issue"
      when: "params.body != null && params.body.action == 'delete'"
    action: deny
`)

	pathOnlyPolicy := []byte(`scope: http
mode: enforce
rules:
  - name: deny-path
    match:
      when: "params.host == 'api.github.com' && params.path == '/admin'"
    action: deny
`)

	tests := []struct {
		name       string
		policyYAML map[string][]byte
		want       bool
	}{
		{"references params.body", map[string][]byte{"http": bodyPolicyHTTP}, true},
		{"operation/path only", map[string][]byte{"http": pathOnlyPolicy}, false},
		{"no policies", nil, false},
		{
			// Scope-matched body policy: exercises real cross-scope detection, not
			// the unknown-scope fail-safe.
			name:       "mixed — scope-matched body policy among path-only",
			policyYAML: map[string][]byte{"http": pathOnlyPolicy, "mcp-github": bodyPolicyMCP},
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := policyRequiresBody(tt.policyYAML); got != tt.want {
				t.Errorf("policyRequiresBody() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPolicyRequiresBodyFailsSafeOnUncompilablePolicy covers the compile-error
// branch: a policy whose bytes fail to compile must be treated as requiring the
// body capability (fail-safe), so an unparseable policy can never silently
// bypass the keep-body-policy gate.
func TestPolicyRequiresBodyFailsSafeOnUncompilablePolicy(t *testing.T) {
	bad := map[string][]byte{"http": []byte("not: valid: keep: policy: [[[\n")}
	if !policyRequiresBody(bad) {
		t.Error("policyRequiresBody on uncompilable policy = false, want true (fail-safe)")
	}
}

// TestCheckKeepPolicyCapabilities covers the daemon capability gate in both
// directions per the companion-case invariant: present capabilities pass;
// absent capabilities fail fast with an actionable message.
func TestCheckKeepPolicyCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		caps         []string
		requiresBody bool
		wantErr      bool
		wantSubstr   string
	}{
		{
			name:         "keep-policy present, no body required",
			caps:         []string{"keep-policy", "host-gateway-v2"},
			requiresBody: false,
			wantErr:      false,
		},
		{
			name:         "keep-policy present, body required and supported",
			caps:         []string{"keep-policy", "keep-body-policy", "host-gateway-v2"},
			requiresBody: true,
			wantErr:      false,
		},
		{
			name:         "body required but keep-body-policy missing",
			caps:         []string{"keep-policy", "host-gateway-v2"},
			requiresBody: true,
			wantErr:      true,
			wantSubstr:   "keep-body-policy",
		},
		{
			name:         "keep-policy missing",
			caps:         []string{"host-gateway-v2"},
			requiresBody: false,
			wantErr:      true,
			wantSubstr:   "keep-policy",
		},
		{
			name:         "nil capabilities (e.g. health probe failed) fails closed",
			caps:         nil,
			requiresBody: true,
			wantErr:      true,
			wantSubstr:   "keep-policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkKeepPolicyCapabilities(tt.caps, tt.requiresBody)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("checkKeepPolicyCapabilities() = nil, want error")
				}
				if tt.wantSubstr != "" && !strings.Contains(err.Error(), tt.wantSubstr) {
					t.Errorf("error %q does not mention %q", err.Error(), tt.wantSubstr)
				}
				return
			}
			if err != nil {
				t.Errorf("checkKeepPolicyCapabilities() = %v, want nil", err)
			}
		})
	}
}
