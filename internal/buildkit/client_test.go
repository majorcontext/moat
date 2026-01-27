package buildkit

import (
	"context"
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
