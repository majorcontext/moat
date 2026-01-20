//go:build darwin

package snapshot

import (
	"os/exec"
	"testing"
)

func TestAPFSBackendName(t *testing.T) {
	backend := NewAPFSBackend()
	if got := backend.Name(); got != "apfs" {
		t.Errorf("Name() = %q, want %q", got, "apfs")
	}
}

func TestAPFSDetection(t *testing.T) {
	// Check if tmutil is available
	if _, err := exec.LookPath("tmutil"); err != nil {
		t.Skip("tmutil not available, skipping APFS detection test")
	}

	// Test that IsAPFS doesn't panic on common paths
	// We don't assert the result since it depends on the filesystem
	paths := []string{
		"/",
		"/tmp",
		"/Users",
	}

	for _, path := range paths {
		// Just verify it doesn't panic
		_ = IsAPFS(path)
	}
}

func TestAPFSBackendImplementsInterface(t *testing.T) {
	// Compile-time check is in the main file, but this verifies at runtime
	var _ Backend = (*APFSBackend)(nil)
}

func TestExtractDateFromSnapshotName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "valid snapshot name",
			input:    "com.apple.TimeMachine.2024-01-15-123456.local",
			expected: "2024-01-15-123456",
		},
		{
			name:     "missing prefix",
			input:    "2024-01-15-123456.local",
			expected: "",
		},
		{
			name:     "missing suffix",
			input:    "com.apple.TimeMachine.2024-01-15-123456",
			expected: "2024-01-15-123456",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "completely different format",
			input:    "some-other-snapshot",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDateFromSnapshotName(tt.input)
			if got != tt.expected {
				t.Errorf("extractDateFromSnapshotName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetMountPoint(t *testing.T) {
	// Test with root path which should always work
	mountPoint, err := getMountPoint("/")
	if err != nil {
		t.Fatalf("getMountPoint(\"/\") failed: %v", err)
	}

	if mountPoint != "/" {
		t.Errorf("getMountPoint(\"/\") = %q, want \"/\"", mountPoint)
	}
}

func TestIsAPFSOnRoot(t *testing.T) {
	// Check if diskutil is available
	if _, err := exec.LookPath("diskutil"); err != nil {
		t.Skip("diskutil not available, skipping APFS check test")
	}

	// Modern macOS systems use APFS for the root volume
	// This test verifies the function works without panicking
	result := IsAPFS("/")
	t.Logf("IsAPFS(\"/\") = %v", result)

	// On modern macOS (10.13+), root should be APFS
	// But we don't strictly assert this since older systems might differ
}
