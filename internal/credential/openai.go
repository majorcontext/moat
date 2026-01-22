package credential

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	openaiAPIURL = "https://api.openai.com/v1/models"

	// openaiKeyPrefix is the expected prefix for OpenAI API keys.
	openaiKeyPrefix = "sk-"

	// codexKeychainService is the macOS Keychain service name for Codex credentials.
	codexKeychainService = "codex-credentials"

	// codexCredentialsFile is the relative path to Codex's auth file.
	codexCredentialsFile = ".codex/auth.json"
)

// OpenAIAuth handles OpenAI API key authentication.
type OpenAIAuth struct {
	HTTPClient *http.Client // Optional; uses http.DefaultClient if nil

	// APIURL allows overriding the endpoint for testing.
	APIURL string
}

// httpClient returns the HTTP client to use for requests.
func (a *OpenAIAuth) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return http.DefaultClient
}

// apiURL returns the API endpoint URL.
func (a *OpenAIAuth) apiURL() string {
	if a.APIURL != "" {
		return a.APIURL
	}
	return openaiAPIURL
}

// PromptForAPIKey prompts the user to enter their OpenAI API key.
func (a *OpenAIAuth) PromptForAPIKey() (string, error) {
	fmt.Println("Enter your OpenAI API key.")
	fmt.Println("You can find or create one at: https://platform.openai.com/api-keys")
	fmt.Print("\nAPI Key: ")

	reader := bufio.NewReader(os.Stdin)
	key, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading input: %w", err)
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("API key cannot be empty")
	}

	// Basic format validation to catch obvious errors early
	if !strings.HasPrefix(key, openaiKeyPrefix) {
		return "", fmt.Errorf("invalid API key format: OpenAI keys start with %q", openaiKeyPrefix)
	}

	return key, nil
}

// ValidateKey validates an OpenAI API key by making a minimal API request.
// Returns nil if the key is valid, or an error describing the problem.
func (a *OpenAIAuth) ValidateKey(ctx context.Context, apiKey string) error {
	// Make a minimal request to validate the key.
	// We use the models endpoint which is lightweight.
	req, err := http.NewRequestWithContext(ctx, "GET", a.apiURL(), nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := a.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// Key is valid - consume and discard the response
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	// Parse error response but use generic messages to avoid leaking sensitive info.
	body, _ := io.ReadAll(resp.Body)

	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &errResp)

	// Use generic error messages to prevent information disclosure
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("invalid API key (check that the key is correct and not expired)")
	case http.StatusForbidden:
		return fmt.Errorf("API key lacks required permissions")
	case http.StatusTooManyRequests:
		return fmt.Errorf("rate limited - key is valid but quota exceeded")
	default:
		return fmt.Errorf("API error (status %d)", resp.StatusCode)
	}
}

// CreateCredential creates a Credential from a validated API key.
func (a *OpenAIAuth) CreateCredential(apiKey string) Credential {
	return Credential{
		Provider:  ProviderOpenAI,
		Token:     apiKey,
		CreatedAt: time.Now(),
	}
}

// CodexAuthToken represents the OAuth token structure stored by Codex CLI.
type CodexAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"` // Unix timestamp in seconds
	TokenType    string `json:"token_type,omitempty"`
}

// CodexAuthFile represents the auth.json file structure.
type CodexAuthFile struct {
	// Token is the ChatGPT subscription token
	Token *CodexAuthToken `json:"token,omitempty"`

	// APIKey is the OpenAI API key (field name matches Codex CLI's format)
	APIKey string `json:"OPENAI_API_KEY,omitempty"`
}

// ExpiresAtTime returns the expiration time as a time.Time.
func (t *CodexAuthToken) ExpiresAtTime() time.Time {
	if t.ExpiresAt == 0 {
		return time.Time{}
	}
	return time.Unix(t.ExpiresAt, 0)
}

// IsExpired returns true if the token has expired.
func (t *CodexAuthToken) IsExpired() bool {
	if t.ExpiresAt == 0 {
		return false // No expiration set means it doesn't expire
	}
	return time.Now().After(t.ExpiresAtTime())
}

// CodexCredentials handles extraction of Codex CLI OAuth credentials.
type CodexCredentials struct{}

// GetCodexCredentials attempts to retrieve Codex CLI OAuth credentials.
// It tries the following sources in order:
// 1. macOS Keychain (if on macOS)
// 2. ~/.codex/auth.json file
//
// Returns the credentials if found, or an error describing what went wrong.
func (c *CodexCredentials) GetCodexCredentials() (*CodexAuthToken, error) {
	// Try keychain first on macOS
	if runtime.GOOS == "darwin" {
		if token, err := c.getFromKeychain(); err == nil {
			return token, nil
		}
		// Fall through to file-based lookup if keychain fails
	}

	// Try credentials file
	return c.getFromFile()
}

// getFromKeychain retrieves Codex credentials from macOS Keychain.
func (c *CodexCredentials) getFromKeychain() (*CodexAuthToken, error) {
	// Use the security command to retrieve the password
	cmd := exec.Command("security", "find-generic-password",
		"-s", codexKeychainService,
		"-w", // Output only the password
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("keychain lookup failed: %w", err)
	}

	// Parse the JSON credentials
	var authFile CodexAuthFile
	if err := json.Unmarshal(output, &authFile); err != nil {
		return nil, fmt.Errorf("parsing keychain credentials: %w", err)
	}

	if authFile.Token == nil {
		return nil, fmt.Errorf("no OAuth token found in keychain")
	}

	return authFile.Token, nil
}

// getFromFile retrieves Codex credentials from ~/.codex/auth.json.
func (c *CodexCredentials) getFromFile() (*CodexAuthToken, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	credPath := filepath.Join(home, codexCredentialsFile)
	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Codex credentials not found at %s\n"+
				"  Have you logged into Codex? Run 'codex login' to authenticate first", credPath)
		}
		return nil, fmt.Errorf("reading credentials file: %w", err)
	}

	var authFile CodexAuthFile
	if err := json.Unmarshal(data, &authFile); err != nil {
		return nil, fmt.Errorf("parsing credentials file: %w", err)
	}

	if authFile.Token == nil {
		return nil, fmt.Errorf("no OAuth token found in %s", credPath)
	}

	return authFile.Token, nil
}

// CreateCredentialFromCodex creates a Moat Credential from Codex OAuth token.
func (c *CodexCredentials) CreateCredentialFromCodex(token *CodexAuthToken) Credential {
	cred := Credential{
		Provider:  ProviderOpenAI,
		Token:     token.AccessToken,
		CreatedAt: time.Now(),
	}

	// Set expiration if available
	if token.ExpiresAt > 0 {
		cred.ExpiresAt = token.ExpiresAtTime()
	}

	return cred
}

// HasCodexCredentials checks if Codex credentials are available.
func (c *CodexCredentials) HasCodexCredentials() bool {
	_, err := c.GetCodexCredentials()
	return err == nil
}

// IsCodexToken returns true if the token appears to be a Codex ChatGPT subscription token.
// ChatGPT tokens are typically longer than API keys and have a different format.
// API keys start with "sk-" (including "sk-proj-", "sk-svcacct-", etc.) regardless of length.
// Subscription tokens are OAuth tokens that don't have the sk- prefix.
func IsCodexToken(token string) bool {
	// API keys always start with "sk-" regardless of length
	// This includes newer formats like sk-proj-..., sk-svcacct-..., etc.
	if strings.HasPrefix(token, openaiKeyPrefix) {
		return false // It's an API key
	}
	// If it doesn't start with sk-, it's likely a subscription/OAuth token
	return true
}
