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

Use --profile (or MOAT_PROFILE env var) to store credentials in a named profile.
Profile-scoped credentials are isolated from the default store.

Run 'moat grant providers' to list all available providers.

Subcommands:
  providers   List all available credential providers
  ssh         Grant SSH access for a specific host
  mcp         Grant credentials for an MCP server

Examples:
  moat grant claude                              # Grant Claude OAuth token (for moat claude)
  moat grant anthropic                           # Grant Anthropic API key (for any agent)
  moat grant github                              # Grant GitHub access
  moat grant aws --role=arn:aws:...              # Grant AWS access via IAM role
  moat grant github --profile myproject          # Grant GitHub access in a profile
  moat grant providers                           # List all available providers
  moat run my-agent . --grant github             # Use credential in a run
  moat run --grant github --profile myproject    # Use profile-scoped credential`,
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
	storeDir := credential.DefaultStoreDir()
	store, err := credential.NewFileStore(storeDir, key)
	if err != nil {
		return "", fmt.Errorf("opening credential store: %w", err)
	}
	if err := store.Save(cred); err != nil {
		return "", fmt.Errorf("saving credential: %w", err)
	}
	return filepath.Join(storeDir, string(cred.Provider)+".enc"), nil
}

func runGrant(cmd *cobra.Command, args []string) error {
	providerName := args[0]

	// Map CLI names to provider names
	// "openai" is the CLI name, but the provider is registered as "codex"
	// "google" is an alias for "gemini"
	// "anthropic" and "claude" are separate registered providers; no remapping needed
	switch providerName {
	case "openai":
		providerName = "codex"
	case "google":
		providerName = "gemini"
	}

	// Look up provider in registry
	prov := provider.Get(providerName)
	if prov == nil {
		return fmt.Errorf("unknown provider: %s\n\nRun 'moat grant providers' to list all available providers",
			args[0])
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

	if credential.ActiveProfile != "" {
		fmt.Printf("Credential saved to %s (profile: %s)\n", credPath, credential.ActiveProfile)
	} else {
		fmt.Printf("Credential saved to %s\n", credPath)
	}
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
