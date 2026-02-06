package cli

import (
	"os"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
)

func TestGrantMCP(t *testing.T) {
	// Save stdin/stdout
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	// Mock stdin with API key
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.Write([]byte("test-api-key-123\n"))
		w.Close()
	}()

	// Redirect stdout to silence prompts
	os.Stdout, _ = os.Open(os.DevNull)

	// Set up temporary credential store
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Run grant command
	cmd := rootCmd
	cmd.SetArgs([]string{"grant", "mcp", "context7"})
	err := cmd.Execute()

	if err != nil {
		t.Fatalf("grant mcp context7 failed: %v", err)
	}

	// Verify credential was saved
	key, _ := credential.DefaultEncryptionKey()
	store, _ := credential.NewFileStore(credential.DefaultStoreDir(), key)
	cred, err := store.Get(credential.Provider("mcp-context7"))

	if err != nil {
		t.Fatalf("failed to retrieve credential: %v", err)
	}

	if cred.Provider != "mcp-context7" {
		t.Errorf("expected provider 'mcp-context7', got %q", cred.Provider)
	}

	if cred.Token != "test-api-key-123" {
		t.Errorf("expected token 'test-api-key-123', got %q", cred.Token)
	}
}
