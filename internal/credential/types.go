// Package credential provides secure credential storage and retrieval.
package credential

import "time"

// Provider identifies a credential provider (github, aws, etc.)
type Provider string

const (
	ProviderGitHub Provider = "github"
	ProviderAWS    Provider = "aws"
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
