package secrets

// Error types for secret resolution failures. Each error type provides
// context to help users diagnose and fix issues with their secret references.

import "fmt"

// UnsupportedSchemeError indicates an unrecognized URI scheme.
type UnsupportedSchemeError struct {
	Scheme string
}

func (e *UnsupportedSchemeError) Error() string {
	return fmt.Sprintf("unsupported secret scheme: %s", e.Scheme)
}

// InvalidReferenceError indicates a malformed secret reference.
type InvalidReferenceError struct {
	Reference string
	Reason    string
}

func (e *InvalidReferenceError) Error() string {
	return fmt.Sprintf("invalid secret reference %q: %s", e.Reference, e.Reason)
}

// NotFoundError indicates the secret was not found in the backend.
type NotFoundError struct {
	Reference string
	Backend   string
}

func (e *NotFoundError) Error() string {
	if e.Backend != "" {
		return fmt.Sprintf("secret not found in %s: %s", e.Backend, e.Reference)
	}
	return fmt.Sprintf("secret not found: %s", e.Reference)
}

// BackendError wraps errors from secret backends with actionable context.
type BackendError struct {
	Backend   string
	Reference string
	Reason    string
	Fix       string
}

func (e *BackendError) Error() string {
	msg := fmt.Sprintf("%s: %s", e.Backend, e.Reason)
	if e.Fix != "" {
		msg += "\n\n  " + e.Fix
	}
	return msg
}
