package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/term"
	"github.com/majorcontext/moat/internal/ui"
)

// Grant acquires a Claude Code OAuth token interactively.
// Offers OAuth-specific options: setup-token, paste existing token, or import
// from local Claude Code installation.
func (p *OAuthProvider) Grant(ctx context.Context) (*provider.Credential, error) {
	reader := bufio.NewReader(os.Stdin)

	claudeAvailable := isClaudeAvailable()
	hasExistingCreds := hasClaudeCodeCredentials()

	for {
		fmt.Println("Choose authentication method:")
		fmt.Println()

		optNum := 1

		setupTokenOpt := 0
		if claudeAvailable {
			setupTokenOpt = optNum
			fmt.Printf("  %d. Claude subscription (OAuth token)\n", optNum)
			fmt.Println("     Runs 'claude setup-token' to get a long-lived token.")
			fmt.Println()
			optNum++
		}

		existingTokenOpt := optNum
		fmt.Printf("  %d. Existing OAuth token\n", optNum)
		fmt.Println("     Paste a token from a previous 'claude setup-token' run.")
		fmt.Println()
		optNum++

		importCredsOpt := 0
		if hasExistingCreds {
			importCredsOpt = optNum
			fmt.Printf("  %d. Import existing Claude Code credentials\n", optNum)
			fmt.Println("     Import OAuth tokens from your local Claude Code installation.")
			fmt.Println("     Note: Imported tokens are short-lived and will not auto-refresh.")
			fmt.Println()
			optNum++
		}

		maxOpt := optNum - 1
		fmt.Printf("Enter choice [1-%d]: ", maxOpt)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(response)

		if response == "" {
			response = "1"
		}

		switch response {
		case fmt.Sprint(setupTokenOpt):
			if setupTokenOpt == 0 {
				fmt.Printf("Invalid choice: %s\n", response)
				continue
			}
			return grantViaSetupToken(ctx, reader)

		case fmt.Sprint(existingTokenOpt):
			return grantViaExistingOAuthToken(ctx, reader)

		case fmt.Sprint(importCredsOpt):
			if importCredsOpt == 0 {
				fmt.Printf("Invalid choice: %s\n", response)
				continue
			}
			return grantViaExistingCreds(ctx)

		default:
			fmt.Printf("Invalid choice: %s\n", response)
			continue
		}
	}
}

// Grant acquires an Anthropic API key interactively.
func (p *AnthropicProvider) Grant(ctx context.Context) (*provider.Credential, error) {
	return grantViaAPIKey(ctx)
}

// isClaudeAvailable checks if the claude CLI is installed.
func isClaudeAvailable() bool {
	cmd := exec.Command("claude", "--version")
	return cmd.Run() == nil
}

// grantViaSetupToken runs `claude setup-token` attached to the user's
// terminal, then asks them to paste the token it displays.
//
// moat deliberately does NOT capture or parse Claude's output. The CLI
// renders setup-token as a TUI whose layout changes between versions, so any
// scraping is brittle and fails silently on upgrades. Letting Claude render
// to the real terminal and having the user paste the token is
// version-independent and robust.
func grantViaSetupToken(ctx context.Context, reader *bufio.Reader) (*provider.Credential, error) {
	fmt.Println()
	fmt.Println("Running 'claude setup-token'. This may open a browser to sign in.")
	fmt.Println("When it finishes, copy the token it displays (it starts with sk-ant-oat01-).")
	fmt.Println()

	cmd := exec.CommandContext(ctx, "claude", "setup-token")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Non-fatal: the CLI may have shown the token before failing during
		// its own cleanup. Let the user paste it anyway.
		fmt.Printf("\nNote: 'claude setup-token' exited with an error: %v\n", err)
		fmt.Println("If it still displayed a token, paste it below.")
	}

	return promptAndValidateOAuthToken(ctx, reader)
}

// grantViaExistingOAuthToken prompts the user to paste an OAuth token they
// already obtained via `claude setup-token`.
func grantViaExistingOAuthToken(ctx context.Context, reader *bufio.Reader) (*provider.Credential, error) {
	return promptAndValidateOAuthToken(ctx, reader)
}

// readPastedToken reads a pasted OAuth token, reassembling it if the source
// terminal soft-wrapped it across multiple lines (a narrow window makes the
// clipboard selection capture real newlines).
//
// A terminal in canonical mode delivers a multi-line paste one line at a time,
// so we cannot rely on the whole paste being buffered after the first line.
// Instead we keep reading lines until a blank line or EOF terminates input,
// then drop all whitespace since tokens contain none.
func readPastedToken(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		if strings.TrimSpace(line) == "" {
			// A blank line ends input once we have content (the user pressing
			// Enter on an empty line); ignore leading blanks.
			if b.Len() > 0 || err != nil {
				if err != nil && err != io.EOF {
					return "", err
				}
				break
			}
			continue
		}
		b.WriteString(line)
		if err != nil {
			if err != io.EOF {
				return "", err
			}
			break
		}
	}

	return stripWhitespace(b.String()), nil
}

// stripWhitespace removes all whitespace. OAuth tokens contain none, so this
// losslessly reassembles a token that wrapping split with newlines or spaces.
func stripWhitespace(s string) string {
	return strings.Map(func(c rune) rune {
		if unicode.IsSpace(c) {
			return -1
		}
		return c
	}, s)
}

// bracketedPasteStart and bracketedPasteEnd are the CSI markers a terminal
// wraps pasted text in when bracketed paste mode (DECSET 2004) is enabled.
const (
	bracketedPasteOn  = "\x1b[?2004h"
	bracketedPasteOff = "\x1b[?2004l"
)

// readTokenFromTerminal reads a pasted token from an interactive terminal
// using bracketed paste, so a wrapped multi-line paste is captured whole with
// no extra keystroke. Falls back to nothing special for typed input (ends on
// Enter). The terminal is put in raw mode only for the duration of the read.
func readTokenFromTerminal(in *os.File) (string, error) {
	state, err := term.EnableRawMode(in)
	if err != nil {
		return "", fmt.Errorf("enabling raw mode: %w", err)
	}
	defer func() {
		fmt.Print(bracketedPasteOff)
		_ = term.RestoreTerminal(state)
		fmt.Println()
	}()
	fmt.Print(bracketedPasteOn)

	return scanBracketedPaste(in)
}

// scanBracketedPaste reads bytes until a bracketed paste (ESC[200~ … ESC[201~)
// completes, or until Enter/EOF for manually typed input. It is pure over an
// io.Reader so it can be tested without a real terminal. All whitespace is
// stripped from the result.
func scanBracketedPaste(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	var typed, pasted strings.Builder
	inPaste := false

	for {
		b, err := br.ReadByte()
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}

		if b == 0x1b { // ESC: possible CSI sequence
			seq, serr := readCSI(br)
			if serr != nil {
				if serr == io.EOF {
					break
				}
				return "", serr
			}
			switch seq {
			case "200~":
				inPaste = true
			case "201~":
				return stripWhitespace(pasted.String()), nil
			}
			continue // ignore all other escape sequences
		}

		if inPaste {
			pasted.WriteByte(b)
			continue
		}

		// Manually typed input (no paste markers).
		switch b {
		case '\r', '\n':
			if typed.Len() > 0 {
				return stripWhitespace(typed.String()), nil
			}
		case 0x7f, 0x08: // backspace / delete
			if s := typed.String(); s != "" {
				typed.Reset()
				typed.WriteString(s[:len(s)-1])
			}
		case 0x03: // Ctrl-C
			return "", fmt.Errorf("input canceled")
		case 0x04: // Ctrl-D
			if typed.Len() > 0 {
				return stripWhitespace(typed.String()), nil
			}
			return "", io.EOF
		default:
			if b >= 0x20 {
				typed.WriteByte(b)
			}
		}
	}

	if inPaste {
		return stripWhitespace(pasted.String()), nil
	}
	return stripWhitespace(typed.String()), nil
}

// readCSI consumes a CSI sequence after an ESC byte and returns its parameter
// and final bytes (e.g. "200~"). If ESC is not followed by '[', the single
// byte is returned so the caller can ignore it.
func readCSI(br *bufio.Reader) (string, error) {
	c, err := br.ReadByte()
	if err != nil {
		return "", err
	}
	if c != '[' {
		return string(rune(c)), nil
	}
	var sb strings.Builder
	for {
		d, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		sb.WriteByte(d)
		if d >= 0x40 && d <= 0x7e { // CSI final byte
			return sb.String(), nil
		}
	}
}

// promptAndValidateOAuthToken reads an OAuth token pasted by the user, checks
// its format, validates it against the API, and returns the credential.
// readOAuthTokenInput reads a pasted OAuth token from stdin. On an interactive
// terminal it uses bracketed paste; otherwise it reads from reader.
//
// reader MUST be the same bufio.Reader the menu prompt read the choice from.
// Piped stdin is typically delivered in a single read, so reading the menu
// choice can buffer the token line inside that reader; creating a fresh
// bufio.Reader on os.Stdin here would read an already-drained stdin and lose
// the token.
func readOAuthTokenInput(reader *bufio.Reader, stdin *os.File) (string, error) {
	if term.IsTerminal(stdin) {
		// Bracketed paste captures the whole paste — including a token the
		// source terminal soft-wrapped onto multiple lines — with no extra
		// keystroke.
		return readTokenFromTerminal(stdin)
	}
	// Piped/non-interactive input: read until a blank line or EOF.
	return readPastedToken(reader)
}

func promptAndValidateOAuthToken(ctx context.Context, reader *bufio.Reader) (*provider.Credential, error) {
	// A blank line and a section header separate moat's prompt from whatever the
	// `claude setup-token` TUI just rendered above it.
	fmt.Println()
	ui.Section("Paste your token into moat")
	fmt.Println("It starts with sk-ant-oat01-.")

	token, err := readOAuthTokenInput(reader, os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("reading input: %w", err)
	}

	if token == "" {
		return nil, fmt.Errorf("OAuth token cannot be empty")
	}

	if !strings.HasPrefix(token, "sk-ant-oat") {
		return nil, fmt.Errorf("invalid token format: expected an OAuth token starting with \"sk-ant-oat\"")
	}

	fmt.Println("\nValidating OAuth token...")
	auth := &anthropicAuth{}
	validateCtx, validateCancel := context.WithTimeout(ctx, 30*time.Second)
	defer validateCancel()

	if err := auth.ValidateOAuthToken(validateCtx, token); err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}
	fmt.Println("OAuth token is valid.")

	cred := &provider.Credential{
		Provider:  "claude",
		Token:     token,
		CreatedAt: time.Now(),
	}

	fmt.Println("\nClaude credential acquired.")
	fmt.Println("You can now run 'moat claude' to start Claude Code.")
	return cred, nil
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
	fmt.Println()
	fmt.Println("  Warning: This imports your current authorization token only.")
	fmt.Println("  It is short-lived and will not be refreshed automatically.")
	fmt.Println("  For a longer-lived session, use the Claude subscription (OAuth)")
	fmt.Println("  or existing OAuth token options instead.")
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
		Provider:  "claude",
		Token:     token.AccessToken,
		Scopes:    token.Scopes,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	// Preserve the real subscription details so the container's .credentials.json
	// reflects the actual plan. Setup-token/pasted grants don't carry these and
	// fall back to defaults (or the moat.yaml override).
	cred.Metadata = subscriptionMetadata(token.SubscriptionType, token.RateLimitTier)

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
