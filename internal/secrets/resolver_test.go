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
	withTestRegistry(func() {
		mock := &mockResolver{
			scheme: "mock",
			values: map[string]string{
				"mock://vault/item/field": "secret-value",
			},
		}
		Register(mock)

		val, err := Resolve(context.Background(), "mock://vault/item/field")
		if err != nil {
			t.Fatal(err)
		}
		if val != "secret-value" {
			t.Errorf("expected 'secret-value', got %q", val)
		}
	})
}

func TestResolve_UnsupportedScheme(t *testing.T) {
	withTestRegistry(func() {
		_, err := Resolve(context.Background(), "unknown://vault/item")
		if err == nil {
			t.Fatal("expected error for unsupported scheme")
		}

		var unsupported *UnsupportedSchemeError
		if !errors.As(err, &unsupported) {
			t.Errorf("expected UnsupportedSchemeError, got %T", err)
		}
	})
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

func TestResolveAll(t *testing.T) {
	withTestRegistry(func() {
		mock := &mockResolver{
			scheme: "mock",
			values: map[string]string{
				"mock://vault/key1": "value1",
				"mock://vault/key2": "value2",
			},
		}
		Register(mock)

		secrets := map[string]string{
			"SECRET_1": "mock://vault/key1",
			"SECRET_2": "mock://vault/key2",
		}

		resolved, err := ResolveAll(context.Background(), secrets)
		if err != nil {
			t.Fatal(err)
		}

		if resolved["SECRET_1"] != "value1" {
			t.Errorf("SECRET_1: expected 'value1', got %q", resolved["SECRET_1"])
		}
		if resolved["SECRET_2"] != "value2" {
			t.Errorf("SECRET_2: expected 'value2', got %q", resolved["SECRET_2"])
		}
	})
}

func TestResolveAll_FailsOnError(t *testing.T) {
	withTestRegistry(func() {
		mock := &mockResolver{
			scheme: "mock",
			values: map[string]string{}, // Empty - all lookups fail
		}
		Register(mock)

		secrets := map[string]string{
			"MISSING": "mock://vault/nonexistent",
		}

		_, err := ResolveAll(context.Background(), secrets)
		if err == nil {
			t.Fatal("expected error for missing secret")
		}
	})
}
