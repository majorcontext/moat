// cmd/moat/cli/grant.go
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/andybons/moat/internal/credential"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/cobra"
)

// AWS grant flags
var (
	awsRole            string
	awsRegion          string
	awsSessionDuration string
	awsExternalID      string
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
  anthropic   Anthropic API key or Claude Code OAuth credentials
  aws         AWS IAM role assumption (uses host credentials to assume role)

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

  # Grant Anthropic access (will auto-detect Claude Code credentials)
  moat grant anthropic

  # Grant Anthropic API access from environment variable
  export ANTHROPIC_API_KEY="sk-ant-..."  # set in your shell profile
  moat grant anthropic

  # Use Anthropic credential for Claude Code
  moat run my-agent . --grant anthropic

  # Grant AWS access via IAM role
  moat grant aws --role=arn:aws:iam::123456789012:role/AgentRole

  # Grant AWS with custom session duration and region
  moat grant aws --role=arn:aws:iam::123456789012:role/AgentRole \
    --region=us-west-2 --session-duration=1h

  # Use AWS credential in a run (credentials auto-refresh)
  moat run my-agent . --grant aws

If you have Claude Code installed and logged in, 'moat grant anthropic' will
offer to import your existing OAuth credentials. This is the easiest way to
get started - no API key required.`,
	Args: cobra.ExactArgs(1),
	RunE: runGrant,
}

func init() {
	rootCmd.AddCommand(grantCmd)
	grantCmd.Flags().StringVar(&awsRole, "role", "", "IAM role ARN to assume (required for aws)")
	grantCmd.Flags().StringVar(&awsRegion, "region", "", "AWS region (default: us-east-1)")
	grantCmd.Flags().StringVar(&awsSessionDuration, "session-duration", "", "Session duration (default: 15m, max: 12h)")
	grantCmd.Flags().StringVar(&awsExternalID, "external-id", "", "External ID for role assumption")
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
	case credential.ProviderAWS:
		if awsRole == "" {
			return fmt.Errorf(`--role is required for AWS grant

Usage: moat grant aws --role=arn:aws:iam::ACCOUNT:role/ROLE_NAME

Options:
  --role             IAM role ARN to assume (required)
  --region           AWS region (default: us-east-1)
  --session-duration Session duration (default: 15m, max: 12h)
  --external-id      External ID for role assumption`)
		}
		return grantAWS(awsRole, awsRegion, awsSessionDuration, awsExternalID)
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
	reader := bufio.NewReader(os.Stdin)

	// Check if claude is available for setup-token
	claudeAvailable := isClaudeAvailable()

	// Offer choices to user
	fmt.Println("Choose authentication method:")
	fmt.Println()
	if claudeAvailable {
		fmt.Println("  1. Claude subscription (recommended)")
		fmt.Println("     Uses 'claude setup-token' to get a long-lived OAuth token.")
		fmt.Println("     Requires a Claude Pro/Max subscription.")
		fmt.Println()
	}
	fmt.Println("  2. Anthropic API key")
	fmt.Println("     Use an API key from console.anthropic.com")
	fmt.Println("     Billed per token to your API account.")
	fmt.Println()

	// Check for existing Claude Code credentials as option 3
	claudeCode := &credential.ClaudeCodeCredentials{}
	hasExistingCreds := claudeCode.HasClaudeCodeCredentials()
	if hasExistingCreds {
		fmt.Println("  3. Import existing Claude Code credentials")
		fmt.Println("     Use OAuth tokens from your local Claude Code installation.")
		fmt.Println()
	}

	var validChoices string
	if claudeAvailable && hasExistingCreds {
		validChoices = "1, 2, or 3"
	} else if claudeAvailable {
		validChoices = "1 or 2"
	} else if hasExistingCreds {
		validChoices = "2 or 3"
	} else {
		validChoices = "2"
	}

	fmt.Printf("Enter choice [%s]: ", validChoices)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(response)

	// Default to option 1 if claude available, otherwise 2
	if response == "" {
		if claudeAvailable {
			response = "1"
		} else {
			response = "2"
		}
	}

	switch response {
	case "1":
		if !claudeAvailable {
			fmt.Println("Claude Code is not installed. Please choose another option.")
			return grantAnthropic() // Recurse to show menu again
		}
		return grantAnthropicViaSetupToken()

	case "2":
		return grantAnthropicViaAPIKey(reader)

	case "3":
		if !hasExistingCreds {
			fmt.Println("No existing Claude Code credentials found. Please choose another option.")
			return grantAnthropic() // Recurse to show menu again
		}
		return grantAnthropicViaExistingCreds(claudeCode)

	default:
		fmt.Printf("Invalid choice: %s\n", response)
		return grantAnthropic() // Recurse to show menu again
	}
}

// isClaudeAvailable checks if the claude CLI is installed.
func isClaudeAvailable() bool {
	cmd := exec.Command("claude", "--version")
	return cmd.Run() == nil
}

// grantAnthropicViaSetupToken uses `claude setup-token` to get an OAuth token.
func grantAnthropicViaSetupToken() error {
	fmt.Println()
	fmt.Println("Running 'claude setup-token' to obtain authentication token...")
	fmt.Println("This may open a browser for authentication.")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "setup-token")
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("claude setup-token failed: %w", err)
	}

	token := extractOAuthToken(string(output))
	if token == "" {
		return fmt.Errorf("could not find OAuth token in claude setup-token output")
	}

	cred := credential.Credential{
		Provider:  credential.ProviderAnthropic,
		Token:     token,
		CreatedAt: time.Now(),
	}
	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}
	fmt.Printf("\nAnthropic credential saved to %s\n", credPath)
	fmt.Println("\nYou can now run 'moat claude' to start Claude Code.")
	return nil
}

// extractOAuthToken extracts the OAuth token from claude setup-token output.
// The output contains ASCII art, messages, and ANSI color codes. The token
// is on its own line and starts with "sk-ant-oat01-".
func extractOAuthToken(output string) string {
	// Strip ANSI escape codes
	output = stripANSI(output)

	// Look for a line containing the OAuth token pattern
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "sk-ant-oat01-") {
			return line
		}
	}
	return ""
}

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	var result strings.Builder
	inEscape := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			// ANSI sequences end with a letter
			if (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z') {
				inEscape = false
			}
			continue
		}
		result.WriteByte(s[i])
	}
	return result.String()
}

// grantAnthropicViaAPIKey prompts for an API key.
func grantAnthropicViaAPIKey(reader *bufio.Reader) error {
	auth := &credential.AnthropicAuth{}

	// Get API key from environment variable or interactive prompt
	var apiKey string
	if envKey := os.Getenv("ANTHROPIC_API_KEY"); envKey != "" {
		apiKey = envKey
		fmt.Println("Using API key from ANTHROPIC_API_KEY environment variable")
	} else {
		var err error
		apiKey, err = auth.PromptForAPIKey()
		if err != nil {
			return fmt.Errorf("reading API key: %w", err)
		}
	}

	// Ask user if they want to validate the key
	fmt.Print("\nValidate API key with a test request? This makes a small API call. [Y/n]: ")
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response == "" || response == "y" || response == "yes" {
		fmt.Println("\nValidating API key...")
		fmt.Println("  POST https://api.anthropic.com/v1/messages")
		fmt.Println(`  {"model":"claude-sonnet-4-20250514","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := auth.ValidateKey(ctx, apiKey); err != nil {
			return fmt.Errorf("validating API key: %w", err)
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

// grantAnthropicViaExistingCreds imports existing Claude Code credentials.
func grantAnthropicViaExistingCreds(claudeCode *credential.ClaudeCodeCredentials) error {
	token, _ := claudeCode.GetClaudeCodeCredentials()

	fmt.Println()
	fmt.Println("Found Claude Code credentials.")
	if token.SubscriptionType != "" {
		fmt.Printf("  Subscription: %s\n", token.SubscriptionType)
	}
	if !token.ExpiresAtTime().IsZero() {
		if token.IsExpired() {
			fmt.Printf("  Status: Expired (was valid until %s)\n", token.ExpiresAtTime().Format(time.RFC3339))
			fmt.Println("\nWarning: Token has expired. You may need to re-authenticate in Claude Code.")
			fmt.Println("Run 'claude' to refresh your credentials, then try again.")
			return fmt.Errorf("Claude Code token has expired")
		}
		fmt.Printf("  Expires: %s\n", token.ExpiresAtTime().Format(time.RFC3339))
	}

	cred := claudeCode.CreateCredentialFromOAuth(token)
	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}
	fmt.Printf("\nClaude Code credentials imported to %s\n", credPath)
	fmt.Println("\nYou can now run 'moat claude' to start Claude Code.")
	if !token.ExpiresAtTime().IsZero() {
		remaining := time.Until(token.ExpiresAtTime())
		if remaining > 24*time.Hour {
			fmt.Printf("Token expires in %d days. Re-run 'moat grant anthropic' if it expires.\n", int(remaining.Hours()/24))
		} else {
			fmt.Printf("Token expires in %.0f hours. Re-run 'moat grant anthropic' if it expires.\n", remaining.Hours())
		}
	}
	return nil
}

func grantAWS(roleARN, region, sessionDuration, externalID string) error {
	// Parse and validate role ARN
	awsCfg, err := credential.ParseRoleARN(roleARN)
	if err != nil {
		return err
	}

	// Override region if specified
	if region != "" {
		awsCfg.Region = region
	}

	// Validate session duration if specified
	if sessionDuration != "" {
		awsCfg.SessionDurationStr = sessionDuration
		if _, sdErr := awsCfg.SessionDuration(); sdErr != nil {
			return sdErr
		}
	}

	awsCfg.ExternalID = externalID

	// Load host AWS credentials
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(awsCfg.Region))
	if err != nil {
		return fmt.Errorf(`no AWS credentials found

Set credentials via:
  - AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY environment variables
  - aws configure
  - aws sso login

Error: %w`, err)
	}

	// Test AssumeRole to verify the role is assumable
	fmt.Println("Testing role assumption...")
	stsClient := sts.NewFromConfig(cfg)

	input := &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleARN),
		RoleSessionName: aws.String(fmt.Sprintf("agentops-grant-test-%d", time.Now().Unix())),
		DurationSeconds: aws.Int32(900), // 15 min for test
	}
	if externalID != "" {
		input.ExternalId = aws.String(externalID)
	}

	result, err := stsClient.AssumeRole(ctx, input)
	if err != nil {
		return fmt.Errorf(`cannot assume role: %w

Check that:
  - The role's trust policy allows your IAM principal
  - You have sts:AssumeRole permission
  - The role ARN is correct`, err)
	}

	fmt.Printf("  Successfully assumed role (test session expires: %s)\n", result.Credentials.Expiration.Format("15:04:05"))

	// Store the config (not credentials)
	// We pack extra fields into Scopes for compatibility with existing storage
	cred := credential.Credential{
		Provider:  credential.ProviderAWS,
		Token:     awsCfg.RoleARN, // Store role ARN in Token field
		Scopes:    []string{awsCfg.Region, awsCfg.SessionDurationStr, awsCfg.ExternalID},
		CreatedAt: time.Now(),
	}

	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}

	fmt.Printf("\nAWS grant saved to %s\n", credPath)
	fmt.Printf("\nRole:             %s\n", roleARN)
	fmt.Printf("Region:           %s\n", awsCfg.Region)
	if sessionDuration != "" {
		fmt.Printf("Session duration: %s\n", sessionDuration)
	} else {
		fmt.Printf("Session duration: 15m (default)\n")
	}
	fmt.Printf("\nUse with: moat run --grant aws <agent>\n")

	return nil
}
