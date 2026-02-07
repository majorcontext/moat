// Package credential provides secure credential storage and retrieval.
package credential

import (
	"fmt"
	"strings"
	"time"
)

// MetaKeyTokenSource is the metadata key for recording how a token was obtained.
// Provider packages define the values (e.g., "cli", "env", "pat").
const MetaKeyTokenSource = "token_source"

// Provider identifies a credential provider (github, aws, etc.)
type Provider string

const (
	ProviderGitHub    Provider = "github"
	ProviderAWS       Provider = "aws"
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
	ProviderGemini    Provider = "gemini"
)

// Credential represents a stored credential.
type Credential struct {
	Provider  Provider          `json:"provider"`
	Token     string            `json:"token"`
	Scopes    []string          `json:"scopes,omitempty"`
	ExpiresAt time.Time         `json:"expires_at,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	Metadata  map[string]string `json:"metadata,omitempty"` // Provider-specific extra data
}

// Store defines the credential storage interface.
type Store interface {
	Save(cred Credential) error
	Get(provider Provider) (*Credential, error)
	Delete(provider Provider) error
	List() ([]Credential, error)
}

// KnownProviders returns a list of all known credential providers.
func KnownProviders() []Provider {
	return []Provider{ProviderGitHub, ProviderAWS, ProviderAnthropic, ProviderOpenAI, ProviderGemini}
}

// IsKnownProvider returns true if the provider is a known credential provider.
func IsKnownProvider(p Provider) bool {
	switch p {
	case ProviderGitHub, ProviderAWS, ProviderAnthropic, ProviderOpenAI, ProviderGemini:
		return true
	default:
		return false
	}
}

// ValidateGrant validates a grant string and returns an error if invalid.
// Grants must be a known provider, optionally with a scope suffix (e.g., "github:repo").
func ValidateGrant(grant string) error {
	if grant == "" {
		return fmt.Errorf("grant cannot be empty")
	}

	provider := ParseGrantProvider(grant)
	if !IsKnownProvider(provider) {
		known := make([]string, 0, 4)
		for _, p := range KnownProviders() {
			known = append(known, string(p))
		}
		return fmt.Errorf("unknown provider %q; known providers: %s", provider, strings.Join(known, ", "))
	}

	return nil
}

// ParseGrantProvider extracts the provider from a grant string.
// Grants can be "provider" or "provider:scope" format.
// For example, "github:repo" returns ProviderGitHub.
func ParseGrantProvider(grant string) Provider {
	if idx := strings.Index(grant, ":"); idx != -1 {
		return Provider(grant[:idx])
	}
	return Provider(grant)
}

// impliedDepsRegistry maps providers to functions returning their implied dependencies.
// Provider packages register via init() using RegisterImpliedDeps.
var impliedDepsRegistry = map[Provider]func() []string{}

// RegisterImpliedDeps registers an implied dependencies function for a provider.
// This is typically called from init() functions in provider packages.
func RegisterImpliedDeps(provider Provider, fn func() []string) {
	impliedDepsRegistry[provider] = fn
}

// ImpliedDependencies returns the dependencies implied by a list of grants.
// For example, a "github" grant implies "gh" and "git" dependencies.
func ImpliedDependencies(grants []string) []string {
	seen := make(map[string]bool)
	var deps []string

	for _, grant := range grants {
		provider := ParseGrantProvider(grant)

		if fn, ok := impliedDepsRegistry[provider]; ok {
			for _, dep := range fn() {
				if !seen[dep] {
					seen[dep] = true
					deps = append(deps, dep)
				}
			}
		}
	}

	return deps
}

// ProviderCredential is a minimal interface for provider.Credential conversion.
// This allows passing credentials to the new provider package without import cycles.
type ProviderCredential interface {
	GetProvider() string
	GetToken() string
	GetScopes() []string
	GetExpiresAt() time.Time
	GetCreatedAt() time.Time
	GetMetadata() map[string]string
}

// GetProvider implements ProviderCredential.
func (c *Credential) GetProvider() string { return string(c.Provider) }

// GetToken implements ProviderCredential.
func (c *Credential) GetToken() string { return c.Token }

// GetScopes implements ProviderCredential.
func (c *Credential) GetScopes() []string { return c.Scopes }

// GetExpiresAt implements ProviderCredential.
func (c *Credential) GetExpiresAt() time.Time { return c.ExpiresAt }

// GetCreatedAt implements ProviderCredential.
func (c *Credential) GetCreatedAt() time.Time { return c.CreatedAt }

// GetMetadata implements ProviderCredential.
func (c *Credential) GetMetadata() map[string]string { return c.Metadata }
