package credentialsource

import (
	"context"
	"fmt"
	"testing"
)

type mockSMClient struct {
	value string
	err   error
}

func (m *mockSMClient) GetSecretValue(_ context.Context, _ string) (string, error) {
	return m.value, m.err
}

func TestAWSSecretsManagerSource(t *testing.T) {
	client := &mockSMClient{value: "db-password-123"}
	src := newAWSSecretsManagerSourceWithClient("my-secret", client)

	if src.Type() != "aws-secretsmanager" {
		t.Fatalf("Type() = %q, want %q", src.Type(), "aws-secretsmanager")
	}

	val, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if val != "db-password-123" {
		t.Fatalf("Fetch() = %q, want %q", val, "db-password-123")
	}
}

func TestAWSSecretsManagerSourceError(t *testing.T) {
	client := &mockSMClient{err: fmt.Errorf("access denied")}
	src := newAWSSecretsManagerSourceWithClient("my-secret", client)

	_, err := src.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "access denied" {
		t.Fatalf("error = %q, want %q", err.Error(), "access denied")
	}
}
