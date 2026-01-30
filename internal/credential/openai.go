package credential

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	openaiAPIURL = "https://api.openai.com/v1/models"

	// openaiKeyPrefix is the expected prefix for OpenAI API keys.
	openaiKeyPrefix = "sk-"
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
