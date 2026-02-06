package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

// Grant acquires GitHub credentials interactively or from environment.
//
// Token acquisition order:
//  1. GITHUB_TOKEN or GH_TOKEN environment variable
//  2. gh CLI token via `gh auth token`
//  3. Interactive PAT prompt
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	// Priority 1: Environment variable
	if token, name := util.CheckEnvVarWithName("GITHUB_TOKEN", "GH_TOKEN"); token != "" {
		fmt.Printf("Using token from %s environment variable\n", name)
		return p.validateAndCreateCredential(ctx, token, SourceEnv)
	}

	// Priority 2: gh CLI
	token, ghErr := getGHCLIToken(ctx)
	if ghErr == nil && token != "" {
		fmt.Println("Found gh CLI authentication")
		confirmed, err := util.Confirm("Use token from gh CLI?")
		if err != nil {
			return nil, fmt.Errorf("reading confirmation: %w", err)
		}
		if confirmed {
			return p.validateAndCreateCredential(ctx, token, SourceCLI)
		}
		fmt.Println() // spacing before prompt
	} else if ghErr != nil && isGHCLIInstalled() {
		// gh CLI is installed but failed - warn user
		fmt.Printf("Note: gh CLI found but 'gh auth token' failed: %v\n", ghErr)
		fmt.Println("You may need to run 'gh auth login' first.")
		fmt.Println()
	}

	// Priority 3: Interactive prompt
	fmt.Println(`Enter a GitHub Personal Access Token.

To create one:
  1. Visit https://github.com/settings/tokens
  2. Click "Generate new token" -> "Fine-grained token" (recommended)
  3. Set expiration and select repositories
  4. Under "Repository permissions", grant "Contents" read/write access
  5. Copy the generated token`)

	token, err := util.PromptForToken("Token")
	if err != nil {
		return nil, fmt.Errorf("reading token: %w", err)
	}
	if token == "" {
		return nil, &provider.GrantError{
			Provider: "github",
			Cause:    fmt.Errorf("no token provided"),
			Hint:     "Run 'moat grant github' and enter a valid GitHub token",
		}
	}

	return p.validateAndCreateCredential(ctx, token, SourcePAT)
}

// validateAndCreateCredential validates the token and creates a credential.
func (p *Provider) validateAndCreateCredential(ctx context.Context, token, source string) (*provider.Credential, error) {
	fmt.Println("Validating token...")

	username, err := validateGitHubToken(ctx, token)
	if err != nil {
		return nil, &provider.GrantError{
			Provider: "github",
			Cause:    err,
			Hint:     "Ensure your token is valid and has appropriate permissions",
		}
	}

	fmt.Printf("Authenticated as: %s\n", username)

	return &provider.Credential{
		Provider:  "github",
		Token:     token,
		CreatedAt: time.Now(),
		Metadata:  map[string]string{provider.MetaKeyTokenSource: source},
	}, nil
}

// validateGitHubToken validates the token by calling the GitHub API /user endpoint.
// Returns the username on success.
func validateGitHubToken(ctx context.Context, token string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "moat")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("validating token: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		// Success - parse username
		var user struct {
			Login string `json:"login"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&user); decodeErr != nil {
			return "", fmt.Errorf("parsing user response: %w", decodeErr)
		}
		return user.Login, nil
	case 401:
		return "", fmt.Errorf("invalid token (401 Unauthorized)")
	case 403:
		return "", fmt.Errorf("token validation failed (403 Forbidden) - token may lack permissions or you may be rate limited")
	default:
		return "", fmt.Errorf("unexpected status validating token: %d", resp.StatusCode)
	}
}

// getGHCLIToken retrieves the GitHub token from gh CLI if available.
func getGHCLIToken(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// isGHCLIInstalled checks if the gh CLI is available in PATH.
func isGHCLIInstalled() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}
