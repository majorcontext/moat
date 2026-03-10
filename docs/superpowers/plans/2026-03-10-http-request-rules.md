# HTTP Request Rules Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-host HTTP method + path rules to Moat's network policy, replacing the host-only `allow` list with expressive `allow`/`deny` rules.

**Architecture:** All rule logic lives in a new `internal/netrules` package: types, parsing, path matching, and evaluation. Config imports it for YAML unmarshaling. Proxy calls a single `netrules.Check()` function. Daemon and run manager pass rules through as opaque data.

**Tech Stack:** Go, YAML parsing (gopkg.in/yaml.v3), existing proxy infrastructure

**Spec:** `docs/superpowers/specs/2026-03-10-http-request-rules-design.md`

---

## File Structure

**New package — `internal/netrules/`:**
- `rule.go` — `Rule`, `HostRules` types, `ParseRule()` function
- `pathmatch.go` — `MatchPath()` with `*`/`**` glob support
- `evaluate.go` — `EvaluateRules()`, `Check()` (the single entry point for proxy)
- `yaml.go` — `NetworkRuleEntry` with custom `UnmarshalYAML`
- `rule_test.go` — tests for parsing
- `pathmatch_test.go` — tests for path matching
- `evaluate_test.go` — tests for rule evaluation and `Check()`

**Modified files (minimal wiring):**
- `internal/config/config.go` — `NetworkConfig` uses `netrules.NetworkRuleEntry`, `Allow` becomes hard error
- `internal/proxy/proxy.go` — `checkNetworkPolicyForRequest` calls `netrules.Check()`; `RunContextData` gets `HostRules` field
- `internal/daemon/runcontext.go` — `RunContext` gets `NetworkRules` field, `ToProxyContextData` passes it
- `internal/daemon/api.go` — `RegisterRequest` gets `NetworkRules` field
- `internal/run/manager.go` — passes rules from config to daemon context

---

## Chunk 1: The netrules Package (Types, Parsing, Path Matching)

### Task 1: Rule types and parsing

**Files:**
- Create: `internal/netrules/rule.go`
- Create: `internal/netrules/rule_test.go`

- [ ] **Step 1: Write failing tests for rule parsing**

```go
// internal/netrules/rule_test.go
package netrules

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

Run: `cd /workspace && go test ./internal/netrules/ -run TestParseRule -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Implement Rule type and ParseRule**

```go
// internal/netrules/rule.go
package netrules

import (
	"fmt"
	"strings"
)

// Rule represents a parsed HTTP request rule (e.g., "allow GET /repos/*").
type Rule struct {
	Action      string `json:"action"`       // "allow" or "deny"
	Method      string `json:"method"`       // HTTP method or "*"
	PathPattern string `json:"path_pattern"` // glob path pattern starting with "/"
}

// HostRules holds the parsed rules for a single host entry.
type HostRules struct {
	Host  string `json:"host"`            // host pattern (e.g., "api.github.com", "*.example.com")
	Rules []Rule `json:"rules,omitempty"` // ordered rules; empty means host-level allow/deny only
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

Run: `cd /workspace && go test ./internal/netrules/ -run TestParseRule -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/netrules/rule.go internal/netrules/rule_test.go
git commit -m "feat(netrules): add Rule type and ParseRule function"
```

### Task 2: Path matching

**Files:**
- Create: `internal/netrules/pathmatch.go`
- Create: `internal/netrules/pathmatch_test.go`

- [ ] **Step 1: Write failing tests for path matching**

```go
// internal/netrules/pathmatch_test.go
package netrules

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

Run: `cd /workspace && go test ./internal/netrules/ -run TestMatchPath -v`
Expected: FAIL — `MatchPath` undefined

- [ ] **Step 3: Implement MatchPath**

```go
// internal/netrules/pathmatch.go
package netrules

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
func matchParts(pattern, reqPath []string) bool {
	for len(pattern) > 0 {
		seg := pattern[0]
		pattern = pattern[1:]

		if seg == "**" {
			// ** at end matches everything remaining
			if len(pattern) == 0 {
				return true
			}
			// Try matching rest of pattern at every position
			for i := 0; i <= len(reqPath); i++ {
				if matchParts(pattern, reqPath[i:]) {
					return true
				}
			}
			return false
		}

		// Need at least one path segment for * or literal
		if len(reqPath) == 0 {
			return false
		}

		if seg == "*" {
			// Matches exactly one segment
			reqPath = reqPath[1:]
			continue
		}

		// Literal match
		if !strings.EqualFold(seg, reqPath[0]) {
			return false
		}
		reqPath = reqPath[1:]
	}

	// Pattern consumed — path must also be consumed
	return len(reqPath) == 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /workspace && go test ./internal/netrules/ -run TestMatchPath -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/netrules/pathmatch.go internal/netrules/pathmatch_test.go
git commit -m "feat(netrules): add path matching with * and ** wildcards"
```

### Task 3: YAML unmarshaling for network.rules entries

**Files:**
- Create: `internal/netrules/yaml.go`
- Create: `internal/netrules/yaml_test.go`

- [ ] **Step 1: Write failing tests for YAML unmarshaling**

```go
// internal/netrules/yaml_test.go
package netrules

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNetworkRuleEntryUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    NetworkRuleEntry
		wantErr bool
	}{
		{
			name: "plain host string",
			yaml: `"api.github.com"`,
			want: NetworkRuleEntry{HostRules: HostRules{Host: "api.github.com"}},
		},
		{
			name: "host with rules",
			yaml: `"api.github.com":
  - "allow GET /repos/*"
  - "deny DELETE /*"`,
			want: NetworkRuleEntry{HostRules: HostRules{
				Host: "api.github.com",
				Rules: []Rule{
					{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
					{Action: "deny", Method: "DELETE", PathPattern: "/*"},
				},
			}},
		},
		{
			name:    "invalid rule string",
			yaml:    `"api.github.com": ["block GET /foo"]`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got NetworkRuleEntry
			err := yaml.Unmarshal([]byte(tt.yaml), &got)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Host != tt.want.Host {
				t.Errorf("Host = %q, want %q", got.Host, tt.want.Host)
			}
			if len(got.Rules) != len(tt.want.Rules) {
				t.Fatalf("got %d rules, want %d", len(got.Rules), len(tt.want.Rules))
			}
			for i, r := range got.Rules {
				if r != tt.want.Rules[i] {
					t.Errorf("Rules[%d] = %+v, want %+v", i, r, tt.want.Rules[i])
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace && go test ./internal/netrules/ -run TestNetworkRuleEntry -v`
Expected: FAIL — `NetworkRuleEntry` undefined

- [ ] **Step 3: Implement NetworkRuleEntry with UnmarshalYAML**

```go
// internal/netrules/yaml.go
package netrules

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// NetworkRuleEntry is the YAML representation of a single entry in network.rules.
// It handles both plain host strings and host-with-rules maps.
type NetworkRuleEntry struct {
	HostRules
}

// UnmarshalYAML handles both "host" strings and {"host": ["rule", ...]} maps.
func (e *NetworkRuleEntry) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		e.Host = value.Value
		return nil

	case yaml.MappingNode:
		if len(value.Content) != 2 {
			return fmt.Errorf("network.rules entry must have exactly one host key, got %d", len(value.Content)/2)
		}
		e.Host = value.Content[0].Value

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

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /workspace && go test ./internal/netrules/ -run TestNetworkRuleEntry -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/netrules/yaml.go internal/netrules/yaml_test.go
git commit -m "feat(netrules): add YAML unmarshaling for mixed host/rules entries"
```

---

## Chunk 2: Rule Evaluation and Check()

### Task 4: Implement EvaluateRules and Check

**Files:**
- Create: `internal/netrules/evaluate.go`
- Create: `internal/netrules/evaluate_test.go`

`Check()` is the single entry point the proxy calls. It takes policy, host rules, host, port, method, path and returns allow/deny. It needs host pattern matching, which currently lives in `internal/proxy/hosts.go`. Rather than duplicating that, `Check()` accepts a `hostMatches func(hostPattern string, host string, port int) bool` callback, keeping netrules independent of the proxy package.

- [ ] **Step 1: Write failing tests for EvaluateRules**

```go
// internal/netrules/evaluate_test.go
package netrules

import "testing"

func TestEvaluateRules(t *testing.T) {
	rules := []Rule{
		{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
		{Action: "allow", Method: "POST", PathPattern: "/repos/*/pulls"},
		{Action: "deny", Method: "DELETE", PathPattern: "/*"},
	}

	tests := []struct {
		name   string
		method string
		path   string
		want   string // "allow", "deny", or "" (no match)
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
	rules := []Rule{
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
	rules := []Rule{
		{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
		{Action: "deny", Method: "GET", PathPattern: "/*"},
	}

	got := EvaluateRules(rules, "GET", "/repos/foo")
	if got != "allow" {
		t.Errorf("expected first-match-wins allow, got %q", got)
	}
}
```

- [ ] **Step 2: Write failing tests for Check**

```go
// Add to internal/netrules/evaluate_test.go

// exactHostMatch is a simple host matcher for testing.
func exactHostMatch(pattern, host string, port int) bool {
	return pattern == host && (port == 80 || port == 443)
}

func TestCheckStrict(t *testing.T) {
	hostRules := []HostRules{
		{
			Host: "api.github.com",
			Rules: []Rule{
				{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
				{Action: "deny", Method: "DELETE", PathPattern: "/*"},
			},
		},
		{Host: "registry.npmjs.org"}, // no sub-rules, host-level allow
	}

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
			got := Check("strict", hostRules, tt.host, tt.port, tt.method, tt.path, exactHostMatch)
			if got != tt.allowed {
				t.Errorf("Check() = %v, want %v", got, tt.allowed)
			}
		})
	}
}

func TestCheckPermissive(t *testing.T) {
	hostRules := []HostRules{
		{
			Host: "api.github.com",
			Rules: []Rule{
				{Action: "deny", Method: "DELETE", PathPattern: "/*"},
			},
		},
	}

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
		{"unlisted host allowed", "other.com", 443, "GET", "/", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Check("permissive", hostRules, tt.host, tt.port, tt.method, tt.path, exactHostMatch)
			if got != tt.allowed {
				t.Errorf("Check() = %v, want %v", got, tt.allowed)
			}
		})
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd /workspace && go test ./internal/netrules/ -run 'TestEvaluateRules|TestCheck' -v`
Expected: FAIL — `EvaluateRules` and `Check` undefined

- [ ] **Step 4: Implement EvaluateRules and Check**

```go
// internal/netrules/evaluate.go
package netrules

import "strings"

// HostMatcher checks if a host pattern matches a given host:port.
// This is provided by the caller (proxy package) to avoid importing proxy internals.
type HostMatcher func(pattern, host string, port int) bool

// EvaluateRules evaluates an ordered list of rules against a request method and path.
// Returns "allow", "deny", or "" (no rule matched — fall through to policy default).
// First matching rule wins.
func EvaluateRules(rules []Rule, method, path string) string {
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
func matchesRule(rule Rule, method, path string) bool {
	if rule.Method != "*" && !strings.EqualFold(rule.Method, method) {
		return false
	}
	return MatchPath(rule.PathPattern, path)
}

// Check is the single entry point for request-level rule evaluation.
// It determines whether a request to host:port with the given method and path
// is allowed under the given policy and rules.
//
// Evaluation order:
//  1. Find matching host entry using hostMatches
//  2. If host has no sub-rules → allowed (host-level entry)
//  3. If host has sub-rules → evaluate in order, first match wins
//  4. No rule match → fall through to policy default (strict=deny, permissive=allow)
//  5. No host entry → fall through to policy default
func Check(policy string, hostRules []HostRules, host string, port int, method, path string, hostMatches HostMatcher) bool {
	for _, hr := range hostRules {
		if !hostMatches(hr.Host, host, port) {
			continue
		}

		// Host matched
		if len(hr.Rules) == 0 {
			return true // host-level allow, no sub-rules
		}

		result := EvaluateRules(hr.Rules, method, path)
		switch result {
		case "allow":
			return true
		case "deny":
			return false
		}

		// No rule matched — fall through to policy default
		return policy != "strict"
	}

	// No host entry matched — fall through to policy default
	return policy != "strict"
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /workspace && go test ./internal/netrules/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/netrules/evaluate.go internal/netrules/evaluate_test.go
git commit -m "feat(netrules): add rule evaluation and Check entry point"
```

---

## Chunk 3: Config Integration

### Task 5: Wire netrules into config parsing

**Files:**
- Modify: `internal/config/config.go:178-182` (NetworkConfig struct)
- Modify: `internal/config/config.go:452-461` (validation)
- Modify: `internal/config/config.go:685-686` (DefaultConfig)
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests for config loading with rules**

```go
// Add to internal/config/config_test.go (or create internal/config/rules_test.go)

func TestNetworkRulesConfig(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantN   int    // number of rule entries
		wantErr string
	}{
		{
			name: "plain host",
			yaml: "network:\n  policy: strict\n  rules:\n    - \"api.github.com\"\n",
			wantN: 1,
		},
		{
			name: "host with rules",
			yaml: "network:\n  policy: strict\n  rules:\n    - \"api.github.com\":\n        - \"allow GET /repos/*\"\n",
			wantN: 1,
		},
		{
			name: "mixed",
			yaml: "network:\n  policy: strict\n  rules:\n    - \"npmjs.org\"\n    - \"api.github.com\":\n        - \"allow GET /repos/*\"\n",
			wantN: 2,
		},
		{
			name:    "old allow field errors",
			yaml:    "network:\n  policy: strict\n  allow:\n    - \"api.github.com\"\n",
			wantErr: "network.allow",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			if len(cfg.Network.Rules) != tt.wantN {
				t.Errorf("got %d rules, want %d", len(cfg.Network.Rules), tt.wantN)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace && go test ./internal/config/ -run TestNetworkRulesConfig -v`
Expected: FAIL — `Rules` field doesn't exist

- [ ] **Step 3: Update NetworkConfig**

In `internal/config/config.go`, replace lines 178-182:

```go
type NetworkConfig struct {
	Policy string                    `yaml:"policy,omitempty"`
	Allow  []string                  `yaml:"allow,omitempty"` // deprecated: hard error
	Rules  []netrules.NetworkRuleEntry `yaml:"rules,omitempty"`
}
```

Add import: `"github.com/majorcontext/moat/internal/netrules"`

- [ ] **Step 4: Add validation for deprecated allow field**

In `internal/config/config.go`, after the network policy validation (around line 461), add:

```go
	if len(cfg.Network.Allow) > 0 {
		return nil, fmt.Errorf("\"network.allow\" is no longer supported, use \"network.rules\" instead\n\nExample:\n  network:\n    rules:\n      - \"api.github.com\"")
	}
```

- [ ] **Step 5: Run tests**

Run: `cd /workspace && go test ./internal/config/ -v`
Expected: PASS (fix any existing tests that use `network.allow`)

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): wire netrules into NetworkConfig, deprecate network.allow"
```

---

## Chunk 4: Proxy Integration

### Task 6: Call netrules.Check from proxy

**Files:**
- Modify: `internal/proxy/proxy.go` (Proxy struct, RunContextData, checkNetworkPolicyForRequest)
- Test: `internal/proxy/proxy_test.go`

- [ ] **Step 1: Write failing tests for proxy with rules**

```go
// Add to internal/proxy/proxy_test.go

func TestProxyWithNetRules(t *testing.T) {
	p := NewProxy()
	rules := []netrules.HostRules{
		{
			Host: "api.github.com",
			Rules: []netrules.Rule{
				{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
				{Action: "deny", Method: "DELETE", PathPattern: "/*"},
			},
		},
		{Host: "registry.npmjs.org"},
	}
	p.SetNetworkPolicyWithRules("strict", nil, nil, rules)

	// Test via checkNetworkPolicyForRequest with method/path
	// (implementation detail — test the public behavior through the proxy)
}
```

The exact test shape depends on how `checkNetworkPolicyForRequest` is refactored. The key behavior to test: requests through the proxy are checked against rules. The implementer should write integration-style tests that send HTTP requests through the proxy and verify allow/deny responses.

- [ ] **Step 2: Add hostRules field and SetNetworkPolicyWithRules to Proxy**

In `internal/proxy/proxy.go`, add to `Proxy` struct:

```go
	hostRules []netrules.HostRules // per-host request rules
```

Add method:

```go
func (p *Proxy) SetNetworkPolicyWithRules(policy string, allows []string, grants []string, rules []netrules.HostRules) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.policy = policy
	p.allowedHosts = nil
	p.hostRules = rules

	for _, hr := range rules {
		p.allowedHosts = append(p.allowedHosts, parseHostPattern(hr.Host))
	}
	for _, pattern := range allows {
		p.allowedHosts = append(p.allowedHosts, parseHostPattern(pattern))
	}
	for _, grant := range grants {
		for _, pattern := range GetHostsForGrant(grant) {
			p.allowedHosts = append(p.allowedHosts, parseHostPattern(pattern))
		}
	}
}
```

- [ ] **Step 3: Add HostRules to RunContextData**

```go
type RunContextData struct {
	// ... existing fields ...
	HostRules []netrules.HostRules
}
```

- [ ] **Step 4: Update checkNetworkPolicyForRequest**

Refactor to call `netrules.Check` when rules are present:

```go
func (p *Proxy) checkNetworkPolicyForRequest(r *http.Request, host string, port int, method, path string) bool {
	if rc := getRunContext(r); rc != nil {
		if len(rc.HostRules) > 0 {
			return netrules.Check(rc.Policy, rc.HostRules, host, port, method, path, hostMatchAdapter(rc.AllowedHosts))
		}
		if rc.Policy != "strict" {
			return true
		}
		return matchHost(rc.AllowedHosts, host, port)
	}

	if len(p.hostRules) > 0 {
		p.mu.RLock()
		defer p.mu.RUnlock()
		return netrules.Check(p.policy, p.hostRules, host, port, method, path, hostMatchAdapter(p.allowedHosts))
	}
	return p.checkNetworkPolicy(host, port)
}

// hostMatchAdapter wraps the proxy's host matching for netrules.HostMatcher.
func hostMatchAdapter(patterns []hostPattern) netrules.HostMatcher {
	return func(pattern, host string, port int) bool {
		hp := parseHostPattern(pattern)
		return matchesPattern(hp, host, port)
	}
}
```

- [ ] **Step 5: Update handleHTTP call site**

In `handleHTTP` (around line 964), change:

```go
// Before:
if !p.checkNetworkPolicyForRequest(r, host, port) {

// After:
if !p.checkNetworkPolicyForRequest(r, host, port, r.Method, r.URL.Path) {
```

- [ ] **Step 6: Update handleConnect for HTTPS rule checking**

For CONNECT tunnels, host-level checking happens at CONNECT time (no method/path yet). After TLS interception, when the inner HTTP request is read (in the goroutine that handles the intercepted connection), add the method/path check. Search for where the inner `http.Request` is parsed after CONNECT and add rule checking there.

Pass `"CONNECT"` and `""` as method/path for the initial CONNECT check (which falls back to host-only matching since no rules will match an empty path — this preserves existing behavior for host-only entries).

- [ ] **Step 7: Update writeBlockedResponse for rule attribution**

```go
func (p *Proxy) writeBlockedResponse(w http.ResponseWriter, host string, detail ...string) {
	w.Header().Set("X-Moat-Blocked", "request-rule")
	w.Header().Set("Proxy-Authenticate", "Moat-Policy")
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusProxyAuthRequired)
	msg := "Moat: request blocked by network policy.\n"
	if len(detail) > 0 && detail[0] != "" {
		msg += detail[0] + "\n"
	}
	msg += fmt.Sprintf("Host: %s\nTo allow this request, update network.rules in moat.yaml.\n", host)
	_, _ = w.Write([]byte(msg))
}
```

- [ ] **Step 8: Run all proxy tests**

Run: `cd /workspace && go test ./internal/proxy/ -v -count=1`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/proxy/proxy.go internal/proxy/proxy_test.go
git commit -m "feat(proxy): integrate netrules.Check into request handling"
```

---

## Chunk 5: Daemon and Run Manager Plumbing

### Task 7: Pass rules through daemon and run manager

**Files:**
- Modify: `internal/daemon/runcontext.go` — add `NetworkRules` field, update `ToProxyContextData`
- Modify: `internal/daemon/api.go` — add `NetworkRules` to `RegisterRequest`
- Modify: `internal/run/manager.go` — pass rules from config

- [ ] **Step 1: Add NetworkRules to daemon RunContext**

In `internal/daemon/runcontext.go`, add field to `RunContext`:

```go
	NetworkRules []netrules.HostRules `json:"network_rules,omitempty"`
```

- [ ] **Step 2: Add NetworkRules to RegisterRequest**

In `internal/daemon/api.go`, add to `RegisterRequest`:

```go
	NetworkRules []netrules.HostRules `json:"network_rules,omitempty"`
```

Update the handler to copy it:

```go
	rc.NetworkRules = req.NetworkRules
```

- [ ] **Step 3: Update ToProxyContextData**

In `ToProxyContextData()`, add:

```go
	if len(rc.NetworkRules) > 0 {
		d.HostRules = make([]netrules.HostRules, len(rc.NetworkRules))
		copy(d.HostRules, rc.NetworkRules)
		// Also add rule hosts to AllowedHosts for host-level matching
		for _, hr := range rc.NetworkRules {
			d.AllowedHosts = append(d.AllowedHosts, proxy.ParseHostPattern(hr.Host))
		}
	}
```

- [ ] **Step 4: Update run manager**

In `internal/run/manager.go`, around line 646-648:

```go
		runCtx.NetworkPolicy = opts.Config.Network.Policy
		for _, entry := range opts.Config.Network.Rules {
			runCtx.NetworkRules = append(runCtx.NetworkRules, entry.HostRules)
		}
```

Update `buildRegisterRequest`:

```go
		NetworkRules: rc.NetworkRules,
```

- [ ] **Step 5: Build and test**

Run: `cd /workspace && go build ./... && go test ./internal/daemon/ ./internal/run/ -v -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/daemon/runcontext.go internal/daemon/api.go internal/run/manager.go
git commit -m "feat(daemon): plumb netrules through daemon and run manager"
```

---

## Chunk 6: Migration, Regressions, and Documentation

### Task 8: Update all network.Allow references

**Files:**
- All files referencing `Network.Allow`, `NetworkAllow`, `network.allow`

- [ ] **Step 1: Find all references**

Run: `grep -rn "Network\.Allow\|NetworkAllow\|network\.allow" --include="*.go" /workspace/internal/`

- [ ] **Step 2: Update each reference**

Migrate to `Network.Rules` / `NetworkRules`. Keep `NetworkAllow` on daemon API structs as a deprecated-but-accepted field for backwards compat (daemon API must be additive-only per CLAUDE.md). Populate it from rules for older daemons.

- [ ] **Step 3: Run full test suite and linter**

Run: `cd /workspace && make test-unit && make lint`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add -u
git commit -m "refactor: migrate network.allow references to network.rules"
```

### Task 9: Update documentation and examples

**Files:**
- Modify: `docs/content/reference/02-moat-yaml.md`
- Modify: `docs/content/concepts/05-networking.md`
- Modify: `examples/firewall/moat.yaml`

- [ ] **Step 1: Update moat.yaml reference**

Replace `network.allow` docs with `network.rules` syntax. Include examples of:
- Plain host entries
- Host with allow/deny rules
- Both strict and permissive modes

- [ ] **Step 2: Update networking concepts**

Add section explaining rule evaluation: first match wins, fall-through to policy default.

- [ ] **Step 3: Update firewall example**

```yaml
# examples/firewall/moat.yaml
network:
  policy: strict
  rules:
    - "httpbin.org"
    - "api.github.com":
        - "allow GET /repos/*"
        - "allow POST /repos/*/pulls"
        - "deny DELETE /*"
```

- [ ] **Step 4: Commit**

```bash
git add docs/ examples/
git commit -m "docs: update networking docs and examples for request rules"
```

### Task 10: Final verification

- [ ] **Step 1: Run full test suite**

Run: `cd /workspace && make test-unit`
Expected: PASS

- [ ] **Step 2: Run linter**

Run: `cd /workspace && make lint`
Expected: PASS

- [ ] **Step 3: Build**

Run: `cd /workspace && go build ./...`
Expected: success
