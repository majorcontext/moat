// cmd/moat/cli/grant_google_oauth.go
package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/spf13/cobra"
)

var grantGoogleOAuthCmd = &cobra.Command{
	Use:   "google-oauth",
	Short: "Grant Google OAuth client credentials for the OAuth relay",
	Long: `Grant Google OAuth client credentials used by the OAuth relay.

The OAuth relay allows multiple agent runs to share a single registered Google
OAuth redirect URI. Instead of each app registering its own callback URL, Moat
provides a single relay endpoint that routes authorization codes to the correct
application.

This command stores the Google OAuth client ID and client secret that Moat
injects into containers as GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET.

Register your redirect URI in the Google Cloud Console:
  http://oauthrelay.localhost:8080/callback  (TODO: use configured proxy port)

Examples:
  # Grant Google OAuth credentials
  moat grant google-oauth

  # Enable in agent.yaml
  cat > agent.yaml <<YAML
  oauth_relay: true
  ports:
    web: 3000
  YAML

  # Moat injects MOAT_OAUTH_RELAY_URL, GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET
  moat claude ./my-app`,
	Args: cobra.NoArgs,
	RunE: runGrantGoogleOAuth,
}

func init() {
	grantCmd.AddCommand(grantGoogleOAuthCmd)
}

func runGrantGoogleOAuth(cmd *cobra.Command, args []string) error {
	fmt.Println("Enter your Google OAuth client credentials.")
	fmt.Println("These are used by the OAuth relay to redirect authorization codes")
	fmt.Println("to the correct application container.")
	fmt.Println()

	// Read client ID
	fmt.Print("Client ID: ")
	var clientID string
	if _, err := fmt.Scanln(&clientID); err != nil {
		return fmt.Errorf("reading client ID: %w", err)
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return fmt.Errorf("client ID cannot be empty")
	}

	// Read client secret
	fmt.Print("Client Secret: ")
	secretBytes, err := readPassword()
	if err != nil {
		return fmt.Errorf("reading client secret: %w", err)
	}
	fmt.Println() // newline after hidden input
	clientSecret := strings.TrimSpace(string(secretBytes))
	if clientSecret == "" {
		return fmt.Errorf("client secret cannot be empty")
	}

	// Store as a single credential with client ID in metadata
	cred := credential.Credential{
		Provider:  credential.ProviderGoogleOAuth,
		Token:     clientSecret,
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			"client_id": clientID,
		},
	}

	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}

	fmt.Printf("\nGoogle OAuth credentials saved to %s\n", credPath)
	fmt.Println()
	proxyPort := 8080
	if globalCfg, loadErr := config.LoadGlobal(); loadErr == nil {
		proxyPort = globalCfg.Proxy.Port
	}
	fmt.Println("Register this redirect URI in the Google Cloud Console:")
	fmt.Printf("  http://oauthrelay.localhost:%d/callback\n", proxyPort)
	fmt.Println()
	fmt.Println("Enable in agent.yaml:")
	fmt.Println()
	fmt.Println("  oauth_relay: true")
	fmt.Println("  ports:")
	fmt.Println("    web: 3000")
	fmt.Println()
	fmt.Println("Then run: moat claude ./my-app")

	return nil
}
