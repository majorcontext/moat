package versions

import (
	"context"
	"testing"
)

func TestPythonResolver_Resolve(t *testing.T) {
	resolver := &PythonResolver{}

	tests := []struct {
		name    string
		version string
		want    string
		wantErr bool
	}{
		{
			name:    "partial 3.12 resolves to latest patch",
			version: "3.12",
			want:    "3.12.8",
		},
		{
			name:    "partial 3.11 resolves to latest patch",
			version: "3.11",
			want:    "3.11.11",
		},
		{
			name:    "partial 3.9 resolves to latest patch",
			version: "3.9",
			want:    "3.9.21",
		},
		{
			name:    "exact version passes through",
			version: "3.11.5",
			want:    "3.11.5",
		},
		{
			name:    "exact version not found",
			version: "3.11.99",
			wantErr: true,
		},
		{
			name:    "nonexistent minor version",
			version: "3.99",
			wantErr: true,
		},
		{
			name:    "invalid format - single part",
			version: "3",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolver.Resolve(context.Background(), tt.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("Resolve() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Resolve() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPythonResolver_LatestStable(t *testing.T) {
	resolver := &PythonResolver{}

	got, err := resolver.LatestStable(context.Background())
	if err != nil {
		t.Fatalf("LatestStable() error = %v", err)
	}

	// Should be 3.13.x
	if got[:4] != "3.13" {
		t.Errorf("LatestStable() = %v, want 3.13.x", got)
	}
}

func TestPythonResolver_Available(t *testing.T) {
	resolver := &PythonResolver{}

	versions, err := resolver.Available(context.Background())
	if err != nil {
		t.Fatalf("Available() error = %v", err)
	}

	if len(versions) == 0 {
		t.Error("Available() returned empty list")
	}

	// Check first version is 3.13.x
	if versions[0][:4] != "3.13" {
		t.Errorf("Available()[0] = %v, want 3.13.x", versions[0])
	}
}
