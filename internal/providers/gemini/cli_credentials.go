package gemini

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// CLIOAuthToken represents the OAuth credentials stored by Gemini CLI
// in ~/.gemini/oauth_creds.json.
type CLIOAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	ExpiryDate   int64  `json:"expiry_date"` // Unix timestamp in milliseconds
}

// ExpiresAtTime returns the expiration time.
func (t *CLIOAuthToken) ExpiresAtTime() time.Time {
	return time.UnixMilli(t.ExpiryDate)
}

// IsExpired returns true if the access token has expired.
func (t *CLIOAuthToken) IsExpired() bool {
	return time.Now().After(t.ExpiresAtTime())
}

// CLICredentials handles extraction of Gemini CLI OAuth credentials.
type CLICredentials struct {
	HomeDir string // Override for testing; empty uses os.UserHomeDir
}

func (c *CLICredentials) homeDir() (string, error) {
	if c.HomeDir != "" {
		return c.HomeDir, nil
	}
	return os.UserHomeDir()
}

// GetCredentials reads OAuth credentials from ~/.gemini/oauth_creds.json.
func (c *CLICredentials) GetCredentials() (*CLIOAuthToken, error) {
	home, err := c.homeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	credPath := filepath.Join(home, ".gemini", "oauth_creds.json")
	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Gemini CLI credentials not found at %s\n"+
				"  Run 'gemini' and select 'Login with Google' to authenticate first", credPath)
		}
		return nil, fmt.Errorf("reading credentials file: %w", err)
	}

	var token CLIOAuthToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("parsing credentials file: %w", err)
	}

	if token.AccessToken == "" {
		return nil, fmt.Errorf("no access token found in %s", credPath)
	}

	return &token, nil
}

// HasCredentials returns true if Gemini CLI credentials are available.
func (c *CLICredentials) HasCredentials() bool {
	_, err := c.GetCredentials()
	return err == nil
}

// CreateMoatCredential creates a provider.Credential from Gemini CLI OAuth token.
func (c *CLICredentials) CreateMoatCredential(token *CLIOAuthToken) *provider.Credential {
	auth := &Auth{}
	return auth.CreateOAuthCredential(token.AccessToken, token.RefreshToken, token.ExpiresAtTime())
}
