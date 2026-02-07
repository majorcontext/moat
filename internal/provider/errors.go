package provider

import (
	"errors"
	"fmt"
)

var (
	// ErrProviderNotFound is returned when a provider is not registered.
	ErrProviderNotFound = errors.New("provider not found")
	// ErrCredentialNotFound is returned when no credential exists for a provider.
	ErrCredentialNotFound = errors.New("credential not found")
	// ErrCredentialExpired is returned when a credential has expired.
	ErrCredentialExpired = errors.New("credential expired")
	// ErrRefreshNotSupported is returned when refresh is attempted on a static credential.
	ErrRefreshNotSupported = errors.New("credential refresh not supported")
	// ErrTokenRevoked is returned when a refresh token has been revoked.
	ErrTokenRevoked = errors.New("refresh token revoked")
)

// GrantError wraps provider-specific grant failures with actionable guidance.
type GrantError struct {
	Provider string
	Cause    error
	Hint     string
}

func (e *GrantError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("grant %s: %v\n\n%s", e.Provider, e.Cause, e.Hint)
	}
	return fmt.Sprintf("grant %s: %v", e.Provider, e.Cause)
}

func (e *GrantError) Unwrap() error {
	return e.Cause
}
