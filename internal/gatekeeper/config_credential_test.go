package gatekeeper

import (
	"context"
	"testing"
)

func TestResolveSourceEnv(t *testing.T) {
	const key = "MOAT_TEST_RESOLVE_ENV"
	t.Setenv(key, "token-abc")

	src, err := ResolveSource(SourceConfig{Type: "env", Var: key})
	if err != nil {
		t.Fatalf("ResolveSource() error: %v", err)
	}
	if src.Type() != "env" {
		t.Fatalf("Type() = %q, want %q", src.Type(), "env")
	}
	val, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if val != "token-abc" {
		t.Fatalf("Fetch() = %q, want %q", val, "token-abc")
	}
}

func TestResolveSourceEnvMissingVar(t *testing.T) {
	_, err := ResolveSource(SourceConfig{Type: "env"})
	if err == nil {
		t.Fatal("expected error for missing var field, got nil")
	}
}

func TestResolveSourceStatic(t *testing.T) {
	src, err := ResolveSource(SourceConfig{Type: "static", Value: "my-key"})
	if err != nil {
		t.Fatalf("ResolveSource() error: %v", err)
	}
	if src.Type() != "static" {
		t.Fatalf("Type() = %q, want %q", src.Type(), "static")
	}
	val, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if val != "my-key" {
		t.Fatalf("Fetch() = %q, want %q", val, "my-key")
	}
}

func TestResolveSourceStaticEmptyValue(t *testing.T) {
	_, err := ResolveSource(SourceConfig{Type: "static"})
	if err == nil {
		t.Fatal("expected error for empty static value, got nil")
	}
}

func TestResolveSourceUnknown(t *testing.T) {
	_, err := ResolveSource(SourceConfig{Type: "vault"})
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
}

func TestResolveSourceAWSMissingSecret(t *testing.T) {
	_, err := ResolveSource(SourceConfig{Type: "aws-secretsmanager"})
	if err == nil {
		t.Fatal("expected error for missing secret field, got nil")
	}
}

func TestResolveSourceExtraneousFields(t *testing.T) {
	tests := []struct {
		name string
		cfg  SourceConfig
	}{
		{"env with value", SourceConfig{Type: "env", Var: "X", Value: "extra"}},
		{"env with secret", SourceConfig{Type: "env", Var: "X", Secret: "extra"}},
		{"static with var", SourceConfig{Type: "static", Value: "v", Var: "extra"}},
		{"static with secret", SourceConfig{Type: "static", Value: "v", Secret: "extra"}},
		{"aws with var", SourceConfig{Type: "aws-secretsmanager", Secret: "s", Var: "extra"}},
		{"aws with value", SourceConfig{Type: "aws-secretsmanager", Secret: "s", Value: "extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveSource(tt.cfg)
			if err == nil {
				t.Fatal("expected error for extraneous fields")
			}
		})
	}
}
