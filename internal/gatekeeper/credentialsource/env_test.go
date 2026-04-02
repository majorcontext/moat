package credentialsource

import (
	"context"
	"testing"
)

func TestEnvSource(t *testing.T) {
	const key = "MOAT_TEST_CRED_SRC_ENV"
	t.Setenv(key, "secret-token-123")

	src := NewEnvSource(key)
	if src.Type() != "env" {
		t.Fatalf("Type() = %q, want %q", src.Type(), "env")
	}

	val, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if val != "secret-token-123" {
		t.Fatalf("Fetch() = %q, want %q", val, "secret-token-123")
	}
}

func TestEnvSourceMissing(t *testing.T) {
	const key = "MOAT_TEST_CRED_SRC_MISSING"
	// Ensure not set.
	t.Setenv(key, "")

	src := NewEnvSource(key)
	_, err := src.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error for missing env var, got nil")
	}
}
