// cmd/moat/cli/grant_mcp.go
package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/andybons/moat/internal/credential"
	"github.com/spf13/cobra"
)

var grantMCPCmd = &cobra.Command{
	Use:   "mcp <server-name>",
	Short: "Grant credentials for an MCP server",
	Long: `Grant credentials for a Model Context Protocol (MCP) server.

The credential is stored securely and injected by the proxy when the agent
makes requests to the MCP server. The agent never sees the raw credential.

Examples:
  # Grant Context7 MCP access
  moat grant mcp context7

  # Configure in agent.yaml
  cat > agent.yaml <<YAML
  mcp:
    - name: context7
      url: https://mcp.context7.com/mcp
      auth:
        grant: mcp-context7
        header: CONTEXT7_API_KEY
  YAML

  # Use in a run
  moat claude ./workspace`,
	Args: cobra.ExactArgs(1),
	RunE: runGrantMCP,
}

func init() {
	grantCmd.AddCommand(grantMCPCmd)
}

func runGrantMCP(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Validate server name doesn't contain problematic characters
	if strings.ContainsAny(name, "/\\:*?\"<>|") {
		return fmt.Errorf("invalid server name: %q contains invalid characters", name)
	}

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
