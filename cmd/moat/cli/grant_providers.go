package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/spf13/cobra"
)

var grantProvidersCmd = &cobra.Command{
	Use:   "providers",
	Short: "List available credential providers",
	Long: `List all credential providers that can be used with 'moat grant'.

Shows built-in providers (shipped with moat) and any custom providers
defined in ~/.moat/providers/.`,
	RunE: runGrantProviders,
}

func init() {
	grantCmd.AddCommand(grantProvidersCmd)
}

// goProviderDescriptions provides descriptions for Go-implemented providers
// that don't implement DescribableProvider. Every provider that survives
// agentOnlyProviders needs an entry here or its own Description() — a blank
// description column is a bug, and TestGrantProviderInfosAllDescribed guards it.
var goProviderDescriptions = map[string]string{
	"github":    "GitHub token",
	"claude":    "Claude Pro/Max OAuth token (for moat claude)",
	"anthropic": "Anthropic API key from console.anthropic.com",
	"codex":     "OpenAI API key or OAuth credentials",
	"gemini":    "Gemini API key or OAuth credentials",
	"aws":       "AWS IAM role assumption",
	"npm":       "npm registry credentials",
	"graphite":  "Graphite API token for stacked PRs",
	"meta":      "Meta Graph API access token",
	"oauth":     "OAuth for a catalog service ('moat grant oauth <name>')",
}

// goProviderCLINames maps internal provider names to their CLI-facing names.
// Only for providers whose canonical name is not what users type: `codex` is
// granted as `openai` via a registry alias. `claude` is NOT aliased here — it
// and `anthropic` are two separate grants (OAuth vs API key), so renaming one
// to the other collapses them into a duplicate row.
var goProviderCLINames = map[string]string{
	"codex": "openai",
}

// agentOnlyProviders are registered as CredentialProviders to carry agent
// runtime behavior, but have no credential of their own — their Grant()
// always errors and points at the real grant. Listing them under
// 'moat grant providers' invites users to run a command that cannot work.
var agentOnlyProviders = map[string]bool{
	"copilot": true, // runs on the github grant
	"pi":      true, // runs on the anthropic or openai grant
}

type providerInfo struct {
	Name        string `json:"provider"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

// grantProviderInfos returns the provider rows for 'moat grant providers',
// sorted by CLI-facing name.
func grantProviderInfos() []providerInfo {
	all := provider.All()

	infos := make([]providerInfo, 0, len(all))
	for _, p := range all {
		// Agent-only providers cannot be granted directly — skip them so the
		// listing only shows names that work as 'moat grant <name>'.
		if agentOnlyProviders[p.Name()] {
			continue
		}
		// Show the CLI-facing name where it differs from the canonical one.
		name := p.Name()
		if cliName, ok := goProviderCLINames[name]; ok {
			name = cliName
		}

		desc := ""
		source := "builtin"

		if dp, ok := p.(provider.DescribableProvider); ok {
			desc = dp.Description()
			source = dp.Source()
		} else if d, ok := goProviderDescriptions[p.Name()]; ok {
			desc = d
		}

		infos = append(infos, providerInfo{
			Name:        name,
			Description: desc,
			Type:        source,
		})
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})

	return infos
}

func runGrantProviders(cmd *cobra.Command, args []string) error {
	infos := grantProviderInfos()

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(infos)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tDESCRIPTION\tTYPE")
	for _, info := range infos {
		fmt.Fprintf(w, "%s\t%s\t%s\n", info.Name, info.Description, info.Type)
	}
	w.Flush()

	fmt.Printf("\nCustom providers can be added at %s\n", filepath.Join(config.GlobalConfigDir(), "providers"))

	return nil
}
