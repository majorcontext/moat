package deps

import (
	"context"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/deps/versions"
)

func TestResolveVersions(t *testing.T) {
	// Use a fresh in-memory cache for testing
	SetVersionCache(versions.NewCache(24*time.Hour, ""))

	ctx := context.Background()

	tests := []struct {
		name     string
		deps     []Dependency
		wantErr  bool
		validate func(t *testing.T, deps []Dependency)
	}{
		{
			name: "no dependencies",
			deps: nil,
		},
		{
			name: "non-runtime dependency unchanged",
			deps: []Dependency{
				{Name: "jq"},
				{Name: "protoc", Version: "25.1"},
			},
			validate: func(t *testing.T, deps []Dependency) {
				if deps[0].Version != "" {
					t.Errorf("jq version should remain empty, got %q", deps[0].Version)
				}
				if deps[1].Version != "25.1" {
					t.Errorf("protoc version should remain 25.1, got %q", deps[1].Version)
				}
			},
		},
		{
			name: "runtime without version unchanged",
			deps: []Dependency{
				{Name: "node"},
			},
			validate: func(t *testing.T, deps []Dependency) {
				if deps[0].Version != "" {
					t.Errorf("node version should remain empty, got %q", deps[0].Version)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveVersions(ctx, tt.deps)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveVersions() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.validate != nil {
				tt.validate(t, result)
			}
		})
	}
}

func TestResolveVersionsPreservesOriginalOnError(t *testing.T) {
	// Use a fresh in-memory cache for testing
	SetVersionCache(versions.NewCache(24*time.Hour, ""))

	ctx := context.Background()

	// Test with an invalid version that won't resolve
	deps := []Dependency{
		{Name: "go", Version: "99.99"}, // Invalid version
	}

	result, err := ResolveVersions(ctx, deps)
	if err != nil {
		t.Fatalf("ResolveVersions() unexpected error: %v", err)
	}

	// Original version should be preserved since resolution failed
	if result[0].Version != "99.99" {
		t.Errorf("Expected version to remain 99.99, got %q", result[0].Version)
	}
}
