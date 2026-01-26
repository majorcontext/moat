package cli

import (
	"os"
	"testing"

	"github.com/andybons/moat/internal/credential"
)

func TestExtractOAuthToken(t *testing.T) {
	// Sample output from claude setup-token with ANSI codes and ASCII art
	sampleOutput := `Welcome to Claude Code v2.1.14
…………………………………………………………………………………………………………………………………………………………

     *                                       █████▓▓░
                                 *         ███▓░     ░░
            ░░░░░░                        ███▓░
    ░░░   ░░░░░░░░░░                      ███▓░
   ░░░░░░░░░░░░░░░░░░░    *                ██▓░░      ▓
                                             ░▓▓███▓▓░
 *                                 ░░░░
                                 ░░░░░░░░
                               ░░░░░░░░░░░░░░░░
       █████████                                        *
      ██▄█████▄██                        *
       █████████      *
…………………█ █   █ █………………………………………………………………………………………………………………


✓ Long-lived authentication token created successfully!

Your OAuth token (valid for 1 year):

sk-ant-oat01-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX

Store this token securely. You won't be able to see it again.

Use this token by setting: export CLAUDE_CODE_OAUTH_TOKEN=<token>
`

	token := extractOAuthToken(sampleOutput)
	expected := "sk-ant-oat01-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"

	if token != expected {
		t.Errorf("extractOAuthToken() = %q, want %q", token, expected)
	}
}

func TestExtractOAuthToken_WithANSI(t *testing.T) {
	// Token with ANSI color codes around it - realistic length and structure
	// The token block ends with a blank line before "Store this token"
	output := "Your OAuth token:\n\n\x1b[32msk-ant-oat01-abc123xyz890abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGH\x1b[0m\n\nStore this token securely."

	token := extractOAuthToken(output)
	expected := "sk-ant-oat01-abc123xyz890abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGH"

	if token != expected {
		t.Errorf("extractOAuthToken() with ANSI = %q, want %q", token, expected)
	}
}

func TestExtractOAuthToken_WithANSICursorMovement(t *testing.T) {
	// Real-world case: Claude CLI uses ANSI cursor movement codes
	// \x1b[1C = cursor right (instead of space)
	// \x1b[1B = cursor down 1 line
	// \x1b[2B = cursor down 2 lines (blank line separator)
	output := "Header\r\x1b[1Bsk-ant-oat01-abc123\x1b[1Cxyz890\x1b[1Cdefghijklmnopqrstuvwxyz1234567890ABCDEFGH\r\x1b[2BStore this token"

	token := extractOAuthToken(output)
	expected := "sk-ant-oat01-abc123xyz890defghijklmnopqrstuvwxyz1234567890ABCDEFGH"

	if token != expected {
		t.Errorf("extractOAuthToken() with cursor movement = %q, want %q", token, expected)
	}
}

func TestExtractOAuthToken_MultiLineWithCR(t *testing.T) {
	// Token wrapped across lines using \r and \x1b[1B (cursor down 1)
	output := "sk-ant-oat01-firstpart12345678901234567890\r\x1b[1Bsecondpart1234567890ABCDEFGH\r\x1b[2BStore"

	token := extractOAuthToken(output)
	expected := "sk-ant-oat01-firstpart12345678901234567890secondpart1234567890ABCDEFGH"

	if token != expected {
		t.Errorf("extractOAuthToken() multiline = %q, want %q", token, expected)
	}
}

func TestExtractOAuthToken_Empty(t *testing.T) {
	output := "No token here\nJust some text"

	token := extractOAuthToken(output)
	if token != "" {
		t.Errorf("extractOAuthToken() should return empty for no token, got %q", token)
	}
}

func TestExtractOAuthToken_MalformedTokens(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{
			name:   "token too short",
			output: "sk-ant-oat01-abc123",
		},
		{
			name:   "partial prefix only",
			output: "sk-ant-oat01-",
		},
		{
			name:   "wrong prefix",
			output: "sk-ant-api01-abcdefghijklmnopqrstuvwxyz",
		},
		{
			name:   "invalid characters",
			output: "sk-ant-oat01-abc!@#$%^&*()12345678901234",
		},
		{
			name:   "prefix in middle of other text",
			output: "someothersk-ant-oat01-abcdefghijklmnopqrst",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := extractOAuthToken(tt.output)
			if token != "" {
				t.Errorf("extractOAuthToken() should reject malformed token, got %q", token)
			}
		})
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"plain text", "plain text"},
		{"\x1b[32mgreen\x1b[0m", "green"},
		{"\x1b[1;31mred bold\x1b[0m", "red bold"},
		{"before\x1b[33myellow\x1b[0mafter", "beforeyellowafter"},
	}

	for _, tt := range tests {
		result := stripANSI(tt.input)
		if result != tt.expected {
			t.Errorf("stripANSI(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

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
