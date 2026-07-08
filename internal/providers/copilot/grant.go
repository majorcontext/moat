package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

// Grant handles Copilot credential acquisition.
type Grant struct{}

func NewGrant() *Grant { return &Grant{} }

func (g *Grant) Execute(ctx context.Context) (*provider.Credential, error) {
	if token, name := util.CheckEnvVarWithName("COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"); token != "" {
		fmt.Printf("Using token from %s environment variable\n", name)
		return validateAndCreateCredential(ctx, token, SourceEnv)
	}

	token, ghErr := getGHCLIToken(ctx)
	if ghErr == nil && token != "" {
		fmt.Println("Found gh CLI authentication")
		confirmed, err := util.Confirm("Use token from gh CLI for Copilot?")
		if err != nil {
			return nil, fmt.Errorf("reading confirmation: %w", err)
		}
		if confirmed {
			return validateAndCreateCredential(ctx, token, SourceCLI)
		}
		fmt.Println()
	} else if ghErr != nil && isGHCLIInstalled() {
		fmt.Printf("Note: gh CLI found but 'gh auth token' failed: %v\n", ghErr)
		fmt.Println("You may need to run 'gh auth login' first.")
		fmt.Println()
	}

	fmt.Println(`Enter a GitHub token that can use Copilot CLI.

Supported token types:
  - GitHub CLI OAuth token from 'gh auth login'
  - Fine-grained PAT from your personal account with "Copilot Requests"

To create a fine-grained PAT:
  1. Visit https://github.com/settings/personal-access-tokens/new
  2. Select your personal account as the resource owner
  3. Add the "Copilot Requests" account permission
  4. Select repository access appropriate for your workflow
  5. Copy the generated token`)

	token, err := util.PromptForToken("Token")
	if err != nil {
		return nil, fmt.Errorf("reading token: %w", err)
	}
	if token == "" {
		return nil, &provider.GrantError{
			Provider: copilotProviderName,
			Cause:    fmt.Errorf("no token provided"),
			Hint:     "Run 'moat grant copilot' and enter a Copilot-capable GitHub token",
		}
	}
	return validateAndCreateCredential(ctx, token, SourcePAT)
}

func validateAndCreateCredential(ctx context.Context, token, source string) (*provider.Credential, error) {
	fmt.Println("Validating Copilot token...")
	if err := validateCopilotToken(ctx, token); err != nil {
		return nil, &provider.GrantError{
			Provider: copilotProviderName,
			Cause:    err,
			Hint:     "Use a GitHub CLI OAuth token or a fine-grained PAT with the Copilot Requests permission",
		}
	}
	fmt.Println("Copilot token validated successfully")
	return &provider.Credential{
		Provider:  copilotProviderName,
		Token:     token,
		CreatedAt: time.Now(),
		Metadata:  map[string]string{provider.MetaKeyTokenSource: source},
	}, nil
}

func validateCopilotToken(ctx context.Context, token string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", "https://api.github.com/copilot_internal/user", nil)
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

func getGHCLIToken(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func isGHCLIInstalled() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}
