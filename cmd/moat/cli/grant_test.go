package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
)

func TestGrantMCP(t *testing.T) {
	// Use isolated test keyring to avoid interfering with user's real credentials
	t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")

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
	t.Setenv("MOAT_HOME", "")

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
	// Canonical form is "mcp:<name>" (mirrors "oauth:<name>").
	cred, err := store.Get(credential.Provider("mcp:context7"))
	if err != nil {
		t.Fatalf("failed to retrieve credential: %v", err)
	}

	if cred.Provider != "mcp:context7" {
		t.Errorf("expected provider 'mcp:context7', got %q", cred.Provider)
	}

	if cred.Token != "test-api-key-123" {
		t.Errorf("expected token 'test-api-key-123', got %q", cred.Token)
	}
}

func TestGrantCopilotUsesGitHub(t *testing.T) {
	err := runGrant(grantCmd, []string{"copilot"})
	if err == nil {
		t.Fatal("runGrant(copilot) = nil, want error")
	}
	if !strings.Contains(err.Error(), "moat grant github") {
		t.Fatalf("runGrant(copilot) error = %v, want moat grant github guidance", err)
	}
}

// TestGrantBaseURLWrongProvider covers the CLI wiring for --base-url: the flag
// only means something to the anthropic grant, and anywhere else it would be
// silently ignored, so it must be rejected with guidance.
func TestGrantBaseURLWrongProvider(t *testing.T) {
	oldBaseURL := grantBaseURL
	defer func() { grantBaseURL = oldBaseURL }()
	grantBaseURL = "https://gw.lunaroute.com"

	for _, prov := range []string{"github", "claude", "openai", "npm"} {
		t.Run(prov, func(t *testing.T) {
			err := runGrant(grantCmd, []string{prov})
			if err == nil {
				t.Fatalf("runGrant(%s --base-url) = nil, want error", prov)
			}
			if !strings.Contains(err.Error(), "moat grant anthropic --base-url") {
				t.Errorf("error = %v, want guidance naming the anthropic grant", err)
			}
		})
	}
}

// TestGrantBaseURLInvalidURL is the companion case: --base-url on the right
// provider still has to name a usable endpoint, and the error says which flag
// was wrong.
func TestGrantBaseURLInvalidURL(t *testing.T) {
	oldBaseURL := grantBaseURL
	defer func() { grantBaseURL = oldBaseURL }()

	for _, raw := range []string{"ftp://gw.example.com", "gw.example.com", "http://", "http://:8080"} {
		t.Run(raw, func(t *testing.T) {
			grantBaseURL = raw
			err := runGrant(grantCmd, []string{"anthropic"})
			if err == nil {
				t.Fatalf("runGrant(anthropic --base-url %q) = nil, want error", raw)
			}
			if !strings.Contains(err.Error(), "--base-url") {
				t.Errorf("error = %v, want it to name the --base-url flag", err)
			}
		})
	}
}
