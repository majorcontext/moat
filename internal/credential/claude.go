// Package credential provides credential management for Moat.
package credential

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

const (
	// ClaudeCodeKeychainService is the macOS Keychain service name for Claude Code credentials.
	ClaudeCodeKeychainService = "Claude Code-credentials"

	// ClaudeCredentialsFile is the relative path to Claude's credentials file.
	ClaudeCredentialsFile = ".claude/.credentials.json"
)

// ClaudeOAuthCredentials represents the OAuth credentials stored by Claude Code.
type ClaudeOAuthCredentials struct {
	ClaudeAiOauth *ClaudeOAuthToken `json:"claudeAiOauth,omitempty"`
}

// ClaudeOAuthToken represents an individual OAuth token from Claude Code.
type ClaudeOAuthToken struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAt        int64    `json:"expiresAt"` // Unix timestamp in milliseconds
	Scopes           []string `json:"scopes"`
	SubscriptionType string   `json:"subscriptionType,omitempty"`
	RateLimitTier    string   `json:"rateLimitTier,omitempty"`
}

// ExpiresAtTime returns the expiration time as a time.Time.
func (t *ClaudeOAuthToken) ExpiresAtTime() time.Time {
	return time.UnixMilli(t.ExpiresAt)
}

// IsExpired returns true if the token has expired.
func (t *ClaudeOAuthToken) IsExpired() bool {
	return time.Now().After(t.ExpiresAtTime())
}

// ClaudeCodeCredentials handles extraction of Claude Code OAuth credentials.
type ClaudeCodeCredentials struct{}

// GetClaudeCodeCredentials attempts to retrieve Claude Code OAuth credentials.
// It tries the following sources in order:
// 1. macOS Keychain (if on macOS)
// 2. ~/.claude/.credentials.json file
//
// Returns the credentials if found, or an error describing what went wrong.
func (c *ClaudeCodeCredentials) GetClaudeCodeCredentials() (*ClaudeOAuthToken, error) {
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

// getFromKeychain retrieves Claude Code credentials from macOS Keychain.
func (c *ClaudeCodeCredentials) getFromKeychain() (*ClaudeOAuthToken, error) {
	// Use the security command to retrieve the password
	cmd := exec.Command("security", "find-generic-password",
		"-s", ClaudeCodeKeychainService,
		"-w", // Output only the password
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("keychain lookup failed: %w", err)
	}

	// Parse the JSON credentials
	var creds ClaudeOAuthCredentials
	if err := json.Unmarshal(output, &creds); err != nil {
		return nil, fmt.Errorf("parsing keychain credentials: %w", err)
	}

	if creds.ClaudeAiOauth == nil {
		return nil, fmt.Errorf("no OAuth credentials found in keychain")
	}

	return creds.ClaudeAiOauth, nil
}

// getFromFile retrieves Claude Code credentials from ~/.claude/.credentials.json.
func (c *ClaudeCodeCredentials) getFromFile() (*ClaudeOAuthToken, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	credPath := filepath.Join(home, ClaudeCredentialsFile)
	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Claude Code credentials not found at %s\n"+
				"  Have you logged into Claude Code? Run 'claude' to authenticate first", credPath)
		}
		return nil, fmt.Errorf("reading credentials file: %w", err)
	}

	var creds ClaudeOAuthCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parsing credentials file: %w", err)
	}

	if creds.ClaudeAiOauth == nil {
		return nil, fmt.Errorf("no OAuth credentials found in %s", credPath)
	}

	return creds.ClaudeAiOauth, nil
}

// CreateCredentialFromOAuth creates a Moat Credential from Claude Code OAuth token.
func (c *ClaudeCodeCredentials) CreateCredentialFromOAuth(token *ClaudeOAuthToken) Credential {
	cred := Credential{
		Provider:  ProviderAnthropic,
		Token:     token.AccessToken,
		Scopes:    token.Scopes,
		CreatedAt: time.Now(),
	}

	// Set expiration if available
	if token.ExpiresAt > 0 {
		cred.ExpiresAt = token.ExpiresAtTime()
	}

	return cred
}

// HasClaudeCodeCredentials checks if Claude Code credentials are available.
func (c *ClaudeCodeCredentials) HasClaudeCodeCredentials() bool {
	_, err := c.GetClaudeCodeCredentials()
	return err == nil
}
