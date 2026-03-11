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
