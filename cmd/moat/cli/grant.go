// cmd/moat/cli/grant.go
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/providers/aws"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// AWS grant flags - these need to be passed to the AWS provider
var (
	awsRole            string
	awsRegion          string
	awsSessionDuration string
	awsExternalID      string
)

var grantCmd = &cobra.Command{
	Use:   "grant <provider>",
	Short: "Grant a credential for use in runs",
	Long: `Grant a credential that can be used by agent runs.

Credentials are stored securely and injected into agent containers when
requested via the --grant flag on 'moat run'.

Supported providers:
  github      GitHub token (from gh CLI, environment, or interactive prompt)
  anthropic   Anthropic API key or Claude Code OAuth credentials
  openai      OpenAI API key
  aws         AWS IAM role assumption (uses host credentials to assume role)

Subcommands:
  ssh         Grant SSH access for a specific host
  mcp         Grant credentials for an MCP server

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

  # Grant OpenAI API access
  moat grant openai

  # Grant OpenAI API access from environment variable
  export OPENAI_API_KEY="sk-..."  # set in your shell profile
  moat grant openai

  # Use OpenAI credential for Codex
  moat run my-agent . --grant openai

If you have Claude Code installed and logged in, 'moat grant anthropic' will
offer to import your existing OAuth credentials.`,
	Args: cobra.MinimumNArgs(1),
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
	providerName := args[0]

	// Map CLI names to provider names
	// "anthropic" is the CLI name, but the provider is registered as "claude"
	// "openai" is the CLI name, but the provider is registered as "codex"
	switch providerName {
	case "anthropic":
		providerName = "claude"
	case "openai":
		providerName = "codex"
	}

	// Look up provider in registry
	prov := provider.Get(providerName)
	if prov == nil {
		return fmt.Errorf("unknown provider: %s\n\nAvailable providers: %s",
			args[0], strings.Join(provider.Names(), ", "))
	}

	// For AWS, validate required flags before calling Grant
	if providerName == "aws" && awsRole == "" {
		return fmt.Errorf(`--role is required for AWS grant

Usage: moat grant aws --role=arn:aws:iam::ACCOUNT:role/ROLE_NAME

Options:
  --role             IAM role ARN to assume (required)
  --region           AWS region (default: us-east-1)
  --session-duration Session duration (default: 15m, max: 12h)
  --external-id      External ID for role assumption`)
	}

	// Call the provider's Grant method
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// For AWS, pass the CLI flags via context
	if providerName == "aws" {
		ctx = aws.WithGrantOptions(ctx, awsRole, awsRegion, awsSessionDuration, awsExternalID)
	}

	provCred, err := prov.Grant(ctx)
	if err != nil {
		return err
	}

	// Convert to credential.Credential for storage
	cred := credential.Credential{
		Provider:  credential.Provider(provCred.Provider),
		Token:     provCred.Token,
		Scopes:    provCred.Scopes,
		ExpiresAt: provCred.ExpiresAt,
		CreatedAt: provCred.CreatedAt,
		Metadata:  provCred.Metadata,
	}

	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}

	fmt.Printf("Credential saved to %s\n", credPath)
	return nil
}

// readPassword reads a password from stdin without echoing.
// This is used by grant subcommands that need to prompt for secrets.
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
