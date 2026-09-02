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
	"time"

	"github.com/majorcontext/moat/internal/log"
)

const (
	anthropicAPIURL = "https://api.anthropic.com/v1/messages"

	// validationModel is the model used for token validation.
	// Using Haiku for minimal cost during key verification.
	validationModel = "claude-haiku-4-5-20251001"

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
//
// A gateway key (baseURL set) skips the sk-ant- prefix check: a third-party
// Anthropic-compatible gateway issues keys in its own format.
func (a *anthropicAuth) PromptForAPIKey(baseURL string) (string, error) {
	if baseURL != "" {
		fmt.Printf("Enter the API key for %s.\n", baseURL)
	} else {
		fmt.Println("Enter your Anthropic API key.")
		fmt.Println("You can find or create one at: https://console.anthropic.com/settings/keys")
	}
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

	// Basic format validation to catch obvious errors early. Only Anthropic's
	// own keys have a known prefix.
	if baseURL == "" && !strings.HasPrefix(key, anthropicKeyPrefix) {
		return "", fmt.Errorf("invalid API key format: Anthropic keys start with %q\n\nFor a key issued by an Anthropic-compatible gateway, pass its endpoint:\n  moat grant anthropic --base-url https://gateway.example.com", anthropicKeyPrefix)
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

// ValidateGatewayKey validates a key against an Anthropic-compatible gateway
// rather than api.anthropic.com.
//
// Only 401 and 403 count as failures. A gateway serves its own model catalog,
// so the fixed validation model is usually unknown to it and the request comes
// back 400 or 404 — which still proves the key authenticated. Being stricter
// would reject working keys on every gateway that does not happen to serve
// Anthropic's model ids.
//
// The key is sent as x-api-key, the same header moat injects at runtime for an
// anthropic credential, so a gateway that only accepts Bearer tokens fails here
// rather than at the first real request.
func (a *anthropicAuth) ValidateGatewayKey(ctx context.Context, apiKey, baseURL string) error {
	endpoint := strings.TrimSuffix(baseURL, "/") + "/v1/messages"
	reqBody := fmt.Sprintf(`{"model":%q,"max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`, validationModel)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	log.Debug("gateway key validation response",
		"subsystem", "grant",
		"action", "validate_gateway",
		"endpoint", endpoint,
		"status", resp.StatusCode,
	)

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s rejected the key (401) — check that it is correct and not revoked", baseURL)
	case http.StatusForbidden:
		return fmt.Errorf("%s rejected the key (403) — the key lacks permission for this endpoint", baseURL)
	}
	return nil
}

// ValidateOAuthToken validates an OAuth token by making a minimal API request.
// OAuth tokens require Bearer auth with specific beta flags, unlike API keys
// which use the x-api-key header.
// Returns nil if the token is valid, or an error describing the problem.
func (a *anthropicAuth) ValidateOAuthToken(ctx context.Context, token string) error {
	reqBody := fmt.Sprintf(`{"model":%q,"max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`, validationModel)
	apiURL := a.apiURL()

	log.Debug("validating OAuth token",
		"subsystem", "grant",
		"action", "validate_oauth",
		"api_url", apiURL,
		"model", validationModel,
		"token_len", len(token),
	)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	start := time.Now()
	resp, err := a.httpClient().Do(req)
	elapsed := time.Since(start)
	if err != nil {
		log.Error("OAuth validation request failed",
			"subsystem", "grant",
			"action", "validate_oauth",
			"error", err,
			"elapsed", elapsed.String(),
		)
		return fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	log.Debug("OAuth validation response",
		"subsystem", "grant",
		"action", "validate_oauth",
		"status", resp.StatusCode,
		"elapsed", elapsed.String(),
	)

	if resp.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	log.Error("OAuth validation failed",
		"subsystem", "grant",
		"action", "validate_oauth",
		"status", resp.StatusCode,
		"response_body", bodyStr,
		"elapsed", elapsed.String(),
	)

	// Check for OAuth-specific errors that indicate the endpoint requirements
	// have changed — flag this so users know it's a moat issue, not their token.
	if strings.Contains(bodyStr, "OAuth") {
		return fmt.Errorf("OAuth validation failed (status %d): the Anthropic OAuth endpoint may have changed — "+
			"try updating moat, or use 'moat doctor claude --test-container' to diagnose", resp.StatusCode)
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("invalid OAuth token (check that the token is correct and not corrupted)")
	case http.StatusForbidden:
		return fmt.Errorf("OAuth token lacks required permissions")
	default:
		return fmt.Errorf("API error (status %d)", resp.StatusCode)
	}
}
