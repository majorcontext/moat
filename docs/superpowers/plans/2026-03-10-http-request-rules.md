# HTTP Request Rules Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-host HTTP method + path rules to Moat's network policy, replacing the host-only `allow` list with expressive `allow`/`deny` rules.

**Architecture:** Rules are parsed from `network.rules` in moat.yaml into structured types. The proxy evaluates rules per-request (first match wins, unmatched falls through to policy default). Rules flow through the same per-run context path as the current allow list.

**Tech Stack:** Go, YAML parsing (gopkg.in/yaml.v3), existing proxy infrastructure

**Spec:** `docs/superpowers/specs/2026-03-10-http-request-rules-design.md`

---

## Chunk 1: Rule Types and Path Matching

### Task 1: Define rule types in config package

**Files:**
- Create: `internal/config/rules.go`
- Test: `internal/config/rules_test.go`

- [ ] **Step 1: Write failing tests for rule parsing**

```go
// internal/config/rules_test.go
package config

import "testing"

func TestParseRule(t *testing.T) {
	tests := []struct {
		input   string
		want    Rule
		wantErr bool
	}{
		{
			input: "allow GET /repos/*",
			want:  Rule{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
		},
		{
			input: "deny DELETE /*",
			want:  Rule{Action: "deny", Method: "DELETE", PathPattern: "/*"},
		},
		{
			input: "allow * /user",
			want:  Rule{Action: "allow", Method: "*", PathPattern: "/user"},
		},
		{
			input: "deny * /admin/**",
			want:  Rule{Action: "deny", Method: "*", PathPattern: "/admin/**"},
		},
		{input: "block GET /foo", wantErr: true},       // invalid action
		{input: "allow /foo", wantErr: true},            // missing method
		{input: "allow GET", wantErr: true},             // missing path
		{input: "", wantErr: true},                       // empty
		{input: "allow GET foo", wantErr: true},          // path must start with /
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseRule(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace && go test ./internal/config/ -run TestParseRule -v`
Expected: FAIL — `ParseRule` undefined

- [ ] **Step 3: Implement Rule type and ParseRule**

```go
// internal/config/rules.go
package config

import (
	"fmt"
	"strings"
)

// Rule represents a parsed HTTP request rule (e.g., "allow GET /repos/*").
type Rule struct {
	Action      string // "allow" or "deny"
	Method      string // HTTP method or "*"
	PathPattern string // glob path pattern starting with "/"
}

// HostRules holds the parsed rules for a single host entry.
type HostRules struct {
	Host  string // host pattern (e.g., "api.github.com", "*.example.com")
	Rules []Rule // ordered rules; empty means host-level allow/deny only
}

// ParseRule parses a rule string like "allow GET /repos/*" into a Rule.
func ParseRule(s string) (Rule, error) {
	s = strings.TrimSpace(s)
	parts := strings.Fields(s)
	if len(parts) != 3 {
		return Rule{}, fmt.Errorf("invalid rule %q: expected \"<allow|deny> <method> <path>\"", s)
	}

	action := strings.ToLower(parts[0])
	if action != "allow" && action != "deny" {
		return Rule{}, fmt.Errorf("invalid action %q in rule %q: must be \"allow\" or \"deny\"", parts[0], s)
	}

	method := strings.ToUpper(parts[1])

	path := parts[2]
	if !strings.HasPrefix(path, "/") {
		return Rule{}, fmt.Errorf("invalid path %q in rule %q: must start with \"/\"", path, s)
	}

	return Rule{
		Action:      action,
		Method:      method,
		PathPattern: path,
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /workspace && go test ./internal/config/ -run TestParseRule -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/rules.go internal/config/rules_test.go
git commit -m "feat(config): add Rule type and ParseRule function"
```

### Task 2: Implement path matching

**Files:**
- Create: `internal/proxy/pathmatch.go`
- Test: `internal/proxy/pathmatch_test.go`

- [ ] **Step 1: Write failing tests for path matching**

```go
// internal/proxy/pathmatch_test.go
package proxy

import "testing"

func TestMatchPath(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// Exact match
		{"/user", "/user", true},
		{"/user", "/user/foo", false},

		// Single-segment wildcard
		{"/repos/*", "/repos/foo", true},
		{"/repos/*", "/repos/foo/bar", false},
		{"/repos/*/pulls", "/repos/foo/pulls", true},
		{"/repos/*/pulls", "/repos/foo/bar/pulls", false},
		{"/*", "/anything", true},
		{"/*", "/", false},

		// Multi-segment wildcard
		{"/repos/**", "/repos/foo", true},
		{"/repos/**", "/repos/foo/bar/baz", true},
		{"/repos/**", "/repos", false},
		{"/admin/**", "/admin/users/123", true},
		{"/**", "/anything/at/all", true},
		{"/**", "/", true},

		// Mixed wildcards
		{"/repos/*/issues/**", "/repos/foo/issues/123/comments", true},
		{"/repos/*/issues/**", "/repos/foo/bar/issues/123", false},

		// Normalization
		{"/repos/*", "/repos/foo/", true},   // trailing slash stripped
		{"/repos/*", "//repos//foo", true},   // double slashes collapsed

		// Root path
		{"/", "/", true},
		{"/", "/foo", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_vs_"+tt.path, func(t *testing.T) {
			got := MatchPath(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("MatchPath(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace && go test ./internal/proxy/ -run TestMatchPath -v`
Expected: FAIL — `MatchPath` undefined

- [ ] **Step 3: Implement MatchPath**

```go
// internal/proxy/pathmatch.go
package proxy

import (
	"path"
	"strings"
)

// MatchPath checks if a request path matches a pattern.
// Patterns support:
//   - "*"  matches a single path segment
//   - "**" matches zero or more path segments
//
// Paths are normalized before matching (double slashes collapsed,
// trailing slashes removed, dot segments resolved).
// Query strings should be stripped before calling this function.
func MatchPath(pattern, reqPath string) bool {
	pattern = normalizePath(pattern)
	reqPath = normalizePath(reqPath)

	patternParts := splitPath(pattern)
	pathParts := splitPath(reqPath)

	return matchParts(patternParts, pathParts)
}

// normalizePath cleans a path: collapses double slashes, resolves dots,
// removes trailing slash (except root "/").
func normalizePath(p string) string {
	p = path.Clean(p)
	if p == "" || p == "." {
		return "/"
	}
	return p
}

// splitPath splits a cleaned path into segments.
// "/" returns an empty slice.
// "/foo/bar" returns ["foo", "bar"].
func splitPath(p string) []string {
	if p == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(p, "/"), "/")
}

// matchParts recursively matches pattern parts against path parts.
func matchParts(pattern, path []string) bool {
	for len(pattern) > 0 {
		seg := pattern[0]
		pattern = pattern[1:]

		if seg == "**" {
			// ** at end matches everything remaining
			if len(pattern) == 0 {
				return true
			}
			// Try matching rest of pattern at every position
			for i := 0; i <= len(path); i++ {
				if matchParts(pattern, path[i:]) {
					return true
				}
			}
			return false
		}

		// Need at least one path segment for * or literal
		if len(path) == 0 {
			return false
		}

		if seg == "*" {
			// Matches exactly one segment
			path = path[1:]
			continue
		}

		// Literal match
		if !strings.EqualFold(seg, path[0]) {
			return false
		}
		path = path[1:]
	}

	// Pattern consumed — path must also be consumed
	return len(path) == 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /workspace && go test ./internal/proxy/ -run TestMatchPath -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/pathmatch.go internal/proxy/pathmatch_test.go
git commit -m "feat(proxy): add path matching with * and ** wildcards"
```

---

## Chunk 2: Config Parsing (network.rules YAML)

### Task 3: Replace `Allow` with `Rules` on NetworkConfig

**Files:**
- Modify: `internal/config/config.go:178-182` (NetworkConfig struct)
- Modify: `internal/config/config.go:452-461` (validation logic)
- Modify: `internal/config/config.go:685-686` (DefaultConfig)
- Test: `internal/config/config_test.go`

The YAML structure for `network.rules` is a list where each entry is either:
- A plain string `"host.com"` (host-only, no sub-rules)
- A map with one key `"host.com": [list of rule strings]`

This requires custom YAML unmarshaling because `yaml.v3` doesn't natively support mixed-type lists.

- [ ] **Step 1: Write failing tests for rules YAML parsing**

```go
// Add to internal/config/rules_test.go

func TestNetworkRulesYAML(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    []HostRules
		wantErr string
	}{
		{
			name: "plain host string",
			yaml: `
network:
  policy: strict
  rules:
    - "api.github.com"
`,
			want: []HostRules{
				{Host: "api.github.com"},
			},
		},
		{
			name: "host with rules",
			yaml: `
network:
  policy: strict
  rules:
    - "api.github.com":
        - "allow GET /repos/*"
        - "deny DELETE /*"
`,
			want: []HostRules{
				{
					Host: "api.github.com",
					Rules: []Rule{
						{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
						{Action: "deny", Method: "DELETE", PathPattern: "/*"},
					},
				},
			},
		},
		{
			name: "mixed entries",
			yaml: `
network:
  policy: strict
  rules:
    - "registry.npmjs.org"
    - "api.github.com":
        - "allow GET /repos/*"
`,
			want: []HostRules{
				{Host: "registry.npmjs.org"},
				{
					Host: "api.github.com",
					Rules: []Rule{
						{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
					},
				},
			},
		},
		{
			name: "old allow field is error",
			yaml: `
network:
  policy: strict
  allow:
    - "api.github.com"
`,
			wantErr: "network.allow",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write yaml to temp file, load with config.Load
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(tt.yaml), 0644)
			cfg, err := Load(dir)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(cfg.Network.Rules) != len(tt.want) {
				t.Fatalf("got %d rules, want %d", len(cfg.Network.Rules), len(tt.want))
			}
			for i, got := range cfg.Network.Rules {
				w := tt.want[i]
				if got.Host != w.Host {
					t.Errorf("rules[%d].Host = %q, want %q", i, got.Host, w.Host)
				}
				if len(got.Rules) != len(w.Rules) {
					t.Errorf("rules[%d] has %d rules, want %d", i, len(got.Rules), len(w.Rules))
					continue
				}
				for j, r := range got.Rules {
					if r != w.Rules[j] {
						t.Errorf("rules[%d].Rules[%d] = %+v, want %+v", i, j, r, w.Rules[j])
					}
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace && go test ./internal/config/ -run TestNetworkRulesYAML -v`
Expected: FAIL — `Rules` field doesn't exist on `NetworkConfig`

- [ ] **Step 3: Update NetworkConfig struct and add custom YAML unmarshaling**

In `internal/config/rules.go`, add the `UnmarshalYAML` method for `NetworkRuleEntry` (the YAML representation) and update `NetworkConfig`:

```go
// Add to internal/config/rules.go

// NetworkRuleEntry is the YAML representation of a single entry in network.rules.
// It handles both plain host strings and host-with-rules maps.
type NetworkRuleEntry struct {
	HostRules
}

// UnmarshalYAML handles both "host" strings and {"host": ["rule", ...]} maps.
func (e *NetworkRuleEntry) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		// Plain host string: "api.github.com"
		e.Host = value.Value
		return nil

	case yaml.MappingNode:
		// Map with one key: "api.github.com": ["allow GET /repos/*", ...]
		if len(value.Content) != 2 {
			return fmt.Errorf("network.rules entry must have exactly one host key, got %d", len(value.Content)/2)
		}
		e.Host = value.Content[0].Value

		// Parse the list of rule strings
		var ruleStrings []string
		if err := value.Content[1].Decode(&ruleStrings); err != nil {
			return fmt.Errorf("network.rules[%s]: %w", e.Host, err)
		}
		for _, rs := range ruleStrings {
			rule, err := ParseRule(rs)
			if err != nil {
				return fmt.Errorf("network.rules[%s]: %w", e.Host, err)
			}
			e.Rules = append(e.Rules, rule)
		}
		return nil

	default:
		return fmt.Errorf("network.rules entry must be a string or map, got %v", value.Kind)
	}
}
```

Then update `NetworkConfig` in `internal/config/config.go`:

```go
// Replace the existing NetworkConfig (lines 178-182):
type NetworkConfig struct {
	Policy string             `yaml:"policy,omitempty"`
	Allow  []string           `yaml:"allow,omitempty"` // deprecated: hard error
	Rules  []NetworkRuleEntry `yaml:"rules,omitempty"`
}
```

Keep the `Allow` field so we can detect it and return a clear error.

- [ ] **Step 4: Add validation in Load() for deprecated `allow` field**

In `internal/config/config.go`, after the existing network policy validation (around line 461), add:

```go
	// Reject deprecated network.allow field
	if len(cfg.Network.Allow) > 0 {
		return nil, fmt.Errorf("\"network.allow\" is no longer supported, use \"network.rules\" instead\n\nExample:\n  network:\n    rules:\n      - \"api.github.com\"")
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /workspace && go test ./internal/config/ -run TestNetworkRulesYAML -v`
Expected: PASS

- [ ] **Step 6: Run all config tests to check for regressions**

Run: `cd /workspace && go test ./internal/config/ -v`
Expected: PASS (existing tests that use `network.allow` will need updating — fix any that break)

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/rules.go internal/config/rules_test.go
git commit -m "feat(config): replace network.allow with network.rules"
```

---

## Chunk 3: Proxy Rule Evaluation

### Task 4: Add rule evaluation to the proxy

**Files:**
- Create: `internal/proxy/rules.go`
- Test: `internal/proxy/rules_test.go`

- [ ] **Step 1: Write failing tests for rule evaluation**

```go
// internal/proxy/rules_test.go
package proxy

import (
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestEvaluateRules(t *testing.T) {
	rules := []config.Rule{
		{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
		{Action: "allow", Method: "POST", PathPattern: "/repos/*/pulls"},
		{Action: "deny", Method: "DELETE", PathPattern: "/*"},
	}

	tests := []struct {
		name     string
		method   string
		path     string
		want     string // "allow", "deny", or "" (no match)
	}{
		{"GET repos matches", "GET", "/repos/foo", "allow"},
		{"POST pulls matches", "POST", "/repos/foo/pulls", "allow"},
		{"DELETE blocked", "DELETE", "/repos/foo", "deny"},
		{"PUT no match", "PUT", "/repos/foo", ""},
		{"GET other path no match", "GET", "/user", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateRules(rules, tt.method, tt.path)
			if got != tt.want {
				t.Errorf("EvaluateRules(%q, %q) = %q, want %q", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestEvaluateRulesWildcardMethod(t *testing.T) {
	rules := []config.Rule{
		{Action: "deny", Method: "*", PathPattern: "/admin/**"},
		{Action: "allow", Method: "*", PathPattern: "/*"},
	}

	tests := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{"admin blocked", "GET", "/admin/users", "deny"},
		{"admin POST blocked", "POST", "/admin/settings", "deny"},
		{"other allowed", "GET", "/api", "allow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateRules(rules, tt.method, tt.path)
			if got != tt.want {
				t.Errorf("EvaluateRules(%q, %q) = %q, want %q", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestEvaluateRulesFirstMatchWins(t *testing.T) {
	// First rule allows GET /repos/*, second denies GET /*.
	// GET /repos/foo should be allowed (first match wins).
	rules := []config.Rule{
		{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
		{Action: "deny", Method: "GET", PathPattern: "/*"},
	}

	got := EvaluateRules(rules, "GET", "/repos/foo")
	if got != "allow" {
		t.Errorf("expected first-match-wins allow, got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace && go test ./internal/proxy/ -run TestEvaluateRules -v`
Expected: FAIL — `EvaluateRules` undefined

- [ ] **Step 3: Implement EvaluateRules**

```go
// internal/proxy/rules.go
package proxy

import (
	"strings"

	"github.com/majorcontext/moat/internal/config"
)

// EvaluateRules evaluates an ordered list of rules against a request method and path.
// Returns "allow", "deny", or "" (no rule matched — fall through to policy default).
// First matching rule wins. Query strings should be stripped from path before calling.
func EvaluateRules(rules []config.Rule, method, path string) string {
	// Strip query string if present
	if idx := strings.IndexByte(path, '?'); idx != -1 {
		path = path[:idx]
	}

	for _, rule := range rules {
		if matchesRule(rule, method, path) {
			return rule.Action
		}
	}
	return ""
}

// matchesRule checks if a single rule matches the given method and path.
func matchesRule(rule config.Rule, method, path string) bool {
	// Check method
	if rule.Method != "*" && !strings.EqualFold(rule.Method, method) {
		return false
	}

	// Check path
	return MatchPath(rule.PathPattern, path)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /workspace && go test ./internal/proxy/ -run TestEvaluateRules -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/rules.go internal/proxy/rules_test.go
git commit -m "feat(proxy): add rule evaluation with first-match-wins semantics"
```

### Task 5: Integrate rule checking into proxy request handling

**Files:**
- Modify: `internal/proxy/proxy.go` (RunContextData, checkNetworkPolicy, writeBlockedResponse)
- Modify: `internal/proxy/proxy.go` (Proxy struct — add rules field)
- Test: `internal/proxy/proxy_test.go`

- [ ] **Step 1: Write failing test for proxy rule enforcement**

```go
// Add to internal/proxy/proxy_test.go (or a new file internal/proxy/rules_integration_test.go)

func TestProxyRuleEnforcement(t *testing.T) {
	p := NewProxy()
	p.SetNetworkPolicyWithRules("strict", nil, nil,
		[]config.HostRules{
			{
				Host: "api.github.com",
				Rules: []config.Rule{
					{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
					{Action: "deny", Method: "DELETE", PathPattern: "/*"},
				},
			},
			{Host: "registry.npmjs.org"}, // no sub-rules, host-level allow
		},
	)

	tests := []struct {
		name    string
		host    string
		port    int
		method  string
		path    string
		allowed bool
	}{
		{"allowed by rule", "api.github.com", 443, "GET", "/repos/foo", true},
		{"denied by rule", "api.github.com", 443, "DELETE", "/repos/foo", false},
		{"no rule match falls to strict deny", "api.github.com", 443, "PUT", "/user", false},
		{"host-only entry allows all", "registry.npmjs.org", 443, "DELETE", "/anything", true},
		{"unlisted host denied", "evil.com", 443, "GET", "/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.checkRequestRule(tt.host, tt.port, tt.method, tt.path)
			if got != tt.allowed {
				t.Errorf("checkRequestRule(%s:%d %s %s) = %v, want %v",
					tt.host, tt.port, tt.method, tt.path, got, tt.allowed)
			}
		})
	}
}

func TestProxyRulePermissiveMode(t *testing.T) {
	p := NewProxy()
	p.SetNetworkPolicyWithRules("permissive", nil, nil,
		[]config.HostRules{
			{
				Host: "api.github.com",
				Rules: []config.Rule{
					{Action: "deny", Method: "DELETE", PathPattern: "/*"},
				},
			},
		},
	)

	tests := []struct {
		name    string
		host    string
		port    int
		method  string
		path    string
		allowed bool
	}{
		{"deny rule blocks", "api.github.com", 443, "DELETE", "/repos/foo", false},
		{"no match falls to permissive allow", "api.github.com", 443, "GET", "/repos/foo", true},
		{"unlisted host allowed (permissive)", "other.com", 443, "GET", "/", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.checkRequestRule(tt.host, tt.port, tt.method, tt.path)
			if got != tt.allowed {
				t.Errorf("checkRequestRule(%s:%d %s %s) = %v, want %v",
					tt.host, tt.port, tt.method, tt.path, got, tt.allowed)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace && go test ./internal/proxy/ -run TestProxyRule -v`
Expected: FAIL — `SetNetworkPolicyWithRules` and `checkRequestRule` undefined

- [ ] **Step 3: Add rules to Proxy struct and implement checkRequestRule**

Add to the `Proxy` struct in `internal/proxy/proxy.go`:

```go
// Add field to Proxy struct (around line 277):
	hostRules []config.HostRules // per-host request rules
```

Add the `SetNetworkPolicyWithRules` method and `checkRequestRule`:

```go
// SetNetworkPolicyWithRules sets the network policy with per-host request rules.
// This replaces SetNetworkPolicy for the new rules-based config.
func (p *Proxy) SetNetworkPolicyWithRules(policy string, allows []string, grants []string, rules []config.HostRules) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.policy = policy
	p.allowedHosts = nil
	p.hostRules = rules

	// Parse host patterns from rules entries
	for _, hr := range rules {
		p.allowedHosts = append(p.allowedHosts, parseHostPattern(hr.Host))
	}

	// Legacy: parse explicit allow patterns (for grant-implied hosts)
	for _, pattern := range allows {
		p.allowedHosts = append(p.allowedHosts, parseHostPattern(pattern))
	}

	// Add hosts from grants
	for _, grant := range grants {
		gh := GetHostsForGrant(grant)
		for _, pattern := range gh {
			p.allowedHosts = append(p.allowedHosts, parseHostPattern(pattern))
		}
	}
}

// checkRequestRule checks if a specific request (method + path) to a host:port is allowed.
// Evaluates host-level rules first, then falls through to policy default.
func (p *Proxy) checkRequestRule(host string, port int, method, path string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return checkRequestRuleInternal(p.policy, p.allowedHosts, p.hostRules, host, port, method, path)
}

// checkRequestRuleInternal is the shared implementation for both proxy-level and per-run rule checking.
func checkRequestRuleInternal(policy string, allowedHosts []hostPattern, hostRules []config.HostRules, host string, port int, method, path string) bool {
	// Find matching host rules entry
	for _, hr := range hostRules {
		hp := parseHostPattern(hr.Host)
		if !matchesPattern(hp, host, port) {
			continue
		}

		// Host matched. If no sub-rules, it's a host-level allow.
		if len(hr.Rules) == 0 {
			return true
		}

		// Evaluate sub-rules (first match wins)
		result := EvaluateRules(hr.Rules, method, path)
		switch result {
		case "allow":
			return true
		case "deny":
			return false
		default:
			// No rule matched — fall through to policy default
		}
	}

	// No host entry matched, or host matched but no rule matched.
	// Check if host is allowed by grant-implied patterns (no sub-rules).
	if matchHost(allowedHosts, host, port) {
		// Host is in allowed list (from grants or rule entries without sub-rules).
		// But we need to check: was it from a rules entry with sub-rules that didn't match?
		// If so, fall through to policy default instead of allowing.
		for _, hr := range hostRules {
			hp := parseHostPattern(hr.Host)
			if matchesPattern(hp, host, port) && len(hr.Rules) > 0 {
				// This host has rules but none matched — fall through to policy
				break
			}
		}
	}

	// Fall through to policy default
	if policy != "strict" {
		return true // permissive: allow by default
	}

	// Strict: only allow if host is in allowed list AND has no sub-rules
	// (sub-rules case was handled above)
	return matchHost(allowedHosts, host, port) && !hostHasRules(hostRules, allowedHosts, host, port)
}

// hostHasRules checks if any hostRules entry with sub-rules matches this host.
func hostHasRules(rules []config.HostRules, patterns []hostPattern, host string, port int) bool {
	for _, hr := range rules {
		if len(hr.Rules) == 0 {
			continue
		}
		hp := parseHostPattern(hr.Host)
		if matchesPattern(hp, host, port) {
			return true
		}
	}
	return false
}
```

Note: The above logic is intentionally written to be clear rather than optimal. The key flow is:
1. Find matching host rules entry → evaluate sub-rules → first match wins
2. No match on sub-rules → fall through to policy default
3. No host entry → fall through to policy default (which checks allowedHosts for strict mode)

**Simplify:** After testing, the implementer should review and simplify `checkRequestRuleInternal` if the logic can be made cleaner. The test cases define the expected behavior; the implementation just needs to satisfy them.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /workspace && go test ./internal/proxy/ -run TestProxyRule -v`
Expected: PASS

- [ ] **Step 5: Integrate into handleHTTP and handleConnect**

In `handleHTTP` (around line 964 in proxy.go), replace the `checkNetworkPolicyForRequest` call with request-level rule checking. The method and path are available from `r.Method` and `r.URL.Path`.

In `checkNetworkPolicyForRequest`, add method/path parameters and use `checkRequestRuleInternal` when the run context has rules:

```go
// Update checkNetworkPolicyForRequest to accept method and path:
func (p *Proxy) checkNetworkPolicyForRequest(r *http.Request, host string, port int, method, path string) bool {
	if rc := getRunContext(r); rc != nil {
		return checkRequestRuleInternal(rc.Policy, rc.AllowedHosts, rc.HostRules, host, port, method, path)
	}
	// Proxy-level check
	if len(p.hostRules) > 0 {
		return p.checkRequestRule(host, port, method, path)
	}
	return p.checkNetworkPolicy(host, port)
}
```

Update call sites in `handleHTTP` and `handleConnect` to pass method and path.

For `handleConnect`, the method and path aren't available until after TLS interception. The host-level check still happens at CONNECT time. After TLS interception, when the inner HTTP request is read, the method+path check happens there. Look at the existing code flow for where inner requests are handled after CONNECT.

- [ ] **Step 6: Update writeBlockedResponse to include rule attribution**

```go
func (p *Proxy) writeBlockedResponse(w http.ResponseWriter, host string, matchedRule ...string) {
	w.Header().Set("X-Moat-Blocked", "request-rule")
	w.Header().Set("Proxy-Authenticate", "Moat-Policy")
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusProxyAuthRequired)
	msg := "Moat: request blocked by network policy.\n"
	if len(matchedRule) > 0 && matchedRule[0] != "" {
		msg += fmt.Sprintf("Rule: %s\n", matchedRule[0])
	}
	msg += fmt.Sprintf("Host: %s\n", host)
	msg += "To allow this request, update network.rules in moat.yaml.\n"
	_, _ = w.Write([]byte(msg))
}
```

- [ ] **Step 7: Run all proxy tests**

Run: `cd /workspace && go test ./internal/proxy/ -v`
Expected: PASS (fix any regressions)

- [ ] **Step 8: Commit**

```bash
git add internal/proxy/proxy.go internal/proxy/rules.go internal/proxy/rules_test.go
git commit -m "feat(proxy): integrate request rules into proxy enforcement"
```

---

## Chunk 4: Plumbing Through Daemon and Run Manager

### Task 6: Add rules to RunContextData and daemon API

**Files:**
- Modify: `internal/proxy/proxy.go` (RunContextData struct — add HostRules field)
- Modify: `internal/daemon/runcontext.go` (RunContext — add rules, update ToProxyContextData)
- Modify: `internal/daemon/api.go` (RegisterRequest — add rules field)
- Modify: `internal/run/manager.go` (pass rules instead of allow list)

- [ ] **Step 1: Add HostRules to RunContextData**

In `internal/proxy/proxy.go`, add to `RunContextData`:

```go
	HostRules []config.HostRules
```

- [ ] **Step 2: Add NetworkRules to daemon RunContext and RegisterRequest**

In `internal/daemon/runcontext.go`, add field:

```go
	NetworkRules []config.HostRules `json:"network_rules,omitempty"`
```

In `internal/daemon/api.go`, add to `RegisterRequest`:

```go
	NetworkRules []config.HostRules `json:"network_rules,omitempty"`
```

Update the registration handler in `api.go` to copy `NetworkRules`:

```go
	rc.NetworkRules = req.NetworkRules
```

- [ ] **Step 3: Update ToProxyContextData to pass rules**

In `internal/daemon/runcontext.go`, in `ToProxyContextData()`, add after the AllowedHosts conversion:

```go
	// Copy host rules.
	if len(rc.NetworkRules) > 0 {
		d.HostRules = make([]config.HostRules, len(rc.NetworkRules))
		copy(d.HostRules, rc.NetworkRules)
	}
```

Also update the AllowedHosts conversion to include hosts from rules entries:

```go
	// Convert allowed hosts from both NetworkAllow and NetworkRules.
	for _, host := range rc.NetworkAllow {
		d.AllowedHosts = append(d.AllowedHosts, proxy.ParseHostPattern(host))
	}
	for _, hr := range rc.NetworkRules {
		d.AllowedHosts = append(d.AllowedHosts, proxy.ParseHostPattern(hr.Host))
	}
```

- [ ] **Step 4: Update run manager to pass rules**

In `internal/run/manager.go`, around line 646-648, change:

```go
		// Before:
		runCtx.NetworkPolicy = opts.Config.Network.Policy
		runCtx.NetworkAllow = opts.Config.Network.Allow

		// After:
		runCtx.NetworkPolicy = opts.Config.Network.Policy
		// Convert NetworkRuleEntry to HostRules for the daemon
		for _, entry := range opts.Config.Network.Rules {
			runCtx.NetworkRules = append(runCtx.NetworkRules, entry.HostRules)
		}
```

Also update `buildRegisterRequest` (around line 3288) to pass `NetworkRules`:

```go
		NetworkRules:  rc.NetworkRules,
```

- [ ] **Step 5: Update SetNetworkPolicy callers**

Find all callers of `SetNetworkPolicy` on the proxy (non-daemon mode) and update them to use `SetNetworkPolicyWithRules` or pass rules through the existing path. Search for `SetNetworkPolicy(` to find all call sites.

- [ ] **Step 6: Build and run tests**

Run: `cd /workspace && go build ./... && go test ./internal/proxy/ ./internal/daemon/ ./internal/run/ -v -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/proxy/proxy.go internal/daemon/runcontext.go internal/daemon/api.go internal/run/manager.go
git commit -m "feat(daemon): plumb request rules through daemon and run manager"
```

---

## Chunk 5: Update Existing Tests and Fix Regressions

### Task 7: Update all references to network.Allow

**Files:**
- Search for all references to `Network.Allow`, `network.allow`, `NetworkAllow` across the codebase
- Update tests, examples, and any other code that references the old field

- [ ] **Step 1: Find all references**

Run: `grep -r "Network\.Allow\|network\.allow\|NetworkAllow\|\.Allow " --include="*.go" /workspace/internal/`

- [ ] **Step 2: Update each reference**

For each file found, update to use `Network.Rules` / `NetworkRules` as appropriate. Keep `NetworkAllow` on the daemon structs as a deprecated-but-present field for backwards compatibility with older daemon processes (per the backwards-compat rule in CLAUDE.md), but populate it from rules for old daemons.

- [ ] **Step 3: Run full test suite**

Run: `cd /workspace && make test-unit`
Expected: PASS

- [ ] **Step 4: Run linter**

Run: `cd /workspace && make lint`
Expected: PASS (or `go vet ./...` if golangci-lint not installed)

- [ ] **Step 5: Commit**

```bash
git add -u
git commit -m "refactor: update all references from network.allow to network.rules"
```

---

## Chunk 6: Documentation and Examples

### Task 8: Update documentation

**Files:**
- Modify: `docs/content/reference/02-moat-yaml.md`
- Modify: `docs/content/concepts/05-networking.md`
- Modify: `examples/firewall/moat.yaml`

- [ ] **Step 1: Update moat.yaml reference docs**

Add the `network.rules` syntax to `docs/content/reference/02-moat-yaml.md`, replacing the `network.allow` documentation. Include examples of plain host entries, host-with-rules entries, and both strict and permissive modes.

- [ ] **Step 2: Update networking concepts doc**

Add a section to `docs/content/concepts/05-networking.md` explaining rule evaluation: first match wins, fall-through to policy default, and the relationship between host-level and request-level rules.

- [ ] **Step 3: Update firewall example**

Update `examples/firewall/moat.yaml` to demonstrate the new rules syntax.

- [ ] **Step 4: Commit**

```bash
git add docs/content/reference/02-moat-yaml.md docs/content/concepts/05-networking.md examples/firewall/moat.yaml
git commit -m "docs: update networking docs for request rules"
```

### Task 9: Final verification

- [ ] **Step 1: Run full test suite**

Run: `cd /workspace && make test-unit`
Expected: PASS

- [ ] **Step 2: Run linter**

Run: `cd /workspace && make lint`
Expected: PASS

- [ ] **Step 3: Build**

Run: `cd /workspace && go build ./...`
Expected: success
