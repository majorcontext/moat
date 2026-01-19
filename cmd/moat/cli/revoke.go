// cmd/agent/cli/revoke.go
package cli

import (
	"fmt"

	"github.com/andybons/moat/internal/credential"
	"github.com/andybons/moat/internal/log"
	"github.com/spf13/cobra"
)

var revokeCmd = &cobra.Command{
	Use:   "revoke <provider>",
	Short: "Revoke a stored credential",
	Long: `Remove a stored credential from the local credential store.

Examples:
  agent revoke github`,
	Args: cobra.ExactArgs(1),
	RunE: runRevoke,
}

func init() {
	rootCmd.AddCommand(revokeCmd)
}

func runRevoke(cmd *cobra.Command, args []string) error {
	provider := credential.Provider(args[0])

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
	fmt.Printf("%s credential revoked\n", provider)
	return nil
}
