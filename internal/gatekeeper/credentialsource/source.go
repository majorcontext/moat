package credentialsource

import "context"

// CredentialSource fetches a credential value from an external system.
type CredentialSource interface {
	Fetch(ctx context.Context) (string, error)
	Type() string
}
