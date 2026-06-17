// Package mcpcatalog holds the registry of well-known MCP servers, used both
// for `moat grant oauth` auto-discovery and for resolving bare `mcp:` names in
// moat.yaml. It is a dependency-free leaf package so config and provider
// packages can import it without an import cycle.
package mcpcatalog

import (
	_ "embed"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed registry.yaml
var registryData []byte

// Entry is a resolved well-known MCP server.
type Entry struct {
	URL    string
	Grant  string
	Header string
	// OAuth reports whether this server authenticates via OAuth (grant
	// "oauth:<name>"), as opposed to an API-key grant (e.g. context7's
	// "mcp-context7"). Only OAuth entries are valid targets for
	// `moat grant oauth` discovery.
	OAuth bool
}

// rawEntry is the on-disk YAML value: either a scalar URL string (an OAuth
// server) or a mapping with an explicit url and auth block.
type rawEntry struct {
	URL  string `yaml:"url"`
	Auth struct {
		Grant  string `yaml:"grant"`
		Header string `yaml:"header"`
	} `yaml:"auth"`
}

func (r *rawEntry) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&r.URL)
	}
	type alias rawEntry
	return node.Decode((*alias)(r))
}

var registry map[string]rawEntry

func init() {
	registry = make(map[string]rawEntry)
	if err := yaml.Unmarshal(registryData, &registry); err != nil {
		// Embedded data is compile-time constant — a parse failure is a bug.
		panic("mcpcatalog: invalid registry.yaml: " + err.Error())
	}
}

// Lookup returns the resolved entry for a name, ok=false if unknown. String
// (OAuth) entries default to grant "oauth:<name>" and header "Authorization".
func Lookup(name string) (Entry, bool) {
	r, ok := registry[name]
	if !ok {
		return Entry{}, false
	}
	e := Entry{URL: r.URL, Grant: r.Auth.Grant, Header: r.Auth.Header}
	if e.Grant == "" {
		e.Grant = "oauth:" + name
	}
	if e.Header == "" {
		e.Header = "Authorization"
	}
	e.OAuth = strings.HasPrefix(e.Grant, "oauth:")
	return e, true
}

// Names returns the sorted list of known server names (for error messages).
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
