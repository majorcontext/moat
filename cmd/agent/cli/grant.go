// cmd/agent/cli/grant.go
package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/andybons/agentops/internal/credential"
	"github.com/andybons/agentops/internal/log"
	"github.com/spf13/cobra"
)

// getGitHubClientID returns the GitHub OAuth client ID from environment.
// Returns empty string if not configured.
func getGitHubClientID() string {
	return os.Getenv("AGENTOPS_GITHUB_CLIENT_ID")
}

var grantCmd = &cobra.Command{
	Use:   "grant <provider>[:<scopes>]",
	Short: "Grant a credential for use in runs",
	Long: `Grant a credential that can be used by agent runs.

Credentials are stored securely and injected into agent containers when
requested via the --grant flag on 'agent run'.

Supported providers:
  github      GitHub OAuth (uses device flow for authentication)
  anthropic   Anthropic API key (for Claude Code and Claude API)

Scope format:
  provider              Use default scopes
  provider:scope        Single scope
  provider:s1,s2,s3     Multiple scopes (comma-separated)

GitHub scopes (see https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps):
  repo                  Full control of private repositories (default)
  read:user             Read user profile data
  user:email            Access user email addresses
  workflow              Update GitHub Action workflows

Examples:
  # Grant GitHub access with default scopes (repo)
  agent grant github

  # Grant GitHub access with repo scope only
  agent grant github:repo

  # Grant GitHub access with multiple scopes
  agent grant github:repo,read:user,user:email

  # Use the credential in a run
  agent run my-agent . --grant github

  # Grant Anthropic API access (interactive prompt)
  agent grant anthropic

  # Grant Anthropic API access (from environment variable)
  export ANTHROPIC_API_KEY="sk-ant-..."  # set in your shell profile
  agent grant anthropic

  # Use Anthropic credential for Claude Code
  agent run claude-code-test . --grant anthropic`,
	Args: cobra.ExactArgs(1),
	RunE: runGrant,
}

func init() {
	rootCmd.AddCommand(grantCmd)
}

func runGrant(cmd *cobra.Command, args []string) error {
	arg := args[0]

	// Parse provider:scopes format
	parts := strings.SplitN(arg, ":", 2)
	providerStr := parts[0]
	var scopes []string
	if len(parts) > 1 {
		scopes = strings.Split(parts[1], ",")
	}

	provider := credential.Provider(providerStr)

	switch provider {
	case credential.ProviderGitHub:
		return grantGitHub(scopes)
	case credential.ProviderAnthropic:
		return grantAnthropic()
	default:
		return fmt.Errorf("unsupported provider: %s", providerStr)
	}
}

func grantGitHub(scopes []string) error {
	clientID := getGitHubClientID()
	if clientID == "" {
		return fmt.Errorf(`GitHub OAuth App not configured

To use 'agent grant github', set the AGENTOPS_GITHUB_CLIENT_ID environment variable:

  export AGENTOPS_GITHUB_CLIENT_ID="your-client-id"

To create a GitHub OAuth App:
  1. Go to https://github.com/settings/developers
  2. Click "New OAuth App"
  3. Enable "Device Flow" in the app settings
  4. Copy the Client ID

See README.md for detailed setup instructions.`)
	}

	if len(scopes) == 0 {
		scopes = []string{"repo"}
	}

	auth := &credential.GitHubDeviceAuth{
		ClientID: clientID,
		Scopes:   scopes,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	log.Info("initiating GitHub device flow")

	deviceCode, err := auth.RequestDeviceCode(ctx)
	if err != nil {
		return fmt.Errorf("requesting device code: %w", err)
	}

	fmt.Printf("\nTo authorize, visit: %s\n", deviceCode.VerificationURI)
	fmt.Printf("Enter code: %s\n\n", deviceCode.UserCode)
	fmt.Println("Waiting for authorization...")

	token, err := auth.PollForToken(ctx, deviceCode.DeviceCode, deviceCode.Interval)
	if err != nil {
		return fmt.Errorf("getting token: %w", err)
	}

	// Store the credential
	store, err := credential.NewFileStore(
		credential.DefaultStoreDir(),
		credential.DefaultEncryptionKey(),
	)
	if err != nil {
		return fmt.Errorf("opening credential store: %w", err)
	}

	cred := credential.Credential{
		Provider:  credential.ProviderGitHub,
		Token:     token.AccessToken,
		Scopes:    scopes,
		CreatedAt: time.Now(),
	}

	if err := store.Save(cred); err != nil {
		return fmt.Errorf("saving credential: %w", err)
	}

	log.Info("GitHub credential saved", "scopes", scopes)
	fmt.Println("GitHub credential saved successfully")
	return nil
}

func grantAnthropic() error {
	auth := &credential.AnthropicAuth{}

	// Get API key from environment variable or interactive prompt
	// Environment variable is preferred for non-interactive/CI use
	var apiKey string
	var err error
	if envKey := os.Getenv("ANTHROPIC_API_KEY"); envKey != "" {
		apiKey = envKey
		fmt.Println("Using API key from ANTHROPIC_API_KEY environment variable")
	} else {
		apiKey, err = auth.PromptForAPIKey()
		if err != nil {
			return fmt.Errorf("reading API key: %w", err)
		}
	}

	// Validate the key
	fmt.Println("\nValidating API key...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := auth.ValidateKey(ctx, apiKey); err != nil {
		return fmt.Errorf("validating API key: %w", err)
	}

	// Store the credential
	store, err := credential.NewFileStore(
		credential.DefaultStoreDir(),
		credential.DefaultEncryptionKey(),
	)
	if err != nil {
		return fmt.Errorf("opening credential store: %w", err)
	}

	cred := auth.CreateCredential(apiKey)
	if err := store.Save(cred); err != nil {
		return fmt.Errorf("saving credential: %w", err)
	}

	log.Info("Anthropic credential saved")
	fmt.Println("Anthropic API key saved successfully")
	return nil
}
