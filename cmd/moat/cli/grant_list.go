package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/spf13/cobra"
)

var grantListCmd = &cobra.Command{
	Use:   "list",
	Short: "List granted credentials",
	Long: `List all credentials stored in the credential store.

Shows the provider, type, and when each credential was granted.

Examples:
  moat grant list          # List all credentials
  moat grant list --json   # Output as JSON`,
	RunE: runGrantList,
}

func init() {
	grantCmd.AddCommand(grantListCmd)
}

func runGrantList(cmd *cobra.Command, args []string) error {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return fmt.Errorf("getting encryption key: %w", err)
	}

	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return fmt.Errorf("opening credential store: %w", err)
	}

	creds, err := store.List()
	if err != nil {
		return fmt.Errorf("listing credentials: %w", err)
	}

	if len(creds) == 0 {
		// Check if there are .enc files that couldn't be decrypted
		if hasUnreadableCredentials(credential.DefaultStoreDir()) {
			ui.Warn("Found encrypted credential files that cannot be decrypted.")
			ui.Warn("This usually means the encryption key has changed.")
			ui.Warn("")
			ui.Warn("To fix:")
			ui.Warn("  1. Re-grant your credentials: moat grant <provider>")
			ui.Warn("  2. Or restore your encryption key from backup")
			ui.Warn("")
			ui.Warn("For details, run with --verbose to see which providers failed")
			return nil
		}

		fmt.Println("No credentials found.")
		fmt.Println("\nGrant a credential with: moat grant <provider>")
		return nil
	}

	if jsonOut {
		// Redact tokens for JSON output
		type jsonCred struct {
			Provider  string `json:"provider"`
			Type      string `json:"type"`
			GrantedAt string `json:"granted_at"`
		}
		out := make([]jsonCred, 0, len(creds))
		for _, c := range creds {
			out = append(out, jsonCred{
				Provider:  string(c.Provider),
				Type:      credType(c),
				GrantedAt: c.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			})
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tTYPE\tGRANTED")
	for _, c := range creds {
		fmt.Fprintf(w, "%s\t%s\t%s\n",
			c.Provider,
			credType(c),
			formatAge(c.CreatedAt),
		)
	}
	w.Flush()

	return nil
}

func credType(c credential.Credential) string {
	switch c.Provider {
	case credential.ProviderAWS:
		return "role"
	case credential.ProviderGitHub:
		return "token"
	case credential.ProviderAnthropic:
		if c.Metadata != nil && c.Metadata["auth_type"] == "oauth" {
			return "oauth"
		}
		return "token"
	case credential.ProviderOpenAI:
		if c.Metadata != nil && c.Metadata["auth_type"] == "oauth" {
			return "oauth"
		}
		return "token"
	default:
		return "token"
	}
}

// hasUnreadableCredentials checks if there are .enc files in the credential directory.
// Used to detect when credentials exist but can't be decrypted (e.g., key changed).
func hasUnreadableCredentials(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".enc" {
			return true
		}
	}
	return false
}
