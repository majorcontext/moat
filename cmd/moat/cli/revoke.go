// cmd/agent/cli/revoke.go
package cli

import (
	"fmt"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/spf13/cobra"
)

var revokeCmd = &cobra.Command{
	Use:   "revoke <provider>",
	Short: "Revoke a stored credential",
	Long: `Revoke a previously granted credential.

Supported providers:
  github            GitHub token
  claude            Claude Code OAuth token
  anthropic         Anthropic API key
  openai            OpenAI API key or OAuth credentials
  aws               AWS IAM role configuration
  mcp-<name>        MCP server credential

The credential file is permanently deleted.

Use --profile (or MOAT_PROFILE env var) to revoke credentials from a named profile.

Examples:
  # Revoke Claude OAuth token
  moat revoke claude

  # Revoke Anthropic API key
  moat revoke anthropic

  # Revoke GitHub access
  moat revoke github

  # Revoke MCP server credential
  moat revoke mcp-context7

  # Revoke a credential from a specific profile
  moat revoke github --profile myproject
`,
	Args: cobra.ExactArgs(1),
	RunE: runRevoke,
}

func init() {
	rootCmd.AddCommand(revokeCmd)
}

func runRevoke(cmd *cobra.Command, args []string) error {
	providerName := args[0]

	provider := credential.Provider(providerName)

	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return fmt.Errorf("getting encryption key: %w", err)
	}
	store, err := credential.NewFileStore(
		credential.DefaultStoreDir(),
		key,
	)
	if err != nil {
		return fmt.Errorf("opening credential store: %w", err)
	}

	// Check if credential exists
	if _, err := store.Get(provider); err != nil {
		return fmt.Errorf("no credential found for %s", provider)
	}

	if err := store.Delete(provider); err != nil {
		return fmt.Errorf("deleting credential: %w", err)
	}

	log.Info("credential revoked", "provider", provider)
	if credential.ActiveProfile != "" {
		fmt.Printf("%s credential revoked (profile: %s)\n", provider, credential.ActiveProfile)
	} else {
		fmt.Printf("%s credential revoked\n", provider)
	}
	return nil
}
