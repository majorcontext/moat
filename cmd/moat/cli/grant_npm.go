package cli

import (
	"fmt"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/providers/npm"
	"github.com/spf13/cobra"
)

var grantNpmCmd = &cobra.Command{
	Use:   "npm",
	Short: "Grant npm registry credentials",
	Long: `Grant npm registry credentials for use in runs.

Auto-discovers registries from ~/.npmrc and NPM_TOKEN environment variable.
Use --host to add a specific registry manually.

Multiple registries are supported — each 'moat grant npm --host=<host>' adds
to the existing credential, and scope-to-registry routing is preserved.

Examples:
  # Auto-discover registries from .npmrc
  moat grant npm

  # Add a specific registry
  moat grant npm --host=npm.company.com

  # Use in runs
  moat run --grant npm -- npm install`,
	RunE: runGrantNpm,
}

var npmHost string

func init() {
	grantCmd.AddCommand(grantNpmCmd)
	grantNpmCmd.Flags().StringVar(&npmHost, "host", "", "Specific registry host (e.g., npm.company.com)")
}

func runGrantNpm(cmd *cobra.Command, args []string) error {
	prov := provider.Get("npm")
	if prov == nil {
		return fmt.Errorf("npm provider not registered")
	}

	ctx := cmd.Context()
	ctx = npm.WithGrantOptions(ctx, npmHost)

	provCred, err := prov.Grant(ctx)
	if err != nil {
		return err
	}

	cred := credential.Credential{
		Provider:  credential.ProviderNpm,
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

	if npmHost != "" {
		fmt.Printf("Added registry %s to npm credential.\n", npmHost)
	}
	fmt.Printf("Credential saved to %s\n", credPath)
	return nil
}
