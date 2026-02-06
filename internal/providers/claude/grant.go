package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// ErrRefreshNotSupported is returned when refresh is not supported for a credential.
var ErrRefreshNotSupported = errors.New("credential refresh not supported")

// Grant acquires Anthropic credentials interactively or from environment.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	reader := bufio.NewReader(os.Stdin)

	// Check if claude is available for setup-token
	claudeAvailable := isClaudeAvailable()

	// Check for existing Claude Code credentials as option 3
	hasExistingCreds := hasClaudeCodeCredentials()

	// If only API key is available, skip the menu
	if !claudeAvailable && !hasExistingCreds {
		return grantViaAPIKey(ctx)
	}

	for {
		// Offer choices to user
		fmt.Println("Choose authentication method:")
		fmt.Println()
		if claudeAvailable {
			fmt.Println("  1. Claude subscription (recommended)")
			fmt.Println("     Uses 'claude setup-token' to get a long-lived OAuth token.")
			fmt.Println("     Requires a Claude Pro/Max subscription.")
			fmt.Println()
		}
		fmt.Println("  2. Anthropic API key")
		fmt.Println("     Use an API key from console.anthropic.com")
		fmt.Println("     Billed per token to your API account.")
		fmt.Println()

		if hasExistingCreds {
			fmt.Println("  3. Import existing Claude Code credentials")
			fmt.Println("     Use OAuth tokens from your local Claude Code installation.")
			fmt.Println()
		}

		var validChoices string
		if claudeAvailable && hasExistingCreds {
			validChoices = "1, 2, or 3"
		} else if claudeAvailable {
			validChoices = "1 or 2"
		} else {
			// hasExistingCreds must be true (we handled !claudeAvailable && !hasExistingCreds above)
			validChoices = "2 or 3"
		}

		fmt.Printf("Enter choice [%s]: ", validChoices)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(response)

		// Default to option 1 if claude available, otherwise 2
		if response == "" {
			if claudeAvailable {
				response = "1"
			} else {
				response = "2"
			}
		}

		switch response {
		case "1":
			if !claudeAvailable {
				fmt.Println("Claude Code is not installed. Please choose another option.")
				continue
			}
			return grantViaSetupToken(ctx)

		case "2":
			return grantViaAPIKey(ctx)

		case "3":
			if !hasExistingCreds {
				fmt.Println("No existing Claude Code credentials found. Please choose another option.")
				continue
			}
			return grantViaExistingCreds()

		default:
			fmt.Printf("Invalid choice: %s\n", response)
			continue
		}
	}
}

// isClaudeAvailable checks if the claude CLI is installed.
func isClaudeAvailable() bool {
	cmd := exec.Command("claude", "--version")
	return cmd.Run() == nil
}

// grantViaSetupToken uses `claude setup-token` to get an OAuth token.
func grantViaSetupToken(ctx context.Context) (*provider.Credential, error) {
	fmt.Println()
	fmt.Println("Running 'claude setup-token' to obtain authentication token...")
	fmt.Println("This may open a browser for authentication.")
	fmt.Println()

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "setup-token")
	cmd.Stdin = os.Stdin

	// Capture both stdout and stderr since the token might be on either stream
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set environment to disable fancy terminal output
	// This helps get clean token output without ANSI cursor movement codes
	cmd.Env = append(os.Environ(),
		"TERM=dumb",
		"NO_COLOR=1",
		"CI=1",
	)

	err := cmd.Run()

	// Debug output when MOAT_DEBUG_ANTHROPIC is set
	debug := os.Getenv("MOAT_DEBUG_ANTHROPIC") != ""
	if debug {
		fmt.Println("--- DEBUG: claude setup-token stdout ---")
		fmt.Println(stdout.String())
		fmt.Println("--- DEBUG: end stdout ---")
		fmt.Println("--- DEBUG: claude setup-token stderr ---")
		fmt.Println(stderr.String())
		fmt.Println("--- DEBUG: end stderr ---")
	}

	if err != nil {
		// Show stderr to user on failure
		if stderr.Len() > 0 {
			fmt.Fprintln(os.Stderr, stderr.String())
		}
		return nil, fmt.Errorf("claude setup-token failed: %w", err)
	}

	// Try to extract token from stdout first, then stderr
	combined := stdout.String() + "\n" + stderr.String()
	token := extractOAuthToken(combined)
	if token == "" {
		return nil, fmt.Errorf("could not find OAuth token in claude setup-token output")
	}

	cred := &provider.Credential{
		Provider:  "anthropic",
		Token:     token,
		CreatedAt: time.Now(),
	}

	fmt.Println("\nClaude credential acquired via setup-token.")
	fmt.Println("You can now run 'moat claude' to start Claude Code.")
	return cred, nil
}

// extractOAuthToken extracts the OAuth token from claude setup-token output.
//
// The token format is: sk-ant-oat01-<base64-data>
// The token appears on its own line(s) between descriptive text.
//
// The Claude CLI output varies:
// - Sometimes uses \n for newlines with blank lines as \n\n or \n<spaces>\n
// - Sometimes uses \r with ANSI cursor codes: \x1b[1B (down 1), \x1b[2B (down 2 = blank line)
//
// Strategy:
// 1. Find "sk-ant-oat01-" in the raw output
// 2. Extract until we hit a "blank line" indicator:
//   - \x1b[2B (ANSI cursor down 2+)
//   - \n followed by whitespace-only line followed by \n
//
// 3. Clean the extracted block (strip ANSI codes and whitespace)
func extractOAuthToken(output string) string {
	debug := os.Getenv("MOAT_DEBUG_ANTHROPIC") != ""

	// Find the start of the token in raw output
	const prefix = "sk-ant-oat01-"
	startIdx := strings.Index(output, prefix)
	if startIdx == -1 {
		if debug {
			fmt.Println("--- DEBUG: No token prefix found in output")
		}
		return ""
	}

	// Extract until we hit a "blank line" indicator
	endIdx := len(output)
	for i := startIdx; i < len(output); i++ {
		// Check for ANSI cursor down 2+ lines: \x1b[NB where N >= 2
		// This is used by Claude CLI to create visual blank lines
		if output[i] == '\x1b' && i+3 < len(output) && output[i+1] == '[' {
			// Parse the number before 'B'
			j := i + 2
			for j < len(output) && output[j] >= '0' && output[j] <= '9' {
				j++
			}
			if j < len(output) && output[j] == 'B' && j > i+2 {
				n := 0
				for k := i + 2; k < j; k++ {
					n = n*10 + int(output[k]-'0')
				}
				if n >= 2 {
					// Find the \r or start of this escape sequence
					endIdx = i
					// Back up past any preceding \r
					for endIdx > startIdx && output[endIdx-1] == '\r' {
						endIdx--
					}
					if debug {
						fmt.Printf("--- DEBUG: Found ANSI cursor down %d at index %d, endIdx=%d\n", n, i, endIdx)
					}
					goto done
				}
			}
		}

		// Check for blank line: \n followed by only whitespace until next \n
		if output[i] == '\n' {
			lineStart := i + 1
			isBlank := true
			for j := lineStart; j < len(output); j++ {
				c := output[j]
				if c == '\n' {
					// Found end of line
					if isBlank {
						endIdx = i
						if debug {
							fmt.Printf("--- DEBUG: Found blank line at index %d\n", i)
						}
						goto done
					}
					break
				}
				if c != ' ' && c != '\t' && c != '\r' {
					break // Non-whitespace found, not a blank line
				}
			}
		}
	}
done:

	tokenBlock := output[startIdx:endIdx]
	if debug {
		fmt.Printf("--- DEBUG: Raw token block: %q\n", tokenBlock)
	}

	// Now clean the token block: strip ANSI and extract only token characters
	cleaned := stripANSI(tokenBlock)
	if debug {
		fmt.Printf("--- DEBUG: After ANSI strip: %q\n", cleaned)
	}

	// Extract only valid token characters
	var token strings.Builder
	for i := 0; i < len(cleaned); i++ {
		c := cleaned[i]
		if isTokenChar(c) {
			token.WriteByte(c)
		}
	}

	result := token.String()
	if debug {
		fmt.Printf("--- DEBUG: Extracted token: %q (length: %d)\n", result, len(result))
	}

	// Validate the token looks reasonable
	if len(result) < 60 {
		if debug {
			fmt.Printf("--- DEBUG: Token too short: %d chars\n", len(result))
		}
		return ""
	}

	return result
}

// isTokenChar returns true if c is a valid OAuth token character.
func isTokenChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') || c == '_' || c == '-'
}

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	var result strings.Builder
	inEscape := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			// ANSI sequences end with a letter
			if (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z') {
				inEscape = false
			}
			continue
		}
		result.WriteByte(s[i])
	}
	return result.String()
}

// grantViaAPIKey prompts for an API key.
func grantViaAPIKey(ctx context.Context) (*provider.Credential, error) {
	auth := &anthropicAuth{}

	// Get API key from environment variable or interactive prompt
	var apiKey string
	if envKey := os.Getenv("ANTHROPIC_API_KEY"); envKey != "" {
		apiKey = envKey
		fmt.Println("Using API key from ANTHROPIC_API_KEY environment variable")
	} else {
		var err error
		apiKey, err = auth.PromptForAPIKey()
		if err != nil {
			return nil, fmt.Errorf("reading API key: %w", err)
		}
	}

	// Validate the key
	fmt.Println("\nValidating API key...")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := auth.ValidateKey(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("validating API key: %w", err)
	}
	fmt.Println("API key is valid.")

	cred := &provider.Credential{
		Provider:  "anthropic",
		Token:     apiKey,
		CreatedAt: time.Now(),
	}

	return cred, nil
}

// grantViaExistingCreds imports existing Claude Code credentials.
func grantViaExistingCreds() (*provider.Credential, error) {
	token, err := getClaudeCodeCredentials()
	if err != nil {
		return nil, err
	}

	fmt.Println()
	fmt.Println("Found Claude Code credentials.")
	if token.SubscriptionType != "" {
		fmt.Printf("  Subscription: %s\n", token.SubscriptionType)
	}
	expiresAt := time.UnixMilli(token.ExpiresAt)
	if !expiresAt.IsZero() {
		if time.Now().After(expiresAt) {
			fmt.Printf("  Status: Expired (was valid until %s)\n", expiresAt.Format(time.RFC3339))
			fmt.Println("\nWarning: Token has expired. You may need to re-authenticate in Claude Code.")
			fmt.Println("Run 'claude' to refresh your credentials, then try again.")
			return nil, fmt.Errorf("Claude Code token has expired")
		}
		fmt.Printf("  Expires: %s\n", expiresAt.Format(time.RFC3339))
	}

	cred := &provider.Credential{
		Provider:  "anthropic",
		Token:     token.AccessToken,
		Scopes:    token.Scopes,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}

	fmt.Println("\nClaude Code credentials imported.")
	fmt.Println("You can now run 'moat claude' to start Claude Code.")
	if !expiresAt.IsZero() {
		remaining := time.Until(expiresAt)
		if remaining > 24*time.Hour {
			fmt.Printf("Token expires in %d days. Re-run 'moat grant claude' if it expires.\n", int(remaining.Hours()/24))
		} else {
			fmt.Printf("Token expires in %.0f hours. Re-run 'moat grant claude' if it expires.\n", remaining.Hours())
		}
	}
	return cred, nil
}

// hasClaudeCodeCredentials checks if Claude Code credentials are available.
func hasClaudeCodeCredentials() bool {
	_, err := getClaudeCodeCredentials()
	return err == nil
}

// getClaudeCodeCredentials attempts to retrieve Claude Code OAuth credentials.
// It tries the following sources in order:
// 1. macOS Keychain (if on macOS)
// 2. ~/.claude/.credentials.json file
func getClaudeCodeCredentials() (*oauthToken, error) {
	// Try keychain first on macOS
	if runtime.GOOS == "darwin" {
		if token, err := getFromKeychain(); err == nil {
			return token, nil
		}
		// Fall through to file-based lookup if keychain fails
	}

	// Try credentials file
	return getFromFile()
}

// getFromKeychain retrieves Claude Code credentials from macOS Keychain.
func getFromKeychain() (*oauthToken, error) {
	// Use the security command to retrieve the password
	cmd := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials",
		"-w", // Output only the password
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("keychain lookup failed: %w", err)
	}

	// Parse the JSON credentials
	var creds oauthCredentials
	if err := json.Unmarshal(output, &creds); err != nil {
		return nil, fmt.Errorf("parsing keychain credentials: %w", err)
	}

	if creds.ClaudeAiOauth == nil {
		return nil, fmt.Errorf("no OAuth credentials found in keychain")
	}

	return creds.ClaudeAiOauth, nil
}

// getFromFile retrieves Claude Code credentials from ~/.claude/.credentials.json.
func getFromFile() (*oauthToken, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	credPath := filepath.Join(home, ".claude", ".credentials.json")
	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Claude Code credentials not found at %s\n"+
				"  Have you logged into Claude Code? Run 'claude' to authenticate first", credPath)
		}
		return nil, fmt.Errorf("reading credentials file: %w", err)
	}

	var creds oauthCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parsing credentials file: %w", err)
	}

	if creds.ClaudeAiOauth == nil {
		return nil, fmt.Errorf("no OAuth credentials found in %s", credPath)
	}

	return creds.ClaudeAiOauth, nil
}
