package oauth

import (
	_ "embed"

	"gopkg.in/yaml.v3"

	"github.com/majorcontext/moat/internal/log"
)

//go:embed registry.yaml
var registryData []byte

// registry maps short names (e.g. "notion") to MCP server URLs for
// OAuth auto-discovery. Loaded once from the embedded registry.yaml.
var registry map[string]string

func init() {
	registry = make(map[string]string)
	if err := yaml.Unmarshal(registryData, &registry); err != nil {
		// Embedded data is compile-time constant — a parse failure here is a bug.
		panic("oauth: invalid registry.yaml: " + err.Error())
	}
}

// LookupServerURL returns the well-known MCP server URL for a named OAuth
// grant, or "" if the name is not in the registry.
func LookupServerURL(name string) string {
	u, ok := registry[name]
	if !ok {
		log.Debug("no registry entry for OAuth name", "name", name)
		return ""
	}
	return u
}
