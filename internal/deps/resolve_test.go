package deps

import (
	"context"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/deps/versions"
)

func TestResolveVersions(t *testing.T) {
	// Pre-seed cache so tests don't hit the network
	cache := versions.NewCache(24*time.Hour, "")
	cache.Set("go@1.25", "1.25.8")
	cache.Set("node@22", "22.14.0")
	SetVersionCache(cache)

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
			name: "runtime without version resolves default",
			deps: []Dependency{
				{Name: "go"},
			},
			validate: func(t *testing.T, deps []Dependency) {
				// When no version is specified, ResolveVersions should populate
				// the registry default and resolve it to a full patch version.
				// This prevents tarball URLs like "go1.25.linux-amd64.tar.gz"
				// (which don't exist) when Go is installed via curl.
				if deps[0].Version != "1.25.8" {
					t.Errorf("go version should be resolved to 1.25.8, got %q", deps[0].Version)
				}
				if deps[0].OriginalVersion != "1.25" {
					t.Errorf("go OriginalVersion should be 1.25, got %q", deps[0].OriginalVersion)
				}
			},
		},
		{
			name: "non-runtime without version unchanged",
			deps: []Dependency{
				{Name: "jq"},
			},
			validate: func(t *testing.T, deps []Dependency) {
				if deps[0].Version != "" {
					t.Errorf("jq version should remain empty, got %q", deps[0].Version)
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

func TestResolveVersionsSetsOriginalVersion(t *testing.T) {
	// Pre-seed cache with known resolutions to avoid network calls
	cache := versions.NewCache(24*time.Hour, "")
	cache.Set("go@1.22", "1.22.12")
	cache.Set("node@20", "20.18.3")
	cache.Set("python@3.11", "3.11.15")
	SetVersionCache(cache)

	ctx := context.Background()
	deps := []Dependency{
		{Name: "go", Version: "1.22"},
		{Name: "node", Version: "20"},
		{Name: "python", Version: "3.11"},
	}

	result, err := ResolveVersions(ctx, deps)
	if err != nil {
		t.Fatalf("ResolveVersions() unexpected error: %v", err)
	}

	// Each resolved dependency should preserve the original version
	tests := []struct {
		name         string
		wantOriginal string
		wantResolved string
	}{
		{"go", "1.22", "1.22.12"},
		{"node", "20", "20.18.3"},
		{"python", "3.11", "3.11.15"},
	}
	for i, tt := range tests {
		if result[i].OriginalVersion != tt.wantOriginal {
			t.Errorf("%s: OriginalVersion = %q, want %q", tt.name, result[i].OriginalVersion, tt.wantOriginal)
		}
		if result[i].Version != tt.wantResolved {
			t.Errorf("%s: Version = %q, want %q", tt.name, result[i].Version, tt.wantResolved)
		}
	}
}

func TestResolveVersionsOriginalVersionEmptyWhenUnchanged(t *testing.T) {
	// OriginalVersion should remain empty when no resolution happens
	SetVersionCache(versions.NewCache(24*time.Hour, ""))

	ctx := context.Background()
	deps := []Dependency{
		{Name: "jq"},                      // Non-runtime dependency
		{Name: "protoc", Version: "25.1"}, // Non-runtime with version
	}

	result, err := ResolveVersions(ctx, deps)
	if err != nil {
		t.Fatalf("ResolveVersions() unexpected error: %v", err)
	}

	for i, dep := range result {
		if dep.OriginalVersion != "" {
			t.Errorf("deps[%d] (%s): OriginalVersion = %q, want empty", i, dep.Name, dep.OriginalVersion)
		}
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

func TestResolveVersionsDefaultPopulatedOnError(t *testing.T) {
	// When no version is specified and resolution fails (e.g., network error),
	// the default version should still be populated so the Dockerfile generator
	// doesn't fall back to the bare default (e.g., "1.25") which produces
	// broken tarball URLs.
	//
	// Pre-seed cache with an error-inducing value by NOT seeding it,
	// then use a canceled context to force resolution failure.
	SetVersionCache(versions.NewCache(24*time.Hour, ""))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately to force resolver failure

	deps := []Dependency{
		{Name: "go"}, // No version; default "1.25" should be populated even on failure
	}

	result, err := ResolveVersions(ctx, deps)
	if err != nil {
		t.Fatalf("ResolveVersions() unexpected error: %v", err)
	}

	// Even though resolution failed, Version should be populated with the
	// registry default rather than remaining empty.
	if result[0].Version != "1.25" {
		t.Errorf("go version should be registry default %q on resolution failure, got %q", "1.25", result[0].Version)
	}
	// OriginalVersion should also be set so selectBaseImage can use it
	// for Docker Hub floating tags.
	if result[0].OriginalVersion != "1.25" {
		t.Errorf("go OriginalVersion should be registry default %q on resolution failure, got %q", "1.25", result[0].OriginalVersion)
	}
}
