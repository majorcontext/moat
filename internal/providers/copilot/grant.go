package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var (
	copilotValidationURL = "https://api.github.com/copilot_internal/user"
	newCopilotHTTPClient = func() *http.Client {
		return &http.Client{Timeout: 10 * time.Second}
	}
)

// ValidateGitHubToken verifies that a GitHub token can authenticate Copilot CLI.
// It is intentionally separate from moat grant github, which remains a general
// GitHub credential setup command and accepts tokens that do not need Copilot.
func ValidateGitHubToken(ctx context.Context, token string) error {
	return validateCopilotToken(ctx, token)
}

func validateCopilotToken(ctx context.Context, token string) error {
	client := newCopilotHTTPClient()
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", copilotValidationURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "moat")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("validating token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	var body struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("invalid token (401 Unauthorized)")
	case http.StatusForbidden:
		if body.Message != "" {
			return fmt.Errorf("token rejected (403 Forbidden): %s", body.Message)
		}
		return fmt.Errorf("token rejected (403 Forbidden); the token may lack Copilot Requests permission or Copilot may be disabled")
	default:
		if body.Message != "" {
			return fmt.Errorf("unexpected status validating token: %d: %s", resp.StatusCode, body.Message)
		}
		return fmt.Errorf("unexpected status validating token: %d", resp.StatusCode)
	}
}
