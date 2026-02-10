package buildkit

import (
	"context"
	"net"
	"os"
	"testing"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		envVal  string
		wantErr bool
	}{
		{
			name:    "with BUILDKIT_HOST set",
			envVal:  "tcp://buildkit:1234",
			wantErr: false,
		},
		{
			name:    "without BUILDKIT_HOST",
			envVal:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldVal := os.Getenv("BUILDKIT_HOST")
			defer func() {
				if oldVal != "" {
					os.Setenv("BUILDKIT_HOST", oldVal)
				} else {
					os.Unsetenv("BUILDKIT_HOST")
				}
			}()
			if tt.envVal != "" {
				os.Setenv("BUILDKIT_HOST", tt.envVal)
			} else {
				os.Unsetenv("BUILDKIT_HOST")
			}

			client, err := NewClient()
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && client.addr != tt.envVal {
				t.Errorf("addr = %v, want %v", client.addr, tt.envVal)
			}
		})
	}
}

func TestNewEmbeddedClient(t *testing.T) {
	contextDialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return nil, nil
	}
	sessionDialer := func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
		return nil, nil
	}

	c := NewEmbeddedClient(contextDialer, sessionDialer)

	if !c.embedded {
		t.Error("expected embedded to be true")
	}
	if c.addr != "" {
		t.Errorf("expected empty addr, got %q", c.addr)
	}
	if len(c.clientOpts) != 2 {
		t.Errorf("expected 2 clientOpts (context dialer + session dialer), got %d", len(c.clientOpts))
	}
}

// Integration test - requires BuildKit running
func TestClient_Ping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	buildkitHost := os.Getenv("BUILDKIT_HOST")
	if buildkitHost == "" {
		t.Skip("BUILDKIT_HOST not set")
	}

	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient() failed: %v", err)
	}

	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		t.Errorf("Ping() failed: %v", err)
	}
}

// Integration test - requires BuildKit running
func TestClient_Build(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	buildkitHost := os.Getenv("BUILDKIT_HOST")
	if buildkitHost == "" {
		t.Skip("BUILDKIT_HOST not set")
	}

	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient() failed: %v", err)
	}

	// Create a temp directory with a simple Dockerfile
	tmpDir := t.TempDir()
	dockerfile := `FROM alpine:3.20
RUN echo "BuildKit integration test"
`
	if err := os.WriteFile(tmpDir+"/Dockerfile", []byte(dockerfile), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	ctx := context.Background()
	tag := "moat-buildkit-test:integration"

	// Build the image
	err = client.Build(ctx, BuildOptions{
		Tag:        tag,
		ContextDir: tmpDir,
		Platform:   "linux/amd64",
		NoCache:    true,
	})

	if err != nil {
		t.Errorf("Build() failed: %v", err)
	}

	// Note: We can't easily verify the image exists in this test because
	// BuildKit exports to the Docker daemon and we don't have a Docker client here.
	// The E2E tests verify the full flow including image availability.
}

// Integration test - verifies BuildKit build with build args
func TestClient_BuildWithArgs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	buildkitHost := os.Getenv("BUILDKIT_HOST")
	if buildkitHost == "" {
		t.Skip("BUILDKIT_HOST not set")
	}

	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient() failed: %v", err)
	}

	// Create a temp directory with a Dockerfile that uses build args
	tmpDir := t.TempDir()
	dockerfile := `FROM alpine:3.20
ARG TEST_ARG
RUN echo "TEST_ARG=${TEST_ARG}"
`
	if err := os.WriteFile(tmpDir+"/Dockerfile", []byte(dockerfile), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	ctx := context.Background()
	tag := "moat-buildkit-test:buildargs"

	// Build the image with build args
	err = client.Build(ctx, BuildOptions{
		Tag:        tag,
		ContextDir: tmpDir,
		Platform:   "linux/amd64",
		NoCache:    true,
		BuildArgs: map[string]string{
			"TEST_ARG": "test-value",
		},
	})

	if err != nil {
		t.Errorf("Build() with build args failed: %v", err)
	}
}

// Integration test - verifies BuildKit error handling
func TestClient_BuildWithInvalidDockerfile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	buildkitHost := os.Getenv("BUILDKIT_HOST")
	if buildkitHost == "" {
		t.Skip("BUILDKIT_HOST not set")
	}

	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient() failed: %v", err)
	}

	// Create a temp directory with an invalid Dockerfile
	tmpDir := t.TempDir()
	dockerfile := `INVALID_INSTRUCTION this should fail
FROM alpine:3.20
`
	if err := os.WriteFile(tmpDir+"/Dockerfile", []byte(dockerfile), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	ctx := context.Background()
	tag := "moat-buildkit-test:invalid"

	// Build should fail with invalid Dockerfile
	err = client.Build(ctx, BuildOptions{
		Tag:        tag,
		ContextDir: tmpDir,
		Platform:   "linux/amd64",
	})

	if err == nil {
		t.Error("Build() should have failed with invalid Dockerfile")
	}
}

// Integration test - verifies BuildKit unreachable error handling
func TestClient_BuildWithUnreachableBuildKit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Save original env var
	origBuildkitHost := os.Getenv("BUILDKIT_HOST")
	defer func() {
		if origBuildkitHost != "" {
			os.Setenv("BUILDKIT_HOST", origBuildkitHost)
		} else {
			os.Unsetenv("BUILDKIT_HOST")
		}
	}()

	// Set an unreachable BuildKit host
	os.Setenv("BUILDKIT_HOST", "tcp://localhost:99999")

	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient() failed: %v", err)
	}

	tmpDir := t.TempDir()
	dockerfile := `FROM alpine:3.20`
	if err := os.WriteFile(tmpDir+"/Dockerfile", []byte(dockerfile), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	ctx := context.Background()
	tag := "moat-buildkit-test:unreachable"

	// Build should fail when BuildKit is unreachable
	err = client.Build(ctx, BuildOptions{
		Tag:        tag,
		ContextDir: tmpDir,
		Platform:   "linux/amd64",
	})

	if err == nil {
		t.Error("Build() should have failed with unreachable BuildKit")
	}

	// Verify error message mentions BuildKit connectivity
	if !containsAny(err.Error(), []string{"BuildKit", "connection", "dial"}) {
		t.Errorf("Error should mention BuildKit connectivity issue, got: %v", err)
	}
}

// Helper function to check if a string contains any of the given substrings
func containsAny(s string, substrings []string) bool {
	for _, substr := range substrings {
		if len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}
