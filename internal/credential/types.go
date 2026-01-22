// Package credential provides secure credential storage and retrieval.
package credential

import (
	"fmt"
	"strings"
	"time"
)

// Provider identifies a credential provider (github, aws, etc.)
type Provider string

const (
	ProviderGitHub    Provider = "github"
	ProviderAWS       Provider = "aws"
	ProviderAnthropic Provider = "anthropic"
)

// Credential represents a stored credential.
type Credential struct {
	Provider  Provider  `json:"provider"`
	Token     string    `json:"token"`
	Scopes    []string  `json:"scopes,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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

// ParseGrantProvider extracts the provider from a grant string.
// Grants can be "provider" or "provider:scope" format.
// For example, "github:repo" returns ProviderGitHub.
func ParseGrantProvider(grant string) Provider {
	if idx := strings.Index(grant, ":"); idx != -1 {
		return Provider(grant[:idx])
	}
	return Provider(grant)
}

// ImpliedDependencies returns the dependencies implied by a list of grants.
// For example, a "github" grant implies "gh" and "git" dependencies.
func ImpliedDependencies(grants []string) []string {
	seen := make(map[string]bool)
	var deps []string

	for _, grant := range grants {
		provider := ParseGrantProvider(grant)

		// Get implied dependencies for this provider
		var implied []string
		switch provider {
		case ProviderGitHub:
			implied = GitHubImpliedDeps()
		case ProviderAWS:
			implied = AWSImpliedDeps()
		}

		for _, dep := range implied {
			if !seen[dep] {
				seen[dep] = true
				deps = append(deps, dep)
			}
		}
	}

	return deps
}
