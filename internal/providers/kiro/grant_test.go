package kiro

import (
	"context"
	"strings"
	"testing"
)

func TestExecuteReadsEnv(t *testing.T) {
	t.Setenv("KIRO_API_KEY", "env-token-123")
	cred, err := NewGrant().Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if cred.Token != "env-token-123" {
		t.Errorf("Token = %q, want env-token-123", cred.Token)
	}
	if cred.Provider != "kiro" {
		t.Errorf("Provider = %q, want kiro", cred.Provider)
	}
	if cred.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

func TestExecutePromptsWhenNoEnv(t *testing.T) {
	t.Setenv("KIRO_API_KEY", "")
	g := NewGrant()
	g.readToken = func() (string, error) { return "  prompted-token  ", nil }
	cred, err := g.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if cred.Token != "prompted-token" {
		t.Errorf("Token = %q, want trimmed prompted-token", cred.Token)
	}
}

func TestExecuteEmptyTokenIsError(t *testing.T) {
	t.Setenv("KIRO_API_KEY", "")
	g := NewGrant()
	g.readToken = func() (string, error) { return "   ", nil }
	_, err := g.Execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty-token error, got %v", err)
	}
}
