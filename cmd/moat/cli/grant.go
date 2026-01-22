// cmd/moat/cli/grant.go
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/andybons/moat/internal/credential"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// AWS grant flags
var (
	awsRole            string
	awsRegion          string
	awsSessionDuration string
	awsExternalID      string
)

// getGitHubTokenFromEnv returns a GitHub token from environment variables.
// Checks GITHUB_TOKEN first, then GH_TOKEN (used by gh CLI).
func getGitHubTokenFromEnv() string {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	return os.Getenv("GH_TOKEN")
}

var grantCmd = &cobra.Command{
	Use:   "grant <provider>",
	Short: "Grant a credential for use in runs",
	Long: `Grant a credential that can be used by agent runs.

Credentials are stored securely and injected into agent containers when
requested via the --grant flag on 'moat run'.

Supported providers:
  github      GitHub token (from gh CLI, environment, or interactive prompt)
  anthropic   Anthropic API key or Claude Code OAuth credentials
  openai      OpenAI API key or ChatGPT subscription credentials
  aws         AWS IAM role assumption (uses host credentials to assume role)

GitHub authentication (in order of precedence):
  1. GITHUB_TOKEN or GH_TOKEN environment variable
  2. gh CLI token (if gh is installed and authenticated)
  3. Interactive prompt for Personal Access Token

Examples:
  # Grant GitHub access (auto-detects gh CLI or prompts for token)
  moat grant github

  # Grant GitHub access from environment variable
  export GITHUB_TOKEN="ghp_..."
  moat grant github

  # Use the credential in a run
  moat run my-agent . --grant github

  # Grant Anthropic access (will auto-detect Claude Code credentials)
  moat grant anthropic

  # Grant Anthropic API access from environment variable
  export ANTHROPIC_API_KEY="sk-ant-..."
  moat grant anthropic

  # Grant AWS access via IAM role
  moat grant aws --role=arn:aws:iam::123456789012:role/AgentRole

  # Grant AWS with custom session duration and region
  moat grant aws --role=arn:aws:iam::123456789012:role/AgentRole \
    --region=us-west-2 --session-duration=1h

  # Use AWS credential in a run (credentials auto-refresh)
  moat run my-agent . --grant aws

  # Grant OpenAI access (will auto-detect Codex credentials)
  moat grant openai

  # Grant OpenAI API access from environment variable
  export OPENAI_API_KEY="sk-..."  # set in your shell profile
  moat grant openai

  # Use OpenAI credential for Codex
  moat run my-agent . --grant openai

If you have Claude Code installed and logged in, 'moat grant anthropic' will
offer to import your existing OAuth credentials. Similarly, 'moat grant openai'
will offer to import Codex credentials if available.`,
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
	providerStr := args[0]
	provider := credential.Provider(providerStr)

	switch provider {
	case credential.ProviderGitHub:
		return grantGitHub()
	case credential.ProviderAnthropic:
		return grantAnthropic()
	case credential.ProviderOpenAI:
		return grantOpenAI()
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

func grantGitHub() error {
	reader := bufio.NewReader(os.Stdin)

	// Priority 1: Environment variable
	if token := getGitHubTokenFromEnv(); token != "" {
		fmt.Println("Using token from environment variable")
		return saveGitHubToken(token)
	}

	// Priority 2: gh CLI
	token, ghErr := getGHCLIToken()
	if ghErr == nil && token != "" {
		fmt.Println("Found gh CLI authentication")
		fmt.Print("Use token from gh CLI? [Y/n]: ")
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response == "" || response == "y" || response == "yes" {
			return saveGitHubToken(token)
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
  2. Click "Generate new token" → "Fine-grained token" (recommended)
  3. Set expiration and select repositories
  4. Under "Repository permissions", grant "Contents" read/write access
  5. Copy the generated token`)
	fmt.Print("Token: ")
	tokenBytes, err := readPassword()
	if err != nil {
		return fmt.Errorf("reading token: %w", err)
	}
	inputToken := strings.TrimSpace(string(tokenBytes))
	fmt.Println() // newline after hidden input

	if inputToken == "" {
		return fmt.Errorf("no token provided")
	}

	return saveGitHubToken(inputToken)
}

// getGHCLIToken retrieves the GitHub token from gh CLI if available.
func getGHCLIToken() (string, error) {
	cmd := exec.Command("gh", "auth", "token")
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

// saveGitHubToken validates and saves a GitHub token.
func saveGitHubToken(token string) error {
	// Validate token with a simple API call
	fmt.Println("Validating token...")

	client := &http.Client{Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
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

	switch resp.StatusCode {
	case 200:
		// Success - continue
	case 401:
		return fmt.Errorf("invalid token (401 Unauthorized)")
	case 403:
		return fmt.Errorf("token validation failed (403 Forbidden) - token may lack permissions or you may be rate limited")
	default:
		return fmt.Errorf("unexpected status validating token: %d", resp.StatusCode)
	}

	// Parse response to show username
	var user struct {
		Login string `json:"login"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&user); decodeErr == nil && user.Login != "" {
		fmt.Printf("Authenticated as: %s\n", user.Login)
	}

	cred := credential.Credential{
		Provider:  credential.ProviderGitHub,
		Token:     token,
		CreatedAt: time.Now(),
	}
	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}
	fmt.Printf("GitHub credential saved to %s\n", credPath)
	return nil
}

// readPassword reads a password from stdin without echoing.
func readPassword() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		return term.ReadPassword(fd)
	}
	// Not a terminal, read normally (for piped input)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	return []byte(strings.TrimSuffix(line, "\n")), err
}

func grantAnthropic() error {
	reader := bufio.NewReader(os.Stdin)

	// Check if claude is available for setup-token
	claudeAvailable := isClaudeAvailable()

	// Check for existing Claude Code credentials as option 3
	claudeCode := &credential.ClaudeCodeCredentials{}
	hasExistingCreds := claudeCode.HasClaudeCodeCredentials()

	// If only API key is available, skip the menu
	if !claudeAvailable && !hasExistingCreds {
		return grantAnthropicViaAPIKey()
	}

	for {
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
		} else {
			// hasExistingCreds must be true (we handled !claudeAvailable && !hasExistingCreds above)
			validChoices = "2 or 3"
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
				continue
			}
			return grantAnthropicViaSetupToken()

		case "2":
			return grantAnthropicViaAPIKey()

		case "3":
			if !hasExistingCreds {
				fmt.Println("No existing Claude Code credentials found. Please choose another option.")
				continue
			}
			return grantAnthropicViaExistingCreds(claudeCode)

		default:
			fmt.Printf("Invalid choice: %s\n", response)
			continue
		}
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

// oauthTokenRegex validates OAuth token format.
// Format: sk-ant-oat01-<base64-encoded-data> where data is alphanumeric plus _ and -.
// The token typically has 3-4 parts separated by hyphens after the prefix.
var oauthTokenRegex = regexp.MustCompile(`^sk-ant-oat01-[A-Za-z0-9_-]{20,}$`)

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
			// Validate format to avoid partial or malformed tokens
			if oauthTokenRegex.MatchString(line) {
				return line
			}
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
func grantAnthropicViaAPIKey() error {
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

	// Validate the key
	fmt.Println("\nValidating API key...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := auth.ValidateKey(ctx, apiKey); err != nil {
		return fmt.Errorf("validating API key: %w", err)
	}
	fmt.Println("API key is valid.")

	cred := auth.CreateCredential(apiKey)
	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}
	fmt.Printf("\nAnthropic API key saved to %s\n", credPath)
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

func grantOpenAI() error {
	reader := bufio.NewReader(os.Stdin)

	// Check if codex CLI is available for device flow login
	codexAvailable := isCodexAvailable()

	// Check for existing Codex credentials as option 3
	codexCreds := &credential.CodexCredentials{}
	hasExistingCreds := codexCreds.HasCodexCredentials()

	// If only API key is available, skip the menu
	if !codexAvailable && !hasExistingCreds {
		return grantOpenAIViaAPIKey()
	}

	for {
		// Offer choices to user
		fmt.Println("Choose authentication method:")
		fmt.Println()
		if codexAvailable {
			fmt.Println("  1. ChatGPT subscription (recommended)")
			fmt.Println("     Uses 'codex login' for OAuth authentication.")
			fmt.Println("     Requires a ChatGPT Pro/Teams subscription.")
			fmt.Println()
		}
		fmt.Println("  2. OpenAI API key")
		fmt.Println("     Use an API key from platform.openai.com")
		fmt.Println("     Billed per token to your API account.")
		fmt.Println()

		if hasExistingCreds {
			fmt.Println("  3. Import existing Codex credentials")
			fmt.Println("     Use OAuth tokens from your local Codex CLI installation.")
			fmt.Println()
		}

		var validChoices string
		if codexAvailable && hasExistingCreds {
			validChoices = "1, 2, or 3"
		} else if codexAvailable {
			validChoices = "1 or 2"
		} else {
			// hasExistingCreds must be true (we handled !codexAvailable && !hasExistingCreds above)
			validChoices = "2 or 3"
		}

		fmt.Printf("Enter choice [%s]: ", validChoices)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(response)

		// Default to option 1 if codex available, otherwise 2
		if response == "" {
			if codexAvailable {
				response = "1"
			} else {
				response = "2"
			}
		}

		switch response {
		case "1":
			if !codexAvailable {
				fmt.Println("Codex CLI is not installed. Please choose another option.")
				continue
			}
			return grantOpenAIViaCodexLogin()

		case "2":
			return grantOpenAIViaAPIKey()

		case "3":
			if !hasExistingCreds {
				fmt.Println("No existing Codex credentials found. Please choose another option.")
				continue
			}
			return grantOpenAIViaExistingCreds(codexCreds)

		default:
			fmt.Printf("Invalid choice: %s\n", response)
			continue
		}
	}
}

// isCodexAvailable checks if the codex CLI is installed.
func isCodexAvailable() bool {
	cmd := exec.Command("codex", "--version")
	return cmd.Run() == nil
}

// grantOpenAIViaCodexLogin uses `codex login` for OAuth authentication.
func grantOpenAIViaCodexLogin() error {
	fmt.Println()
	fmt.Println("Running 'codex login' for authentication...")
	fmt.Println("This may open a browser for authentication.")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("codex login failed: %w", err)
	}

	// After successful login, import the credentials
	codexCreds := &credential.CodexCredentials{}
	token, err := codexCreds.GetCodexCredentials()
	if err != nil {
		return fmt.Errorf("failed to retrieve credentials after login: %w", err)
	}

	cred := codexCreds.CreateCredentialFromCodex(token)
	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}
	fmt.Printf("\nOpenAI credential saved to %s\n", credPath)
	fmt.Println("\nYou can now run 'moat codex' to start Codex.")
	return nil
}

// grantOpenAIViaAPIKey prompts for an API key.
func grantOpenAIViaAPIKey() error {
	auth := &credential.OpenAIAuth{}

	// Get API key from environment variable or interactive prompt
	var apiKey string
	if envKey := os.Getenv("OPENAI_API_KEY"); envKey != "" {
		apiKey = envKey
		fmt.Println("Using API key from OPENAI_API_KEY environment variable")
	} else {
		var err error
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
	fmt.Println("API key is valid.")

	cred := auth.CreateCredential(apiKey)
	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}
	fmt.Printf("\nOpenAI API key saved to %s\n", credPath)
	return nil
}

// grantOpenAIViaExistingCreds imports existing Codex credentials.
func grantOpenAIViaExistingCreds(codexCreds *credential.CodexCredentials) error {
	token, _ := codexCreds.GetCodexCredentials()

	fmt.Println()
	fmt.Println("Found Codex credentials.")
	if !token.ExpiresAtTime().IsZero() {
		if token.IsExpired() {
			fmt.Printf("  Status: Expired (was valid until %s)\n", token.ExpiresAtTime().Format(time.RFC3339))
			fmt.Println("\nWarning: Token has expired. You may need to re-authenticate in Codex.")
			fmt.Println("Run 'codex login' to refresh your credentials, then try again.")
			return fmt.Errorf("Codex token has expired")
		}
		fmt.Printf("  Expires: %s\n", token.ExpiresAtTime().Format(time.RFC3339))
	}

	cred := codexCreds.CreateCredentialFromCodex(token)
	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}
	fmt.Printf("\nCodex credentials imported to %s\n", credPath)
	fmt.Println("\nYou can now run 'moat codex' to start Codex.")
	if !token.ExpiresAtTime().IsZero() {
		remaining := time.Until(token.ExpiresAtTime())
		if remaining > 24*time.Hour {
			fmt.Printf("Token expires in %d days. Re-run 'moat grant openai' if it expires.\n", int(remaining.Hours()/24))
		} else {
			fmt.Printf("Token expires in %.0f hours. Re-run 'moat grant openai' if it expires.\n", remaining.Hours())
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
