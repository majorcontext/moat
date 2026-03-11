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
