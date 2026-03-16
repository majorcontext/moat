package secrets

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEnvResolver_Scheme(t *testing.T) {
	r := &EnvResolver{}
	if r.Scheme() != "env" {
		t.Errorf("expected scheme 'env', got %q", r.Scheme())
	}
}

func TestEnvResolver_Resolve(t *testing.T) {
	r := &EnvResolver{}
	ctx := context.Background()

	t.Setenv("MOAT_TEST_SECRET", "hunter2")

	val, err := r.Resolve(ctx, "env://MOAT_TEST_SECRET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "hunter2" {
		t.Errorf("expected 'hunter2', got %q", val)
	}
}

func TestEnvResolver_Resolve_EmptyValue(t *testing.T) {
	r := &EnvResolver{}
	ctx := context.Background()

	t.Setenv("MOAT_TEST_EMPTY", "")

	val, err := r.Resolve(ctx, "env://MOAT_TEST_EMPTY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "" {
		t.Errorf("expected empty string, got %q", val)
	}
}

func TestEnvResolver_Resolve_NotSet(t *testing.T) {
	r := &EnvResolver{}
	ctx := context.Background()

	_, err := r.Resolve(ctx, "env://MOAT_TEST_DEFINITELY_NOT_SET_12345")
	if err == nil {
		t.Fatal("expected error for unset variable")
	}

	var backendErr *BackendError
	if !errors.As(err, &backendErr) {
		t.Fatalf("expected BackendError, got %T: %v", err, err)
	}
	if backendErr.Backend != "host environment" {
		t.Errorf("expected backend 'host environment', got %q", backendErr.Backend)
	}
	if !strings.Contains(backendErr.Fix, "export MOAT_TEST_DEFINITELY_NOT_SET_12345") {
		t.Errorf("expected fix to contain export hint, got %q", backendErr.Fix)
	}
}

func TestEnvResolver_Resolve_InvalidReference(t *testing.T) {
	r := &EnvResolver{}
	ctx := context.Background()

	tests := []struct {
		name string
		ref  string
	}{
		{"wrong scheme", "op://something"},
		{"empty var name", "env://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Resolve(ctx, tt.ref)
			if err == nil {
				t.Fatal("expected error")
			}
			var invalid *InvalidReferenceError
			if !errors.As(err, &invalid) {
				t.Fatalf("expected InvalidReferenceError, got %T: %v", err, err)
			}
		})
	}
}

func TestEnvResolver_Resolve_CanceledContext(t *testing.T) {
	r := &EnvResolver{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.Resolve(ctx, "env://ANYTHING")
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestEnvResolver_GlobalDispatch(t *testing.T) {
	// Exercises the init() registration path through the package-level Resolve function.
	t.Setenv("MOAT_TEST_DISPATCH", "dispatched-value")

	val, err := Resolve(context.Background(), "env://MOAT_TEST_DISPATCH")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "dispatched-value" {
		t.Errorf("expected 'dispatched-value', got %q", val)
	}
}

func TestEnvResolver_ResolveAll_Integration(t *testing.T) {
	withTestRegistry(func() {
		Register(&EnvResolver{})

		t.Setenv("MOAT_TEST_A", "value-a")
		t.Setenv("MOAT_TEST_B", "value-b")

		refs := map[string]string{
			"VAR_A": "env://MOAT_TEST_A",
			"VAR_B": "env://MOAT_TEST_B",
		}

		resolved, err := ResolveAll(context.Background(), refs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resolved["VAR_A"] != "value-a" {
			t.Errorf("VAR_A: expected 'value-a', got %q", resolved["VAR_A"])
		}
		if resolved["VAR_B"] != "value-b" {
			t.Errorf("VAR_B: expected 'value-b', got %q", resolved["VAR_B"])
		}
	})
}
