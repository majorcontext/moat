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

// getGitHubClientID returns the GitHub OAuth client ID from environment or default.
func getGitHubClientID() string {
	if id := os.Getenv("AGENTOPS_GITHUB_CLIENT_ID"); id != "" {
		return id
	}
	// Placeholder for development - users must set AGENTOPS_GITHUB_CLIENT_ID
	return "Ov23liYourClientID"
}

var grantCmd = &cobra.Command{
	Use:   "grant <provider>",
	Short: "Grant a credential for use in runs",
	Long: `Grant a credential that can be used by agent runs.

Examples:
  agent grant github                    # GitHub with default scopes
  agent grant github:repo               # GitHub with repo scope only
  agent grant github:repo,read:user     # GitHub with multiple scopes`,
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
	default:
		return fmt.Errorf("unsupported provider: %s", providerStr)
	}
}

func grantGitHub(scopes []string) error {
	if len(scopes) == 0 {
		scopes = []string{"repo"}
	}

	auth := &credential.GitHubDeviceAuth{
		ClientID: getGitHubClientID(),
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
