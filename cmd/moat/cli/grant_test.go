package cli

import "testing"

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
	// Token with ANSI color codes around it
	output := "Some text\n\x1b[32msk-ant-oat01-abc123xyz\x1b[0m\nMore text"

	token := extractOAuthToken(output)
	expected := "sk-ant-oat01-abc123xyz"

	if token != expected {
		t.Errorf("extractOAuthToken() with ANSI = %q, want %q", token, expected)
	}
}

func TestExtractOAuthToken_Empty(t *testing.T) {
	output := "No token here\nJust some text"

	token := extractOAuthToken(output)
	if token != "" {
		t.Errorf("extractOAuthToken() should return empty for no token, got %q", token)
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
