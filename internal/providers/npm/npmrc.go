package npm

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// ParseNpmrc parses an .npmrc file and extracts registry entries.
//
// It handles two types of lines:
//   - Token lines: //registry.npmjs.org/:_authToken=npm_xxxx
//   - Scope lines: @myorg:registry=https://npm.company.com/
//
// Returns the discovered entries with host-to-token and scope-to-host mappings merged.
func ParseNpmrc(r io.Reader) ([]RegistryEntry, error) {
	// Collect host→token and scope→host mappings, then merge.
	hostTokens := make(map[string]string) // host → token
	scopeHosts := make(map[string]string) // @scope → host
	hostOrder := make([]string, 0)        // preserve insertion order

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// Token line: //host/:_authToken=TOKEN or //host/path/:_authToken=TOKEN
		if strings.HasPrefix(line, "//") {
			host, token, ok := parseTokenLine(line)
			if ok {
				if _, exists := hostTokens[host]; !exists {
					hostOrder = append(hostOrder, host)
				}
				hostTokens[host] = token
			}
			continue
		}

		// Scope line: @scope:registry=https://host/
		if strings.HasPrefix(line, "@") {
			scope, host, ok := parseScopeLine(line)
			if ok {
				scopeHosts[scope] = host
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading npmrc: %w", err)
	}

	// Build scope→host reverse map: host → []scopes
	hostScopes := make(map[string][]string)
	for scope, host := range scopeHosts {
		hostScopes[host] = append(hostScopes[host], scope)
	}

	// Merge into RegistryEntry list
	entries := make([]RegistryEntry, 0, len(hostTokens))
	for _, host := range hostOrder {
		token := hostTokens[host]
		entry := RegistryEntry{
			Host:        host,
			Token:       token,
			Scopes:      hostScopes[host],
			TokenSource: SourceNpmrc,
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// parseTokenLine parses a line like "//registry.npmjs.org/:_authToken=npm_xxxx".
// Returns the host and token. Environment variable references (${VAR}) return empty token.
func parseTokenLine(line string) (host, token string, ok bool) {
	// Format: //host/:_authToken=TOKEN or //host/path/:_authToken=TOKEN
	idx := strings.Index(line, ":_authToken=")
	if idx == -1 {
		return "", "", false
	}

	hostPart := line[2:idx] // strip leading "//"
	hostPart = strings.TrimSuffix(hostPart, "/")

	// Extract just the hostname (strip any path components)
	// "registry.npmjs.org" → "registry.npmjs.org"
	// "npm.company.com/path" → "npm.company.com"
	if slashIdx := strings.Index(hostPart, "/"); slashIdx != -1 {
		hostPart = hostPart[:slashIdx]
	}

	token = line[idx+len(":_authToken="):]
	token = strings.TrimSpace(token)

	// Skip environment variable references like ${NPM_TOKEN}
	if strings.HasPrefix(token, "${") || strings.HasPrefix(token, "$") {
		return hostPart, "", true
	}

	return hostPart, token, true
}

// parseScopeLine parses a line like "@myorg:registry=https://npm.company.com/".
// Returns the scope (with @) and the registry host.
func parseScopeLine(line string) (scope, host string, ok bool) {
	// Format: @scope:registry=URL
	colonIdx := strings.Index(line, ":registry=")
	if colonIdx == -1 {
		return "", "", false
	}

	scope = line[:colonIdx] // "@myorg"
	registryURL := line[colonIdx+len(":registry="):]
	registryURL = strings.TrimSpace(registryURL)

	// Parse URL to extract host
	parsed, err := url.Parse(registryURL)
	if err != nil {
		return "", "", false
	}

	host = parsed.Hostname()
	if host == "" {
		return "", "", false
	}

	return scope, host, true
}

// GenerateNpmrc generates a stub .npmrc for use inside containers.
// Scope-to-registry routing is real (npm needs this), but tokens are placeholders.
func GenerateNpmrc(entries []RegistryEntry, placeholder string) string {
	var b strings.Builder

	// Write scope→registry mappings first
	for _, entry := range entries {
		for _, scope := range entry.Scopes {
			fmt.Fprintf(&b, "%s:registry=https://%s/\n", scope, entry.Host)
		}
	}

	// Write token lines with placeholder
	for _, entry := range entries {
		fmt.Fprintf(&b, "//%s/:_authToken=%s\n", entry.Host, placeholder)
	}

	return b.String()
}
