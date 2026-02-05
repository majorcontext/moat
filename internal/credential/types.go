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

// AWSConfig holds AWS IAM role configuration for credential injection.
// Unlike other providers, AWS stores role config (not tokens) since credentials
// are obtained at runtime via STS AssumeRole.
type AWSConfig struct {
	RoleARN            string `json:"role_arn"`
	Region             string `json:"region,omitempty"`
	SessionDurationStr string `json:"session_duration,omitempty"`
	ExternalID         string `json:"external_id,omitempty"`
}

// SessionDuration parses the session duration string and validates it.
// Returns default of 15 minutes if not set.
// AWS allows 15 minutes to 12 hours for assumed role sessions.
func (c *AWSConfig) SessionDuration() (time.Duration, error) {
	if c.SessionDurationStr == "" {
		return 15 * time.Minute, nil
	}
	d, err := time.ParseDuration(c.SessionDurationStr)
	if err != nil {
		return 0, fmt.Errorf("invalid session duration %q: %w", c.SessionDurationStr, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("session duration %v must be positive", d)
	}
	if d < 15*time.Minute {
		return 0, fmt.Errorf("session duration %v is less than minimum 15m", d)
	}
	if d > 12*time.Hour {
		return 0, fmt.Errorf("session duration %v exceeds maximum 12h", d)
	}
	return d, nil
}

// KnownProviders returns a list of all known credential providers.
func KnownProviders() []Provider {
	return []Provider{ProviderGitHub, ProviderAWS, ProviderAnthropic, ProviderOpenAI}
}

// IsKnownProvider returns true if the provider is a known credential provider.
func IsKnownProvider(p Provider) bool {
	switch p {
	case ProviderGitHub, ProviderAWS, ProviderAnthropic, ProviderOpenAI:
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
