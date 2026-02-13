package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

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
// that don't implement DescribableProvider.
var goProviderDescriptions = map[string]string{
	"github": "GitHub token",
	"claude": "Anthropic API key or OAuth credentials",
	"codex":  "OpenAI API key or OAuth credentials",
	"gemini": "Gemini API key or OAuth credentials",
	"aws":    "AWS IAM role assumption",
	"npm":    "npm registry credentials",
}

// goProviderCLINames maps internal provider names to their CLI-facing names.
var goProviderCLINames = map[string]string{
	"claude": "anthropic",
	"codex":  "openai",
}

type providerInfo struct {
	Name        string `json:"provider"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

func runGrantProviders(cmd *cobra.Command, args []string) error {
	all := provider.All()

	infos := make([]providerInfo, 0, len(all))
	for _, p := range all {
		// Skip agent-only providers (claude, codex, gemini) from grant listing
		// if they have a CLI name alias — we show the alias instead
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

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(infos)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tDESCRIPTION\tTYPE")
	for _, info := range infos {
		fmt.Fprintf(w, "%s\t%s\t%s\n", info.Name, info.Description, info.Type)
	}
	w.Flush()

	fmt.Println("\nCustom providers can be added at ~/.moat/providers/")

	return nil
}
