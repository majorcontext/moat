// cmd/moat/cli/grant.go
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andybons/moat/internal/credential"
	"github.com/spf13/cobra"
)

// getGitHubClientID returns the GitHub OAuth client ID from environment.
// Returns empty string if not configured.
func getGitHubClientID() string {
	return os.Getenv("MOAT_GITHUB_CLIENT_ID")
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
  moat grant github

  # Grant GitHub access with repo scope only
  moat grant github:repo

  # Grant GitHub access with multiple scopes
  moat grant github:repo,read:user,user:email

  # Use the credential in a run
  moat run my-agent . --grant github

  # Grant Anthropic API access (interactive prompt)
  moat grant anthropic

  # Grant Anthropic API access (from environment variable)
  export ANTHROPIC_API_KEY="sk-ant-..."  # set in your shell profile
  moat grant anthropic

  # Use Anthropic credential for Claude Code
  moat run claude-code-test . --grant anthropic`,
	Args: cobra.ExactArgs(1),
	RunE: runGrant,
}

func init() {
	rootCmd.AddCommand(grantCmd)
}

// saveCredential stores a credential and returns the file path.
func saveCredential(cred credential.Credential) (string, error) {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return "", fmt.Errorf("getting encryption key: %w", err)
	}
	store, err := credential.NewFileStore(
		credential.DefaultStoreDir(),
		key,
	)
	if err != nil {
		return "", fmt.Errorf("opening credential store: %w", err)
	}
	if err := store.Save(cred); err != nil {
		return "", fmt.Errorf("saving credential: %w", err)
	}
	return filepath.Join(credential.DefaultStoreDir(), string(cred.Provider)+".enc"), nil
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

To use 'moat grant github', set the MOAT_GITHUB_CLIENT_ID environment variable:

  export MOAT_GITHUB_CLIENT_ID="your-client-id"

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

	fmt.Println("Initiating GitHub device flow...")

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

	cred := credential.Credential{
		Provider:  credential.ProviderGitHub,
		Token:     token.AccessToken,
		Scopes:    scopes,
		CreatedAt: time.Now(),
	}
	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}
	fmt.Printf("GitHub credential saved to %s\n", credPath)
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

	// Ask user if they want to validate the key (costs a small API call)
	fmt.Print("\nValidate API key with a test request? This makes a small API call. [Y/n]: ")
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response == "" || response == "y" || response == "yes" {
		fmt.Println("\nValidating API key...")
		fmt.Println("  POST https://api.anthropic.com/v1/messages")
		fmt.Println(`  {"model":"claude-sonnet-4-20250514","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if validateErr := auth.ValidateKey(ctx, apiKey); validateErr != nil {
			return fmt.Errorf("validating API key: %w", validateErr)
		}
		fmt.Println("API key is valid.")
	} else {
		fmt.Println("Skipping validation.")
	}

	cred := auth.CreateCredential(apiKey)
	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}
	fmt.Printf("Anthropic API key saved to %s\n", credPath)
	return nil
}
