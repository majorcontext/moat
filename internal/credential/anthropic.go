// Package credential provides credential management for AgentOps.
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
	anthropicAPIURL = "https://api.anthropic.com/v1/messages"
)

// AnthropicAuth handles Anthropic API key authentication.
type AnthropicAuth struct {
	HTTPClient *http.Client // Optional; uses http.DefaultClient if nil

	// APIURL allows overriding the endpoint for testing.
	APIURL string
}

// httpClient returns the HTTP client to use for requests.
func (a *AnthropicAuth) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return http.DefaultClient
}

// apiURL returns the API endpoint URL.
func (a *AnthropicAuth) apiURL() string {
	if a.APIURL != "" {
		return a.APIURL
	}
	return anthropicAPIURL
}

// PromptForAPIKey prompts the user to enter their Anthropic API key.
func (a *AnthropicAuth) PromptForAPIKey() (string, error) {
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

	return key, nil
}

// ValidateKey validates an Anthropic API key by making a minimal API request.
// Returns nil if the key is valid, or an error describing the problem.
func (a *AnthropicAuth) ValidateKey(ctx context.Context, apiKey string) error {
	// Make a minimal request to validate the key.
	// We use a simple message request with max_tokens=1 to minimize cost.
	reqBody := `{"model":"claude-sonnet-4-20250514","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`

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

	// Try to parse error response
	body, _ := io.ReadAll(resp.Body)

	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if json.Unmarshal(body, &errResp) != nil || errResp.Error.Message == "" {
		return fmt.Errorf("unexpected response (%d): %s", resp.StatusCode, string(body))
	}

	msg := errResp.Error.Message
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("invalid API key: %s", msg)
	case http.StatusForbidden:
		return fmt.Errorf("API key lacks required permissions: %s", msg)
	case http.StatusBadRequest:
		if strings.Contains(msg, "credit") {
			return fmt.Errorf("API key has insufficient credits: %s", msg)
		}
		return fmt.Errorf("invalid request: %s", msg)
	default:
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, msg)
	}
}

// CreateCredential creates a Credential from a validated API key.
func (a *AnthropicAuth) CreateCredential(apiKey string) Credential {
	return Credential{
		Provider:  ProviderAnthropic,
		Token:     apiKey,
		CreatedAt: time.Now(),
	}
}
