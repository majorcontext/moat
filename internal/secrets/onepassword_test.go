package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestOnePasswordResolver_Scheme(t *testing.T) {
	r := &OnePasswordResolver{}
	if r.Scheme() != "op" {
		t.Errorf("expected scheme 'op', got %q", r.Scheme())
	}
}

func TestOnePasswordResolver_ParseError_NotSignedIn(t *testing.T) {
	r := &OnePasswordResolver{}

	// Simulate "not signed in" error from op CLI
	stderr := []byte("[ERROR] 2024/01/15 10:00:00 You are not currently signed in")
	err := r.parseOpError(stderr, "op://Dev/OpenAI/api-key")

	var backendErr *BackendError
	if !errors.As(err, &backendErr) {
		t.Fatalf("expected BackendError, got %T", err)
	}
	if backendErr.Backend != "1Password" {
		t.Errorf("expected backend '1Password', got %q", backendErr.Backend)
	}
	if !strings.Contains(backendErr.Fix, "op signin") {
		t.Errorf("expected fix to mention 'op signin', got %q", backendErr.Fix)
	}
}

func TestOnePasswordResolver_ParseError_ItemNotFound(t *testing.T) {
	r := &OnePasswordResolver{}

	stderr := []byte("[ERROR] 2024/01/15 10:00:00 \"OpenAI\" isn't an item")
	err := r.parseOpError(stderr, "op://Dev/OpenAI/api-key")

	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected NotFoundError, got %T", err)
	}
}

func TestOnePasswordResolver_ParseError_VaultNotFound(t *testing.T) {
	r := &OnePasswordResolver{}

	stderr := []byte("[ERROR] 2024/01/15 10:00:00 \"Dev\" isn't a vault")
	err := r.parseOpError(stderr, "op://Dev/OpenAI/api-key")

	var backendErr *BackendError
	if !errors.As(err, &backendErr) {
		t.Fatalf("expected BackendError, got %T", err)
	}
	if !strings.Contains(backendErr.Reason, "vault") {
		t.Errorf("expected reason to mention vault, got %q", backendErr.Reason)
	}
	if !strings.Contains(backendErr.Fix, "Dev") {
		t.Errorf("expected fix to mention vault name 'Dev', got %q", backendErr.Fix)
	}
}

func TestOnePasswordResolver_ParseError_GenericError(t *testing.T) {
	r := &OnePasswordResolver{}

	stderr := []byte("some unexpected error message")
	err := r.parseOpError(stderr, "op://Dev/OpenAI/api-key")

	var backendErr *BackendError
	if !errors.As(err, &backendErr) {
		t.Fatalf("expected BackendError, got %T", err)
	}
	if backendErr.Backend != "1Password" {
		t.Errorf("expected backend '1Password', got %q", backendErr.Backend)
	}
	if !strings.Contains(backendErr.Reason, "unexpected error") {
		t.Errorf("expected reason to contain error message, got %q", backendErr.Reason)
	}
}
