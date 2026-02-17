// cmd/moat/cli/grant_mcp.go
package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/mcpoauth"
	"github.com/spf13/cobra"
)

var (
	mcpOAuth    bool
	mcpClientID string
	mcpAuthURL  string
	mcpTokenURL string
	mcpScopes   string
)

var grantMCPCmd = &cobra.Command{
	Use:   "mcp <server-name>",
	Short: "Grant credentials for an MCP server",
	Long: `Grant credentials for a Model Context Protocol (MCP) server.

The credential is stored securely and injected by the proxy when the agent
makes requests to the MCP server. The agent never sees the raw credential.

Two authentication methods are supported:

  Token (default):  Prompts for an API key or token and stores it directly.
  OAuth (--oauth):  Runs a browser-based OAuth authorization code flow to
                    obtain access and refresh tokens.

Examples:
  # Grant with a static API token
  moat grant mcp context7

  # Grant with OAuth (browser-based authorization)
  moat grant mcp notion \
    --oauth \
    --client-id=YOUR_CLIENT_ID \
    --auth-url=https://api.notion.com/v1/oauth/authorize \
    --token-url=https://api.notion.com/v1/oauth/token

  # Configure token auth in agent.yaml
  mcp:
    - name: context7
      url: https://mcp.context7.com/mcp
      auth:
        grant: mcp-context7
        header: CONTEXT7_API_KEY

  # Configure OAuth auth in agent.yaml
  mcp:
    - name: notion
      url: https://api.notion.com/v2/mcp
      auth:
        type: oauth
        grant: mcp-notion
        client_id: YOUR_CLIENT_ID
        auth_url: https://api.notion.com/v1/oauth/authorize
        token_url: https://api.notion.com/v1/oauth/token`,
	Args: cobra.ExactArgs(1),
	RunE: runGrantMCP,
}

func init() {
	grantCmd.AddCommand(grantMCPCmd)
	grantMCPCmd.Flags().BoolVar(&mcpOAuth, "oauth", false, "use OAuth authorization code flow")
	grantMCPCmd.Flags().StringVar(&mcpClientID, "client-id", "", "OAuth client ID (required with --oauth)")
	grantMCPCmd.Flags().StringVar(&mcpAuthURL, "auth-url", "", "OAuth authorization URL (required with --oauth)")
	grantMCPCmd.Flags().StringVar(&mcpTokenURL, "token-url", "", "OAuth token URL (required with --oauth)")
	grantMCPCmd.Flags().StringVar(&mcpScopes, "scopes", "", "OAuth scopes (space-separated)")
}

func runGrantMCP(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Validate server name doesn't contain problematic characters
	if strings.ContainsAny(name, "/\\:*?\"<>|") {
		return fmt.Errorf("invalid server name: %q contains invalid characters", name)
	}

	if mcpOAuth {
		return runGrantMCPOAuth(name)
	}

	return runGrantMCPToken(name)
}

// runGrantMCPToken handles the static token flow (existing behavior).
func runGrantMCPToken(name string) error {
	fmt.Printf("Enter credential for MCP server '%s'\n", name)
	fmt.Printf("This will be stored as grant 'mcp-%s'\n\n", name)
	fmt.Print("Credential: ")

	credBytes, err := readPassword()
	if err != nil {
		return fmt.Errorf("reading credential: %w", err)
	}
	fmt.Println() // newline after hidden input

	credentialStr := strings.TrimSpace(string(credBytes))
	if credentialStr == "" {
		return fmt.Errorf("no credential provided")
	}

	// Validate credential is non-empty (V0 does not validate against server)
	fmt.Println("Validating credential...")
	if len(credentialStr) < 8 {
		fmt.Println("Warning: Credential seems short. MCP server may reject it.")
	}

	// Store credential
	cred := credential.Credential{
		Provider:  credential.Provider(fmt.Sprintf("mcp-%s", name)),
		Token:     credentialStr,
		CreatedAt: time.Now(),
	}

	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}

	fmt.Printf("\nMCP credential 'mcp-%s' saved to %s\n", name, credPath)
	fmt.Printf("\nConfigure in agent.yaml:\n\n")
	fmt.Printf(`mcp:
  - name: %s
    url: https://mcp.example.com/mcp
    auth:
      grant: mcp-%s
      header: YOUR_HEADER_NAME

`, name, name)
	fmt.Println("Then run: moat claude ./workspace")

	return nil
}

// runGrantMCPOAuth handles the OAuth authorization code flow.
func runGrantMCPOAuth(name string) error {
	// Validate required OAuth flags
	if mcpClientID == "" {
		return fmt.Errorf("--client-id is required with --oauth")
	}
	if mcpAuthURL == "" {
		return fmt.Errorf("--auth-url is required with --oauth")
	}
	if mcpTokenURL == "" {
		return fmt.Errorf("--token-url is required with --oauth")
	}

	fmt.Printf("Starting OAuth flow for MCP server '%s'\n", name)
	fmt.Printf("This will be stored as grant 'mcp-%s'\n", name)

	cfg := mcpoauth.Config{
		AuthURL:  mcpAuthURL,
		TokenURL: mcpTokenURL,
		ClientID: mcpClientID,
		Scopes:   mcpScopes,
	}

	ctx := context.Background()
	tokenResp, err := mcpoauth.Authorize(ctx, cfg)
	if err != nil {
		return fmt.Errorf("OAuth authorization failed: %w", err)
	}

	// Build metadata to store alongside the token
	metadata := map[string]string{
		"auth_type": "oauth",
		"token_url": mcpTokenURL,
		"client_id": mcpClientID,
	}
	if tokenResp.RefreshToken != "" {
		metadata["refresh_token"] = tokenResp.RefreshToken
	}

	// Store credential with metadata
	cred := credential.Credential{
		Provider:  credential.Provider(fmt.Sprintf("mcp-%s", name)),
		Token:     tokenResp.AccessToken,
		ExpiresAt: tokenResp.ExpiresAt,
		CreatedAt: time.Now(),
		Metadata:  metadata,
	}

	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}

	fmt.Printf("\nMCP OAuth credential 'mcp-%s' saved to %s\n", name, credPath)
	if !tokenResp.ExpiresAt.IsZero() {
		fmt.Printf("Token expires: %s\n", tokenResp.ExpiresAt.Format(time.RFC3339))
	}
	if tokenResp.RefreshToken != "" {
		fmt.Println("Refresh token stored for automatic renewal.")
	}

	fmt.Printf("\nConfigure in agent.yaml:\n\n")
	fmt.Printf(`mcp:
  - name: %s
    url: https://mcp.example.com/mcp
    auth:
      type: oauth
      grant: mcp-%s
      client_id: %s
      auth_url: %s
      token_url: %s

`, name, name, mcpClientID, mcpAuthURL, mcpTokenURL)
	fmt.Println("Then run: moat claude ./workspace")

	return nil
}
