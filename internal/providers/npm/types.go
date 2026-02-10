package npm

import (
	"encoding/json"
	"fmt"
)

// RegistryEntry represents a single npm registry with its authentication token.
type RegistryEntry struct {
	Host        string   `json:"host"`                   // "registry.npmjs.org"
	Token       string   `json:"token"`                  // "npm_aaaa..."
	Scopes      []string `json:"scopes,omitempty"`       // ["@myorg"] — empty for default registry
	TokenSource string   `json:"token_source,omitempty"` // "npmrc", "env", "manual"
}

// NpmTokenPlaceholder is a format-valid npm token placeholder for container .npmrc files.
// The proxy replaces the real Authorization header at the network layer.
// npm tokens use the format "npm_" followed by base36 characters.
const NpmTokenPlaceholder = "npm_moatProxyInjected00000000"

// DefaultRegistry is the default npm registry host.
const DefaultRegistry = "registry.npmjs.org"

// Token source constants.
const (
	SourceNpmrc  = "npmrc"  // Discovered from .npmrc file
	SourceEnv    = "env"    // From NPM_TOKEN environment variable
	SourceManual = "manual" // Entered interactively
)

// MarshalEntries encodes registry entries as JSON for storage in the Token field.
func MarshalEntries(entries []RegistryEntry) (string, error) {
	data, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("marshaling registry entries: %w", err)
	}
	return string(data), nil
}

// UnmarshalEntries decodes registry entries from the JSON-encoded Token field.
func UnmarshalEntries(token string) ([]RegistryEntry, error) {
	var entries []RegistryEntry
	if err := json.Unmarshal([]byte(token), &entries); err != nil {
		return nil, fmt.Errorf("unmarshaling registry entries: %w", err)
	}
	return entries, nil
}
