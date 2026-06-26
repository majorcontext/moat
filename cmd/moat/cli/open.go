package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/browser"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/routing"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/spf13/cobra"
)

var openPrintOnly bool

var openCmd = &cobra.Command{
	Use:   "open [agent] [endpoint]",
	Short: "Open an agent's endpoint in your browser",
	Long: `Open the routing-proxy URL for a running agent (or a specific endpoint)
in your default browser.

With no arguments, opens the discovery index listing every running agent and
its endpoints. With an agent name, opens that agent's page; add an endpoint
name to open a specific endpoint directly. When no agent is given, moat uses
the agent named in the current directory's moat.yaml, or the only running
agent if there's exactly one.

The URL is always printed, so on a headless or SSH session you can copy it
even when no browser is available.

Examples:
  moat open                # index of every running agent and endpoint
  moat open demo           # the demo agent (its endpoint index, or its sole endpoint)
  moat open demo web       # demo's "web" endpoint
  moat open --print demo   # print the URL without launching a browser`,
	Args: cobra.MaximumNArgs(2),
	RunE: runOpen,
}

func init() {
	openCmd.Flags().BoolVarP(&openPrintOnly, "print", "p", false, "print the URL without opening a browser")
	rootCmd.AddCommand(openCmd)
}

func runOpen(_ *cobra.Command, args []string) error {
	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")

	lock, err := routing.LoadProxyLock(proxyDir)
	if err != nil {
		return fmt.Errorf("reading proxy state: %w", err)
	}
	if lock == nil || !lock.IsAlive() {
		return fmt.Errorf("no routing proxy is running — start an agent that exposes ports first (see `ports:` in moat.yaml)")
	}

	routes, err := routing.NewRouteTable(proxyDir)
	if err != nil {
		return fmt.Errorf("reading routes: %w", err)
	}
	registry := routes.Snapshot()

	var agent, endpoint string
	if len(args) >= 1 {
		agent = args[0]
	}
	if len(args) >= 2 {
		endpoint = args[1]
	}

	if agent == "" {
		agent = defaultOpenAgent(registry)
	}

	if agent != "" {
		if _, ok := registry[agent]; !ok {
			return fmt.Errorf("unknown agent %q (running: %s)", agent, joinSortedKeys(registry))
		}
	}
	if endpoint != "" {
		if _, ok := registry[agent][endpoint]; !ok {
			return fmt.Errorf("agent %q has no endpoint %q (endpoints: %s)", agent, endpoint, joinSortedKeys(registry[agent]))
		}
	}

	url := buildOpenURL(agent, endpoint, lock.Port)
	fmt.Println(url)
	if openPrintOnly {
		return nil
	}
	if err := browser.Open(url); err != nil {
		fmt.Println(ui.Dim("(couldn't launch a browser automatically — open the URL above)"))
	}
	return nil
}

// buildOpenURL builds the routing-proxy URL for an agent/endpoint. An empty
// agent yields the global discovery index; an empty endpoint yields the
// agent's base host. Mirrors the [endpoint.]agent.localhost hostname scheme.
func buildOpenURL(agent, endpoint string, port int) string {
	host := "localhost"
	switch {
	case agent != "" && endpoint != "":
		host = endpoint + "." + agent + ".localhost"
	case agent != "":
		host = agent + ".localhost"
	}
	return fmt.Sprintf("https://%s:%d/", host, port)
}

// defaultOpenAgent picks the agent to open when none was given: the agent named
// in the current directory's moat.yaml if it's running, else the sole running
// agent, else "" (the global index).
func defaultOpenAgent(registry map[string]map[string]string) string {
	if cfg, err := config.Load("."); err == nil && cfg != nil && cfg.Name != "" {
		if _, ok := registry[cfg.Name]; ok {
			return cfg.Name
		}
	}
	if len(registry) == 1 {
		for name := range registry {
			return name
		}
	}
	return ""
}

// joinSortedKeys returns the map's keys sorted and comma-joined, or "none".
func joinSortedKeys[V any](m map[string]V) string {
	if len(m) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
