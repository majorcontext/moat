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

	// oauthProfileURL is the endpoint for fetching OAuth user profile
	// information including subscription type.
	oauthProfileURL = "https://api.anthropic.com/api/oauth/profile"

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

// OAuthProfileInfo contains subscription information from the OAuth profile endpoint.
type OAuthProfileInfo struct {
	SubscriptionType string
	RateLimitTier    string
}

// FetchOAuthProfile calls the OAuth profile endpoint to retrieve subscription
// information for the authenticated user. This is used at container setup time
// to determine account capabilities (e.g. Max subscription for 1M context).
//
// Returns nil with no error if the endpoint returns a non-200 status (best-effort).
func (a *anthropicAuth) FetchOAuthProfile(ctx context.Context, token string) (*OAuthProfileInfo, error) {
	profileURL := oauthProfileURL
	if a.APIURL != "" {
		// For testing: derive profile URL from the overridden base URL
		profileURL = strings.TrimSuffix(a.APIURL, "/v1/messages") + "/api/oauth/profile"
	}

	req, err := http.NewRequestWithContext(ctx, "GET", profileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating profile request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	start := time.Now()
	resp, err := a.httpClient().Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("fetching OAuth profile: %w", err)
	}
	defer resp.Body.Close()

	log.Debug("OAuth profile response",
		"subsystem", "claude",
		"status", resp.StatusCode,
		"elapsed", elapsed.String(),
	)

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		// Non-200 is not fatal — long-lived tokens return 403 here
		return nil, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading profile response: %w", err)
	}

	// Parse the profile response. The exact structure may vary, so we
	// use a flexible map and look for subscription-related fields.
	var profile map[string]any
	if err := json.Unmarshal(body, &profile); err != nil {
		log.Debug("could not parse OAuth profile response",
			"subsystem", "claude",
			"error", err,
		)
		return nil, nil
	}

	log.Debug("OAuth profile fetched",
		"subsystem", "claude",
		"fields", mapKeys(profile),
	)

	info := &OAuthProfileInfo{}

	// Look for subscription type in common field names
	for _, key := range []string{"subscription_type", "subscriptionType", "plan_type", "planType"} {
		if v, ok := profile[key].(string); ok && v != "" {
			info.SubscriptionType = v
			break
		}
	}

	// Look for rate limit tier
	for _, key := range []string{"rate_limit_tier", "rateLimitTier"} {
		if v, ok := profile[key].(string); ok && v != "" {
			info.RateLimitTier = v
			break
		}
	}

	// Check nested "subscription" object
	if info.SubscriptionType == "" {
		if sub, ok := profile["subscription"].(map[string]any); ok {
			if v, ok := sub["type"].(string); ok && v != "" {
				info.SubscriptionType = v
			}
			if v, ok := sub["rate_limit_tier"].(string); ok && v != "" {
				info.RateLimitTier = v
			}
		}
	}

	if info.SubscriptionType == "" {
		return nil, nil
	}

	return info, nil
}

// mapKeys returns the keys of a map for debug logging.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
