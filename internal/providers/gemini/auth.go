package gemini

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// Auth handles Gemini API key authentication.
type Auth struct {
	HTTPClient *http.Client
	APIURL     string
}

func (a *Auth) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return http.DefaultClient
}

func (a *Auth) apiURL() string {
	if a.APIURL != "" {
		return a.APIURL
	}
	return ModelsURL
}

// PromptForAPIKey prompts the user to enter their Gemini API key.
func (a *Auth) PromptForAPIKey() (string, error) {
	fmt.Println("Enter your Gemini API key.")
	fmt.Println("You can create one at: https://aistudio.google.com/apikey")
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

// ValidateKey validates a Gemini API key by listing models.
func (a *Auth) ValidateKey(ctx context.Context, apiKey string) error {
	url := a.apiURL() + "?key=" + apiKey

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := a.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	_, _ = io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusBadRequest:
		return fmt.Errorf("invalid API key (check that the key is correct)")
	case http.StatusUnauthorized:
		return fmt.Errorf("invalid API key (check that the key is correct and not revoked)")
	case http.StatusForbidden:
		return fmt.Errorf("API key lacks required permissions")
	default:
		return fmt.Errorf("API error (status %d)", resp.StatusCode)
	}
}

// CreateAPIKeyCredential creates a provider.Credential from a validated API key.
func (a *Auth) CreateAPIKeyCredential(apiKey string) *provider.Credential {
	return &provider.Credential{
		Provider:  "gemini",
		Token:     apiKey,
		CreatedAt: time.Now(),
	}
}

// CreateOAuthCredential creates a provider.Credential from OAuth tokens.
// The access token is stored in Token, the refresh token in Metadata.
func (a *Auth) CreateOAuthCredential(accessToken, refreshToken string, expiresAt time.Time) *provider.Credential {
	return &provider.Credential{
		Provider:  "gemini",
		Token:     accessToken,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			"auth_type":     "oauth",
			"refresh_token": refreshToken,
		},
	}
}

// IsOAuthCredential returns true if the credential is a Gemini OAuth credential.
func IsOAuthCredential(cred *provider.Credential) bool {
	return cred != nil && cred.Metadata != nil && cred.Metadata["auth_type"] == "oauth"
}
