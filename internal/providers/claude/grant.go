package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
)

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
			return grantViaExistingCreds(ctx)

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

	// Build a clean environment that forces dumb terminal mode.
	// We filter out keys we override to avoid duplicates — on Linux (glibc),
	// the first occurrence of a duplicate env var wins, so appending to
	// os.Environ() without filtering means our overrides get ignored.
	overrides := map[string]string{
		"TERM":     "dumb",
		"NO_COLOR": "1",
		"CI":       "1",
		"COLUMNS":  "10000",
	}
	var env []string
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if _, override := overrides[key]; !override {
			env = append(env, e)
		}
	}
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	err := cmd.Run()

	log.Debug("claude setup-token completed",
		"subsystem", "grant",
		"exit_error", err,
		"stdout_len", stdout.Len(),
		"stderr_len", stderr.Len(),
	)
	if log.Verbose() {
		log.Debug("claude setup-token raw output",
			"subsystem", "grant",
			"raw_stdout", stdout.String(),
			"raw_stderr", stderr.String(),
		)
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

	log.Debug("extracted OAuth token",
		"subsystem", "grant",
		"token_len", len(token),
	)

	// Validate the token to catch corruption from ANSI parsing
	fmt.Println("\nValidating OAuth token...")
	auth := &anthropicAuth{}
	validateCtx, validateCancel := context.WithTimeout(ctx, 30*time.Second)
	defer validateCancel()

	if err := auth.ValidateOAuthToken(validateCtx, token); err != nil {
		log.Error("OAuth token validation failed after extraction",
			"subsystem", "grant",
			"error", err,
			"token_len", len(token),
		)
		return nil, fmt.Errorf("token validation failed: %w", err)
	}
	fmt.Println("OAuth token is valid.")

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
	// Find the start of the token in raw output
	const prefix = "sk-ant-oat01-"
	startIdx := strings.Index(output, prefix)
	if startIdx == -1 {
		log.Debug("no token prefix found in output",
			"subsystem", "grant",
			"action", "extract_token",
			"output_len", len(output),
		)
		return ""
	}

	log.Debug("found token prefix",
		"subsystem", "grant",
		"action", "extract_token",
		"start_idx", startIdx,
		"output_len", len(output),
	)

	// Extract until we hit a "blank line" indicator
	endIdx := len(output)
	endReason := "end_of_output"
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
					endReason = fmt.Sprintf("ansi_cursor_down_%d", n)
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
						endReason = "blank_line"
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
	log.Debug("token block extracted",
		"subsystem", "grant",
		"action", "extract_token",
		"end_reason", endReason,
		"block_len", len(tokenBlock),
	)
	if log.Verbose() {
		log.Debug("token block raw content",
			"subsystem", "grant",
			"action", "extract_token",
			"raw_block", fmt.Sprintf("%q", tokenBlock),
		)
	}

	// Now clean the token block: strip ANSI and extract only token characters
	cleaned := stripANSI(tokenBlock)

	// Extract only valid token characters, tracking what was removed
	var token strings.Builder
	var removed strings.Builder
	for i := 0; i < len(cleaned); i++ {
		c := cleaned[i]
		if isTokenChar(c) {
			token.WriteByte(c)
		} else {
			removed.WriteString(fmt.Sprintf("[%d]=%q ", i, string(c)))
		}
	}

	result := token.String()
	log.Debug("token extraction complete",
		"subsystem", "grant",
		"action", "extract_token",
		"result_len", len(result),
		"cleaned_len", len(cleaned),
		"removed_chars", removed.String(),
	)

	// Validate the token looks reasonable
	if len(result) < 60 {
		log.Debug("token too short, rejecting",
			"subsystem", "grant",
			"action", "extract_token",
			"result_len", len(result),
			"min_len", 60,
		)
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
func grantViaExistingCreds(ctx context.Context) (*provider.Credential, error) {
	log.Debug("importing existing Claude Code credentials",
		"subsystem", "grant",
		"os", runtime.GOOS,
	)

	token, err := getClaudeCodeCredentials()
	if err != nil {
		log.Debug("failed to get Claude Code credentials",
			"subsystem", "grant",
			"error", err,
		)
		return nil, err
	}

	log.Debug("found Claude Code credentials",
		"subsystem", "grant",
		"access_token_len", len(token.AccessToken),
		"has_refresh_token", token.RefreshToken != "",
		"expires_at_ms", token.ExpiresAt,
		"scopes", strings.Join(token.Scopes, ","),
		"subscription_type", token.SubscriptionType,
	)

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

	// Validate the token against the API
	fmt.Println("\nValidating OAuth token...")
	auth := &anthropicAuth{}
	validateCtx, validateCancel := context.WithTimeout(ctx, 30*time.Second)
	defer validateCancel()

	if err := auth.ValidateOAuthToken(validateCtx, token.AccessToken); err != nil {
		log.Error("OAuth token validation failed for imported credentials",
			"subsystem", "grant",
			"error", err,
			"token_len", len(token.AccessToken),
		)
		return nil, fmt.Errorf("token validation failed: %w", err)
	}
	fmt.Println("OAuth token is valid.")

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
			log.Debug("credentials found in keychain",
				"subsystem", "grant",
				"token_len", len(token.AccessToken),
			)
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

	log.Debug("read credentials file",
		"subsystem", "grant",
		"path", credPath,
		"file_size", len(data),
	)
	if log.Verbose() {
		log.Debug("credentials file content",
			"subsystem", "grant",
			"raw_json", string(data),
		)
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
