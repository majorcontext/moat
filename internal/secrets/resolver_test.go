package secrets

import (
	"context"
	"errors"
	"testing"
)

type mockResolver struct {
	scheme string
	values map[string]string
}

func (m *mockResolver) Scheme() string {
	return m.scheme
}

func (m *mockResolver) Resolve(ctx context.Context, ref string) (string, error) {
	if v, ok := m.values[ref]; ok {
		return v, nil
	}
	return "", &NotFoundError{Reference: ref}
}

func TestResolve_DispatchesToCorrectResolver(t *testing.T) {
	// Register mock resolver
	mock := &mockResolver{
		scheme: "mock",
		values: map[string]string{
			"mock://vault/item/field": "secret-value",
		},
	}
	Register(mock)
	defer clearRegistry()

	val, err := Resolve(context.Background(), "mock://vault/item/field")
	if err != nil {
		t.Fatal(err)
	}
	if val != "secret-value" {
		t.Errorf("expected 'secret-value', got %q", val)
	}
}

func TestResolve_UnsupportedScheme(t *testing.T) {
	clearRegistry()

	_, err := Resolve(context.Background(), "unknown://vault/item")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}

	var unsupported *UnsupportedSchemeError
	if !errors.As(err, &unsupported) {
		t.Errorf("expected UnsupportedSchemeError, got %T", err)
	}
}

func TestResolve_InvalidReference(t *testing.T) {
	_, err := Resolve(context.Background(), "no-scheme-here")
	if err == nil {
		t.Fatal("expected error for invalid reference")
	}

	var invalid *InvalidReferenceError
	if !errors.As(err, &invalid) {
		t.Errorf("expected InvalidReferenceError, got %T", err)
	}
}
