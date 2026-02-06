package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	anthropicAPIURL = "https://api.anthropic.com/v1/messages"

	// validationModel is the model used for API key validation.
	// Using Haiku for minimal cost during key verification.
	validationModel = "claude-3-haiku-20240307"

	// anthropicKeyPrefix is the expected prefix for Anthropic API keys.
	anthropicKeyPrefix = "sk-ant-"
)

// anthropicAuth handles Anthropic API key authentication.
type anthropicAuth struct {
	HTTPClient *http.Client // Optional; uses http.DefaultClient if nil
	APIURL     string       // Optional; allows overriding the endpoint for testing
}

// httpClient returns the HTTP client to use for requests.
func (a *anthropicAuth) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return http.DefaultClient
}

// apiURL returns the API endpoint URL.
func (a *anthropicAuth) apiURL() string {
	if a.APIURL != "" {
		return a.APIURL
	}
	return anthropicAPIURL
}

// PromptForAPIKey prompts the user to enter their Anthropic API key.
func (a *anthropicAuth) PromptForAPIKey() (string, error) {
	fmt.Println("Enter your Anthropic API key.")
	fmt.Println("You can find or create one at: https://console.anthropic.com/settings/keys")
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
	if !strings.HasPrefix(key, anthropicKeyPrefix) {
		return "", fmt.Errorf("invalid API key format: Anthropic keys start with %q", anthropicKeyPrefix)
	}

	return key, nil
}

// ValidateKey validates an Anthropic API key by making a minimal API request.
// Returns nil if the key is valid, or an error describing the problem.
func (a *anthropicAuth) ValidateKey(ctx context.Context, apiKey string) error {
	// Make a minimal request to validate the key.
	// We use a simple message request with max_tokens=1 to minimize cost.
	reqBody := fmt.Sprintf(`{"model":%q,"max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`, validationModel)

	req, err := http.NewRequestWithContext(ctx, "POST", a.apiURL(), strings.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

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
	// API error messages might contain partial key material or other sensitive data.
	body, _ := io.ReadAll(resp.Body)

	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &errResp)

	// Use generic error messages to prevent information disclosure
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("invalid API key (check that the key is correct and not expired)")
	case http.StatusForbidden:
		return fmt.Errorf("API key lacks required permissions")
	case http.StatusBadRequest:
		// Check error type for credit-related issues (safe to check type, not message)
		if errResp.Error.Type == "invalid_request_error" && strings.Contains(errResp.Error.Message, "credit") {
			return fmt.Errorf("API key has insufficient credits")
		}
		return fmt.Errorf("invalid request (status %d)", resp.StatusCode)
	default:
		return fmt.Errorf("API error (status %d)", resp.StatusCode)
	}
}
