package proxy

import (
	"strconv"
	"strings"
)

// hostPattern represents a parsed host pattern for matching.
type hostPattern struct {
	pattern    string // the original pattern
	host       string // the host part (without port)
	port       int    // specific port, or 0 if unspecified (matches only ports 80 and 443)
	isWildcard bool   // true if pattern starts with *.
}

// parseHostPattern parses a host pattern string into a hostPattern struct.
// Supports patterns like:
//   - api.example.com
//   - api.example.com:8080
//   - *.example.com
//   - *.example.com:443
func parseHostPattern(s string) hostPattern {
	p := hostPattern{
		pattern: s,
		port:    0, // 0 = unspecified, will match only ports 80 and 443
	}

	// Check for wildcard prefix
	if strings.HasPrefix(s, "*.") {
		p.isWildcard = true
		s = s[2:] // Remove "*."
	}

	// Split host and port
	host, portStr, hasPort := strings.Cut(s, ":")
	p.host = strings.ToLower(host)

	if hasPort {
		port, err := strconv.Atoi(portStr)
		if err == nil && port > 0 && port <= 65535 {
			p.port = port
		}
	}

	return p
}

// matchHost checks if the given host:port matches any of the patterns.
// Matching rules:
//   - Exact match: "api.github.com" matches only "api.github.com"
//   - Wildcard: "*.github.com" matches "api.github.com", "foo.bar.github.com"
//   - Port matching: if pattern has port, must match exactly;
//     if pattern has no port (0), match 80 or 443
func matchHost(patterns []hostPattern, host string, port int) bool {
	for _, pattern := range patterns {
		if matchesPattern(pattern, host, port) {
			return true
		}
	}
	return false
}

// matchesPattern checks if a single pattern matches the host:port.
func matchesPattern(pattern hostPattern, host string, port int) bool {
	// Check port matching first
	if pattern.port != 0 {
		// Pattern has specific port - must match exactly
		if pattern.port != port {
			return false
		}
	} else {
		// Pattern has no port (0) - only match default ports 80 and 443
		if port != 80 && port != 443 {
			return false
		}
	}

	// Check host matching
	if pattern.isWildcard {
		// Wildcard matching: *.example.com matches api.example.com, foo.bar.example.com
		// The host must end with .{pattern.host}
		suffix := "." + pattern.host // already lowercase from parsing
		return strings.HasSuffix(strings.ToLower(host), suffix)
	}

	// Exact host match
	return strings.EqualFold(pattern.host, host)
}

// grantHosts maps grant names to their allowed host patterns.
var grantHosts = map[string][]string{
	"github": {
		"github.com",
		"api.github.com",
		"*.githubusercontent.com",
		"*.github.com",
	},
	"anthropic": {
		"api.anthropic.com",
		"*.anthropic.com",
	},
	"openai": {
		"api.openai.com", // API key endpoint
		"chatgpt.com",    // ChatGPT subscription endpoint
		"*.openai.com",   // Other OpenAI services
	},
	"aws": {
		"sts.amazonaws.com",
		"sts.*.amazonaws.com",
		"*.amazonaws.com",
	},
}

// GetHostsForGrant returns the host patterns for a given grant name.
// Supports scoped grants like "github:repo" by extracting the provider name.
// Returns an empty slice if the grant is unknown.
func GetHostsForGrant(grant string) []string {
	// Extract provider from scoped grant (e.g., "github:repo" -> "github")
	provider := grant
	if idx := strings.Index(grant, ":"); idx != -1 {
		provider = grant[:idx]
	}

	hosts, ok := grantHosts[provider]
	if !ok {
		return []string{}
	}
	return hosts
}
