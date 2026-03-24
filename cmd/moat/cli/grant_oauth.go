// cmd/moat/cli/grant_oauth.go
package cli

import (
	"fmt"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/providers/oauth"
	"github.com/spf13/cobra"
)

var (
	oauthURL          string
	oauthAuthURL      string
	oauthTokenURL     string
	oauthClientID     string
	oauthClientSecret string
	oauthScopes       string
)

var grantOAuthCmd = &cobra.Command{
	Use:   "oauth <name>",
	Short: "Grant OAuth credentials for a service",
	Long: `Grant OAuth credentials via browser-based authorization.

Opens a browser for OAuth authorization and stores the token securely.
Supports automatic discovery for MCP servers that implement OAuth metadata.

Well-known services (asana, cloudflare, hubspot, linear, notion, stripe)
are auto-discovered without needing --url or a config file.

Examples:
  # Auto-discover a well-known service
  moat grant oauth notion

  # Auto-discover from a custom MCP server URL
  moat grant oauth myservice --url https://mcp.example.com

  # Use config from ~/.moat/oauth/linear.yaml
  moat grant oauth linear

  # Explicit OAuth endpoints
  moat grant oauth custom \
    --auth-url https://auth.example.com/authorize \
    --token-url https://auth.example.com/token \
    --client-id abc123`,
	Args: cobra.ExactArgs(1),
	RunE: runGrantOAuth,
}

func init() {
	grantCmd.AddCommand(grantOAuthCmd)
	grantOAuthCmd.Flags().StringVar(&oauthURL, "url", "", "MCP server URL for OAuth discovery")
	grantOAuthCmd.Flags().StringVar(&oauthAuthURL, "auth-url", "", "OAuth authorization endpoint")
	grantOAuthCmd.Flags().StringVar(&oauthTokenURL, "token-url", "", "OAuth token endpoint")
	grantOAuthCmd.Flags().StringVar(&oauthClientID, "client-id", "", "OAuth client ID")
	grantOAuthCmd.Flags().StringVar(&oauthClientSecret, "client-secret", "", "OAuth client secret")
	grantOAuthCmd.Flags().StringVar(&oauthScopes, "scopes", "", "OAuth scopes")
}

func runGrantOAuth(cmd *cobra.Command, args []string) error {
	name := args[0]
	if strings.ContainsAny(name, "/\\:*?\"<>|") {
		return fmt.Errorf("invalid name: %q contains invalid characters", name)
	}

	ctx := cmd.Context()

	var cfg *oauth.Config
	var resource string

	// Resolution order: CLI flags -> config file -> MCP discovery

	// 1. CLI flags
	if oauthAuthURL != "" || oauthTokenURL != "" || oauthClientID != "" {
		cfg = &oauth.Config{
			AuthURL:      oauthAuthURL,
			TokenURL:     oauthTokenURL,
			ClientID:     oauthClientID,
			ClientSecret: oauthClientSecret,
			Scopes:       oauthScopes,
		}
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("invalid OAuth flags: %w", err)
		}
	}

	// 2. Config file
	if cfg == nil {
		fileCfg, err := oauth.LoadConfig(oauth.DefaultConfigDir(), name)
		if err == nil {
			cfg = fileCfg
			log.Debug("loaded OAuth config from file", "name", name)
		}
	}

	// 3. MCP discovery (--url flag → moat.yaml → built-in registry)
	if cfg == nil {
		mcpURL := oauthURL
		if mcpURL == "" {
			mcpURL = findMCPServerURL(name)
		}
		if mcpURL == "" {
			mcpURL = oauth.LookupServerURL(name)
		}
		if mcpURL != "" {
			fmt.Printf("Attempting OAuth discovery for %s...\n", mcpURL)
			discovered, res, err := oauth.DiscoverFromMCPServer(ctx, mcpURL)
			if err == nil {
				cfg = discovered
				resource = res
				fmt.Println("Discovered OAuth endpoints.")
				// Cache discovered config (with DCR client_id)
				if cfg.ClientID != "" {
					if saveErr := oauth.SaveConfig(oauth.DefaultConfigDir(), name, cfg); saveErr != nil {
						log.Debug("failed to cache discovered config", "error", saveErr)
					}
				}
			} else {
				fmt.Printf("Discovery failed: %v\n", err)
			}
		}
	}

	if cfg == nil {
		return fmt.Errorf("no OAuth configuration found for %q\n\n"+
			"Provide one of:\n"+
			"  1. CLI flags: --auth-url, --token-url, --client-id\n"+
			"  2. Config file: ~/.moat/oauth/%s.yaml\n"+
			"  3. MCP server URL: --url <mcp-server-url>\n\n"+
			"See: https://majorcontext.com/moat/guides/mcp", name, name)
	}

	// Run the OAuth flow
	provCred, err := oauth.RunGrant(ctx, name, cfg, resource)
	if err != nil {
		return err
	}

	// Store credential
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

	fmt.Printf("\nOAuth credential 'oauth:%s' saved to %s\n", name, credPath)
	if !provCred.ExpiresAt.IsZero() {
		fmt.Printf("Expires: %s (auto-refresh enabled)\n", provCred.ExpiresAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Printf("\nUse in moat.yaml:\n\n")
	fmt.Printf("grants:\n  - oauth:%s\n\nmcp:\n  - name: %s\n    url: <server-url>\n    auth:\n      grant: oauth:%s\n\n", name, name, name)

	return nil
}

// findMCPServerURL looks for an MCP server URL in moat.yaml matching the name.
func findMCPServerURL(name string) string {
	cfg, err := config.Load(".")
	if err != nil {
		return ""
	}
	if cfg == nil {
		return ""
	}
	for _, mcp := range cfg.MCP {
		if mcp.Name == name {
			return mcp.URL
		}
	}
	return ""
}
