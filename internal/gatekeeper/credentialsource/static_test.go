package credentialsource

import (
	"context"
	"testing"
)

func TestStaticSource(t *testing.T) {
	src := NewStaticSource("my-api-key")
	if src.Type() != "static" {
		t.Fatalf("Type() = %q, want %q", src.Type(), "static")
	}

	val, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if val != "my-api-key" {
		t.Fatalf("Fetch() = %q, want %q", val, "my-api-key")
	}
}
