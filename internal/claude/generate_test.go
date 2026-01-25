package claude

import (
	"os"
	"testing"
)

func TestGeneratedConfigCleanup(t *testing.T) {
	// Create a temp directory to simulate generated config
	tempDir := t.TempDir()

	// Create a file in it to verify cleanup
	testFile := tempDir + "/test.txt"
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	config := &GeneratedConfig{
		TempDir:    tempDir,
		StagingDir: tempDir,
	}

	// Verify cleanup works
	if err := config.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Error("temp dir should be removed after cleanup")
	}
}

func TestGeneratedConfigCleanupEmpty(t *testing.T) {
	config := &GeneratedConfig{}

	// Cleanup on empty config should not error
	if err := config.Cleanup(); err != nil {
		t.Fatalf("Cleanup on empty config: %v", err)
	}
}
